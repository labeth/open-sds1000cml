// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
	"open-sds/app/internal/panel/wsview"
)

// simFE is the fake front end with the offset reaching the engine's offset
// code, as the real one's does, so a simulated capture places the trace
// where the V/div and offset put it.
type simFE struct {
	*fakeFE
	eng *fakeEng
}

// TRLC-LINKS: REQ-SDS-224
func (s *simFE) SetOffset(ch int, volts float64) uint16 {
	code := s.fakeFE.SetOffset(ch, volts)
	s.eng.set(func(st *engine.Stats) {
		if ch == 0 {
			st.OffC1 = code
		} else {
			st.OffC2 = code
		}
	})
	return code
}

// simBench is a controller whose frames are captures of a simulated signal
// (volts at time t on channel ch, the timebase in use given) at the fake
// engine's timebase and the front end's V/div and offset: 25 codes a
// division about 128, clamped at the ADC's 0 and 255.
type simBench struct {
	c   *Controller
	eng *fakeEng
	fe  *simFE
	mu  sync.Mutex
	seq uint64
}

// TRLC-LINKS: REQ-SDS-224
func newSimBench(t *testing.T, sig func(t, tdiv float64, ch int) float64) *simBench {
	t.Helper()
	eng := newEng()
	fe := &simFE{fakeFE: &fakeFE{idx: [2]int{8, 8}}, eng: eng}
	c := New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.decode(idle(), true)
	b := &simBench{c: c, eng: eng, fe: fe}
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(b.capture(sig)) })
	return b
}

// capture is one simulated frame at the settings in use.
// TRLC-LINKS: REQ-SDS-224
func (b *simBench) capture(sig func(t, tdiv float64, ch int) float64) *engine.Frame {
	st := b.eng.Snapshot()
	band, _ := engine.PlanTdiv(st.TdivS)
	dt := band.CaptureIntervalNs() * 1e-9
	n := min(int(10*st.TdivS/dt), 20000)
	idx, _ := b.fe.Snapshot()
	b.mu.Lock()
	b.seq++
	f := &engine.Frame{Valid: n, SampleS: dt, TdivS: st.TdivS, Seq: b.seq, EdgeX: -1}
	b.mu.Unlock()
	for ch, offCode := range [2]uint16{st.OffC1, st.OffC2} {
		vpc := analog.AnalogVdiv(idx[ch]) / 25
		offV := b.fe.OffsetVolts(ch, offCode)
		codes := make([]uint8, n)
		for i := range codes {
			v := sig(float64(i)*dt, st.TdivS, ch)
			codes[i] = uint8(min(max(math.Round(128+(v+offV)/vpc), 0), 255))
		}
		if ch == 0 {
			f.C1 = codes
		} else {
			f.C2 = codes
		}
	}
	return f
}

// run is AUTO in workspace ws (on view, -1 its own) to its end, and the
// notice it left.
// TRLC-LINKS: REQ-SDS-224
func (b *simBench) run(ws wsview.WS, view int) string {
	b.c.mu.Lock()
	b.c.ui.ws = ws
	if view >= 0 {
		b.c.viewMode = view
	}
	b.c.mu.Unlock()
	b.c.runAutoset(make(chan struct{}))
	b.c.mu.Lock()
	defer b.c.mu.Unlock()
	return b.c.ui.notice
}

// square is a square wave of frequency f between lo and hi volts.
// TRLC-LINKS: REQ-SDS-224
func square(f, lo, hi float64) func(t float64) float64 {
	return func(t float64) float64 {
		if math.Mod(t*f, 1) < 0.5 {
			return hi
		}
		return lo
	}
}

// vdivOf is a channel's V/div on the bench.
// TRLC-LINKS: REQ-SDS-224
func (b *simBench) vdivOf(ch int) float64 {
	idx, _ := b.fe.Snapshot()
	return analog.Detents[idx[ch]].VdivV
}

