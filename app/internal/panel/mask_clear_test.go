// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// installTestMask puts a two-column mask in the engine with its test in mode.
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
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		seq++
		n := 64
		sig := make([]uint8, n)
		for i := range sig {
			sig[i] = 100
			if i >= n/2 {
				sig[i] = 180
			}
		}
		fn(&engine.Frame{Seq: seq, C1: sig, C2: sig, Valid: n, WinCols: n, EdgeX: float64(n / 2), SampleS: 2e-9, TdivS: 1e-6})
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
// any I2C address and direction, as a trigger never armed.
// TRLC-LINKS: REQ-SDS-139, REQ-SDS-013
func TestDefaultSetupClearsMatchAndMask(t *testing.T) {
	c, eng, _ := newC(t)
	withClock(c)
	c.SheetSet("bus.protocol", nil, "I2C")
	c.SheetSet("bus.i2c_address", nil, "0x50")
	c.SheetSet("bus.protocol", nil, "UART")
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
}

// The master's mask is built in the geometry of the live captures the test
// judges. On the SRAM images the averaged master is a filtered record at
// 256 ns while the live captures are peak-detect readouts at 488 ns over
// another window, and a mask stamped with the master's geometry had every
// live capture skipped. Mapped by time from the trigger, a live capture passes
// its own master's mask. A clear while the master waits drops its mask, and
// Clear refs forgets the master that was REF A.
// TRLC-LINKS: REQ-SDS-218, REQ-SDS-139
func TestMasterMaskInLiveGeometry(t *testing.T) {
	wave := func(tS float64) uint8 { // a UART-like line: 8.68 us bits
		if bit := int(math.Floor(tS/8.68e-6)) + 1000; bit%3 == 0 || bit%7 == 1 {
			return 60
		}
		return 200
	}
	frame := func(n, win int, edge, sampleS float64, peak bool, filter string) *engine.Frame {
		sig := make([]uint8, n)
		for i := range sig {
			sig[i] = wave((float64(i) - edge) * sampleS)
		}
		return &engine.Frame{C1: sig, C2: sig, Valid: n, WinCols: win, EdgeX: edge, SampleS: sampleS, TdivS: 100e-6, PeakDetect: peak, Filter: filter}
	}
	master := frame(4721, 3906, 2408, 256e-9, false, "CIC3 + compensated FIR; block average 64/64 (waveform aligned)")
	live := frame(4096, 2049, 1355, 488e-9, true, "peak-detect display readout")
	finish := func(c *Controller) {
		c.mu.Lock()
		c.stack = &stackState{n: 64, src: 0, prevMode: 0, prevAvg: 16}
		c.mu.Unlock()
		c.stackFinish(master)
	}

	c, eng, _ := newC(t)
	withClock(c)
	finish(c)
	if eng.Snapshot().MaskSet {
		t.Fatal("a mask before any live capture: built in the averaged geometry")
	}
	c.masterMaskFrame(master) // an averaged capture still in flight: not the geometry to judge
	// one made while Average was switched off: the filtered record, its average not named
	c.masterMaskFrame(frame(4721, 3906, 2408, 256e-9, false, "CIC3 + compensated FIR"))
	c.masterMaskFrame(live)
	c.masterMaskFrame(live)
	if eng.Snapshot().MaskSet {
		t.Fatal("a mask before the live geometry held for three captures")
	}
	c.masterMaskFrame(live)
	m := eng.mask
	if m == nil || !m.FrameIdent || m.WinCols != 2049 || m.SampleS != 488e-9 || !m.PeakDetect || m.TdivS != 100e-6 || eng.Snapshot().MaskMode != 1 {
		t.Fatalf("the master's mask %+v, test mode %d: want the live geometry", m, eng.Snapshot().MaskMode)
	}
	left := int(math.Round(live.EdgeX - float64(m.WinCols)*0.5))
	for j := 0; j < m.WinCols; j++ {
		if v := live.C1[left+j]; v < m.Lo[j] || v > m.Hi[j] {
			t.Fatalf("column %d: the live sample %d is outside its master's mask [%d, %d]", j, v, m.Lo[j], m.Hi[j])
		}
	}
	c.clearRefs()
	if got := rowByID["cmp.stack_master"].status(c.snap()); got != "none" {
		t.Fatalf("after Clear refs the master row says %q", got)
	}

	c, eng, _ = newC(t)
	withClock(c)
	finish(c)
	c.clearMask()
	for range 3 {
		c.masterMaskFrame(live)
	}
	if eng.Snapshot().MaskSet {
		t.Fatal("Clear mask while the master waited: its mask was built anyway")
	}
}
