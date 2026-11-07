// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"math"
	"testing"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
)

// pulseTrain builds a record with pulses of given widths (samples) above a
// low rail, separated by gaps.
// TRLC-LINKS: REQ-SDS-011
func pulseTrain(n int, widths []int, gap int, lo, hi uint8) []uint8 {
	out := make([]uint8, n)
	for i := range out {
		out[i] = lo
	}
	pos := gap
	for _, w := range widths {
		for i := 0; i < w && pos+i < n; i++ {
			out[pos+i] = hi
		}
		pos += w + gap
	}
	return out
}

// TRLC-LINKS: REQ-SDS-011
func TestQualifyPulseWidthWindow(t *testing.T) {
	// Pulses of 10, 50, 10 samples at 100 ns/sample → 1 µs, 5 µs, 1 µs.
	sig := pulseTrain(1000, []int{10, 50, 10}, 200, 50, 200)
	p := core.DefaultTrigParams()

	// cond=any: every pulse qualifies; anchor = completing edge nearest centre.
	if xc := core.QualifyPulse(sig, 100, p, true); xc < 0 {
		t.Fatal("any: no pulse found")
	}

	// inside [4µs, 6µs]: only the 50-sample (5 µs) pulse qualifies.
	p.PulseWMinNs, p.PulseWMaxNs, p.PulseCond = 4000, 6000, CondInside
	xc := core.QualifyPulse(sig, 100, p, true)
	// The 5 µs pulse spans samples 410..459; its completing (falling) edge
	// is at ~460.
	if xc < 455 || xc > 465 {
		t.Fatalf("inside: anchor %v, want ≈460 (the 5µs pulse exit)", xc)
	}

	// greater than 6µs: nothing qualifies → hold.
	p.PulseWMinNs, p.PulseWMaxNs, p.PulseCond = 0, 6000, CondGreater
	if xc := core.QualifyPulse(sig, 100, p, true); xc != -1 {
		t.Fatalf("greater: got %v, want -1", xc)
	}

	// less than 2µs: the 1 µs pulses qualify.
	p.PulseWMinNs, p.PulseWMaxNs, p.PulseCond = 2000, 0, CondLess
	if xc := core.QualifyPulse(sig, 100, p, true); xc < 0 {
		t.Fatal("less: no 1µs pulse found")
	}
}

// TRLC-LINKS: REQ-SDS-011
func TestQualifyPulseLowPolarity(t *testing.T) {
	// Low pulses: invert the train.
	sig := pulseTrain(1000, []int{20}, 400, 200, 50) // one low dip of 20 samples
	for i := range sig {
		sig[i] = 250 - sig[i]
	}
	_ = sig
	// Build directly: high rail with a low dip.
	sig = make([]uint8, 1000)
	for i := range sig {
		sig[i] = 200
	}
	for i := 480; i < 500; i++ {
		sig[i] = 50
	}
	p := core.DefaultTrigParams()
	xc := core.QualifyPulse(sig, 100, p, false) // low pulse
	// Completing edge = the rising exit at ~500.
	if xc < 495 || xc > 505 {
		t.Fatalf("low pulse anchor %v, want ≈500", xc)
	}
	if xc := core.QualifyPulse(sig, 100, p, true); xc >= 480 && xc < 500 {
		t.Fatalf("high-pulse search anchored inside the low dip: %v", xc)
	}
}

// TRLC-LINKS: REQ-SDS-011
func TestQualifierPublishPolicy(t *testing.T) {
	// A pulse-qualified engine holds frames without a qualifying pulse even
	// in AUTO (the qualifier IS the trigger), and publishes when one appears.
	fb := newFakeBus()
	fb.mu.Lock()
	fb.wave = func(_ int) (uint8, uint8) { return 128, 128 } // flat: no pulses
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	e.SetTrigType(int(TrigPulse))

	e.oneFrame(false) // AUTO decimated + qualifier: flat → hold
	if s := e.Snapshot(); s.Published != 0 || s.Held != 1 {
		t.Fatalf("flat qualifier frame: pub=%d held=%d, want 0/1", s.Published, s.Held)
	}

	// Now a real pulse train appears.
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) {
		if i%256 < 32 {
			return 200, 200
		}
		return 56, 56
	}
	fb.mu.Unlock()
	e.oneFrame(false)
	s := e.Snapshot()
	if s.Published != 1 {
		t.Fatalf("qualifying pulse frame not published: %+v", s)
	}
	f, _ := e.Consume()
	if f.TriggerAt < 0 {
		t.Fatal("published qualifier frame has no anchor")
	}
}

// TRLC-LINKS: REQ-SDS-012
func TestAverageModeInEngine(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	e.SetAcqMode(AcqAverage)
	e.SetAvgCount(4)
	for range 4 {
		e.oneFrame(false)
	}
	f, fresh := e.Consume()
	if !fresh {
		t.Fatal("no averaged frame published")
	}
	// The averaged frame is the WinCols window centred on the edge.
	if f.Len() != e.band.WinCols() || math.Abs(f.TriggerAt-float64(f.Len())/2) > 1 {
		t.Fatalf("averaged frame: valid=%d edge=%v", f.Len(), f.TriggerAt)
	}
}