// onScreen checks the latest capture of ch is not clipped and its swing is
// centred within tol divisions.
// TRLC-LINKS: REQ-SDS-224, REQ-SDS-225
func (b *simBench) onScreen(t *testing.T, ch int, tol float64) {
	t.Helper()
	if b.c.clipped(ch) {
		t.Fatalf("C%d left clipped", ch+1)
	}
	if e := b.c.rawCenterErr(ch); math.Abs(e) > tol {
		t.Fatalf("C%d centred %.2f div off", ch+1, e)
	}
}

// A 3.3 V, 1 MHz square on both channels: SCOPE shows two cycles with the
// swing on 6.6 of the eight divisions (500 mV/div), a rising edge at 50 %
// at mid-screen, and says so.
// TRLC-LINKS: REQ-SDS-224
func TestAutoScopeFramesTwoCycles(t *testing.T) {
	t.Parallel()
	sq := square(1e6, 0, 3.3)
	b := newSimBench(t, func(tt, _ float64, _ int) float64 { return sq(tt) })
	note := b.run(wsview.Scope, -1)
	st := b.eng.Snapshot()
	if math.Abs(st.TdivS-200e-9) > 1e-12 {
		t.Fatalf("SCOPE timebase %g, want 200ns (two cycles of 1 MHz)", st.TdivS)
	}
	for ch := 0; ch < 2; ch++ {
		if v := b.vdivOf(ch); v != 0.5 {
			t.Fatalf("C%d %g V/div, want 0.5 (3.3 V on 6.6 divisions)", ch+1, v)
		}
		b.onScreen(t, ch, 0.4)
	}
	if !st.TrigRising || st.TrigPosFrac != 0.5 || st.TrigType != 0 {
		t.Fatalf("trigger rising %v at %g type %d, want a rising edge at mid-screen", st.TrigRising, st.TrigPosFrac, st.TrigType)
	}
	if lvl := (31437 - float64(st.TrigCode)) / 911; math.Abs(lvl-1.65) > 0.2 {
		t.Fatalf("trigger level %.2f V, want the 50 %% point 1.65 V", lvl)
	}
	if note != "AUTO: 2 cycles, C1 500mV/div, C2 500mV/div" {
		t.Fatalf("notice %q", note)
	}
}

// MEASURE takes ten cycles and fits the swing as large as stays on the
// screen: a 1.45 V swing goes on 7.25 divisions at 200 mV/div, where SCOPE's
// six-ish leaves it at 500 mV/div.
// TRLC-LINKS: REQ-SDS-224
func TestAutoMeasureFramesTenCycles(t *testing.T) {
	t.Parallel()
	sq := square(1e6, -0.725, 0.725)
	b := newSimBench(t, func(tt, _ float64, _ int) float64 { return sq(tt) })
	note := b.run(wsview.Measure, -1)
	if td := b.eng.Snapshot().TdivS; math.Abs(td-1e-6) > 1e-12 {
		t.Fatalf("MEASURE timebase %g, want 1us (ten cycles of 1 MHz)", td)
	}
	if v := b.vdivOf(0); v != 0.2 {
		t.Fatalf("MEASURE C1 %g V/div, want 0.2 (1.45 V on 7.25 divisions)", v)
	}
	b.onScreen(t, 0, 0.2)
	if !strings.HasPrefix(note, "AUTO (MEASURE): 10 cycles, C1 200mV/div") {
		t.Fatalf("notice %q", note)
	}
	if got := detentFor(1.45, goalScope.fill); analog.Detents[got].VdivV != 0.5 {
		t.Fatalf("SCOPE fits 1.45 V at %g V/div, want 0.5", analog.Detents[got].VdivV)
	}
}

