// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"math"
	"strings"
	"testing"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// A deep raw screen is decimated to a display-sized live record; a screen that
// already fits is left alone.
// TRLC-LINKS: REQ-SDS-010
func TestLiveSRAMPlanBoundsTheScreen(t *testing.T) {
	for _, tdiv := range []float64{200e-6, 500e-6, 10e-3, 1} {
		p := PlanSRAM(tdiv)
		live, ok := acq.LiveSRAMPlan(p, liveScreenSamples)
		if !ok {
			t.Fatalf("%g s/div: screen %d not reduced", tdiv, p.Screen)
		}
		if (live.Screen > liveScreenSamples && live.Log < 20) || live.Log < 4 || live.Samples < live.Screen || live.Samples > int(sramcapture.Words) {
			t.Fatalf("%g s/div: bad live plan %+v", tdiv, live)
		}
		if span, want := float64(live.Screen)*live.SampleS, 10*tdiv; span < want*0.99 || span > want*1.01 {
			t.Fatalf("%g s/div: live screen spans %g s, want %g", tdiv, span, want)
		}
	}
	for _, tdiv := range []float64{1e-6, 500e-9} {
		if _, ok := acq.LiveSRAMPlan(PlanSRAM(tdiv), liveScreenSamples); ok {
			t.Fatalf("%g s/div: a short raw screen was decimated", tdiv)
		}
	}
}

// The live decimated record keeps the display's bandwidth: the precision
// filter's -3 dB (13.5 % of the decimated rate) stays above the 768-px plot's
// pixel Nyquist at every timebase, and a spectrum lease's decimated record
// holds 8192-16384 samples, the FFT's points one for one (at 500us/div /1024
// would pass 36.6 kHz, under the plot's 76.8 kHz pixel Nyquist, and a
// 115200 Bd UART's bits vanish).
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-070
func TestLiveSRAMPlanKeepsTheDisplayBandwidth(t *testing.T) {
	for _, tdiv := range []float64{20e-6, 50e-6, 200e-6, 500e-6, 2e-3, 10e-3} {
		p := PlanSRAM(tdiv)
		live, ok := acq.LiveSRAMPlan(p, liveBudget("averaging", 0, 0))
		if !ok {
			continue
		}
		bw, pixNyq := 0.1346/live.SampleS, 768/(2*10*tdiv)
		if bw < 1.3*pixNyq {
			t.Errorf("%g s/div: /%d keeps %.0f Hz, under 1.3 x the pixel Nyquist %.0f Hz", tdiv, 1<<live.Log, bw, pixNyq)
		}
		spec, ok := acq.LiveSRAMPlan(p, liveBudget(blockSpectrum, 0, 0))
		if ok && (spec.Screen > spectrumLiveSamples || spec.Screen <= spectrumLiveSamples/2 && spec.Log > 4) {
			t.Errorf("%g s/div: the spectrum's record holds %d samples at /%d", tdiv, spec.Screen, 1<<spec.Log)
		}
	}
	if live, _ := acq.LiveSRAMPlan(PlanSRAM(500e-6), liveBudget("averaging", 0, 0)); live.Log != 9 {
		t.Errorf("500us/div: /%d, want /512", 1<<live.Log)
	}
	if spec, _ := acq.LiveSRAMPlan(PlanSRAM(500e-6), liveBudget(blockSpectrum, 0, 0)); spec.Log != 8 {
		t.Errorf("500us/div spectrum: /%d, want /256", 1<<spec.Log)
	}
	if spec, ok := acq.LiveSRAMPlan(PlanSRAM(200e-6), liveBudget(blockSpectrum, 0, 0)); !ok || spec.Log != 6 || spec.Screen != 15625 {
		t.Errorf("200us/div spectrum: /%d, %d samples; want /64, 15625 (an 8192-point FFT one for one)", 1<<spec.Log, spec.Screen)
	}
	if p := PlanSRAM(5e-6); liveDecimatesPlan(blockSpectrum, p) {
		t.Error("5us/div spectrum decimated: 25000 samples stay at the full rate")
	} else if _, ok := acq.LiveSRAMPlan(p, liveBudget(blockSpectrum, 0, 0)); ok {
		t.Error("5us/div spectrum: the full-rate screen planned decimated")
	}
}

