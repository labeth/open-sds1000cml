// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package framecalc

import (
	"math"
	"math/rand"
	"testing"
)

// Mags' half-length real transform gives the full complex transform's
// magnitudes: same bins, same peak.
// TRLC-LINKS: REQ-SDS-070
func TestMagsMatchesTheComplexTransform(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, n := range []int{16, 1024, 8192} {
		x := make([]float64, n)
		for i := range x {
			x[i] = 128 + 50*math.Sin(float64(i)*0.37) + 20*math.Sin(float64(i)*2.1) + rng.Float64()*4
		}
		var mean float64
		for _, v := range x {
			mean += v
		}
		mean /= float64(n)
		re, im := make([]float64, n), make([]float64, n)
		for i, v := range x {
			re[i] = (v - mean) * (0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1)))
		}
		FFT(re, im)
		mags, peak, peakK := Mags(x)
		if len(mags) != n/2 {
			t.Fatalf("n %d: %d bins", n, len(mags))
		}
		for k := range mags {
			if want := math.Hypot(re[k], im[k]); math.Abs(mags[k]-want) > 1e-9*math.Max(1, want) {
				t.Fatalf("n %d bin %d: %g, want %g", n, k, mags[k], want)
			}
		}
		if want := math.Hypot(re[peakK], im[peakK]); peak != mags[peakK] || math.Abs(peak-want) > 1e-9*want {
			t.Fatalf("n %d: peak %g at %d", n, peak, peakK)
		}
	}
}
