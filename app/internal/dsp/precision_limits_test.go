package dsp

import (
	"math"
	"testing"
)

func TestPrecisionNoiseAgainstImpulse(t *testing.T) {
	for _, r := range []int{16, 32, 64, 256} {
		h := []float64{1}
		for stage := 0; stage < 3; stage++ {
			out := make([]float64, len(h)+r-1)
			for i, v := range h {
				for j := 0; j < r; j++ {
					out[i+j] += v / float64(r)
				}
			}
			h = out
		}
		combined := make([]float64, len(h)+(len(precisionTaps)-1)*r)
		for i, c := range precisionTaps {
			for j, v := range h {
				combined[i*r+j] += float64(c) / (1 << 22) * v
			}
		}
		power := 0.0
		for _, v := range combined {
			power += v * v
		}
		gain, bw := PrecisionLimits(r)
		if math.Abs(gain-1/math.Sqrt(power)) > 1e-8 || !(bw > .075 && bw < .5) {
			t.Fatalf("R%d limits %g %g", r, gain, bw)
		}
		t.Logf("R%d ideal +%.2f bits single, %.2f with four; -3dB %.3f output Fs", r, math.Log2(gain), math.Log2(gain*2), bw)
	}
}
