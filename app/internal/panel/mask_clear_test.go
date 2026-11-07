// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import (
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// installTestMask puts a two-column mask in the engine with its test in mode.
// TRLC-LINKS: REQ-SDS-139, REQ-SDS-218
func installTestMask(c *Controller, mode int) {
	c.eng.SetMask(&engine.Mask{Lo: []uint8{10, 10}, Hi: []uint8{200, 200}, WinCols: 2})
	c.eng.SetMaskMode(mode)
}

// Clear mask (the COMPARE sheet, the phone's Menus) removes the mask: its
// test goes off and its counts are zeroed. With nothing to clear it is not
// available.
// TRLC-LINKS: REQ-SDS-139, REQ-SDS-218
func TestClearMaskRow(t *testing.T) {
	c, eng, _ := newC(t)
	withClock(c)
	if res := c.SheetAction("cmp.clear_mask", "clear"); res.OK {
		t.Fatalf("with no mask: %+v", res)
	}
	installTestMask(c, 2)
	eng.set(func(st *engine.Stats) { st.MaskPass, st.MaskFail = 40, 2 })
	res := c.SheetAction("cmp.clear_mask", "clear")
	st := eng.Snapshot()
	if !res.OK || st.MaskSet || st.MaskMode != 0 || st.MaskPass != 0 || st.MaskFail != 0 || !strings.HasPrefix(res.Notice, "Mask cleared") {
		t.Fatalf("clear: %+v, the engine's mask %v, mode %d, pass %d, fail %d", res, st.MaskSet, st.MaskMode, st.MaskPass, st.MaskFail)
	}
}

// COMPARE's MASK row push: a mask built with its test off counts, counting
// stops on a fail, and after stop on fail the push clears the mask, so it
// takes three pushes at most and no menu.
// TRLC-LINKS: REQ-SDS-139, REQ-SDS-218
func TestCompareMaskPushClears(t *testing.T) {
	c, eng, _ := newC(t)
	withClock(c)
	c.mu.Lock()
	c.ui.cmpRow = cmpMask
	c.mu.Unlock()
	installTestMask(c, 0)
	for _, want := range []struct {
		text string
		mode int
	}{{"push: count", 1}, {"push: stop on fail", 2}} {
		if got := cmpActionText(c.snap(), cmpMask); got != want.text {
			t.Fatalf("before the push to mode %d: %q, want %q", want.mode, got, want.text)
		}
		c.cmpAction()
		if st := eng.Snapshot(); !st.MaskSet || st.MaskMode != want.mode {
			t.Fatalf("after %q: mask %v mode %d", want.text, st.MaskSet, st.MaskMode)
		}
	}
	if got := cmpActionText(c.snap(), cmpMask); got != "push: clear mask" {
		t.Fatalf("stopping on a fail, the push says %q", got)
	}
	c.cmpAction()
	if st := eng.Snapshot(); st.MaskSet || st.MaskMode != 0 || !strings.HasPrefix(c.View().Notice, "Mask cleared") {
		t.Fatalf("the third push: mask %v mode %d, notice %q", st.MaskSet, st.MaskMode, c.View().Notice)
	}
	if got := cmpActionText(c.snap(), cmpMask); !strings.HasPrefix(got, "push: build") {
		t.Fatalf("with no mask the push says %q", got)
	}
}

// A mask build running when the mask is cleared installs nothing when it
// ends, though its frames would make one.
// TRLC-LINKS: REQ-SDS-139
func TestClearStopsAMaskBuild(t *testing.T) {
	c, eng, _ := newC(t)
	var seq uint64
	c.SetFrameSource(func(fn func(*sample.Frame)) {
		seq++
		n := 64
		sig := make([]uint8, n)
		for i := range sig {
			sig[i] = 100
			if i >= n/2 {
				sig[i] = 180
			}
		}
		fn(&sample.Frame{Seq: seq, WinPoints: n, TriggerAt: float64(n / 2), SampleS: 2e-9, Snapshot: sample.Settings{TdivS: 1e-6}, Traces: []sample.Trace{{Codes8: sig, Valid: n}, {Ch: 1, Codes8: sig, Valid: n}}})
	})
	c.mu.Lock()
	gen := c.maskGen
	c.mu.Unlock()
	c.clearMask()
	c.maskBuildRun(gen, 8, 1, 2, 0, .5)
	if eng.Snapshot().MaskSet {
		t.Fatal("a build started before Clear mask installed its mask")
	}
	c.mu.Lock()
	gen = c.maskGen
	c.mu.Unlock()
	c.maskBuildRun(gen, 8, 1, 2, 0, .5)
	if !eng.Snapshot().MaskSet {
		t.Fatal("a build after the clear installed nothing: the test's frames make no mask")
	}
}