// MEASURE's AUTO sets the cursors up on the signal it framed: the time
// cursors one period apart from the trigger's rising edge (dt reads the
// period), the voltage cursors on the top and base (dV reads the amplitude).
// TRLC-LINKS: REQ-SDS-217, REQ-SDS-224
func TestAutoMeasurePlacesTheCursors(t *testing.T) {
	t.Parallel()
	sq := square(1e6, -0.725, 0.725)
	b := newSimBench(t, func(tt, _ float64, _ int) float64 { return sq(tt) })
	b.run(wsview.Measure, -1)
	s := b.c.snap()
	ta, tb := cursorTimes(s, 1)
	if dt := tb - ta; math.Abs(dt-1e-6) > 0.05e-6 {
		t.Fatalf("time cursors %g s apart (A %g, B %g), want one period: 1 us", dt, ta, tb)
	}
	va, vb := cursorVolts(s, 0)
	if math.Abs(va-0.725) > 0.05 || math.Abs(vb+0.725) > 0.05 {
		t.Fatalf("voltage cursors at %g V and %g V, want the top 0.725 and the base -0.725", va, vb)
	}
}

// SPECTRUM takes fifty cycles of the FFT source on the fastest timebase
// holding them, puts the marker on its fundamental and says the span: the
// FFT source C2's 100 kHz beside a 1 MHz C1.
// TRLC-LINKS: REQ-SDS-224
func TestAutoSpectrumFramesManyCycles(t *testing.T) {
	t.Parallel()
	fast, slow := square(1e6, 0, 3.3), square(100e3, 0, 3.3)
	b := newSimBench(t, func(tt, _ float64, ch int) float64 {
		if ch == 1 {
			return slow(tt)
		}
		return fast(tt)
	})
	b.c.mu.Lock()
	b.c.fftSrc = 1
	b.c.mu.Unlock()
	note := b.run(wsview.Spectrum, viewFFT)
	st := b.eng.Snapshot()
	if math.Abs(st.TdivS-50e-6) > 1e-12 {
		t.Fatalf("SPECTRUM timebase %g, want 50us (50 cycles of C2's 100 kHz)", st.TdivS)
	}
	if st.TrigSource != 1 {
		t.Fatalf("SPECTRUM triggers on C%d, want the FFT source C2", st.TrigSource+1)
	}
	b.c.mu.Lock()
	f, set := b.c.ui.specF, b.c.ui.specFSet
	b.c.mu.Unlock()
	if !set || math.Abs(f-100e3) > 1e3 {
		t.Fatalf("marker %g Hz (set %v), want C2's fundamental 100 kHz", f, set)
	}
	if !strings.HasPrefix(note, "AUTO (SPECTRUM): 50 cycles, span ") || !strings.HasSuffix(note, "marker 100kHz") {
		t.Fatalf("notice %q", note)
	}
}

// A slow signal in SPECTRUM stays on a sampled timebase: 50 cycles of
// 1 kHz want 5 ms/div, an envelope band with no spectrum; 2 ms/div holds 20.
// TRLC-LINKS: REQ-SDS-224
func TestAutoSpectrumStaysSampled(t *testing.T) {
	t.Parallel()
	c, _, _ := newC(t)
	if td := c.displayTdiv(goalSpectrum, 1e3); td != 2e-3 {
		t.Fatalf("SPECTRUM on 1 kHz takes %g s/div, want 2ms", td)
	}
	if td := c.displayTdiv(goalSpectrum, 1e6); td != 5e-6 {
		t.Fatalf("SPECTRUM on 1 MHz takes %g s/div, want 5us", td)
	}
	if td := c.displayTdiv(goalMeasure, 300e3); td != 5e-6 {
		t.Fatalf("MEASURE on 300 kHz takes %g s/div, want 5us (at least ten cycles)", td)
	}
	if td := c.displayTdiv(goalScope, 1e3); td != 200e-6 {
		t.Fatalf("SCOPE on 1 kHz takes %g s/div, want 200us", td)
	}
}

