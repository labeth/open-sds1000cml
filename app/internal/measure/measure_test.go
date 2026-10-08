// ENGMODEL-OWNER-UNIT: FU-APP-MEASURE
package measure

import (
	"math"
	"testing"
)

// square builds a 50%-duty square wave: `cycles` cycles of `period` samples,
// low `base` for the first half, high `top` for the second.
// TRLC-LINKS: REQ-SDS-017
func square(period, cycles, base, top int) []uint8 {
	out := make([]uint8, period*cycles)
	for i := range out {
		if i%period < period/2 {
			out[i] = uint8(base)
		} else {
			out[i] = uint8(top)
		}
	}
	return out
}

// TRLC-LINKS: REQ-SDS-017
func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// TRLC-LINKS: REQ-SDS-017
func TestSquareWave(t *testing.T) {
	// 100-sample period @ 1 µs/sample → 10 kHz, 50 % duty. base 56, top 200.
	sig := square(100, 10, 56, 200)
	r := Compute(sig, 1.0/32, 0, 1e-6)
	if r == nil || !r.HasTiming {
		t.Fatalf("no timing on a clean square: %+v", r)
	}
	if !approx(r.Freq, 10000, 50) {
		t.Fatalf("freq = %.1f Hz, want ~10000", r.Freq)
	}
	if !approx(r.Duty, 50, 2) {
		t.Fatalf("duty = %.1f%%, want ~50", r.Duty)
	}
	// Vtop/Vbase from the histogram modes; Vampl = (200-56)/32 V.
	if !approx(r.Vampl, float64(200-56)/32, 1e-9) {
		t.Fatalf("vampl = %.4f, want %.4f", r.Vampl, float64(200-56)/32)
	}
	if !approx(r.Vtop, (200.0-128)/32, 1e-9) || !approx(r.Vbase, (56.0-128)/32, 1e-9) {
		t.Fatalf("vtop/vbase = %.4f/%.4f", r.Vtop, r.Vbase)
	}
	// A hard square edges within one sample → a resolved, sub-sample rise/fall
	// (10→90 % of a one-interval step = 0.8 sample = 0.8 µs), not zero.
	if r.RiseS <= 0 || r.RiseS > 1.5e-6 {
		t.Fatalf("rise = %.2e s, want a small nonzero (~0.8 µs)", r.RiseS)
	}
	if r.FallS <= 0 || r.FallS > 1.5e-6 {
		t.Fatalf("fall = %.2e s, want a small nonzero (~0.8 µs)", r.FallS)
	}
}

// TRLC-LINKS: REQ-SDS-017
func TestDutyCycle(t *testing.T) {
	// 25 % duty: high for the last quarter of each 100-sample period.
	out := make([]uint8, 1000)
	for i := range out {
		if i%100 >= 75 {
			out[i] = 200
		} else {
			out[i] = 56
		}
	}
	r := Compute(out, 1.0/32, 0, 1e-6)
	if !approx(r.Duty, 25, 2) {
		t.Fatalf("duty = %.1f%%, want ~25", r.Duty)
	}
}

// TRLC-LINKS: REQ-SDS-017
func TestTrapezoidRiseTime(t *testing.T) {
	// Trapezoid: 40-sample linear ramp base→top, hold, ramp down, hold.
	base, top, ramp := 56, 200, 40
	var out []uint8
	seg := func(f func(k int) int, n int) {
		for k := 0; k < n; k++ {
			out = append(out, uint8(f(k)))
		}
	}
	for c := 0; c < 6; c++ {
		seg(func(k int) int { return base }, 30)
		seg(func(k int) int { return base + (top-base)*k/ramp }, ramp) // rising
		seg(func(k int) int { return top }, 30)
		seg(func(k int) int { return top - (top-base)*k/ramp }, ramp) // falling
	}
	r := Compute(out, 1.0/32, 0, 1e-6)
	if r == nil || !r.HasTiming {
		t.Fatalf("no timing on trapezoid: %+v", r)
	}
	// 10→90 % of a linear ramp spans 0.8 of the ramp → ~32 samples = 32 µs.
	want := 0.8 * float64(ramp) * 1e-6
	if !approx(r.RiseS, want, 4e-6) {
		t.Fatalf("rise = %.2e s, want ~%.2e", r.RiseS, want)
	}
	if !approx(r.FallS, want, 4e-6) {
		t.Fatalf("fall = %.2e s, want ~%.2e", r.FallS, want)
	}
}

