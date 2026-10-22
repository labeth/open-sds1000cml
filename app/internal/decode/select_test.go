// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

// nthValue is the value a sort would put at k, for every k, on lines of
// runs with ties, NaNs and infinities; the input is left as it was.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214
func TestNthValueIsTheSortedValue(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 300; trial++ {
		n := rng.Intn(60)
		v := make([]float64, n)
		for i := range v {
			switch rng.Intn(9) {
			case 0:
				v[i] = math.NaN()
			case 1:
				v[i] = math.Inf(1 - 2*rng.Intn(2))
			default:
				v[i] = float64(rng.Intn(7)) // ties, as run lengths in samples have
			}
		}
		in := append([]float64(nil), v...)
		want := append([]float64(nil), v...)
		sort.Float64s(want)
		for k := 0; k < n; k++ {
			got := nthValue(v, k)
			if got != want[k] && !(math.IsNaN(got) && math.IsNaN(want[k])) {
				t.Fatalf("%v: k %d got %v want %v", in, k, got, want[k])
			}
		}
		for i := range v {
			if v[i] != in[i] && !(math.IsNaN(v[i]) && math.IsNaN(in[i])) {
				t.Fatalf("the input changed at %d", i)
			}
		}
	}
}