// COMPARE with REF A stored puts the scope back on REF A's setup (timebase,
// V/div, offset, trigger) so the live trace sits on it; without a reference
// it frames as SCOPE does.
// TRLC-LINKS: REQ-SDS-224, REQ-SDS-138
func TestAutoCompareTakesTheReference(t *testing.T) {
	t.Parallel()
	sq := square(1e6, 0, 3.3)
	b := newSimBench(t, func(tt, _ float64, _ int) float64 { return sq(tt) })
	note := b.run(wsview.Compare, -1)
	if !strings.HasPrefix(note, "AUTO (COMPARE): 2 cycles") {
		t.Fatalf("COMPARE without a reference: notice %q, want SCOPE's framing", note)
	}
	// the reference, at a setup of its own
	b.c.setTdivNearest(1e-6)
	b.c.setVdiv(0, nearestDetent(1.0))
	b.fe.SetOffset(0, -1.2)
	b.eng.SetTrigLevelCode(30000)
	b.eng.SetTrigSlope(false)
	_ = b.c.setTrigPos(0.3)
	b.c.captureRef(0)
	want := b.c.takeSetup()
	// moved off it, then AUTO
	b.c.setTdivNearest(20e-6)
	b.c.setVdiv(0, nearestDetent(5.0))
	b.fe.SetOffset(0, 0)
	b.eng.SetTrigSlope(true)
	note = b.run(wsview.Compare, -1)
	if got := b.c.takeSetup(); got != want {
		t.Fatalf("COMPARE AUTO left %+v, want REF A's %+v", got, want)
	}
	if note != "AUTO (COMPARE): REF A's setup, 1us/div, C1 1V/div, C2 500mV/div" {
		t.Fatalf("notice %q", note)
	}
	// a mask built here wins over the reference
	b.c.setTdivNearest(500e-9)
	b.eng.SetMask(&engine.Mask{Lo: make([]uint8, 4), Hi: make([]uint8, 4), WinCols: 4})
	b.c.noteMaskSetup()
	mask := b.c.takeSetup()
	b.c.setTdivNearest(20e-6)
	if note = b.run(wsview.Compare, -1); !strings.Contains(note, "the mask's setup, 500ns/div") || b.c.takeSetup() != mask {
		t.Fatalf("COMPARE with a mask: notice %q", note)
	}
}

// The clipping check: a capture at the fit's timebase can miss part of the
// swing (here a level at -2 V shows only on the fast screen AUTO leaves, as
// a bus's rare level or a burst can), and the fit then leaves the trace
// past the screen's bottom. AUTO steps coarser and re-centres until it is
// not clipped, and says so.
// TRLC-LINKS: REQ-SDS-225
func TestAutoStepsCoarserWhenClipped(t *testing.T) {
	t.Parallel()
	sq := square(1e6, 0, 2)
	b := newSimBench(t, func(tt, tdiv float64, _ int) float64 {
		if tdiv < 1e-6 && math.Mod(tt*1e6, 1) > 0.9 {
			return -2
		}
		return sq(tt)
	})
	note := b.run(wsview.Scope, -1)
	for ch := 0; ch < 2; ch++ {
		if b.c.clipped(ch) {
			t.Fatalf("C%d left clipped at %g V/div", ch+1, b.vdivOf(ch))
		}
		if v := b.vdivOf(ch); v != 1 {
			t.Fatalf("C%d %g V/div, want 1 (the 4 V swing on four divisions)", ch+1, v)
		}
	}
	if !strings.HasSuffix(note, "coarser: it clipped") {
		t.Fatalf("notice %q", note)
	}
}

// clipStretch reads a stretch past the screen's top or bottom as clipping,
// a ringing edge's sample or two past it not.
// TRLC-LINKS: REQ-SDS-225
func TestClipStretch(t *testing.T) {
	mk := func(n int, v uint8) []uint8 {
		s := make([]uint8, 100)
		for i := range s {
			s[i] = 128
		}
		for i := 10; i < 10+n; i++ {
			s[i] = v
		}
		return s
	}
	for _, tc := range []struct {
		n    int
		v    uint8
		want bool
	}{{3, 255, false}, {4, 255, true}, {4, 0, true}, {50, 229, false}, {50, 231, true}, {50, 25, true}, {50, 27, false}} {
		if got := clipStretch(mk(tc.n, tc.v)); got != tc.want {
			t.Errorf("%d samples at %d: clipped %v, want %v", tc.n, tc.v, got, tc.want)
		}
	}
}

