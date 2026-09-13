package dsp

import (
	"math"
	"testing"
)

func TestPrecisionDCAndFraction(t *testing.T) {
	s := make([]uint16, 1000)
	for i := range s {
		s[i] = 25728
	}
	ConditionQ8(s, make([]uint16, len(s)))
	for i, v := range s {
		if v != 25728 {
			t.Fatalf("lost half code at %d: %d", i, v)
		}
	}
}
func TestPrecisionRejectsAlternatingAndKeepsBoundary(t *testing.T) {
	s := make([]uint16, 1000)
	for i := range s {
		s[i] = uint16(100*256 + 256*(i%2))
	}
	ConditionQ8(s, make([]uint16, len(s)))
	for i := PrecisionGuard; i < len(s)-PrecisionGuard; i++ {
		if math.Abs(float64(s[i])-25728) > 1 {
			t.Fatalf("Nyquist not suppressed: %d", s[i])
		}
	}
	if s[0] != 25600 || s[1] != 25856 {
		t.Fatal("invented missing boundary input")
	}
}
func TestPrecisionFilterCenteredPassband(t *testing.T) {
	const n = 2048
	const f = 1.0 / 64
	s := make([]uint16, n)
	for i := range s {
		s[i] = uint16(math.Round((128 + 20*math.Sin(2*math.Pi*f*float64(i))) * 256))
	}
	ConditionQ8(s, make([]uint16, n))
	// The FIR compensates the CIC's slight attenuation: compare against the
	// analytic inverse-CIC amplitude, without fitting phase or amplitude.
	a := 20 / math.Pow(math.Sin(math.Pi*f)/(math.Pi*f), 3)
	for i := PrecisionGuard; i < n-PrecisionGuard; i++ {
		want := 128 + a*math.Sin(2*math.Pi*f*float64(i))
		if math.Abs(float64(s[i])/256-want) > .015 {
			t.Fatalf("passband/phase mismatch at %d", i)
		}
	}
}
