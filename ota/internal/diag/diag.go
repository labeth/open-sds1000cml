// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

// Package diag is the SDS1000 diagnostics stick's board: booted by the
// factory firmware on another SDS1000 variant, the core collector
// (open-dso-core/supervisor/diag) leaves one archive of what that board is
// and does, for a port of this firmware to its population. This package is
// what its stages do on this family: the passive stage reads the ARM's
// devices, the factory firmware, the FPGA over the inherited /dev/Gpmc and
// the factory app's settings over SCPI; the active stage traces the factory
// app's register traffic while it steps the factory app through its settings
// over SCPI, and puts the settings back; the takeover stage stops the factory
// app, takes a census of every FPGA ball with the probe image, and runs this
// firmware's app on the board. Nothing writes the instrument's flash; the
// FPGA images are volatile.
package diag

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	core "github.com/labeth/open-dso-core/supervisor/diag"
	"github.com/labeth/open-dso-core/supervisor/vxi11"

	"github.com/labeth/open-sds1000cml/ota/internal/buildinfo"
)

// Run is the program: the core collector on this board.
// TRLC-LINKS: REQ-SDS-284
func Run(args []string, stderr io.Writer) int { return core.Run(args, stderr, &stick{}) }

// stick is one run's board state, shared by its stages.
type stick struct {
	c         *core.Collection
	vx        *vxi11.Client // the factory app's SCPI, nil until reached
	ps        []core.Proc
	pid       int    // the factory app (or -pid)
	fpgaOK    bool   // the inherited /dev/Gpmc answered a register read
	fpgaVer   uint16 // the version register's value
	st        scpiState
	sweepDone bool     // the factory app ran on through the sweep and answered after the restore
	inventory []string // the ARM's devices (armInventory)
}

var _ core.Board = (*stick)(nil)

// Version is the build on the summary's first line.
// TRLC-LINKS: REQ-SDS-284
func (s *stick) Version() string { return buildinfo.Version }

// Child runs the collector's own child processes: "trace" and "memread".
// TRLC-LINKS: REQ-SDS-285
func (s *stick) Child(name string, args []string, stderr io.Writer) (int, bool) {
	switch name {
	case "trace":
		return traceChild(args, stderr), true
	case "memread":
		return memChild(args, stderr), true
	}
	return 0, false
}

// Passive reads the ARM's devices, the factory firmware, the processes, the
// FPGA and the screen, then waits for the factory app's SCPI server and reads
// its settings. Without SCPI no later stage runs.
// TRLC-LINKS: REQ-SDS-284
func (s *stick) Passive(c *core.Collection, a *Archive) bool {
	s.c = c
	s.inventory = armInventory("/")
	a.AddText("system/arm_inventory.txt", strings.Join(s.inventory, "\n")+"\n")
	av := armVerdict(s.inventory, loadBoardReference().ARM)
	c.Note("arm devices: %s: %s", av.Verdict, av.Detail)
	c.Say("firmware files")
	core.CollectFirmware(a, firmwareRoots, firmwareSkip)
	c.Say("processes")
	s.ps = core.CollectProcs(a)
	s.pid = factoryPID(s.ps)
	c.Note("factory app pid: %d", s.pid)
	if c.PID != 0 {
		s.pid = c.PID
		c.Note("traced instead: pid %d", s.pid)
	}
	var fpgaNote string
	fpgaNote, s.fpgaVer, s.fpgaOK = collectFPGA(a)
	c.Note("fpga: %s", fpgaNote)
	c.Note("screen: %s", core.CollectScreen(a, "start"))

	c.Say("waiting for the factory app's SCPI server")
	vx, err := dialFactory(c.Wait)
	if err != nil {
		a.Errorf("scpi: %v", err)
		c.Note("scpi: %v", err)
		return false
	}
	s.vx = vx
	s.st = readState(vx)
	a.AddJSON("scpi/state.json", s.st)
	c.Note("identity: %s", s.st.Answers["*IDN?"])
	return true
}

