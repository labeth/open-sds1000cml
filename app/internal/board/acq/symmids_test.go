// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"fmt"
	"math"
	"testing"
)

// The table's middles ascend, so each's run is the bucket-by-bucket test.
// TRLC-LINKS: REQ-SDS-013
func TestSymMidsAscend(t *testing.T) {
	for i := 1; i < len(SymMids); i++ {
		if !(SymMids[i] > SymMids[i-1]) {
			t.Fatalf("middle %d (%v) not above %d (%v)", i, SymMids[i], i-1, SymMids[i-1])
		}
	}
	tab := &SymTab{Prescale: 2}
	for _, w := range [][2]float64{{0, math.Inf(1)}, {1e-6, 2e-6}, {3e-3, 1e-2}, {5, 4}, {math.NaN(), 1}, {0, math.NaN()}} {
		var got, want []int
		tab.Each(true, w[0], w[1], func(i int) { got = append(got, i) })
		clo, chi := tab.Count(w[0]), tab.Count(w[1])
		for i, mid := range SymMids {
			if mid >= clo && mid <= chi {
				want = append(want, i|1024)
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("window %v: %v, want %v", w, got, want)
		}
	}
}
