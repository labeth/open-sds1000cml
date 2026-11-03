// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

// Package diag is the diagnostics stick's collector: booted by the factory
// firmware on another SDS1000 variant, it leaves one archive of what that
// board is and does, for a port of this firmware to its population. The
// passive stage only reads; the active stage, run only when asked, traces
// the factory app's register traffic while it steps the factory app through
// its settings over SCPI, and puts the settings back. Neither loads an FPGA
// image or writes the instrument's flash.
package diag

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/labeth/open-sds1000cml/ota/internal/buildinfo"
	"github.com/labeth/open-sds1000cml/ota/internal/vxi11"
)

// The modes a stick runs in: MODE on the stick names one.
const (
	ModePassive = "passive"
	ModeActive  = "active"
)

// archiveMax caps the archive's stored bytes (the trace is the big member).
const archiveMax = 512 << 20

// Run is the program: no subcommand collects; "trace" and "memread" are the
// collector's own child processes. It returns the exit code.
// TRLC-LINKS: REQ-SDS-284, REQ-SDS-285, REQ-SDS-286
func Run(args []string, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "trace":
			return traceChild(args[1:], stderr)
		case "memread":
			return memChild(args[1:], stderr)
		}
	}
	fs := flag.NewFlagSet("sdsdiag", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "", "passive or active (empty: the MODE file next to the program)")
	out := fs.String("out", "", "where the archive goes (empty: the stick's root, above the program's directory)")
	wait := fs.Duration("wait", 150*time.Second, "how long to wait for the factory app's SCPI server")
	dwell := fs.Duration("dwell", 700*time.Millisecond, "how long each sweep step is held (active)")
	steps := fs.Int("steps", 0, "run only the first steps of the sweep (active; 0: all)")
	limit := fs.Duration("limit", 20*time.Minute, "the longest the active sweep runs before it puts the settings back")
	pid := fs.Int("pid", 0, "trace this process instead of the factory app (active)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "sdsdiag:", err)
		return 1
	}
	dir := filepath.Dir(self)
	if *mode == "" {
		*mode = strings.TrimSpace(readStr(filepath.Join(dir, "MODE")))
	}
	if *mode != ModeActive {
		*mode = ModePassive
	}
	if *out == "" {
		*out = filepath.Dir(dir)
	}
	c := &collection{
		self: self, mode: *mode, outDir: *out, wait: *wait, dwell: *dwell, steps: *steps, limit: *limit, pid: *pid,
		status: filepath.Join(*out, "sds-diag-status.txt"),
	}
	if err := c.run(); err != nil {
		c.say("FAILED: %v", err)
		_, _ = fmt.Fprintln(stderr, "sdsdiag:", err)
		return 1
	}
	return 0
}

// collection is one run of the collector.
type collection struct {
	self, mode, outDir, status string
	wait, dwell, limit         time.Duration
	steps                      int // the sweep's first steps only (0: all)
	pid                        int // the process to trace (0: the factory app)
	summary                    []string
}