// ActiveGate is Gate on what the passive stage found.
// TRLC-LINKS: REQ-SDS-287
func (s *stick) ActiveGate(*core.Collection) []core.Check {
	return Gate(gateInput{cpuinfo: core.ReadStr("/proc/cpuinfo"), procs: s.ps, pid: s.pid, fpgaOK: s.fpgaOK, fpgaVer: s.fpgaVer, scpi: &s.st})
}

// Active reads the SoC's registers and runs the settings sweep under the
// register trace.
// TRLC-LINKS: REQ-SDS-285
func (s *stick) Active(c *core.Collection, a *Archive) {
	c.Say("active: SoC registers")
	collectMem(a, c.Self)
	c.Say("active: the settings sweep with the register trace (about %d minutes)", int(s.sweepTime().Minutes()+0.5))
	if err := s.sweep(a, s.vx, s.pid, s.st); err != nil {
		a.Errorf("sweep: %v", err)
		c.Note("sweep: %v", err)
	}
	c.Note("screen after: %s", core.CollectScreen(a, "end"))
}

// Close ends the SCPI link.
// TRLC-LINKS: REQ-SDS-284
func (s *stick) Close() {
	if s.vx != nil {
		s.vx.Close()
	}
}

// ScreenOn turns the backlight on (gpio7, spec 08 §10.1).
// TRLC-LINKS: REQ-SDS-292
func (s *stick) ScreenOn() error { return writeSys("/sys/class/gpio/gpio7/value", "1") }

// stepTime is about how long one traced step takes on the SDS1102CML+: the
// factory app takes a step slowly while traced (136 steps took 21 minutes).
const stepTime = 9 * time.Second

// sweepTime is about how long the active sweep takes, at most the limit.
// TRLC-LINKS: REQ-SDS-285
func (s *stick) sweepTime() time.Duration {
	n := len(SweepSteps())
	if s.c.Steps > 0 {
		n = min(n, s.c.Steps)
	}
	return min(time.Duration(n)*(stepTime+s.c.Dwell-700*time.Millisecond), s.c.Limit)
}