// DEFAULT SETUP clears the mask and the serial trigger's match: no bytes,
// any I2C address and direction, as a trigger never armed; the bus settings
// return to power-on with the decoder (SENT showed the last bus's rate).
// TRLC-LINKS: REQ-SDS-139, REQ-SDS-013
func TestDefaultSetupClearsMatchAndMask(t *testing.T) {
	c, eng, _ := newC(t)
	withClock(c)
	c.SheetSet("bus.protocol", nil, "I2C")
	c.SheetSet("bus.i2c_address", nil, "0x50")
	c.SheetSet("bus.protocol", nil, "UART")
	c.mu.Lock()
	c.decBaud, c.decChA, c.decChB, c.decCPOL = 999873, 1, 0, true // the bus found before
	c.mu.Unlock()
	if res := c.SheetSet("bus.match", nil, "AA"); !res.OK {
		t.Fatalf("match: %+v", res)
	}
	installTestMask(c, 1)
	res := c.SheetAction("sys.default", "")
	p := eng.SerialConfig()
	if !res.OK || len(p.Bytes) != 0 || len(p.Pattern) != 0 || p.Addr != -1 || p.RW != 2 || rowText(c, "bus.match") != "any byte" {
		t.Fatalf("Default setup: %+v, the match %+v, the row %q", res, p, rowText(c, "bus.match"))
	}
	if st := eng.Snapshot(); st.MaskSet || st.MaskMode != 0 {
		t.Fatalf("Default setup kept the mask: set %v mode %d", st.MaskSet, st.MaskMode)
	}
	// the bus settings return to power-on: a bus found later starts afresh
	c.mu.Lock()
	baud, chA, chB, cpol, cpha := c.decBaud, c.decChA, c.decChB, c.decCPOL, c.decCPHA
	c.mu.Unlock()
	if baud != 115200 || chA != 0 || chB != 1 || cpol || cpha {
		t.Fatalf("bus after default setup: %d baud, C%d/C%d, cpol %v cpha %v", baud, chA+1, chB+1, cpol, cpha)
	}
}

// A master's mask is built from the live raw captures after it, in their
// geometry, as the MASK row builds one: on the SRAM images the averaged master
// is a filtered record at 256 ns while the live captures are peak-detect
// readouts at 488 ns over another window, and averaging flattens the ringing
// every raw capture shows. Averaged captures still in flight are not part of
// it, a live capture passes it, the test starts in the master's mode, and a
// clear while it builds drops it.
// TRLC-LINKS: REQ-SDS-218, REQ-SDS-139
func TestMasterMaskFromLiveCaptures(t *testing.T) {
	wave := func(tS float64) uint8 { // a UART-like line: 8.68 us bits
		if bit := int(math.Floor(tS/8.68e-6)) + 1000; bit%3 == 0 || bit%7 == 1 {
			return 60
		}
		return 200
	}
	frame := func(n, win int, edge, sampleS float64, peak bool, filter string) *sample.Frame {
		sig := make([]uint8, n)
		for i := range sig {
			sig[i] = wave((float64(i) - edge) * sampleS)
		}
		f := &sample.Frame{WinPoints: win, TriggerAt: edge, SampleS: sampleS, Cond: sample.Conditioning{Filter: filter}, Snapshot: sample.Settings{TdivS: 100e-6}, Traces: []sample.Trace{{Codes8: sig, Valid: n}, {Ch: 1, Codes8: sig, Valid: n}}}
		if peak {
			f.Content, f.Flags = sample.PeakPairs, sample.Ordered
		}
		return f
	}
	master := frame(4721, 3906, 2408, 256e-9, false, "CIC3 + compensated FIR; block average 64/64 (waveform aligned)")
	live := frame(4096, 2049, 1355, 488e-9, true, "peak-detect display readout")
	start := func(prevMask int, feed func(seq uint64) *sample.Frame) (*Controller, *fakeEng) {
		c, eng, _ := newC(t)
		var seq uint64
		var mu sync.Mutex
		c.SetFrameSource(func(fn func(*sample.Frame)) {
			mu.Lock()
			seq++
			f := *feed(seq)
			f.Seq = seq
			mu.Unlock()
			fn(&f)
		})
		c.mu.Lock()
		c.maskN = 8
		c.stack = &stackState{n: 64, src: 0, prevMode: 0, prevAvg: 16, prevMask: prevMask}
		c.mu.Unlock()
		c.stackFinish(master)
		return c, eng
	}
	built := func(eng *fakeEng) bool {
		for range 100 {
			if eng.Snapshot().MaskSet {
				return true
			}
			time.Sleep(20 * time.Millisecond)
		}
		return false
	}

	// the averaged captures first, then the live ones
	c, eng := start(2, func(seq uint64) *sample.Frame {
		if seq <= 3 {
			return master
		}
		return live
	})
	if !built(eng) {
		t.Fatal("no mask from the live captures after the master")
	}
	m := eng.mask
	if m == nil || !m.FrameIdent || m.WinCols != 2049 || m.SampleS != 488e-9 || !m.PeakDetect || m.TdivS != 100e-6 || eng.Snapshot().MaskMode != 2 {
		t.Fatalf("the master's mask %+v, test mode %d: want the live geometry, stop on fail", m, eng.Snapshot().MaskMode)
	}
	left := int(math.Round(live.TriggerAt - float64(m.WinCols)*0.5))
	for j := range m.WinCols {
		if v := live.Codes8(0)[left+j]; v < m.Lo[j] || v > m.Hi[j] {
			t.Fatalf("column %d: the live sample %d is outside its master's mask [%d, %d]", j, v, m.Lo[j], m.Hi[j])
		}
	}
	if err := c.clearRefs(); err != nil {
		t.Fatal(err)
	}
	if got := rowByID["cmp.stack_master"].status(c.snap()); got != "none" {
		t.Fatalf("after Clear refs the master row says %q", got)
	}

	// Clear mask while it builds: nothing is installed
	c, eng = start(0, func(uint64) *sample.Frame { return live })
	c.clearMask()
	if built(eng) {
		t.Fatal("Clear mask while the master's mask was built: installed anyway")
	}
}

