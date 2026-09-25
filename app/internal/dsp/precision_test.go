// ENGMODEL-OWNER-UNIT: FU-APP-DSP
package dsp

import (
	"math"
	"math/rand"
	"testing"
)

// TRLC-LINKS: REQ-SDS-037
func TestPrecisionMatchesDirectConvolution(t *testing.T) {
	rng := rand.New(rand.NewSource(903))
	s := make([]uint16, 4096)
	for i := range s {
		s[i] = uint16(rng.Uint32())
	}
	original := append([]uint16(nil), s...)
	ConditionQ8(s, make([]uint16, len(s)))
	for i := range s {
		want := original[i]
		if i >= PrecisionGuard && i < len(s)-PrecisionGuard {
			var sum int64
			for j, tap := range precisionTaps {
				sum += int64(tap) * int64(original[i-PrecisionGuard+j])
			}
			v := (sum + (1 << 21)) >> 22
			if v < 0 {
				v = 0
			}
			if v > 65535 {
				v = 65535
			}
			want = uint16(v)
		}
		if s[i] != want {
			t.Fatalf("sample %d got %d want %d", i, s[i], want)
		}
	}
}

// TRLC-LINKS: REQ-SDS-037
func BenchmarkPrecisionFullRecord(b *testing.B) {
	s := make([]uint16, 524288)
	scratch := make([]uint16, len(s))
	for i := range s {
		s[i] = uint16(i * 101)
	}
	b.SetBytes(int64(len(s) * 2))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ConditionQ8(s, scratch)
	}
}

// TRLC-LINKS: REQ-SDS-037
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
// TRLC-LINKS: REQ-SDS-037
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
// TRLC-LINKS: REQ-SDS-037
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