// sweep steps the factory app through SweepSteps, each step in a register
// trace window of its own: a tracer child attaches, the step's command goes
// out, and the tracer ends once the factory app has written its registers
// and gone quiet (or after a few seconds). The factory app polls its
// registers thousands of times a second, and traced through a whole sweep it
// had no time left to answer SCPI; between windows it runs untraced. The
// read-back query and the restore of the saved settings run untraced, and the
// settings are read again at the end to show they are back.
// TRLC-LINKS: REQ-SDS-285
func (s *stick) sweep(a *Archive, vx *vxi11.Client, pid int, st scpiState) error {
	c := s.c
	if pid == 0 {
		return errors.New("no factory app running: nothing to trace")
	}
	var log strings.Builder
	fmt.Fprintf(&log, "# step t_us cmd | the factory app's answer to the step's query | ms | trace window\n")
	steps := SweepSteps()
	if c.Steps > 0 && c.Steps < len(steps) {
		steps = steps[:c.Steps]
	}
	regs := regMap{}
	end := time.Now().Add(c.Limit)
	ranOn := true // the factory app ran on after every window
	for i, step := range steps {
		if time.Now().After(end) {
			fmt.Fprintf(&log, "# the sweep's %v ran out after %d of %d steps\n", c.Limit, i, len(steps))
			c.Note("sweep: cut short after %d of %d steps (the %v limit)", i, len(steps), c.Limit)
			break
		}
		if i%10 == 0 {
			c.Say("active: step %d of %d", i, len(steps))
		}
		file := filepath.Join(c.OutDir, fmt.Sprintf(".sds-diag-step-%03d.gz", i))
		t0 := time.Now()
		t := core.MonoMicros()
		var ans string
		var qerr error
		werr := s.window(pid, file, func() error { return vx.Send(step.Cmd) })
		time.Sleep(c.Dwell)
		ans, qerr = vx.Query(queryOf(step.Cmd))
		fmt.Fprintf(&log, "%03d %d %s | %s %s | %d | %s\n", i, t, step.Cmd, strings.TrimSpace(ans), core.ErrText(qerr),
			time.Since(t0).Milliseconds(), core.ErrText(werr))
		a.AddFile(fmt.Sprintf("trace/steps/%03d-%s.txt.gz", i, step.Group), file, 64<<20)
		if err := regs.addTrace(i, file); err != nil {
			a.Errorf("register map, step %d: %v", i, err)
		}
		_ = os.Remove(file)
		if errors.Is(werr, errStopped) {
			fmt.Fprintf(&log, "# the factory app did not run on after step %d: the sweep ends here\n", i)
			c.Note("sweep: ended after step %d: %v", i, werr)
			ranOn = false
			break
		}
	}
	fmt.Fprintf(&log, "# restore\n")
	for _, cmd := range restoreCmds(st) {
		fmt.Fprintf(&log, "%d %s %s\n", core.MonoMicros(), cmd, core.ErrText(vx.Send(cmd)))
		time.Sleep(200 * time.Millisecond)
	}
	a.AddText("trace/steps.txt", log.String())
	a.AddText("trace/register_map.txt", regs.text(steps))
	c.Note("registers the factory app used: %d (trace/register_map.txt)", len(regs))
	a.AddJSON("trace/sweep.json", SweepSteps())
	after := readState(vx)
	a.AddJSON("scpi/state_after.json", after)
	var diff []string
	for _, q := range stateQueries {
		if st.Answers[q] != after.Answers[q] {
			diff = append(diff, fmt.Sprintf("%s %q -> %q", q, st.Answers[q], after.Answers[q]))
		}
	}
	c.Note("settings after the sweep: %d differ from before %v", len(diff), diff)
	s.sweepDone = ranOn && after.Answers["*IDN?"] == st.Answers["*IDN?"]
	return nil
}

// window sends cmd while a tracer child traces the factory app: the child
// attaches and says ready, cmd goes out, and the child ends 1 s after the
// factory app's last register write (or after 5 s, or when told to stop
// once the command was answered with no write). Traced, the factory app takes
// a V/div step slowly; the rest of the step, and its answer, run untraced.
// The child's exit detaches the factory app; resume makes it run on.
// TRLC-LINKS: REQ-SDS-285
func (s *stick) window(pid int, file string, send func() error) error {
	tr := exec.Command(s.c.Self, "trace", strconv.Itoa(pid), "5000", file, "1000") //nolint:gosec,noctx // G204: this program as the tracer; it ends by itself within 5 s
	in, err := tr.StdinPipe()
	if err != nil {
		return err
	}
	out, err := tr.StdoutPipe()
	if err != nil {
		return err
	}
	if err := tr.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	ready := make(chan bool, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		ready <- strings.TrimSpace(line) == "ready"
		_, _ = io.Copy(io.Discard, out)
		done <- tr.Wait()
	}()
	stopTracer := func() {
		_, _ = io.WriteString(in, "stop\n")
		_ = in.Close()
	}
	wait := func() error {
		select {
		case err := <-done:
			return err
		case <-time.After(20 * time.Second):
			_ = tr.Process.Kill() // a tracer's exit detaches the factory app
			<-done                //nolint:errcheck // the killed tracer's exit status says nothing new
			return errors.New("the tracer did not end: killed")
		}
	}
	select {
	case ok := <-ready:
		if !ok {
			stopTracer()
			_ = wait()
			return errors.Join(errors.New("the tracer did not attach"), resume(pid))
		}
	case <-time.After(5 * time.Second):
		stopTracer()
		_ = wait()
		return errors.Join(errors.New("the tracer did not attach in 5 s"), resume(pid))
	}
	sent := make(chan error, 1)
	go func() { sent <- send() }()
	var serr error
	var werr error
	select {
	case werr = <-done: // writes seen, then quiet: the window is over
	case serr = <-sent: // answered first (a step that writes nothing, or writes before it answers)
		time.Sleep(300 * time.Millisecond)
		stopTracer()
		werr = wait()
	}
	if err := resume(pid); err != nil {
		return err
	}
	if serr == nil {
		select {
		case serr = <-sent:
		case <-time.After(20 * time.Second):
			serr = errors.New("the command got no answer in 20 s")
		}
	}
	return errors.Join(werr, serr)
}