// A trace pinned at the ADC's rail on the final screen says nothing of how
// far past it the signal goes, so the clipping check steps on past its two
// steps until it is not: C2 showed 200 mV during the fit (50 mV/div) and
// 3.3 V on the screen AUTO leaves.
// TRLC-LINKS: REQ-SDS-225
func TestAutoStepsOnWhileRailed(t *testing.T) {
	t.Parallel()
	big, small := square(1e6, 0, 3.3), square(1e6, 0, 0.2)
	b := newSimBench(t, func(tt, tdiv float64, ch int) float64 {
		if ch == 1 && tdiv >= 1e-6 {
			return small(tt)
		}
		return big(tt)
	})
	b.run(wsview.Scope, -1)
	if b.c.clipped(1) {
		t.Fatalf("C2 left clipped at %g V/div", b.vdivOf(1))
	}
	if v := b.vdivOf(1); v < 0.5 {
		t.Fatalf("C2 %g V/div, want 500 mV/div or coarser for 3.3 V", v)
	}
}

// A filtered capture rings on a square's edges (a 3.3 V square read 3.8 V
// and fitted 1 V/div where the screen's 3.4 V fits 500 mV/div): the fit
// takes the extremes within 2 % of the step past its levels. A
// triangle, with its samples spread between them, keeps its extremes.
// TRLC-LINKS: REQ-SDS-224
func TestRingFreeTwoLevel(t *testing.T) {
	const vpc = 0.02
	sq := make([]uint8, 1000)
	tri := make([]uint8, 1000)
	for i := range sq {
		sq[i] = 46
		if i%100 >= 50 {
			sq[i] = 211
		}
		switch i % 100 { // the filter's overshoot after each edge
		case 50, 51:
			sq[i] = 224
		case 0, 1:
			sq[i] = 33
		}
		tri[i] = uint8(46 + 165*math.Abs(float64(i%100)-50)/50)
	}
	r := ringFree(measure.Compute(sq, vpc, 0, 1e-6), sq, vpc, 0)
	if math.Abs(r.Vpp-3.43) > 0.03 {
		t.Fatalf("square read %.2f V, want its 3.3 V step and 2 %% past each level (3.43 V)", r.Vpp)
	}
	m := measure.Compute(tri, vpc, 0, 1e-6)
	if got := ringFree(m, tri, vpc, 0); got.Vpp != m.Vpp {
		t.Fatalf("triangle read %.2f V, want its %.2f V kept", got.Vpp, m.Vpp)
	}
}

// A filtered capture's ringing past the screen on a square's edges is not
// clipping; its level past the screen, or a stretch at the ADC's rail, is.
// TRLC-LINKS: REQ-SDS-225
func TestClipFiltered(t *testing.T) {
	sq := func(lo, hi, ring uint8) []uint8 {
		s := make([]uint8, 1000)
		for i := range s {
			s[i] = lo
			if i%100 >= 50 {
				s[i] = hi
			}
			if k := i % 100; k >= 50 && k < 55 {
				s[i] = ring
			}
		}
		return s
	}
	if clipFiltered(sq(35, 221, 240)) {
		t.Fatal("ringing past the screen's top read as clipping")
	}
	if !clipStretch(sq(35, 221, 240)) {
		t.Fatal("an unfiltered capture's stretch past the screen must read as clipping")
	}
	if !clipFiltered(sq(35, 236, 250)) {
		t.Fatal("a top level past the screen not read as clipping")
	}
	if !clipFiltered(sq(0, 200, 210)) {
		t.Fatal("a base at the ADC's rail not read as clipping")
	}
}

