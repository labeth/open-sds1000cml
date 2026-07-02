package engine

import (
	"math"
	"testing"
)

// pulseTrain builds a record with pulses of given widths (samples) above a
// low rail, separated by gaps.
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

func TestQualifyPulseWidthWindow(t *testing.T) {
	// Pulses of 10, 50, 10 samples at 100 ns/sample → 1 µs, 5 µs, 1 µs.
	sig := pulseTrain(1000, []int{10, 50, 10}, 200, 50, 200)
	p := defaultTrigParams()

	// cond=any: every pulse qualifies; anchor = completing edge nearest centre.
	if xc := qualifyPulse(sig, 100, p, true); xc < 0 {
		t.Fatal("any: no pulse found")
	}

	// inside [4µs, 6µs]: only the 50-sample (5 µs) pulse qualifies.
	p.pulseWMinNs, p.pulseWMaxNs, p.pulseCond = 4000, 6000, CondInside
	xc := qualifyPulse(sig, 100, p, true)
	// The 5 µs pulse spans samples 410..459; its completing (falling) edge
	// is at ~460.
	if xc < 455 || xc > 465 {
		t.Fatalf("inside: anchor %v, want ≈460 (the 5µs pulse exit)", xc)
	}

	// greater than 6µs: nothing qualifies → hold.
	p.pulseWMinNs, p.pulseWMaxNs, p.pulseCond = 0, 6000, CondGreater
	if xc := qualifyPulse(sig, 100, p, true); xc != -1 {
		t.Fatalf("greater: got %v, want -1", xc)
	}

	// less than 2µs: the 1 µs pulses qualify.
	p.pulseWMinNs, p.pulseWMaxNs, p.pulseCond = 2000, 0, CondLess
	if xc := qualifyPulse(sig, 100, p, true); xc < 0 {
		t.Fatal("less: no 1µs pulse found")
	}
}

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
	p := defaultTrigParams()
	xc := qualifyPulse(sig, 100, p, false) // low pulse
	// Completing edge = the rising exit at ~500.
	if xc < 495 || xc > 505 {
		t.Fatalf("low pulse anchor %v, want ≈500", xc)
	}
	if xc := qualifyPulse(sig, 100, p, true); xc >= 480 && xc < 500 {
		t.Fatalf("high-pulse search anchored inside the low dip: %v", xc)
	}
}

func TestQualifyPulseFlatReject(t *testing.T) {
	flat := make([]uint8, 500)
	for i := range flat {
		flat[i] = 128 + uint8(i%3) // 2-code noise
	}
	if xc := qualifyPulse(flat, 100, defaultTrigParams(), true); xc != -1 {
		t.Fatalf("flat rail produced pulse event %v", xc)
	}
}

func TestQualifySlope(t *testing.T) {
	// A slow ramp (100 samples lo→hi) and a fast step, both rising.
	sig := make([]uint8, 2000)
	for i := range sig {
		sig[i] = 50
	}
	for i := 0; i < 100; i++ { // slow ramp at 500..599: 50 → 200
		sig[500+i] = uint8(50 + 150*i/100)
	}
	for i := 600; i < 1400; i++ {
		sig[i] = 200
	}
	sig[1400] = 50 // fall back
	for i := 1401; i < 2000; i++ {
		sig[i] = 50
	}

	p := defaultTrigParams()
	// lo=0.2, hi=0.8 of span 150: lo≈80, hi≈170. The ramp crosses lo at
	// ~i=520 and hi at ~i=580 → traversal ≈60 samples ≈ 6 µs at 100 ns.
	p.slopeTMinNs, p.slopeTMaxNs, p.slopeCond = 4000, 8000, CondInside
	xc := qualifySlope(sig, 100, p, true)
	if xc < 570 || xc > 590 {
		t.Fatalf("slope anchor %v, want ≈580 (second-threshold crossing)", xc)
	}

	// A window that excludes the 6 µs traversal → hold.
	p.slopeTMinNs, p.slopeTMaxNs, p.slopeCond = 0, 1000, CondInside
	if xc := qualifySlope(sig, 100, p, true); xc != -1 {
		t.Fatalf("slope traversal should not qualify: %v", xc)
	}
}