// errStopped is a factory app that is gone or stays stopped after a window:
// the sweep ends there and the settings are put back.
var errStopped = errors.New("the factory app did not run on after the trace window")

// resume makes sure the factory app runs on after a trace window. Attaching
// stops every thread with SIGSTOP, which starts a stop of the whole thread
// group; the tracer's exit detaches the threads but leaves that group stop
// in place, and the factory app would stay frozen. SIGCONT ends it, as strace
// does when it detaches from a process it stopped.
// TRLC-LINKS: REQ-SDS-285
func resume(pid int) error {
	for range 20 {
		_ = syscall.Kill(pid, syscall.SIGCONT)
		switch core.ProcState(pid) {
		case "":
			return errStopped // gone
		case "T", "t":
			time.Sleep(50 * time.Millisecond)
		default:
			return nil
		}
	}
	return errStopped
}

// queryOf is the query that reads back what cmd set: its header with a "?".
// TRLC-LINKS: REQ-SDS-285
func queryOf(cmd string) string {
	h, _, _ := strings.Cut(cmd, " ")
	return h + "?"
}

// traceChild is "sdsdiag trace pid millis file quiet-millis": one trace
// window, "ready" on stdout once attached.
// TRLC-LINKS: REQ-SDS-285
func traceChild(args []string, stderr io.Writer) int {
	if len(args) != 4 {
		_, _ = fmt.Fprintln(stderr, "usage: sdsdiag trace pid millis file quiet-millis")
		return 2
	}
	pid, err1 := strconv.Atoi(args[0])
	ms, err2 := strconv.Atoi(args[1])
	quiet, err3 := strconv.Atoi(args[3])
	if err1 != nil || err2 != nil || err3 != nil {
		_, _ = fmt.Fprintln(stderr, "sdsdiag trace: pid, millis and quiet-millis are numbers")
		return 2
	}
	stop := make(chan struct{})
	go func() { // "stop" (or the parent's end of stdin) ends the window
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		close(stop)
	}()
	o := TraceOpts{
		For: time.Duration(ms) * time.Millisecond, Out: args[2], MaxLines: 2_000_000,
		QuietAfterWrite: time.Duration(quiet) * time.Millisecond, Ready: os.Stdout, Stop: stop,
	}
	if err := Trace(pid, o); err != nil {
		_, _ = fmt.Fprintln(stderr, "sdsdiag trace:", err)
		return 1
	}
	return 0
}

// memChild is "sdsdiag memread base len" (hex).
// TRLC-LINKS: REQ-SDS-285
func memChild(args []string, stderr io.Writer) int {
	if len(args) != 2 {
		_, _ = fmt.Fprintln(stderr, "usage: sdsdiag memread base len (hex)")
		return 2
	}
	base, err1 := strconv.ParseUint(args[0], 16, 32)
	n, err2 := strconv.ParseUint(args[1], 16, 32)
	if err1 != nil || err2 != nil || n > 1<<16 {
		_, _ = fmt.Fprintln(stderr, "sdsdiag memread: base and len in hex, len at most 0x10000")
		return 2
	}
	if err := MemRead(uint32(base), uint32(n)); err != nil {
		_, _ = fmt.Fprintln(stderr, "sdsdiag memread:", err)
		return 1
	}
	return 0
}