// A 3.3 V square whose capture at the fit's slower timebase rings 0.25 V past
// both levels (a filtered capture's ringing: the bench's 1 MHz square read
// 3.8 V there and fitted 1 V/div one AUTO in eight) still ends on 500 mV/div:
// AUTO reads the swing again on the screen it leaves, where it shows 3.3 V.
// TRLC-LINKS: REQ-SDS-224
func TestAutoRefinesOnTheScreenItLeaves(t *testing.T) {
	t.Parallel()
	sq := square(1e6, 0, 3.3)
	b := newSimBench(t, func(tt, tdiv float64, _ int) float64 {
		v := sq(tt)
		if ph := math.Mod(tt*1e6, 0.5); tdiv >= 2e-6 && ph < 0.1 {
			if v > 0 {
				return v + 0.25
			}
			return v - 0.25
		}
		return v
	})
	b.run(wsview.Scope, -1)
	if st := b.eng.Snapshot(); math.Abs(st.TdivS-200e-9) > 1e-12 {
		t.Fatalf("timebase %g, want 200ns", st.TdivS)
	}
	for ch := 0; ch < 2; ch++ {
		if v := b.vdivOf(ch); v != 0.5 {
			t.Fatalf("C%d %g V/div, want 0.5 (3.3 V on the screen AUTO leaves)", ch+1, v)
		}
		b.onScreen(t, ch, 0.4)
	}
}

// A clipped trace whose own span fits the screen is off centre, not too
// large: the clipping check centres it rather than stepping coarser (an
// ARINC 429 line without the bandwidth limit stayed 2 div high, clipped).
// TRLC-LINKS: REQ-SDS-225
func TestClippedTraceThatFitsIsCentred(t *testing.T) {
	t.Parallel()
	sq := square(1e6, 0, 3.3)
	b := newSimBench(t, func(tt, _ float64, _ int) float64 { return sq(tt) })
	b.c.setTdivNearest(1e-6)
	b.c.setVdiv(1, nearestDetent(0.5))
	b.fe.SetOffset(1, -1.06) // the 0..3.3 V square from code 75 to 240: past the top, short of the rail
	if !b.c.clipped(1) {
		t.Fatal("the rig's trace is not clipped: the test proves nothing")
	}
	fixed, ok := b.c.recentreClipped(make(chan struct{}), 1, goalScope)
	if !ok || !fixed || b.vdivOf(1) != 0.5 {
		t.Fatalf("fixed %v ok %v at %g V/div", fixed, ok, b.vdivOf(1))
	}
	b.onScreen(t, 1, 0.4)
}

// A 10 Hz pulse on C2 beside a 1 kHz square on C1: every capture AUTO takes
// shows C2 at one level. Fitted to that level's noise, C2 went off the screen
// when the other level came; as a quiet line it keeps 2 V/div at 0 V, where
// both levels of the pulse show.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-224
func TestAutoLeavesAQuietChannelCoarse(t *testing.T) {
	t.Parallel()
	sq := square(1e3, 0, 3.3)
	b := newSimBench(t, func(tt, _ float64, ch int) float64 {
		if ch == 1 { // a 1 ms pulse 60 ms into each 100 ms: past every capture AUTO takes
			if ph := math.Mod(tt, 0.1); ph >= 0.06 && ph < 0.061 {
				return 3.3
			}
			return 0.1 * math.Sin(tt*7e6) // the low level, noise on it (a code or two at 2 V/div)
		}
		return sq(tt)
	})
	b.run(wsview.Scope, -1)
	if v := b.vdivOf(0); v != 0.5 {
		t.Fatalf("C1 %g V/div, want 0.5", v)
	}
	if v, off := b.vdivOf(1), b.fe.OffsetReqV(1); v != 2 || off != 0 {
		t.Fatalf("C2 %g V/div at %g V, want 2 V/div at 0 V", v, off)
	}
	if b.c.clipped(1) {
		t.Fatal("C2 left clipped")
	}
}