// TRLC-LINKS: REQ-SDS-017
func TestFlatHasNoTiming(t *testing.T) {
	flat := make([]uint8, 500)
	for i := range flat {
		flat[i] = 128
	}
	r := Compute(flat, 1.0/32, 0, 1e-6)
	if r == nil {
		t.Fatal("nil result for flat record")
	}
	if r.HasTiming {
		t.Fatalf("flat record reported timing: %+v", r)
	}
	if r.Vpp != 0 {
		t.Fatalf("flat vpp = %v, want 0", r.Vpp)
	}
}

// TRLC-LINKS: REQ-SDS-017
func TestClipped(t *testing.T) {
	n := 800
	// Clean square well inside the range (codes 56/200) — not clipped.
	clean := square(100, 8, 56, 200)
	if Clipped(clean) {
		t.Fatal("clean square flagged as clipped")
	}
	// Low-side clipping: this hardware floors at ~code 6 (not 0), flat-topped there.
	lowClip := make([]uint8, n)
	for i := range lowClip {
		if i%100 < 50 {
			lowClip[i] = 6 // pinned at the low rail band
		} else {
			lowClip[i] = 180
		}
	}
	if !Clipped(lowClip) {
		t.Fatal("low-rail clipping (code 6) not detected")
	}
	// High-side clipping at the hard clamp (code 254), flat-topped — the case the
	// old <=1/>=254 detector missed (it needed >=254; a signal topping at 253 is
	// also caught now).
	hiClip := make([]uint8, n)
	for i := range hiClip {
		if i%100 < 50 {
			hiClip[i] = 60
		} else {
			hiClip[i] = 254
		}
	}
	if !Clipped(hiClip) {
		t.Fatal("high-rail clipping (code 254) not detected")
	}
	// A clean square whose real HIGH level sits near the top of screen (code 249,
	// = 3.9 divisions) but is NOT clamped must NOT be flagged (the 1 V/div
	// false-positive found on hardware).
	nearTop := make([]uint8, n)
	for i := range nearTop {
		if i%100 < 50 {
			nearTop[i] = 141 // ~0.4 V at 1 V/div
		} else {
			nearTop[i] = 249 // ~3.8 V at 1 V/div — near the top edge, not clamped
		}
	}
	if Clipped(nearTop) {
		t.Fatal("clean signal topping at code 249 falsely flagged as clipped")
	}
	// A clean sine with real headroom (peaks ~228/28, inside codes 8–248) is NOT
	// clipped — it never enters the rail band.
	clean2 := make([]uint8, n)
	for i := range clean2 {
		clean2[i] = uint8(128 + int(100*math.Sin(float64(i)/float64(n)*8*math.Pi)))
	}
	if Clipped(clean2) {
		t.Fatal("clean sine with headroom falsely flagged as clipped")
	}
}

// TRLC-LINKS: REQ-SDS-017
func TestEmptyRecord(t *testing.T) {
	if Compute(nil, 1, 0, 1e-6) != nil {
		t.Fatal("expected nil for empty record")
	}
}

// TRLC-LINKS: REQ-SDS-017
func TestOvershoot(t *testing.T) {
	// Square with a one-sample overshoot spike above the settled top.
	sig := square(100, 8, 56, 200)
	for i := 50; i < len(sig); i += 100 {
		sig[i] = 230 // ring above top (200) right after the rising edge
	}
	r := Compute(sig, 1.0/32, 0, 1e-6)
	if r.Overshoot <= 0 {
		t.Fatalf("overshoot not detected: %.2f%%", r.Overshoot)
	}
	// (230-200)/(200-56) ≈ 20.8 %.
	if !approx(r.Overshoot, 30.0/144*100, 5) {
		t.Fatalf("overshoot = %.1f%%, want ~20.8", r.Overshoot)
	}
}

// avgWidthBrute is the pre-merge reference implementation (rescan per crossing).
// TRLC-LINKS: REQ-SDS-017
func avgWidthBrute(from, to []float64) float64 {
	if len(from) == 0 || len(to) == 0 {
		return 0
	}
	var sum float64
	var cnt int
	for _, f := range from {
		for _, t := range to {
			if t > f {
				sum += t - f
				cnt++
				break
			}
		}
	}
	if cnt == 0 {
		return 0
	}
	return sum / float64(cnt)
}

