// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"math"
	"testing"
)

// TRLC-LINKS: REQ-SDS-215
func BenchmarkFinite(b *testing.B) {
	v := []float64{1, -2.5, math.NaN(), math.Inf(1), 3e300, -0.0, math.Inf(-1), 7}
	n := 0
	for b.Loop() {
		for _, x := range v {
			if finite(x) {
				n++
			}
		}
	}
	_ = n
}

// finite is the NaN and infinity test it was.
// TRLC-LINKS: REQ-SDS-215
func TestFiniteIsTheTest(t *testing.T) {
	for _, x := range []float64{0, -0.0, 1, -1, math.MaxFloat64, -math.MaxFloat64, math.SmallestNonzeroFloat64, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if finite(x) != (!math.IsNaN(x) && !math.IsInf(x, 0)) {
			t.Fatalf("%v", x)
		}
	}
}

// TRLC-LINKS: REQ-SDS-217
func BenchmarkStatsAdd(b *testing.B) {
	var s Stats
	v := 0.0
	for b.Loop() {
		v += 0.37
		s.Add(math.Sin(v))
	}
}

// The builtins pick what math.Min and math.Max did, signed zeros included.
// TRLC-LINKS: REQ-SDS-217
func TestStatsMinMaxSignedZero(t *testing.T) {
	for _, p := range [][2]float64{{0, math.Copysign(0, -1)}, {math.Copysign(0, -1), 0}, {1, 2}, {-3, -1}} {
		var s Stats
		s.Add(p[0])
		s.Add(p[1])
		if math.Float64bits(s.Min()) != math.Float64bits(math.Min(p[0], p[1])) || math.Float64bits(s.Max()) != math.Float64bits(math.Max(p[0], p[1])) {
			t.Fatalf("%v: min %v max %v", p, s.Min(), s.Max())
		}
	}
}