// A trace whose own span is more than the screen's eight divisions is
// over the screen (the clipping check steps it on coarser past its two
// steps); one that fits is not.
// TRLC-LINKS: REQ-SDS-225
func TestOverScreen(t *testing.T) {
	t.Parallel()
	sq := square(1e6, -2.45, 2.45) // 4.9 V: 9.8 div at 500 mV/div, short of the rails
	b := newSimBench(t, func(tt, _ float64, ch int) float64 {
		if ch == 1 {
			return sq(tt) / 2
		}
		return sq(tt)
	})
	b.c.setTdivNearest(1e-6)
	for ch := 0; ch < 2; ch++ {
		b.c.setVdiv(ch, nearestDetent(0.5))
		b.fe.SetOffset(ch, 0)
	}
	if !b.c.overScreen(0) || b.c.railing(0) {
		t.Fatalf("C1 9.8 div: over %v railing %v, want over and not railing", b.c.overScreen(0), b.c.railing(0))
	}
	if b.c.overScreen(1) {
		t.Fatal("C2 4.9 div read as over the screen")
	}
}

// A line whose level changes slower than the frequency AUTO finds on it (an
// ARINC 429 line without the bandwidth limit: a 50 MHz NULL toggle of +-0.38
// V in every fast capture, its 3.3 V data level only in a long window) is
// fitted for its whole swing: the long window's capture widens it. Fitted to
// the toggle, it took 200mV/div and the data level ran off the screen.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-224
func TestAutoFitsALevelSlowerThanItsToggle(t *testing.T) {
	t.Parallel()
	sq := square(1e6, 0, 3.3)
	toggle := square(5e6, -0.3, 0.3)
	b := newSimBench(t, func(tt, tdiv float64, ch int) float64 {
		if ch == 1 {
			if tdiv >= 1e-3 && math.Mod(tt, 1e-3) < 0.4e-3 {
				return 3.3 // a word's data level
			}
			return toggle(tt)
		}
		return sq(tt)
	})
	b.run(wsview.Scope, -1)
	if v := b.vdivOf(1); v < 0.5 {
		t.Fatalf("C2 %g V/div: fitted to the toggle, its 3.3 V level off the screen", v)
	}
}

// A second AUTO press cancels the sweep and puts back what it changed: the
// timebase, the V/div and offsets, the trigger. Cancelled, it left the
// sweep's 2 V/div and 1us/div and said nothing.
// TRLC-LINKS: REQ-SDS-137
func TestAutoCancelPutsTheSettingsBack(t *testing.T) {
	t.Parallel()
	sq := square(1e6, 0, 3.3)
	b := newSimBench(t, func(tt, _ float64, _ int) float64 { return sq(tt) })
	b.c.setVdiv(0, nearestDetent(0.2))
	b.fe.SetOffset(0, -0.3)
	b.eng.SetTrigSource(1)
	vidx0, _ := b.fe.Snapshot()
	tdiv0 := b.eng.Snapshot().TdivS
	b.c.autoset()
	time.Sleep(600 * time.Millisecond) // the sweep is under way: 2 V/div, a fast timebase
	if !b.c.AutosetBusy() {
		t.Fatal("the sweep ended before the second press: the test proves nothing")
	}
	b.c.autoset() // cancel
	for end := time.Now().Add(10 * time.Second); b.c.AutosetBusy() && time.Now().Before(end); {
		time.Sleep(20 * time.Millisecond)
	}
	vidx, _ := b.fe.Snapshot()
	st := b.eng.Snapshot()
	if st.TdivSet() != tdiv0 || vidx != vidx0 || b.fe.OffsetReqV(0) != -0.3 || st.TrigSource != 1 {
		t.Fatalf("after the cancel: tdiv %g (was %g), V/div %v (was %v), C1 offset %g, trigger C%d", st.TdivSet(), tdiv0, vidx, vidx0, b.fe.OffsetReqV(0), st.TrigSource+1)
	}
	b.c.mu.Lock()
	n := b.c.ui.notice
	b.c.mu.Unlock()
	if !strings.Contains(n, "AUTO cancelled") {
		t.Fatalf("notice %q", n)
	}
}
