// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/labeth/open-dso-core/hal"
	core "github.com/labeth/open-dso-core/supervisor/diag"
	"github.com/labeth/open-dso-core/supervisor/fdinherit"
	"github.com/labeth/open-dso-core/supervisor/vxi11"
	keep "github.com/labeth/open-dso-core/supervisor/watchdog"

	"github.com/labeth/open-sds1000cml/ota/internal/gpmc"
	"github.com/labeth/open-sds1000cml/ota/internal/watchdog"
)

// factoryRBFLen is the factory FPGA image's length on the SDS1102CML+: an
// uncompressed EP4CE10 (or EP4CE6, the same die) bitstream.
const factoryRBFLen = 368011

// The stick's own files the takeover stage runs, next to sdsdiag.
const (
	probeRBF     = "probe.rbf"
	probePullRBF = "probepull.rbf"
	stickApp     = "app"
)

// takeoverInput is what the passive and active stages learned that the
// takeover gate judges.
type takeoverInput struct {
	sweepDone  bool   // the factory app ran on through the sweep and answered after the restore
	alive      bool   // the factory app runs on after the sweep
	cfg        uint16 // CS3 0x07, the configuration port
	cfgErr     error
	factoryRBF []int64 // the lengths of the factory firmware's .rbf files
	files      map[string]bool
}

// TakeoverGate is the checks the takeover stage needs before it stops the
// factory app for good: the active stage poked the factory app and it acted
// as on the SDS1102CML+ (it ran on after every step and answered after the
// restore, and runs on), the FPGA's configuration port is where this firmware loads its images
// (CS3 0x07, the fabric configured), the factory image is a bitstream for the
// same FPGA, and the stick carries the probe images and the app.
// TRLC-LINKS: REQ-SDS-288
func TakeoverGate(in takeoverInput) []core.Check {
	var cs []core.Check
	add := func(name string, ok bool, f string, args ...any) {
		cs = append(cs, core.Check{Name: name, OK: ok, Detail: fmt.Sprintf(f, args...)})
	}
	add("active stage", in.sweepDone, "the factory app ran on through every step of the sweep and answered after its settings were put back")
	add("factory app after", in.alive, "the factory app runs on after the sweep")
	want := uint16(cfgNSTATUS | cfgCONFDONE)
	add("configuration port", in.cfgErr == nil && in.cfg&want == want,
		"CS3 0x07 reads nSTATUS and CONF_DONE high (the configured fabric): 0x%04x %s", in.cfg, core.ErrText(in.cfgErr))
	add("fpga part", slices.Contains(in.factoryRBF, factoryRBFLen),
		"the factory FPGA image is %d bytes, an EP4CE10's: %v", factoryRBFLen, in.factoryRBF)
	var missing []string
	for _, f := range []string{probeRBF, probePullRBF, stickApp} {
		if !in.files[f] {
			missing = append(missing, f)
		}
	}
	add("stick files", len(missing) == 0, "the probe images and the app are on the stick (missing: %v)", missing)
	return cs
}

// factoryRBFs is the lengths of the .rbf files of the factory firmware.
// TRLC-LINKS: REQ-SDS-288
func factoryRBFs(roots []string) []int64 {
	var out []int64
	for _, root := range roots {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(strings.ToLower(p), ".rbf") && !strings.HasPrefix(p, "/usr/bin/siglent/usr/media") {
				out = append(out, info.Size())
			}
			return nil
		})
	}
	return out
}

// shellNames are the processes that hold the inherited descriptors without
// being the factory app: the shells of the boot chain.
var shellNames = map[string]bool{"sh": true, "ash": true, "hush": true, "bash": true, "busybox": true, "init": true}

// kin reports whether pid is this process's ancestor (the stick's startup.sh,
// which holds the descriptors sdsdiag inherited) or its descendant.
// TRLC-LINKS: REQ-SDS-288
func kin(self, pid int) bool {
	up := func(from, to int) bool {
		for i := 0; i < 64 && from > 1; i++ {
			if from = fdinherit.PPid(from); from == to {
				return true
			}
		}
		return false
	}
	return up(self, pid) || up(pid, self)
}