// A bus decode's lease reads records sampled just finely enough: a 115200 Bd
// UART at 10ms/div (a bit 8.7 us, wanted every 1.1 us) reads a quarter of
// the full record's 390,000 words a screen; a fast bus keeps the full rate.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-214
func TestBusLeaseSamplesFinelyEnough(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	setCaps(e, func(c *acq.Caps) { c.Envelope = true })
	b, _ := PlanTdiv(10e-3)
	e.band = b
	_, full, _, _, _ := e.sramConfig()
	e.LeaseBusSamples(8.7e-6/8, 5*time.Second)
	if e.rollActive() {
		t.Fatal("the bus lease rolls")
	}
	if _, p, _, _, _ := e.sramConfig(); p.SampleS > 8.7e-6/8 || 3*p.Screen > full.Screen {
		t.Fatalf("10ms/div bus lease: a sample every %g s, %d samples a screen (full rate %d)", p.SampleS, p.Screen, full.Screen)
	}
	e.LeaseBusSamples(1e-9, 5*time.Second)
	if _, p, _, _, _ := e.sramConfig(); p.Log > 7 {
		t.Fatalf("a 1 ns feature decimated /%d", 1<<p.Log)
	}
}

// A bus lease finer than the first decimated sample (32 ns) is served at the
// full rate where the raw screen fits the record: /api/record.bin asking 10 ns
// at 100us/div got 64 ns records and refused, while 4 ns was served.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-214, REQ-SDS-280
func TestBusLeaseFinerThanTheDecimationTakesTheFullRate(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	setCaps(e, func(c *acq.Caps) { c.Envelope = true })
	for _, c := range []struct{ tdiv, want float64 }{{100e-6, 10e-9}, {100e-6, 20e-9}, {50e-6, 10e-9}, {20e-6, 20e-9}, {200e-6, 30e-9}} {
		b, _ := PlanTdiv(c.tdiv)
		e.band = b
		e.LeaseBusSamples(c.want, 5*time.Second)
		if _, p, _, _, _ := e.sramConfig(); p.SampleS > c.want {
			t.Errorf("%g s/div, a sample wanted every %g s: planned every %g s (/%d)", c.tdiv, c.want, p.SampleS, 1<<p.Log)
		}
	}
}

// A stopped record's recall (its precision words and the board's
// conditioning) and its own conditioning come back byte-identical.
// TRLC-LINKS: REQ-SDS-033
func TestHeldRecallRoundTrip(t *testing.T) {
	var h heldRecall
	mk := func(n int, fill byte) *sample.Frame {
		f := &sample.Frame{Traces: []sample.Trace{{Codes8: make([]uint8, n), Precision: make([]uint16, n), Valid: n}, {Ch: 1, Codes8: make([]uint8, n), Precision: make([]uint16, n), Valid: n}}}
		for i := range n {
			f.Codes8(0)[i], f.Codes8(1)[i], f.Precision(0)[i], f.Precision(1)[i] = fill+byte(i), fill-byte(i), uint16(i)<<4, uint16(i)<<5
		}
		return f
	}
	src := mk(100, 7)
	src.Cond.Filter = "read"
	key := heldKey{offset: 0, words: 50}
	h.store(key, src)
	src.Cond.Filter, src.Cond.Guard, src.Cond.BandwidthHz = "cal", 3, 1e6
	h.storeCond(heldCondKey{held: key}, src)
	dst := mk(100, 0)
	q1, q2 := dst.Precision(0)[:0], dst.Precision(1)[:0]
	dst.Traces[0].Precision, dst.Traces[1].Precision = nil, nil // a full-rate frame: the storage only
	if n := h.restore(dst, q1, q2); n != 100 || dst.Cond.Filter != "read" || len(dst.Precision(0)) != 100 {
		t.Fatalf("restored %d samples, %+v", n, dst.Cond)
	}
	h.restoreCond(dst, dst.Precision(0), dst.Precision(1))
	for i := range src.Codes8(0) {
		if dst.Codes8(0)[i] != src.Codes8(0)[i] || dst.Codes8(1)[i] != src.Codes8(1)[i] || dst.Precision(0)[i] != src.Precision(0)[i] || dst.Precision(1)[i] != src.Precision(1)[i] {
			t.Fatalf("sample %d differs", i)
		}
	}
	if dst.Cond.Filter != "cal" || dst.Cond.Guard != 3 || dst.Cond.BandwidthHz != 1e6 {
		t.Fatalf("conditioning fields lost: %+v", dst)
	}
}

