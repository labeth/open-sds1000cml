// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/labeth/open-sds1000cml/ota/internal/fdinherit"
	"github.com/labeth/open-sds1000cml/ota/internal/gpmc"
	"github.com/labeth/open-sds1000cml/ota/internal/vxi11"
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
	sweepDone  bool   // the active sweep ran to its end and put the settings back
	alive      bool   // the factory app runs on after the sweep
	cfg        uint16 // CS3 0x07, the configuration port
	cfgErr     error
	factoryRBF []int64 // the lengths of the factory firmware's .rbf files
	files      map[string]bool
}

// TakeoverGate is the checks the takeover stage needs before it stops the
// factory app for good: the active stage poked the factory app and it acted
// as on the SDS1102CML+ (every step done, the settings back, the app running
// on), the FPGA's configuration port is where this firmware loads its images
// (CS3 0x07, the fabric configured), the factory image is a bitstream for the
// same FPGA, and the stick carries the probe images and the app.
// TRLC-LINKS: REQ-SDS-288
func TakeoverGate(in takeoverInput) []Check {
	var cs []Check
	add := func(name string, ok bool, f string, args ...any) {
		cs = append(cs, Check{Name: name, OK: ok, Detail: fmt.Sprintf(f, args...)})
	}
	add("active stage", in.sweepDone, "the sweep stepped every setting and put the settings back")
	add("factory app after", in.alive, "the factory app runs on after the sweep")
	want := uint16(cfgNSTATUS | cfgCONFDONE)
	add("configuration port", in.cfgErr == nil && in.cfg&want == want,
		"CS3 0x07 reads nSTATUS and CONF_DONE high (the configured fabric): 0x%04x %s", in.cfg, errText(in.cfgErr))
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
func takeover(vx *vxi11.Client, fd int, log func(string, ...any)) (*watchdog.Device, error) {
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
	wd := watchdog.New("/dev/watchdog")
	if err := wd.Acquire(15*time.Second, 15*time.Second); err != nil {
		return nil, fmt.Errorf("watchdog: %w", err)
	}
	log("watchdog acquired")
	return wd, nil
}

// takeoverStage is the third stage, run in auto mode after the active stage:
// when its gate passes, it takes the scope over, loads the probe image
// without and with the pull-ups and takes the census of every ball, judges
// the board's population from it, and runs this firmware's app on the board.
// The scope then needs a power-cycle: the factory app is gone.
// TRLC-LINKS: REQ-SDS-288, REQ-SDS-289
func (c *collection) takeoverStage(a *Archive, vx *vxi11.Client, pid int) {
	dir := filepath.Dir(c.self)
	fd := fdinherit.Find("/dev/Gpmc")
	bus := devBus{fd: fd}
	cfg, cfgErr := bus.Read(planeCS3, cs3Config)
	files := map[string]bool{}
	images := map[string][]byte{}
	for _, f := range []string{probeRBF, probePullRBF, stickApp} {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil && len(b) > 0 { //nolint:gosec // G304: the stick's own files
			files[f] = true
			if f != stickApp {
				images[f] = b
			}
		}
	}
	st := procState(pid)
	checks := TakeoverGate(takeoverInput{
		sweepDone: c.sweepDone, alive: st != "" && st != "T" && st != "t" && st != "Z",
		cfg: cfg, cfgErr: cfgErr, factoryRBF: factoryRBFs(firmwareRoots), files: files,
	})
	a.AddJSON("takeover/gate.json", checks)
	if ok, why := passed(checks); !ok {
		c.note("takeover gate: the takeover stage does not run (%s)", why)
		c.say("no takeover: %s", why)
		return
	}
	c.note("takeover gate: every check passed - the takeover stage runs")
	c.say("takeover: the factory app stops now; switch the scope off and on when this says DONE")
	var log strings.Builder
	logf := func(f string, args ...any) { fmt.Fprintf(&log, "%d "+f+"\n", append([]any{monoMicros()}, args...)...) }
	defer func() { a.AddText("takeover/log.txt", log.String()) }()
	wd, err := takeover(vx, fd, logf)
	if err != nil {
		logf("takeover: %v", err)
		c.note("takeover: %v", err)
		return
	}
	defer wd.Disarm()
	balls := probeBalls()
	var census [2]Census
	for i, f := range []string{probeRBF, probePullRBF} {
		c.say("takeover: the probe image %s", f)
		t := time.Now()
		if err := configure(bus, images[f], time.Sleep); err != nil {
			census[i].Error = "load: " + err.Error()
			logf("%s: %v", f, err)
			continue
		}
		logf("%s loaded in %v", f, time.Since(t).Round(time.Millisecond))
		census[i] = runCensus(bus, len(balls), time.Sleep)
		logf("%s census: %v %s", f, census[i].Steps, census[i].Error)
		a.AddJSON("probe/census-"+strings.TrimSuffix(f, ".rbf")+".json", census[i])
	}
	vs := verdicts(balls, census[0], census[1])
	a.AddJSON("probe/verdicts.json", vs)
	a.AddJSON("probe/balls.json", balls)
	a.AddText("probe/report.txt", verdictText(vs, balls, census[0], census[1]))
	for _, v := range vs {
		if v.Verdict != vPresent || strings.HasPrefix(v.Part, "converter") {
			c.note("probe: %s: %s", v.Part, v.Verdict)
		}
	}
	c.say("takeover: this firmware's app on the board (%v)", appRun)
	c.note("%s", c.runApp(a, filepath.Join(dir, stickApp)))
	c.note("screen with the app: %s", collectScreen(a, "app"))
}