// TestAvgWidthMergeParity property-checks the merge-pass avgWidth against the
// brute-force reference on randomized ascending crossing lists (the shape
// Compute produces), including empty/disjoint/interleaved cases.
// TRLC-LINKS: REQ-SDS-017
func TestAvgWidthMergeParity(t *testing.T) {
	rng := uint64(1)
	rand01 := func() float64 { // xorshift; deterministic, no seed plumbing
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		return float64(rng%1e6) / 1e6
	}
	ascending := func(n int, start float64) []float64 {
		out := make([]float64, n)
		x := start
		for i := range out {
			x += rand01() * 3
			out[i] = x
		}
		return out
	}
	for trial := 0; trial < 500; trial++ {
		nf, nt := trial%17, (trial*7)%23
		from := ascending(nf, rand01()*10)
		to := ascending(nt, rand01()*10)
		got, want := avgWidth(from, to), avgWidthBrute(from, to)
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("trial %d: avgWidth=%v brute=%v from=%v to=%v", trial, got, want, from, to)
		}
	}
	// Degenerate pins: all `to` before every `from`.
	if got := avgWidth([]float64{5, 6}, []float64{1, 2}); got != 0 {
		t.Fatalf("no-pair case = %v, want 0", got)
	}
}

// TRLC-LINKS: REQ-SDS-017
func TestFractionalAcquisitionMeasurements(t *testing.T) {
	q := []uint16{25600, 25728, 25600, 25728}
	r := ComputeQ8(q, 1, 0, 1)
	if r.Vmean != -27.75 || r.Vpp != .5 || r.VrmsAC != .25 || r.Vrms != math.Hypot(27.75, .25) {
		t.Fatalf("fractional mean/range/AC RMS lost: %+v", r)
	}
	r = ComputeAcquisition([]byte{100, 101, 100, 101}, q, 1, 0, 1, 1)
	if r.Vmean != 0 || r.Vmin != -.25 || r.Vmax != .25 {
		t.Fatalf("AC coupling quantized: %+v", r)
	}
}

