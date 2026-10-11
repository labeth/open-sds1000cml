// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"math"
	"testing"
)

// TestStats checks the running figures against a direct two-pass computation,
// including the values that are skipped and an empty or single-value run.
// TRLC-LINKS: REQ-SDS-217
func TestStats(t *testing.T) {
	var s Stats
	if s.N() != 0 || !math.IsNaN(s.Mean()) || !math.IsNaN(s.Min()) || !math.IsNaN(s.Max()) ||
		!math.IsNaN(s.Sdev()) || !math.IsNaN(s.Last()) {
		t.Fatalf("empty: N %d mean %v min %v max %v sdev %v last %v", s.N(), s.Mean(), s.Min(), s.Max(), s.Sdev(), s.Last())
	}
	s.Add(math.NaN())
	s.Add(math.Inf(1))
	s.Add(math.Inf(-1))
	if s.N() != 0 {
		t.Fatalf("non-finite values counted: N %d", s.N())
	}
	s.Add(2.5)
	if s.N() != 1 || s.Mean() != 2.5 || s.Min() != 2.5 || s.Max() != 2.5 || s.Last() != 2.5 || !math.IsNaN(s.Sdev()) {
		t.Fatalf("one value: N %d mean %v min %v max %v last %v sdev %v", s.N(), s.Mean(), s.Min(), s.Max(), s.Last(), s.Sdev())
	}
	s.Reset()
	// a frequency read around 1 MHz: Welford keeps the spread exact where a
	// sum of squares (1e12-scale terms) would lose it
	r := rng{s: 99}
	var vals []float64
	for i := 0; i < 5000; i++ {
		v := 1e6 + 0.37*r.gauss()
		vals = append(vals, v)
		s.Add(v)
		if i%500 == 0 {
			s.Add(math.NaN()) // a capture without a reading
		}
	}
	mean := 0.0
	for _, v := range vals {
		mean += v
	}
	mean /= float64(len(vals))
	ss := 0.0
	mn, mx := math.Inf(1), math.Inf(-1)
	for _, v := range vals {
		ss += (v - mean) * (v - mean)
		mn, mx = math.Min(mn, v), math.Max(mx, v)
	}
	sd := math.Sqrt(ss / float64(len(vals)))
	if s.N() != len(vals) || math.Abs(s.Mean()-mean) > 1e-12*mean || s.Min() != mn || s.Max() != mx ||
		math.Abs(s.Sdev()-sd) > 1e-9 || s.Last() != vals[len(vals)-1] {
		t.Errorf("N %d mean %.12g (%.12g) min %v max %v sdev %.12g (%.12g)", s.N(), s.Mean(), mean, s.Min(), s.Max(), s.Sdev(), sd)
	}
	if math.Abs(s.Sdev()-0.37) > 0.02 {
		t.Errorf("sdev %.4f, want about 0.37", s.Sdev())
	}
	s.Reset()
	if s.N() != 0 || !math.IsNaN(s.Mean()) {
		t.Errorf("after Reset: N %d mean %v", s.N(), s.Mean())
	}
	// identical values: no negative variance from rounding
	for i := 0; i < 1000; i++ {
		s.Add(0.1)
	}
	if s.Sdev() != 0 || s.Mean() != 0.1 {
		t.Errorf("constant: mean %v sdev %v", s.Mean(), s.Sdev())
	}
}