func TestQualifySlopeSingleStepEdge(t *testing.T) {
	// A hard square edge that spans lo→hi in ONE sample step (the decimated
	// cal-square case): the traversal exists at index c with time 0 and must
	// be found, not missed.
	sig := make([]uint8, 2048)
	for i := range sig {
		if (i/256)%2 == 0 {
			sig[i] = 40
		} else {
			sig[i] = 210
		}
	}
	p := defaultTrigParams() // loFrac 0.2, hiFrac 0.8, cond any
	xc := qualifySlope(sig, 400, p, true)
	if xc < 0 {
		t.Fatal("single-step rising edge not detected (regression: k must start at c)")
	}
	// Rising crossings (40→210) are at 256, 768, 1280, 1792; the nearest to
	// centre 1024 is 768 (tie with 1280, first wins). The sub-sample frac is
	// (lvl−40)/(210−40) with lvl≈170.
	if xc < 767 || xc > 769 {
		t.Fatalf("single-step slope anchor %v, want ≈768 (nearest rising crossing)", xc)
	}
}

func TestQualifyVideo(t *testing.T) {
	// Composite-ish: negative sync pulses every 200 samples dipping to 20
	// from a 150 rail; "video" content rides above.
	sig := make([]uint8, 2000)
	for i := range sig {
		sig[i] = 150
	}
	for line := 0; line < 10; line++ {
		base := line * 200
		for i := 0; i < 15; i++ {
			if base+i < len(sig) {
				sig[base+i] = 20
			}
		}
	}
	p := defaultTrigParams() // PAL, line 0 (any), negative sync

	// Any line: the sync edge nearest the centre (sample 1000 → line 5 at 1000).
	xc := qualifyVideo(sig, p)
	if xc < 995 || xc > 1005 {
		t.Fatalf("video any-line anchor %v, want ≈1000", xc)
	}

	// Line 3: the 3rd sync EDGE in the record. The record starts inside the
	// first sync pulse (no crossing), so detected edges are at ≈200, 400,
	// 600 — the 3rd is ≈600.
	p.videoLine = 3
	xc = qualifyVideo(sig, p)
	if xc < 595 || xc > 605 {
		t.Fatalf("video line-3 anchor %v, want ≈600", xc)
	}

	// Line 100: fewer than 100 sync edges → hold.
	p.videoLine = 100
	if xc := qualifyVideo(sig, p); xc != -1 {
		t.Fatalf("line-100 with 10 lines: %v, want -1", xc)
	}
}

func TestEresBoxcar(t *testing.T) {
	// A boxcar of 15 shrinks σ ≈ √15; check it smooths an alternating signal
	// to near its mean and preserves the ends without wrap.
	sig := make([]uint8, 200)
	for i := range sig {
		if i%2 == 0 {
			sig[i] = 100
		} else {
			sig[i] = 140
		}
	}
	scratch := make([]uint16, 200)
	eresBoxcar(sig, 15, scratch)
	for i := 20; i < 180; i++ {
		if sig[i] < 115 || sig[i] > 125 {
			t.Fatalf("boxcar[%d] = %d, want ≈120", i, sig[i])
		}
	}
	// Ends: kernel shrinks (no wrap, no fabricated tail) — still near mean.
	if sig[0] < 110 || sig[0] > 130 {
		t.Fatalf("boxcar[0] = %d (shrunk kernel)", sig[0])
	}
}

func TestEresLenForBits(t *testing.T) {
	cases := map[float64]int{0.5: 1, 1.0: 3, 1.5: 7, 2.0: 15, 2.5: 31, 3.0: 63}
	for b, want := range cases {
		if got := EresLenForBits(b); got != want {
			t.Errorf("EresLenForBits(%v) = %d, want %d (even → odd-down)", b, got, want)
		}
	}
}