// TRLC-LINKS: REQ-SDS-017
func TestQ8SmallRippleOnLargeDC(t *testing.T) {
	// Full precision depth excluding the 31-sample filter guard at each end.
	const n = 524288 - 62
	want := math.Sqrt(float64(n-1)) / float64(n) / 256
	for _, dc := range []uint16{0, 32768, 60000} {
		for _, outlier := range []int{0, n - 1} {
			sig := make([]uint16, n)
			for i := range sig {
				sig[i] = dc
			}
			sig[outlier]++
			r := ComputeQ8(sig, 1, 0, 1e-6)
			if math.Abs(r.VrmsAC-want) > want*1e-9 {
				t.Fatalf("DC %d outlier %d: AC RMS %.12g, want %.12g", dc, outlier, r.VrmsAC, want)
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-017
func TestNoisyTriangleTiming(t *testing.T) {
	q := make([]uint16, 500*40)
	for i := range q {
		phase := i % 500
		if phase > 250 {
			phase = 500 - phase
		}
		// Large oversampling makes even modest code noise recross mid-level.
		q[i] = uint16(80*256 + phase*80*256/250 + ((i*17)%7-3)*128)
	}
	r := ComputeQ8(q, 1, 0, 2e-9)
	if !r.HasTiming || math.Abs(r.Freq/1e6-1) > .005 {
		t.Fatalf("noisy triangle frequency: %+v", r)
	}
}

// TRLC-LINKS: REQ-SDS-021
func TestDropTimingKeepsAmplitude(t *testing.T) {
	sig := make([]uint8, 2000)
	for i := range sig {
		sig[i] = 60
		if (i/50)%2 == 1 {
			sig[i] = 200
		}
	}
	r := Compute(sig, 0.04, 0, 2e-9)
	if !r.HasTiming || r.Freq == 0 {
		t.Fatalf("square wave without timing: %+v", r)
	}
	c := r.WithoutTiming()
	if c.HasTiming || c.Freq != 0 || c.Period != 0 || c.Duty != 0 || c.Vpp != r.Vpp {
		t.Fatalf("WithoutTiming = %+v", c)
	}
	if !r.HasTiming {
		t.Fatal("WithoutTiming changed the original")
	}
}

// TRLC-LINKS: REQ-SDS-017
func TestQ8TopBaseIgnoreOvershoot(t *testing.T) {
	// A 0..3.3 V square at 1 V/div (25 codes/div, offset −1.65 V) with 16%
	// overshoot for 30 samples after each edge, as CH1 measured on the bench.
	const vpc = 1.0 / 25
	code := func(v float64) uint16 { return uint16((128 + (v-1.65)*25) * 256) }
	q := make([]uint16, 0, 5000)
	for p := 0; p < 10; p++ {
		for i := 0; i < 250; i++ {
			v := 3.3
			if i < 30 {
				v = 3.84
			}
			q = append(q, code(v))
		}
		for i := 0; i < 250; i++ {
			v := 0.0
			if i < 30 {
				v = -0.5
			}
			q = append(q, code(v))
		}
	}
	r := ComputeQ8(q, vpc, -1.65, 2e-9)
	if math.Abs(r.Vtop-3.3) > 0.03 || math.Abs(r.Vbase) > 0.03 {
		t.Fatalf("top/base %.3f/%.3f, want 3.3/0 (overshoot must not win)", r.Vtop, r.Vbase)
	}
	if r.Overshoot < 10 {
		t.Fatalf("overshoot %.1f%%, want about 16%%", r.Overshoot)
	}
}

// A 1 M-sample Q8.8 square keeps its top and base: the settled-level sum
// overflowed a 32-bit int (run with GOARCH=386 to cover the device's width).
// TRLC-LINKS: REQ-SDS-017
func TestQ8TopBaseLongRecord(t *testing.T) {
	q := make([]uint16, 1<<20)
	for i := range q {
		if (i/500)%2 == 0 {
			q[i] = 210 << 8
		} else {
			q[i] = 128 << 8
		}
	}
	r := ComputeQ8(q, 0.04, 0, 2e-9)
	if math.Abs(r.Vtop-3.28) > 0.01 || math.Abs(r.Vbase) > 0.01 || !r.HasTiming {
		t.Fatalf("top %v base %v timing %v", r.Vtop, r.Vbase, r.HasTiming)
	}
}

// A peak-detect envelope still yields frequency and duty from its bucket
// midpoints; a period shorter than eight buckets yields none.
// TRLC-LINKS: REQ-SDS-021
func TestEnvelopeTiming(t *testing.T) {
	envelope := func(periodSamples, bucket, buckets int) []uint8 {
		env := make([]uint8, 0, 2*buckets)
		for b := 0; b < buckets; b++ {
			mn, mx := uint8(255), uint8(0)
			for i := b * bucket; i < (b+1)*bucket; i++ {
				v := uint8(78)
				if i%periodSamples < periodSamples*3/10 { // 30 % duty
					v = 178
				}
				mn, mx = min(mn, v), max(mx, v)
			}
			env = append(env, mn, mx)
		}
		return env
	}
	const sampleS = 2e-9
	bucket := 96                         // samples per bucket; one envelope value per half bucket
	env := envelope(10000, bucket, 1024) // 20 us period, ~104 buckets each
	r := Compute(env, .04, 0, float64(bucket)*sampleS/2).WithEnvelopeTiming(env, .04, 0, float64(bucket)*sampleS/2)
	if !r.HasTiming || math.Abs(r.Freq-50e3)/50e3 > .01 || math.Abs(r.Duty-30) > 2 || r.RiseS != 0 {
		t.Fatalf("envelope timing: freq %.1f duty %.1f rise %g has %v", r.Freq, r.Duty, r.RiseS, r.HasTiming)
	}
	// Across periods: a reported frequency is right to within a bucket per
	// period, or there is none.
	for period := 300; period <= 30000; period = period*21/20 + 1 {
		e := envelope(period, bucket, 1024)
		ss := float64(bucket) * sampleS / 2
		r := Compute(e, .04, 0, ss).WithEnvelopeTiming(e, .04, 0, ss)
		want := 1 / (float64(period) * sampleS)
		if r.HasTiming && math.Abs(r.Freq-want)/want > float64(bucket)/float64(period)+.01 {
			t.Fatalf("period %d samples: %.0f Hz, want %.0f", period, r.Freq, want)
		}
		if !r.HasTiming && period > 16*bucket {
			t.Fatalf("period %d samples (%d buckets): no timing", period, period/bucket)
		}
	}
	fast := envelope(400, bucket, 1024) // ~4 buckets a period: unresolved
	ss := float64(bucket) * sampleS / 2
	if r := Compute(fast, .04, 0, ss).WithEnvelopeTiming(fast, .04, 0, ss); r.HasTiming || r.Freq != 0 {
		t.Fatalf("unresolved envelope reports %.1f Hz", r.Freq)
	}
}
