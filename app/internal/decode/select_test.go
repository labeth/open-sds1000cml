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

// runningMedian gives medianOf of every prefix of a list, ties and all.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func TestRunningMedianIsMedianOf(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 200; trial++ {
		var m runningMedian
		var v []float64
		if m.value() != 0 {
			t.Fatal("empty median not 0")
		}
		for i := 0; i < 1+rng.Intn(300); i++ {
			x := float64(rng.Intn(20)) + rng.Float64()*float64(trial%2)
			v = append(v, x)
			m.add(x)
			if got, want := m.value(), medianOf(v); got != want {
				t.Fatalf("trial %d after %d: %v, want %v", trial, len(v), got, want)
			}
		}
	}
}