// takeover stops the factory app for good, as the agent's takeover does: STOP
// over its SCPI, the fill counter frozen (the engine idle, never mid-frame),
// SIGKILL to every other holder of /dev/Gpmc that is not a shell nor this
// process's ancestor or descendant, and the
// watchdog, which nothing else pets now. sdsdiag keeps the inherited
// descriptor, so the chip select stays up. It returns the watchdog.
// TRLC-LINKS: REQ-SDS-288
func takeover(vx *vxi11.Client, fd int, log func(string, ...any)) (*keep.Keeper, error) {
	if err := vx.Send("STOP"); err != nil {
		return nil, fmt.Errorf("STOP: %w", err)
	}
	_ = vx.Send("TRMD STOP")
	vx.Close()
	r := gpmc.NewReader(fd)
	frozen := false
	var seen []uint16
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
		ok, s, err := r.FillFrozen(3, 50*time.Millisecond)
		seen = s
		if err == nil && ok {
			frozen = true
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !frozen {
		return nil, fmt.Errorf("the factory app's acquisition did not stop: fill %v", seen)
	}
	log("idle: fill frozen at %v", seen)
	self := os.Getpid()
	killed := 0
	for round := range 4 {
		var left []fdinherit.Holder
		for _, h := range fdinherit.HoldersOf("/dev/Gpmc") {
			if h.PID != self && h.PID != 1 && !kin(self, h.PID) && !shellNames[filepath.Base(h.Exe)] && !shellNames[h.Comm] {
				left = append(left, h)
			}
		}
		if len(left) == 0 {
			break
		}
		for _, h := range left {
			if syscall.Kill(h.PID, syscall.SIGKILL) == nil {
				killed++
				log("SIGKILL pid %d %s (round %d)", h.PID, h.Comm, round+1)
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	if killed == 0 {
		return nil, errors.New("no factory process held /dev/Gpmc")
	}
	wd := keep.NewKeeper(watchdog.New("/dev/watchdog"))
	if err := wd.Acquire(15*time.Second, 15*time.Second); err != nil {
		return nil, fmt.Errorf("watchdog: %w", err)
	}
	log("watchdog acquired")
	return wd, nil
}

// TakeoverGate is TakeoverGate on what the earlier stages found: the
// configuration port read through the inherited /dev/Gpmc, the factory
// images' lengths and the stick's own files.
// TRLC-LINKS: REQ-SDS-288
func (s *stick) TakeoverGate(c *core.Collection) []core.Check {
	dir := filepath.Dir(c.Self)
	fd := fdinherit.Find("/dev/Gpmc")
	cfg, cfgErr := devBus{fd: fd}.Read(planeCS3, cs3Config)
	files := map[string]bool{}
	for _, f := range []string{probeRBF, probePullRBF, stickApp} {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil && len(b) > 0 { //nolint:gosec // G304: the stick's own files
			files[f] = true
		}
	}
	st := core.ProcState(s.pid)
	return TakeoverGate(takeoverInput{
		sweepDone: s.sweepDone, alive: st != "" && st != "T" && st != "t" && st != "Z",
		cfg: cfg, cfgErr: cfgErr, factoryRBF: factoryRBFs(firmwareRoots), files: files,
	})
}

// probeCensus is the census of every FPGA ball through the probe image,
// loaded without and with the pull-ups, as a hal.Census. It keeps both
// censuses for the archive and the board tests.
type probeCensus struct {
	bus    regBus
	dir    string // the stick's files
	balls  []Ball
	a      *Archive
	say    func(string, ...any)
	logf   func(string, ...any)
	census [2]Census
	vs     []Verdict
}

var _ hal.Census = (*probeCensus)(nil)

// Run loads each probe image and takes its census. It writes the FPGA (a
// volatile image), so it refuses the read-only first contact.
// TRLC-LINKS: REQ-SDS-288
func (p *probeCensus) Run(_ context.Context, writes bool) ([]hal.Verdict, error) {
	if !writes {
		return nil, errors.New("the probe census loads the probe images")
	}
	for i, f := range []string{probeRBF, probePullRBF} {
		p.say("takeover: the probe image %s", f)
		image, _ := os.ReadFile(filepath.Join(p.dir, f)) //nolint:gosec // G304: the stick's own files
		t := time.Now()
		if err := configure(p.bus, image, time.Sleep); err != nil {
			p.census[i].Error = "load: " + err.Error()
			p.logf("%s: %v", f, err)
			continue
		}
		p.logf("%s loaded in %v", f, time.Since(t).Round(time.Millisecond))
		p.census[i] = runCensus(p.bus, len(p.balls), time.Sleep)
		p.logf("%s census: %v %s", f, p.census[i].Steps, p.census[i].Error)
		p.a.AddJSON("probe/census-"+strings.TrimSuffix(f, ".rbf")+".json", p.census[i])
	}
	p.vs = verdicts(p.balls, p.census[0], p.census[1])
	out := make([]hal.Verdict, len(p.vs))
	for i, v := range p.vs {
		out[i] = hal.Verdict{Part: v.Part, Verdict: v.Verdict, Detail: v.Detail}
	}
	return out, nil
}

// Takeover is the third stage, run in auto mode after the active stage when
// its gate passes: it takes the scope over, takes the census of every ball
// with the probe images, judges the board's population from it, and runs
// this firmware's app on the board. The scope then needs a power-cycle: the
// factory app is gone.
// TRLC-LINKS: REQ-SDS-288, REQ-SDS-289
func (s *stick) Takeover(c *core.Collection, a *Archive) {
	dir := filepath.Dir(c.Self)
	fd := fdinherit.Find("/dev/Gpmc")
	bus := devBus{fd: fd}
	c.Say("takeover: the factory app stops now; switch the scope off and on when this says DONE")
	var log strings.Builder
	logf := func(f string, args ...any) {
		fmt.Fprintf(&log, "%d "+f+"\n", append([]any{core.MonoMicros()}, args...)...)
	}
	defer func() { a.AddText("takeover/log.txt", log.String()) }()
	wd, err := takeover(s.vx, fd, logf)
	if err != nil {
		logf("takeover: %v", err)
		c.Note("takeover: %v", err)
		return
	}
	defer wd.Disarm()
	c.TookOver = true
	pc := &probeCensus{bus: bus, dir: dir, balls: probeBalls(), a: a, say: c.Say, logf: logf}
	_, _ = pc.Run(context.Background(), true) // writes is set: it does not refuse
	balls, census, vs := pc.balls, pc.census, pc.vs
	if census[1].Error == "" && len(census[1].Off) == len(balls) {
		c.Say("takeover: the front end, trigger, SRAM and panel (the panel LEDs light one by one)")
		t := s.boardTests(bus, balls, vs, logf)
		a.AddJSON("probe/board_tests.json", t)
		vs = append(vs, boardVerdicts(t, s.inventory)...)
	}
	a.AddJSON("probe/verdicts.json", vs)
	a.AddJSON("probe/balls.json", balls)
	a.AddText("probe/report.txt", verdictText(vs, balls, census[0], census[1]))
	for _, v := range vs {
		c.Note("probe: %s: %s", v.Part, v.Verdict)
	}
	c.Say("takeover: this firmware's app on the board (%v)", core.AppRun)
	c.Note("%s", c.RunApp(a, filepath.Join(dir, stickApp), []string{"SCOPE_GPMC=/dev/Gpmc"}))
	c.Note("screen with the app: %s", core.CollectScreen(a, "app"))
}

// boardTests runs, on the pull-up probe image, the tests that drive the
// board as the acquisition images and the factory firmware do: each
// channel's front end, the trigger comparator, the SRAM, the panel chain and
// key interrupt, the ARM's outputs to the FPGA and the LED walk. vs names
// the converter cores present.
// TRLC-LINKS: REQ-SDS-290, REQ-SDS-291
func (s *stick) boardTests(bus regBus, balls []Ball, vs []Verdict, logf func(string, ...any)) BoardTests {
	var t BoardTests
	p, _, err := openProbe(bus, len(balls), time.Sleep)
	if err != nil {
		t.Error = err.Error()
		return t
	}
	defer func() {
		_ = bus.Write(planeCS1, prLegs, 0)
		_ = bus.Write(planeCS1, prCtl, 0)
		_ = setOffsetDAC(bus, 0, dacMid)
		_ = setOffsetDAC(bus, 1, dacMid)
	}()
	// D1, D2, G2 and A11 as the acquisition images hold them, every
	// converter running
	_ = bus.Write(planeCS1, prCtl, ctlOthers)
	_ = bus.Write(planeCS1, prLegs, allLegs)
	bn := &bench{b: bus, p: p, cores: map[string][]int{}, sleep: time.Sleep, log: logf}
	for _, v := range vs {
		core, ok := strings.CutPrefix(v.Part, "converter ")
		if !ok || v.Verdict != vPresent {
			continue
		}
		for i, b := range balls {
			if strings.HasPrefix(b.Name, core+".") {
				bn.cores[core] = append(bn.cores[core], i)
			}
		}
		ch := int(core[len(core)-1] - '1')
		bn.chans[ch] = append(bn.chans[ch], core)
	}
	sp, err := openFrontEnd()
	if err != nil {
		t.FrontEnd[0].Error, t.FrontEnd[1].Error = err.Error(), err.Error()
	} else {
		defer sp.Close()
		bn.f = sp
		for ch := range 2 {
			if len(bn.chans[ch]) == 0 {
				t.FrontEnd[ch].Error = "no converter of the channel is present"
				continue
			}
			t.FrontEnd[ch] = bn.frontEndTests(ch)
		}
		// both channels at their signal detents, centred there
		var det [2]int
		var centre [2]uint16
		for ch := range 2 {
			det[ch], centre[ch] = 6, dacMid
			for d, dt := range detents {
				if fr := t.FrontEnd[ch]; dt.vdiv == fr.Signal && d < len(fr.Detents) && fr.Detents[d].Error == "" {
					det[ch], centre[ch] = d, fr.Detents[d].Center
				}
			}
		}
		t.Trigger = bn.triggerTests(balls, det, centre)
		for ch := range 2 {
			t.Edges[ch] = bn.edges(balls, t.Trigger, ch, det[ch], centre[ch])
		}
	}
	logf("front end and trigger measured")
	t.SRAM = p.sramTest()
	logf("sram: %+v", t.SRAM)
	t.Panel = p.panelTests()
	logf("panel: %+v", t.Panel)
	t.GPIO113, t.PWM = p.armOutputs(balls)
	t.I2C = i2cScan()
	logf("i2c answers: %v", t.I2C)
	logf("gpio113 %+v pwm %+v", t.GPIO113, t.PWM)
	if err := ledWalk(bus, time.Sleep); err != nil {
		logf("leds: %v", err)
	}
	return t
}

// edges runs the edge tests on channel ch: its pair E1 core, triggered by its
// comparator at the middle of the levels where it toggled on the square.
// TRLC-LINKS: REQ-SDS-290
func (bn *bench) edges(balls []Ball, tr TriggerResult, ch, det int, centre uint16) EdgeResult {
	core := []string{"E1.CH1", "E1.CH2"}[ch]
	if _, ok := bn.cores[core]; !ok {
		return EdgeResult{Error: "converter " + core + " is not present"}
	}
	b := tr.Comparator[ch]
	if b == "" {
		return EdgeResult{Error: "no trigger comparator for the channel"}
	}
	ball := slices.IndexFunc(balls, func(x Ball) bool { return x.Ball == b })
	p := tr.Patterns[b][0]
	var band []int
	for i := 1; i < len(p); i++ {
		if p[i] == '~' {
			band = append(band, i)
		}
	}
	if ball < 0 || len(band) == 0 {
		return EdgeResult{Error: "the comparator's levels are not known"}
	}
	level := uint16(min(band[len(band)/2]*2048, 0xffff)) //nolint:gosec // G115: a DAC code
	r := bn.edgeTests(ch, ball, det, centre, level)
	r.Ball = b
	return r
}