// DEFAULT SETUP returns the channels to full bandwidth and the right way up:
// no 20 MHz limit stays on after it.
// TRLC-LINKS: REQ-SDS-136
func TestDefaultSetupClearsBWLimitAndInvert(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	for _, row := range []string{"ch1.bwl", "ch2.bwl", "ch1.invert", "ch2.invert"} {
		if res := c.SheetSet(row, nil, "On"); !res.OK {
			t.Fatalf("%s: %+v", row, res)
		}
	}
	if res := c.SheetAction("sys.default", ""); !res.OK {
		t.Fatalf("default setup: %+v", res)
	}
	for _, row := range []string{"ch1.bwl", "ch2.bwl", "ch1.invert", "ch2.invert"} {
		if got := rowText(c, row); got != "Off" {
			t.Errorf("%s after default setup: %q", row, got)
		}
	}
}

// DEFAULT SETUP returns the pulse, slope and video settings to their power-on
// values: a pulse width "> 13 us", a 12-30 ns slope and NTSC line 10 set from
// the web do not stay after it.
// TRLC-LINKS: REQ-SDS-136
func TestDefaultSetupResetsTheQualifiers(t *testing.T) {
	c, eng, _ := newC(t)
	eng.SetPulseParams(0.3, 0, 13000, 2)
	eng.SetSlopeParams(0.1, 0.9, 12, 30, 3)
	eng.SetVideoParams(1, 10, false)
	c.SheetSet("trig.type", nil, "Pulse")
	if rowText(c, "trig.pulse_max") != "13us" {
		t.Fatalf("the web's pulse width: %q", rowText(c, "trig.pulse_max"))
	}
	c.DefaultSetup()
	q := eng.Snapshot().TrigQual
	if q.PulseLvl != 0.5 || q.PulseMinNs != 100 || q.PulseMaxNs != 1000 || q.PulseCond != 0 || q.SlopeLo != 0.2 || q.SlopeHi != 0.8 ||
		q.SlopeMinNs != 100 || q.SlopeMaxNs != 1000 || q.SlopeCond != 0 || q.VideoStd != 0 || q.VideoLine != 0 || !q.VideoNeg {
		t.Fatalf("after default setup: %+v", q)
	}
	for row, want := range map[string]string{
		"trig.type": "Edge", "trig.pulse_when": "Any", "trig.pulse_max": "1us", "trig.slope_when": "Any",
		"trig.video_std": "PAL", "trig.video_sync": "Negative",
	} {
		if got := rowText(c, row); got != want {
			t.Errorf("%s after default setup: %q, want %q", row, got, want)
		}
	}
}