// Peak stream words decode to (min, max) pairs per channel, ordered to
// continue from the view: a falling edge inside a bucket is one transition.
// TRLC-LINKS: REQ-SDS-035
func TestPeakSamplesOrdered(t *testing.T) {
	e := &Engine{}
	r := &rollView{}
	r.reset(planRollPeak(0.01), 0.01, [2]core.ChScale{})
	// {max2,max1,min2,min1}: high, high->low (edge), low; CH2 constant 100.
	raw := []byte{
		200, 100, 200, 100, // bucket 0: CH1 200..200
		10, 100, 200, 100, // bucket 1: CH1 10..200 (falling edge)
		10, 100, 10, 100, // bucket 2: CH1 10..10
	}
	q1, q2 := e.peakSamples(r, raw)
	want := []uint8{200, 200, 200, 10, 10, 10}
	for i, v := range want {
		if core.RoundQ8(q1[i]) != v || core.RoundQ8(q2[i]) != 100 {
			t.Fatalf("CH1 %v, want %v; CH2 %v", q1, want, q2)
		}
	}
	if p := planRollPeak(0.01); p.screen > rollScreenSamples || p.log < 8 || p.screen%2 != 0 {
		t.Fatalf("peak plan %+v", p)
	}
}

// Every roll timebase keeps the view within rollScreenSamples, grouping in
// software beyond the largest decimation (50 s/div was 476k samples, and a
// knob step re-sized and re-drew them for seconds).
// TRLC-LINKS: REQ-SDS-010
func TestRollPlansBounded(t *testing.T) {
	for _, tdiv := range []float64{0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 50} {
		p := planRoll(tdiv)
		if p.screen > rollScreenSamples || p.group < 1 || p.chunk < 8 {
			t.Fatalf("roll plan at %g s/div: %+v", tdiv, p)
		}
		if span := float64(p.screen) * p.sampleS; math.Abs(span-10*tdiv) > p.sampleS {
			t.Fatalf("roll plan at %g s/div spans %g s", tdiv, span)
		}
		q := planRollPeak(tdiv)
		if q.screen > rollScreenSamples || q.group < 1 || q.screen%2 != 0 {
			t.Fatalf("peak plan at %g s/div: %+v", tdiv, q)
		}
		if span := float64(q.screen) * q.sampleS; math.Abs(span-10*tdiv) > 2*q.sampleS {
			t.Fatalf("peak plan at %g s/div spans %g s", tdiv, span)
		}
	}
	if planRoll(50).group == 1 || planRollPeak(50).group == 1 {
		t.Fatal("50 s/div should group in software")
	}
}

// Software groups average (chunked) or merge extremes (stream) and carry a
// partial group to the next update.
// TRLC-LINKS: REQ-SDS-010
func TestRollGroupCarries(t *testing.T) {
	r := &rollView{}
	r.reset(rollPlan{group: 4, screen: 100}, 1, [2]core.ChScale{})
	o1, _, _ := r.groupAvg(0, []uint16{100, 200, 300}, []uint16{1, 1, 1})
	if len(o1) != 0 {
		t.Fatalf("partial group published %v", o1)
	}
	o1, o2, held := r.groupAvg(2, []uint16{500, 700, 900, 1100, 1300}, []uint16{1, 1, 1, 1, 1})
	// 100 200 300 | 500 ; held 300 300 is placed before the new samples:
	// groups: {100,200,300,300} {300,500,700,900} ; carry {1100,1300}
	if len(o1) != 2 || o1[0] != 225 || o1[1] != 600 || o2[0] != 1 || held[0] || held[1] {
		t.Fatalf("groups %v %v held %v", o1, o2, held)
	}
	// a gap of six held samples: {1100,1300,1300,1300} {1300 x4} - the second
	// mostly held
	if o1, _, held := r.groupAvg(6, nil, nil); len(o1) != 2 || held[0] || !held[1] {
		t.Fatalf("held groups %v %v", o1, held)
	}
	r.reset(rollPlan{group: 3, screen: 100}, 1, [2]core.ChScale{})
	// bytes: min1 min2 max1 max2
	if out := r.groupPeak([]byte{50, 60, 70, 80, 40, 61, 75, 79}); len(out) != 0 {
		t.Fatalf("partial peak group published %v", out)
	}
	out := r.groupPeak([]byte{55, 59, 90, 81, 1, 2, 3, 4})
	if len(out) != 4 || out[0] != 40 || out[1] != 59 || out[2] != 90 || out[3] != 81 || len(r.carry) != 4 {
		t.Fatalf("peak group %v carry %v", out, r.carry)
	}
}