// TRLC-LINKS: REQ-SDS-126
func TestUniformityStats(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	for range 6 {
		e.oneFrame(false)
	}
	s := e.Snapshot()
	// Identical frames, software-centred: uniformity must be ~0.
	if s.WinColStd > 1 {
		t.Fatalf("WinColStd = %v for identical frames, want ≈0", s.WinColStd)
	}
}

// The SRAM path anchors a qualified pulse where the display can fill a
// screen around it, nearest the hardware edge - not at the window centre.
// TRLC-LINKS: REQ-SDS-011
func TestQualifyPulseAnchorsWithinScreenSpan(t *testing.T) {
	// 10-sample pulses every 110 samples over a 2000-sample window, 1000 on screen.
	widths := make([]int, 18)
	for i := range widths {
		widths[i] = 10
	}
	sig := pulseTrain(2000, widths, 100, 50, 200)
	p := core.DefaultTrigParams()
	at := softAnchor(len(sig), 1000, 0.2, 400)
	if at.Lo != 200 || at.Hi != 1200 {
		t.Fatalf("span %+v", at)
	}
	x := core.QualifyPulseAt(sig, 1, p, true, at)
	if x < 350 || x > 450 {
		t.Fatalf("anchor %.1f, want the pulse ending nearest the hardware edge at 400", x)
	}
	// No hardware edge: still inside the fillable span.
	if x := core.QualifyPulseAt(sig, 1, p, true, softAnchor(len(sig), 1000, 0.8, -1)); x < 800 || x > 1800 {
		t.Fatalf("anchor %.1f outside [800,1800]", x)
	}
	// A pulse only outside the span is no trigger.
	only := pulseTrain(2000, []int{10}, 1900, 50, 200)
	if x := core.QualifyPulseAt(only, 1, p, true, at); x >= 0 {
		t.Fatalf("anchored at %.1f outside the span", x)
	}
}

// An AC-coupled trigger source's level sits that far above the channel's raw
// mean, not above the offset-shifted zero, so a 0 V level fires where the AC
// trace crosses its centre.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-096
func TestACTriggerLevelFollowsMean(t *testing.T) {
	e := &Engine{}
	e.chVdivBits[0].Store(math.Float64bits(1))
	e.trigCPV[0].Store(math.Float64bits(trigCPVDefault))
	e.trigZero[0].Store(math.Float64bits(trigZeroDefault))
	e.trigCode = uint16(math.Round(trigZeroDefault)) // 0 V
	f := &sample.Frame{Traces: []sample.Trace{{Codes8: make([]uint8, 100), Valid: 100}, {Ch: 1, Codes8: make([]uint8, 100), Valid: 100}}}
	for i := range f.Codes8(0) {
		f.Codes8(0)[i] = 128
		if i%2 == 0 {
			f.Codes8(0)[i] = 210 // 0..3.3 V square: mean 169
		}
	}
	e.noteChannelMeans(f)
	if got := e.trigDispLevel(0); got != 128 {
		t.Fatalf("DC level %d, want 128", got)
	}
	e.SetChannelCoupling(0, couplingAC)
	if got := e.trigDispLevel(0); got != 169 {
		t.Fatalf("AC level %d, want the mean 169", got)
	}
}

// An inverted qualifier range from the web or SCPI is read as a range; a
// zero limit stays "unset".
// TRLC-LINKS: REQ-SDS-011
func TestQualifierLimitsOrdered(t *testing.T) {
	e := &Engine{}
	e.SetPulseParams(0.5, 3000, 2000, CondInside)
	if e.tp.PulseWMinNs != 2000 || e.tp.PulseWMaxNs != 3000 {
		t.Fatalf("pulse %g..%g", e.tp.PulseWMinNs, e.tp.PulseWMaxNs)
	}
	e.SetSlopeParams(0.3, 0.2, 1000, 500, CondInside)
	if e.tp.SlopeLoFrac != 0.2 || e.tp.SlopeHiFrac != 0.3 || e.tp.SlopeTMinNs != 500 || e.tp.SlopeTMaxNs != 1000 {
		t.Fatalf("slope %+v", e.tp)
	}
	e.SetSlopeParams(0.2, 0.8, 1000, 0, CondLess)
	if e.tp.SlopeTMinNs != 1000 || e.tp.SlopeTMaxNs != 0 {
		t.Fatalf("unset T max moved: %g..%g", e.tp.SlopeTMinNs, e.tp.SlopeTMaxNs)
	}
}

// A decimated record keeps the FPGA's raw-sample trigger where its averages
// hide the crossing (an edge faster than one record sample: a 1 MHz square at
// 50 ms/div reads as its mean): the band past the converters' spread keeps
// noise from firing it. A full-rate record is judged by its own crossing.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-016
func TestDecimatedRecordKeepsTheRawTrigger(t *testing.T) {
	mean := make([]uint8, 200)
	for i := range mean {
		mean[i] = 125
	}
	e := &Engine{}
	for _, c := range []struct {
		dec  uint32
		want bool
	}{{256, true}, {16, true}, {1, false}} {
		f := &sample.Frame{TriggerAt: 98, AcqDecim: int(c.dec), Traces: []sample.Trace{{Valid: 200}, {Ch: 1, Valid: 200}}}
		if got := e.placeEdgeTrigger(f, mean, nil, 130, true); got != c.want || f.TriggerAt != 98 {
			t.Errorf("decimation %d: kept %v (want %v), at %g", c.dec, got, c.want, f.TriggerAt)
		}
	}
}