// say writes a progress line to the status file on the stick: the person who
// booted the stick sees what it is doing when they take it out.
// TRLC-LINKS: REQ-SDS-286
func (c *collection) say(format string, args ...any) {
	line := fmt.Sprintf("%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	f, err := os.OpenFile(c.status, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // G302,G304: a text file the owner reads on the stick
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}

// note adds a line to the summary.
// TRLC-LINKS: REQ-SDS-286
func (c *collection) note(format string, args ...any) {
	c.summary = append(c.summary, fmt.Sprintf(format, args...))
}

// run collects into a new archive on the stick.
// TRLC-LINKS: REQ-SDS-284, REQ-SDS-285, REQ-SDS-286
func (c *collection) run() error {
	name := archiveName(c.outDir, c.mode)
	path := filepath.Join(c.outDir, name)
	c.say("collecting (%s) into %s - leave the scope on until DONE", c.mode, name)
	f, err := os.Create(path) //nolint:gosec // G304: the archive on the stick
	if err != nil {
		return err
	}
	a := NewArchive(f, archiveMax)
	c.note("sdsdiag %s, mode %s", buildinfo.Version, c.mode)
	c.note("kernel: %s", strings.TrimSpace(readStr("/proc/version")))
	c.note("cpu: %s", cpuLine())
	c.note("mac: %s", strings.TrimSpace(readStr("/sys/class/net/eth0/address")))

	c.say("system")
	collectSystem(a)
	c.say("firmware files")
	collectFirmware(a, firmwareRoots)
	c.say("processes")
	ps := collectProcs(a)
	pid := factoryPID(ps)
	c.note("factory app pid: %d", pid)
	if c.pid != 0 {
		pid = c.pid
		c.note("traced instead: pid %d", pid)
	}
	c.note("fpga: %s", collectFPGA(a))
	c.note("screen: %s", collectScreen(a, "start"))

	c.say("waiting for the factory app's SCPI server")
	vx, err := dialFactory(c.wait)
	if err != nil {
		a.Errorf("scpi: %v", err)
		c.note("scpi: %v", err)
	} else {
		st := readState(vx)
		a.AddJSON("scpi/state.json", st)
		c.note("identity: %s", st.Answers["*IDN?"])
		if c.mode == ModeActive {
			c.say("active: SoC registers")
			collectMem(a, c.self)
			c.say("active: the settings sweep with the register trace (about %d s)", int(c.sweepTime().Seconds()))
			if err := c.sweep(a, vx, pid, st); err != nil {
				a.Errorf("sweep: %v", err)
				c.note("sweep: %v", err)
			}
			c.note("screen after: %s", collectScreen(a, "end"))
		}
		vx.Close()
	}
	errs := a.Errors()
	c.note("collector errors: %d (errors.txt)", len(errs))
	a.AddText("summary.txt", strings.Join(c.summary, "\n")+"\n")
	if err := a.Close(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(c.outDir, strings.TrimSuffix(name, ".tar.gz")+".txt"), []byte(strings.Join(c.summary, "\n")+"\n"), 0o644) //nolint:gosec // G306: the summary the owner reads on the stick
	c.say("DONE: send %s to the project (it holds this scope's firmware files: send it privately)", name)
	return nil
}

// sweepTime is about how long the active sweep takes: each step's trace
// window, the dwell and the read-back, at most the limit.
// TRLC-LINKS: REQ-SDS-285
func (c *collection) sweepTime() time.Duration {
	n := len(SweepSteps())
	if c.steps > 0 {
		n = min(n, c.steps)
	}
	return min(time.Duration(n)*(c.dwell+1500*time.Millisecond), c.limit)
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
func (c *collection) sweep(a *Archive, vx *vxi11.Client, pid int, st scpiState) error {
	if pid == 0 {
		return errors.New("no factory app running: nothing to trace")
	}
	var log strings.Builder
	fmt.Fprintf(&log, "# step t_us cmd | the factory app's answer to the step's query | ms | trace window\n")
	steps := SweepSteps()
	if c.steps > 0 && c.steps < len(steps) {
		steps = steps[:c.steps]
	}
	end := time.Now().Add(c.limit)
	for i, s := range steps {
		if time.Now().After(end) {
			fmt.Fprintf(&log, "# the sweep's %v ran out after %d of %d steps\n", c.limit, i, len(steps))
			c.note("sweep: cut short after %d of %d steps (the %v limit)", i, len(steps), c.limit)
			break
		}
		if i%10 == 0 {
			c.say("active: step %d of %d", i, len(steps))
		}
		file := filepath.Join(c.outDir, fmt.Sprintf(".sds-diag-step-%03d.gz", i))
		t0 := time.Now()
		t := monoMicros()
		var ans string
		var qerr error
		werr := c.window(pid, file, func() error { return vx.Send(s.Cmd) })
		time.Sleep(c.dwell)
		ans, qerr = vx.Query(queryOf(s.Cmd))
		fmt.Fprintf(&log, "%03d %d %s | %s %s | %d | %s\n", i, t, s.Cmd, strings.TrimSpace(ans), errText(qerr),
			time.Since(t0).Milliseconds(), errText(werr))
		a.AddFile(fmt.Sprintf("trace/steps/%03d-%s.txt.gz", i, s.Group), file, 64<<20)
		_ = os.Remove(file)
		if errors.Is(werr, errStopped) {
			fmt.Fprintf(&log, "# the factory app did not run on after step %d: the sweep ends here\n", i)
			c.note("sweep: ended after step %d: %v", i, werr)
			break
		}
	}
	fmt.Fprintf(&log, "# restore\n")
	for _, cmd := range restoreCmds(st) {
		fmt.Fprintf(&log, "%d %s %s\n", monoMicros(), cmd, errText(vx.Send(cmd)))
		time.Sleep(200 * time.Millisecond)
	}
	a.AddText("trace/steps.txt", log.String())
	a.AddJSON("trace/sweep.json", SweepSteps())
	after := readState(vx)
	a.AddJSON("scpi/state_after.json", after)
	var diff []string
	for _, q := range stateQueries {
		if st.Answers[q] != after.Answers[q] {
			diff = append(diff, fmt.Sprintf("%s %q -> %q", q, st.Answers[q], after.Answers[q]))
		}
	}
	c.note("settings after the sweep: %d differ from before %v", len(diff), diff)
	return nil
}

// window sends cmd while a tracer child traces the factory app: the child
// attaches and says ready, cmd goes out, and the child ends 1 s after the
// factory app's last register write (or after 5 s, or when told to stop
// once the command was answered with no write). Traced, the factory app takes
// a V/div step slowly; the rest of the step, and its answer, run untraced.
// The child's exit detaches the factory app; resume makes it run on.
// TRLC-LINKS: REQ-SDS-285
func (c *collection) window(pid int, file string, send func() error) error {
	tr := exec.Command(c.self, "trace", strconv.Itoa(pid), "5000", file, "1000") //nolint:gosec,noctx // G204: this program as the tracer; it ends by itself within 5 s
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
		switch procState(pid) {
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

// procState is the state letter of /proc/pid/stat ("R", "S", "T", ...), or
// "" when the process is gone.
// TRLC-LINKS: REQ-SDS-285
func procState(pid int) string {
	st := readStr(fmt.Sprintf("/proc/%d/stat", pid))
	i := strings.LastIndexByte(st, ')') // the name in parentheses can hold spaces
	if i < 0 || i+2 >= len(st) {
		return ""
	}
	return st[i+2 : i+3]
}

// queryOf is the query that reads back what cmd set: its header with a "?".
// TRLC-LINKS: REQ-SDS-285
func queryOf(cmd string) string {
	h, _, _ := strings.Cut(cmd, " ")
	return h + "?"
}

// archiveName is the next free archive name in dir for this scope and mode:
// the MAC (the clock is not set on these scopes) and a sequence number.
// TRLC-LINKS: REQ-SDS-286
func archiveName(dir, mode string) string {
	mac := strings.ReplaceAll(strings.TrimSpace(readStr("/sys/class/net/eth0/address")), ":", "")
	if mac == "" {
		mac = "nomac"
	}
	for n := 1; ; n++ {
		name := fmt.Sprintf("sds-diag-%s-%s-%d.tar.gz", mac, mode, n)
		if _, err := os.Stat(filepath.Join(dir, name)); errors.Is(err, os.ErrNotExist) {
			return name
		}
	}
}

// cpuLine is the processor and hardware lines of /proc/cpuinfo.
// TRLC-LINKS: REQ-SDS-284
func cpuLine() string {
	var parts []string
	for l := range strings.SplitSeq(readStr("/proc/cpuinfo"), "\n") {
		if strings.HasPrefix(l, "Processor") || strings.HasPrefix(l, "Hardware") || strings.HasPrefix(l, "model name") {
			parts = append(parts, strings.TrimSpace(l[strings.Index(l, ":")+1:]))
		}
	}
	return strings.Join(parts, ", ") + " (" + runtime.GOARCH + ")"
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