func TestAverageRing(t *testing.T) {
	r := &avgRing{}
	r.reset(4, 100)
	f := &Frame{C1: make([]uint8, 1000), C2: make([]uint8, 1000), Valid: 1000}
	// Frames with an edge at varying positions; alignment centres them all.
	for k := 0; k < 4; k++ {
		edge := 480 + k*10
		for i := range f.C1 {
			if i < edge {
				f.C1[i] = 60
			} else {
				f.C1[i] = 200
			}
			f.C2[i] = f.C1[i]
		}
		r.push(f, float64(edge))
	}
	r.meanInto(f)
	if f.Valid != 100 || f.EdgeX != 50 {
		t.Fatalf("mean frame: valid=%d edge=%v, want 100/50", f.Valid, f.EdgeX)
	}
	// After alignment every ring entry has its edge at column 50: the mean
	// must be a sharp edge (low before, high after), not a smear.
	if f.C1[40] != 60 || f.C1[60] != 200 {
		t.Fatalf("aligned mean: c1[40]=%d c1[60]=%d, want 60/200", f.C1[40], f.C1[60])
	}
}

func TestAverageRingNoOffRecordBias(t *testing.T) {
	// Frames whose edge is far off centre contribute off-record columns at
	// one window end; those must NOT be averaged as a fabricated 128 —
	// otherwise the window edge is pulled toward mid-scale.
	r := &avgRing{}
	r.reset(4, 200)
	f := &Frame{C1: make([]uint8, 400), C2: make([]uint8, 400), Valid: 400}
	for k := 0; k < 4; k++ {
		for i := range f.C1 {
			f.C1[i], f.C2[i] = 40, 40 // a flat low rail, no 128 anywhere
		}
		r.push(f, 350) // edge at 350 → shift 250; left columns fall off-record
	}
	r.meanInto(f)
	// Every contributing column is 40; no column should read the 128 fill.
	for i := 0; i < 200; i++ {
		if f.C1[i] != 40 && f.C1[i] != 128 {
			t.Fatalf("col %d = %d, expected 40 (real) or 128 (no-contribution)", i, f.C1[i])
		}
		// Columns that DID have contributions must be the real 40, not a
		// 40/128 blend (e.g. 84).
		if f.C1[i] > 40 && f.C1[i] < 128 {
			t.Fatalf("col %d = %d is a fabricated-fill blend", i, f.C1[i])
		}
	}
}

func TestQualifierPublishPolicy(t *testing.T) {
	// A pulse-qualified engine holds frames without a qualifying pulse even
	// in AUTO (the qualifier IS the trigger), and publishes when one appears.
	fb := newFakeBus()
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) { return 128, 128 } // flat: no pulses
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
	if f.EdgeX < 0 {
		t.Fatal("published qualifier frame has no anchor")
	}
}

func TestAverageModeInEngine(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	e.SetAcqMode(AcqAverage)
	e.SetAvgCount(4)
	for i := 0; i < 4; i++ {
		e.oneFrame(false)
	}
	f, fresh := e.Consume()
	if !fresh {
		t.Fatal("no averaged frame published")
	}
	// The averaged frame is the WinCols window centred on the edge.
	if f.Valid != e.band.WinCols() || math.Abs(f.EdgeX-float64(f.Valid)/2) > 1 {
		t.Fatalf("averaged frame: valid=%d edge=%v", f.Valid, f.EdgeX)
	}
}

func TestUniformityStats(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	for i := 0; i < 6; i++ {
		e.oneFrame(false)
	}
	s := e.Snapshot()
	// Identical frames, software-centred: uniformity must be ~0.
	if s.WinColStd > 1 {
		t.Fatalf("WinColStd = %v for identical frames, want ≈0", s.WinColStd)
	}
}
