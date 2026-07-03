package measure

import (
	"math"
	"testing"
)

// square builds a 50%-duty square wave: `cycles` cycles of `period` samples,
// low `base` for the first half, high `top` for the second.
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

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

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

func TestEmptyRecord(t *testing.T) {
	if Compute(nil, 1, 0, 1e-6) != nil {
		t.Fatal("expected nil for empty record")
	}
}

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
