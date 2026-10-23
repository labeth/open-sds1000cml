// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"math"
	"testing"
)

// floorInt agrees with math.Floor on whole, fractional, negative, huge and
// NaN positions.
// TRLC-LINKS: REQ-SDS-021
func TestFloorIntIsFloor(t *testing.T) {
	for _, v := range []float64{0, 0.5, 1, 1.999999, -0.000001, -0.5, -1, -1.5, 12345.678, -98765.4321,
		1<<31 - 0.5, -1 << 31, -1<<31 - 0.5, 3e9, -3e9, math.Nextafter(7, 0), math.Nextafter(-7, 0)} {
		if got, want := floorInt(v), int(math.Floor(v)); got != want {
			t.Errorf("floorInt(%v) = %d, want %d", v, got, want)
		}
	}
	for k := -3000; k <= 3000; k++ {
		v := float64(k) * 0.37
		if got, want := floorInt(v), int(math.Floor(v)); got != want {
			t.Fatalf("floorInt(%v) = %d, want %d", v, got, want)
		}
	}
	_ = floorInt(math.NaN())
}