// A mask build that gets no locked frame (the trigger off the signal) gives
// up within seconds and its row says why, on the sheet and the phone too, not
// "building" for minutes (2.5 at 128 frames) and then "none".
// TRLC-LINKS: REQ-SDS-139
func TestMaskBuildSaysWhyItFailed(t *testing.T) {
	c, _, _ := newC(t)
	defer func(w time.Duration) { maskNoLockWait = w }(maskNoLockWait)
	maskNoLockWait = 300 * time.Millisecond
	var seq uint64
	c.SetFrameSource(func(fn func(*sample.Frame)) {
		seq++
		f := smallFrame()
		f.Seq, f.TriggerAt, f.Status.Triggered = seq, -1, false // free-running: no trigger located
		fn(f)
	})
	c.mu.Lock()
	c.maskBuilding = true
	gen := c.maskGen
	c.mu.Unlock()
	start := time.Now()
	c.maskBuildRun(gen, 8, 1, 2, 0, .5)
	got := rowModel(c.snap(), rowByID["cmp.build_mask"]).Text
	if d := time.Since(start); d > 2*time.Second || got != "build failed - no locked frames: put the trigger level on the signal" {
		t.Fatalf("after %v the row says %q", d.Round(time.Millisecond), got)
	}
}

// DEFAULT SETUP returns the mask build's frames and tolerance and super-res's
// channel, grid and stop to their factory values (a restart keeps them), and
// the trigger level to 0 V with no display invert wired (the level's reset
// does not depend on the invert).
// TRLC-LINKS: REQ-SDS-136
func TestDefaultSetupResetsMaskBuildAndSuperres(t *testing.T) {
	c, eng, _ := newC(t) // no inverter
	for row, text := range map[string]string{
		"cmp.mask_frames": "128", "cmp.mask_tol": "12pt/20cd", "acq.sr_channel": "C2",
		"acq.sr_grid": "x64", "acq.sr_stop": "Time",
	} {
		if res := c.SheetSet(row, nil, text); !res.OK {
			t.Fatalf("%s %s: %+v", row, text, res)
		}
	}
	eng.SetTrigLevelCode(30000)
	c.DefaultSetup()
	for row, want := range map[string]string{
		"cmp.mask_frames": "32", "cmp.mask_tol": "5pt/8cd", "acq.sr_channel": "C1",
		"acq.sr_grid": "x32", "acq.sr_stop": "Bits", "acq.sr_target": "4",
	} {
		if got := rowText(c, row); got != want {
			t.Errorf("%s after default setup: %q, want %q", row, got, want)
		}
	}
	if got := eng.Snapshot().TrigCode; got != 31437 {
		t.Errorf("trigger level code after default setup %d, want 31437 (0 V)", got)
	}
}

// fakeEng's precision rate: DEFAULT SETUP sets the engine's own.
// TRLC-LINKS: REQ-SDS-136
func (f *fakeEng) SetPrecisionRate(hz float64) float64 {
	f.set(func(st *engine.Stats) { st.PrecisionRateHz = hz })
	return hz
}

// DEFAULT SETUP restores every setting to power-on, each row's own default
// (its Position push) included: the averaging count, ERES length, precision
// rate and roll mode, the chosen measurements, the FFT source, the master's
// depth and the stream's stop.
// TRLC-LINKS: REQ-SDS-136
func TestDefaultSetupRestoresPowerOnSettings(t *testing.T) {
	c, eng, _ := newC(t)
	for id, v := range map[string]int{
		"acq.averages": 4, "acq.eres": 3, "cmp.master_depth": 16, "meas.freq": 1, "meas.pkpk": 1,
		"view.fft_source": 1, "bus.stream_trigger": 1, "time.roll": 1,
	} {
		if err := rowByID[id].set(c, v); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if err := rowByID["bus.stream_value"].setN(c, 0x41); err != nil {
		t.Fatal(err)
	}
	eng.SetPrecisionRate(3906250)
	c.DefaultSetup()
	for id, want := range map[string]string{
		"acq.averages": "16", "acq.eres": "1", "cmp.master_depth": "64", "meas.freq": "Off",
		"meas.pkpk": "Off", "view.fft_source": "CH1", "bus.stream_trigger": "On error", "bus.stream_value": "0x00", "time.roll": "Chunked",
	} {
		if got := rowText(c, id); got != want {
			t.Errorf("%s after default setup: %q, want %q", id, got, want)
		}
	}
	if got := eng.Snapshot().PrecisionRateHz; got != 31.25e6 {
		t.Errorf("precision rate after default setup: %g, want 31.25e6", got)
	}
}
