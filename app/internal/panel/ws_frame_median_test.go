// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

// median is the value at the middle of the sorted values, as a sort gives
// it: random, repeated and NaN-holding inputs of every small length.
// TRLC-LINKS: REQ-SDS-216
func TestMedianAsSorted(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for n := 1; n < 200; n++ {
		for trial := 0; trial < 20; trial++ {
			xs := make([]float64, n)
			for i := range xs {
				switch trial % 4 {
				case 0:
					xs[i] = rng.NormFloat64()
				case 1:
					xs[i] = float64(rng.Intn(4))
				case 2:
					xs[i] = float64(i)
				default:
					xs[i] = -90 + rng.Float64()
					if rng.Intn(9) == 0 {
						xs[i] = math.NaN()
					}
				}
			}
			s := append([]float64(nil), xs...)
			sort.Float64s(s)
			want, got := s[n/2], median(xs)
			if !(got == want || math.IsNaN(got) && math.IsNaN(want)) {
				t.Fatalf("n %d trial %d: median %v, sorted middle %v", n, trial, got, want)
			}
		}
	}
	if !math.IsNaN(median(nil)) {
		t.Fatal("median of none is not NaN")
	}
}
