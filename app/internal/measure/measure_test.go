// ENGMODEL-OWNER-UNIT: FU-APP-MEASURE
package measure

import (
	"math"
	"math/rand"
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

// An inverted channel reads its levels negated and its edges swapped.
// TRLC-LINKS: REQ-SDS-021
func TestResultInverted(t *testing.T) {
	r := &Result{Vpp: 3.3, Vmax: 3.3, Vmin: 0, Vmean: 1.6, Vtop: 3.2, Vbase: 0.1, Vampl: 3.1,
		Overshoot: 5, Preshoot: 1, Freq: 5000, Duty: 30, RiseS: 1e-9, FallS: 2e-9,
		PosWidthS: 60e-6, NegWidthS: 140e-6, HasTiming: true}
	i := r.Inverted()
	if i.Vmax != 0 || i.Vmin != -3.3 || i.Vmean != -1.6 || i.Vtop != -0.1 || i.Vbase != -3.2 || i.Vpp != 3.3 || i.Vampl != 3.1 {
		t.Fatalf("levels %+v", i)
	}
	if i.Overshoot != 1 || i.Preshoot != 5 || i.RiseS != 2e-9 || i.FallS != 1e-9 || i.PosWidthS != 140e-6 || i.NegWidthS != 60e-6 || i.Duty != 70 || i.Freq != 5000 {
		t.Fatalf("timing %+v", i)
	}
	if r.Vmax != 3.3 {
		t.Fatal("Inverted must not modify its receiver (cached results)")
	}
}

// An envelope frame's edge shape comes from its full-rate edge windows: the
// rise and fall of the windows' clean edges and their over/preshoot, the
// envelope's frequency and duty kept; a window without an edge adds nothing.
// TRLC-LINKS: REQ-SDS-021
func TestEdgeDetailFromWindows(t *testing.T) {
	const sampleS = 2e-9
	// edge is 2048 samples at 78 (base) or 178 (top), switching at sample 1000
	// through a 10-sample linear edge, a 5-code overshoot after it.
	edge := func(rising bool) []uint8 {
		w := make([]uint8, 2048)
		for i := range w {
			x := float64(i-1000) / 10
			v := 78 + 100*math.Max(0, math.Min(1, x))
			if i >= 1010 && i < 1020 {
				v += 5
			}
			if !rising {
				v = 256 - v
			}
			w[i] = uint8(math.Round(v))
		}
		return w
	}
	env := make([]uint8, 0, 2048)
	for b := 0; b < 1024; b++ { // a 30 % square, 104 buckets a period
		v := uint8(78)
		if b%104 < 31 {
			v = 178
		}
		env = append(env, v, v)
	}
	ss := 96 * sampleS / 2
	base := Compute(env, .04, 0, ss).WithEnvelopeTiming(env, .04, 0, ss)
	if base.RiseS != 0 || base.EdgeShape {
		t.Fatalf("envelope alone: rise %g edge shape %v", base.RiseS, base.EdgeShape)
	}
	r := base.WithEdgeDetail(.04, 0, EdgeWindow{Sig: edge(true), SampleS: sampleS, Shape: true}, EdgeWindow{Sig: edge(false), SampleS: sampleS, Shape: true})
	if !r.EdgeShape || math.Abs(r.RiseS-16e-9) > 2.5e-9 || math.Abs(r.FallS-16e-9) > 2.5e-9 {
		t.Fatalf("edge windows: rise %g fall %g shape %v", r.RiseS, r.FallS, r.EdgeShape)
	}
	if math.Abs(r.Overshoot-5) > 1.5 || r.Freq != base.Freq || r.Duty != base.Duty {
		t.Fatalf("edge windows: overshoot %.1f %%, freq %g (was %g), duty %g (was %g)", r.Overshoot, r.Freq, base.Freq, r.Duty, base.Duty)
	}
	flat := make([]uint8, 2048)
	for i := range flat {
		flat[i] = 128
	}
	if f := base.WithEdgeDetail(.04, 0, EdgeWindow{Sig: flat, SampleS: sampleS, Shape: true}); f.EdgeShape || f.RiseS != 0 {
		t.Fatalf("a flat window gave rise %g", f.RiseS)
	}
	// A decimated record's window: its edge is the filter's, no shape.
	if d := base.WithEdgeDetail(.04, 0, EdgeWindow{Sig: edge(true), SampleS: sampleS}); d.EdgeShape || d.RiseS != 0 {
		t.Fatalf("a decimated window gave rise %g", d.RiseS)
	}
}

// An envelope of a signal faster than its buckets holds no mean or RMS: every
// bucket spans the swing, so the pairs' mean is the midpoint (a pulse train's
// read 1.63 V for 0.60 V). It is flagged, and the edge windows - several
// periods each then - give the mean and RMS.
// TRLC-LINKS: REQ-SDS-021
func TestAliasedEnvelopeMeanFromWindows(t *testing.T) {
	const vpc = 3.3 / 100
	const off = -78 * vpc         // code 50 reads 0 V, 150 reads 3.3 V
	env := make([]uint8, 0, 2048) // every bucket spans 50..150: a pulse train faster than a bucket
	for b := 0; b < 1024; b++ {
		env = append(env, 50, 150)
	}
	r := Compute(env, vpc, off, 1e-6).WithEnvelopeTiming(env, vpc, off, 1e-6)
	if !r.EnvAliased {
		t.Fatal("an envelope spanning the swing in every bucket is not flagged")
	}
	if got := r.WithEdgeDetail(vpc, off); !got.EnvAliased {
		t.Fatal("no windows: the mean must stay unknown")
	}
	pulses := func() []uint8 { // 18 % high, a 60-sample period, 1024 samples
		w := make([]uint8, 1024)
		for i := range w {
			w[i] = 50
			if i%60 < 11 {
				w[i] = 150
			}
		}
		return w
	}
	got := r.WithEdgeDetail(vpc, off, EdgeWindow{Sig: pulses(), SampleS: 64e-9}, EdgeWindow{Sig: pulses(), SampleS: 64e-9})
	frac := float64(11*17+11) / 1024 // high samples in the window
	if got.EnvAliased || math.Abs(got.Vmean-3.3*frac) > 0.05 || math.Abs(got.Vrms-3.3*math.Sqrt(frac)) > 0.05 {
		t.Fatalf("mean %.3f rms %.3f aliased %v, want %.3f %.3f", got.Vmean, got.Vrms, got.EnvAliased, 3.3*frac, 3.3*math.Sqrt(frac))
	}
	// A resolved envelope (a slow square) is not flagged.
	slow := make([]uint8, 0, 2048)
	for b := 0; b < 1024; b++ {
		v := uint8(50)
		if b%200 < 100 {
			v = 150
		}
		slow = append(slow, v, v)
	}
	if s := Compute(slow, vpc, off, 1e-6).WithEnvelopeTiming(slow, vpc, off, 1e-6); s.EnvAliased {
		t.Fatal("a resolved square flagged as aliased")
	}
}

// A pulse narrower than a bucket - a bucket spanning the swing between two at
// the same level - leaves the envelope's timing unknown: bucket midpoints miss
// it and the frequency read too low (51 kHz for 66.8 kHz).
// TRLC-LINKS: REQ-SDS-021
func TestEnvelopeNarrowPulseDropsTiming(t *testing.T) {
	env := make([]uint8, 0, 2048) // 30 % square, 104-bucket period, and a one-bucket pulse
	for b := 0; b < 1024; b++ {
		lo, hi := uint8(78), uint8(78)
		switch {
		case b%104 < 31:
			lo, hi = 178, 178
		case b%104 == 60:
			hi = 178 // a pulse inside one low bucket
		case b%104 == 80 || b%104 == 81:
			hi = 178 // a pulse across two low buckets
		}
		env = append(env, lo, hi)
	}
	ss := 96 * 2e-9 / 2
	if r := Compute(env, .04, 0, ss).WithEnvelopeTiming(env, .04, 0, ss); r.HasTiming {
		t.Fatalf("timing with sub-bucket pulses: %.0f Hz", r.Freq)
	}
	// the one-bucket pulse alone, then the two-bucket one alone
	one, two := append([]uint8(nil), env...), append([]uint8(nil), env...)
	for b := 0; b < 1024; b++ {
		switch b % 104 {
		case 80, 81:
			one[2*b+1] = 78
		case 60:
			two[2*b+1] = 78
		}
	}
	for name, e := range map[string][]uint8{"one-bucket": one, "two-bucket": two} {
		if r := Compute(e, .04, 0, ss).WithEnvelopeTiming(e, .04, 0, ss); r.HasTiming {
			t.Errorf("timing with a %s pulse: %.0f Hz", name, r.Freq)
		}
	}
}

// A Q8.8 record's top and base are the settled levels of its modal codes, the
// same as from a 65536-bin histogram coarsened per code, without building it.
// TRLC-LINKS: REQ-SDS-017
func TestComputeQ8LevelsMatchTheFineHistogram(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	reference := func(q []uint16) (top, base int) { // the per-bin coarsening
		hist := make([]int, 65536)
		cmin, cmax := int(q[0]), int(q[0])
		for _, v := range q {
			hist[v]++
			cmin, cmax = min(cmin, int(v)), max(cmax, int(v))
		}
		coarse := make([]int, 256)
		for i, c := range hist {
			coarse[i/256] += c
		}
		settled := func(code int) int {
			var n, sum int64
			for c := code * 256; c < code*256+256; c++ {
				n += int64(hist[c])
				sum += int64(hist[c]) * int64(c)
			}
			return int((sum + n/2) / n)
		}
		mid := (cmin + cmax) / 2
		top, base = cmax, cmin
		if c := modeInRange(coarse, mid/256+1, cmax/256); c >= 0 {
			top = settled(c)
		}
		if c := modeInRange(coarse, cmin/256, mid/256); c >= 0 {
			base = settled(c)
		}
		return top, base
	}
	for trial := 0; trial < 300; trial++ {
		n := 64 + rng.Intn(4000)
		lo, hi := 20+rng.Intn(80), 140+rng.Intn(100)
		q := make([]uint16, n)
		for i := range q {
			c := lo
			if rng.Intn(100) < 40 {
				c = hi
			}
			q[i] = uint16(c<<8 + rng.Intn(768) - 384) // about +-1.5 codes of noise
		}
		top, base := reference(q)
		r := ComputeQ8(q, 1, 0, 2e-9)
		if gotTop, gotBase := int(math.Round(r.Vtop*256))+32768, int(math.Round(r.Vbase*256))+32768; gotTop != top || gotBase != base {
			t.Fatalf("trial %d: top %d base %d, want %d %d", trial, gotTop, gotBase, top, base)
		}
	}
}

// A signal faster than the envelope's buckets has no timing there; edge
// windows holding whole periods give its frequency, period, duty and widths.
// A resolved envelope keeps its own; windows shorter than a period give none.
// TRLC-LINKS: REQ-SDS-021
func TestWindowTimingWhenTheEnvelopeHasNone(t *testing.T) {
	const vpc = 3.3 / 100
	const off = -78 * vpc
	env := make([]uint8, 0, 2048) // every bucket spans the swing
	for b := 0; b < 1024; b++ {
		env = append(env, 50, 150)
	}
	r := Compute(env, vpc, off, 492e-9).WithEnvelopeTiming(env, vpc, off, 492e-9)
	if r.HasTiming {
		t.Fatal("an aliased envelope reports timing")
	}
	square := func(period, high, phase int) []uint8 { // 2048 samples
		w := make([]uint8, 2048)
		for i := range w {
			w[i] = 50
			if (i+phase)%period < high {
				w[i] = 150
			}
		}
		return w
	}
	// 1 MHz at 2 ns, 30 % duty: four periods a window
	got := r.WithEdgeDetail(vpc, off, EdgeWindow{Sig: square(500, 150, 0), SampleS: 2e-9, Shape: true},
		EdgeWindow{Sig: square(500, 150, 230), SampleS: 2e-9, Shape: true})
	if !got.HasTiming || got.EnvTiming || math.Abs(got.Freq-1e6) > 1e3 || math.Abs(got.Duty-30) > 0.5 ||
		math.Abs(got.PosWidthS-300e-9) > 3e-9 || math.Abs(got.NegWidthS-700e-9) > 3e-9 {
		t.Fatalf("timing %v freq %.0f duty %.2f widths %.1f/%.1f ns", got.HasTiming, got.Freq, got.Duty, got.PosWidthS*1e9, got.NegWidthS*1e9)
	}
	// a period longer than the window: nothing to time
	if slow := r.WithEdgeDetail(vpc, off, EdgeWindow{Sig: square(5000, 1500, 0), SampleS: 2e-9, Shape: true}); slow.HasTiming {
		t.Fatalf("timing %.0f Hz from a window shorter than a period", slow.Freq)
	}
	// a resolved envelope keeps its own timing
	resolved := &Result{HasTiming: true, EnvTiming: true, Freq: 2500, Period: 400e-6, Duty: 50}
	if kept := resolved.WithEdgeDetail(vpc, off, EdgeWindow{Sig: square(500, 150, 0), SampleS: 2e-9, Shape: true}); kept.Freq != 2500 || !kept.EnvTiming {
		t.Fatalf("the envelope's timing replaced: %.0f Hz", kept.Freq)
	}
}

// TestBurstClockFrequency: a clock in bursts (an SPI flash read: 69 bytes of
// a 100 MHz clock, then an idle gap) reads its clock's frequency, not the
// average rate across the gaps (a 104 MHz read measured 75.5 MHz).
// TRLC-LINKS: REQ-SDS-017
func TestBurstClockFrequency(t *testing.T) {
	sig := make([]uint8, 0, 20000)
	for len(sig) < 19000 {
		for c := 0; c < 552; c++ {
			sig = append(sig, 40, 40, 200, 200, 200) // 10 ns at 2 ns a sample, wrong duty on purpose
		}
		for k := 0; k < 1000; k++ {
			sig = append(sig, 40)
		}
	}
	r := Compute(sig, 0.04, 0, 2e-9)
	if !r.HasTiming || math.Abs(r.Freq/100e6-1) > .001 {
		t.Fatalf("freq %.2f MHz, want the burst's 100 MHz", r.Freq/1e6)
	}
}