// A V/div round trip keeps the roll history: codes clip on the screen at the
// sensitive scale, but the history is volts (a history of codes leaves a 3.3 V
// square topped at 2.7 V after 1 -> 0.5 -> 1 V/div).
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
func TestRollRescaleKeepsHistory(t *testing.T) {
	r := &rollView{}
	one := [2]core.ChScale{{Vdiv: 1, Off: 0}, {Vdiv: 1, Off: 0}}
	r.reset(rollPlan{group: 1, screen: 6}, 1, one)
	in := []uint16{128 << 8, 140 << 8, 215 << 8, 41 << 8, 250 << 8, 5 << 8} // -0..+4.9 V
	r.push(0, in, in, nil)
	for i, v := range in {
		if r.q1[i] != v {
			t.Fatalf("code %d: %d, want %d (volts round trip)", i, r.q1[i], v)
		}
	}
	half := [2]core.ChScale{{Vdiv: 0.5, Off: 0}, {Vdiv: 0.5, Off: 0}}
	r.rescale(half, time.Now())
	if r.c1[4] != 255 || r.c1[5] != 0 {
		t.Fatalf("at 0.5 V/div the extremes should clip on screen: %v", r.c1)
	}
	r.rescale(one, time.Now())
	for i, v := range in {
		if r.q1[i] != v {
			t.Fatalf("after 1 -> 0.5 -> 1 V/div code %d is %d, want %d", i, r.q1[i], v)
		}
	}
}

// A peak view holds a gap with its last (min, max) pair, not one value.
// TRLC-LINKS: REQ-SDS-010
func TestRollPeakHoldPairs(t *testing.T) {
	r := &rollView{}
	one := [2]core.ChScale{{Vdiv: 1}, {Vdiv: 1}}
	r.reset(rollPlan{group: 1, screen: 8, peak: true}, 1, one)
	lo, hi := uint16(100<<8), uint16(200<<8)
	r.push(0, []uint16{lo, hi, lo, hi}, []uint16{lo, hi, lo, hi}, nil)
	r.push(4, nil, nil, nil)
	for i, want := range []uint16{lo, hi, lo, hi, lo, hi, lo, hi} {
		if r.q1[i] != want {
			t.Fatalf("held view %v", r.q1)
		}
	}
}

// The roll view knows which samples no capture covered - the view's past
// before the first chunk and the gaps between chunks, held at the last value -
// and measurements read its longest captured stretch, not the held ones (a
// 1 kHz square measured across them read 769 Hz).
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func TestRollHeldMarksGaps(t *testing.T) {
	r := &rollView{}
	r.reset(rollPlan{group: 1, screen: 12}, 1, [2]core.ChScale{{Vdiv: 1}, {Vdiv: 1}})
	chunk := func(n int) []uint16 { return make([]uint16, n) }
	r.push(0, chunk(4), chunk(4), nil) // the past filled from the first chunk: 8 held, 4 captured
	r.push(3, chunk(5), chunk(5), nil) // 3 held, 5 captured
	got := ""
	var gotSb285 strings.Builder
	for _, h := range r.held {
		if h {
			gotSb285.WriteString("H")
		} else {
			gotSb285.WriteString(".")
		}
	}
	got += gotSb285.String()
	// the newest 12: the first chunk (4), the gap (3), the second chunk (5)
	if got != "....HHH....." {
		t.Fatalf("held mask %s", got)
	}
	f := &sample.Frame{Traces: []sample.Trace{{Valid: 12, Held: r.held}, {Ch: 1, Valid: 12, Held: r.held}}}
	if a, b := MeasureRange(f); a != 7 || b != 12 {
		t.Fatalf("measure range %d..%d, want the second chunk 7..12", a, b)
	}
	if a, b := MeasureRange(&sample.Frame{Traces: []sample.Trace{{Valid: 12}, {Ch: 1, Valid: 12}}}); a != 0 || b != 12 {
		t.Fatalf("a frame without held samples: %d..%d", a, b)
	}
}

// The board's roll plans, chunked and streamed, are the engine's, at every
// timebase it rolls at.
// TRLC-LINKS: REQ-SDS-010
func TestBoardRollPlanIsTheEngines(t *testing.T) {
	for _, tdiv := range SupportedTdivs() {
		if tdiv < RollMinTdiv*0.999 {
			continue
		}
		p, b := planRoll(tdiv), acq.PlanRoll(tdiv)
		if b != (acq.RollPlan{Log: p.log, SampleS: p.sampleS, Screen: p.screen, Chunk: p.chunk, Group: p.group}) {
			t.Errorf("%g s/div: the board plans %+v, the engine %+v", tdiv, b, p)
		}
		p, k := planRollPeak(tdiv), acq.PlanRollPeak(tdiv)
		if k != (acq.RollPeakPlan{Log: p.log, SampleS: p.sampleS, Screen: p.screen, Group: p.group}) {
			t.Errorf("%g s/div: the board streams %+v, the engine %+v", tdiv, k, p)
		}
	}
}
