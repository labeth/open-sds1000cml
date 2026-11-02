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

// TRLC-LINKS: REQ-SDS-099
func BenchmarkPrecisionLimits(b *testing.B) {
	for b.Loop() {
		PrecisionLimits(16)
	}
}

// conditionRef is ConditionQ8's first loop: the oracle and the baseline.
// TRLC-LINKS: REQ-SDS-037
func conditionRef(samples, scratch []uint16) {
	copy(scratch[:len(samples)], samples)
	for i := PrecisionGuard; i < len(samples)-PrecisionGuard; i++ {
		window := (*[63]uint16)(scratch[i-31 : i+32])
		v := (precisionWindow(window) + (1 << 21)) >> 22
		samples[i] = uint16(min(max(v, 0), 65535))
	}
}

// TRLC-LINKS: REQ-SDS-037
func BenchmarkPrecisionRecordRef(b *testing.B) {
	s := make([]uint16, 65536)
	scratch := make([]uint16, len(s))
	for b.Loop() {
		conditionRef(s, scratch)
	}
}

// TRLC-LINKS: REQ-SDS-037
func BenchmarkPrecisionRecord(b *testing.B) {
	s := make([]uint16, 65536)
	scratch := make([]uint16, len(s))
	for b.Loop() {
		ConditionQ8(s, scratch)
	}
}

// ConditionQ8 is the reference loop sample for sample.
// TRLC-LINKS: REQ-SDS-037
func TestConditionIsTheReference(t *testing.T) {
	a := make([]uint16, 5000)
	for i := range a {
		a[i] = uint16(i*7919) ^ uint16(i*i)
	}
	b := append([]uint16(nil), a...)
	scratch := make([]uint16, len(a))
	ConditionQ8(a, scratch)
	conditionRef(b, scratch)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("sample %d: %d, want %d", i, a[i], b[i])
		}
	}
}

// Every interior length is the reference's, whatever is left after whole
// groups of samples (the block filters several at a time).
// TRLC-LINKS: REQ-SDS-037
func TestConditionEveryLengthIsTheReference(t *testing.T) {
	rng := rand.New(rand.NewSource(37))
	for n := len(precisionTaps); n < len(precisionTaps)+12; n++ {
		a := make([]uint16, n)
		for i := range a {
			a[i] = uint16(rng.Uint32())
		}
		b := append([]uint16(nil), a...)
		scratch := make([]uint16, n)
		ConditionQ8(a, scratch)
		conditionRef(b, scratch)
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("length %d sample %d: %d, want %d", n, i, a[i], b[i])
			}
		}
	}
}

// RoundQ8 is (v + 128) >> 8 at most 255 for every sample, at every length.
// TRLC-LINKS: REQ-SDS-037
func TestRoundQ8(t *testing.T) {
	src := make([]uint16, 70000)
	for i := range src {
		src[i] = uint16(i)
	}
	src[69999], src[69998] = 65535, 65407
	for _, n := range []int{0, 1, 15, 16, 17, 33, 70000} {
		dst := make([]uint8, n+1)
		dst[n] = 7
		RoundQ8(dst[:n], src[:n])
		for i := 0; i < n; i++ {
			if want := uint8(min((uint32(src[i])+128)>>8, 255)); dst[i] != want {
				t.Fatalf("n %d sample %d (%d): %d, want %d", n, i, src[i], dst[i], want)
			}
		}
		if dst[n] != 7 {
			t.Fatalf("n %d wrote past the end", n)
		}
	}
}

// TRLC-LINKS: REQ-SDS-037
func BenchmarkRoundQ8(b *testing.B) {
	src := make([]uint16, 65536)
	dst := make([]uint8, len(src))
	for i := range src {
		src[i] = uint16(i * 7)
	}
	for b.Loop() {
		RoundQ8(dst, src)
	}
}

// TRLC-LINKS: REQ-SDS-037
func BenchmarkRoundQ8Loop(b *testing.B) {
	src := make([]uint16, 65536)
	dst := make([]uint8, len(src))
	for i := range src {
		src[i] = uint16(i * 7)
	}
	for b.Loop() {
		for i, v := range src {
			dst[i] = uint8(min((uint32(v)+128)>>8, 255))
		}
	}
}

// SplitQ8 parts the pairs and rounds them at every length.
// TRLC-LINKS: REQ-SDS-037
func TestSplitQ8(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	for _, n := range []int{1, 7, 8, 9, 16, 23, 4099} {
		b := make([]byte, 4*n)
		for i := range b {
			b[i] = byte(rng.Uint32())
		}
		q1, q2 := make([]uint16, n+1), make([]uint16, n+1)
		c1, c2 := make([]uint8, n+1), make([]uint8, n+1)
		q1[n], q2[n], c1[n], c2[n] = 9, 9, 9, 9
		SplitQ8(q1[:n], q2[:n], c1[:n], c2[:n], b)
		for k := 0; k < n; k++ {
			x, y := uint16(b[4*k])|uint16(b[4*k+1])<<8, uint16(b[4*k+2])|uint16(b[4*k+3])<<8
			cx, cy := uint8(min((uint32(x)+128)>>8, 255)), uint8(min((uint32(y)+128)>>8, 255))
			if q1[k] != x || q2[k] != y || c1[k] != cx || c2[k] != cy {
				t.Fatalf("n %d pair %d: %d %d %d %d, want %d %d %d %d", n, k, q1[k], q2[k], c1[k], c2[k], x, y, cx, cy)
			}
		}
		if q1[n] != 9 || q2[n] != 9 || c1[n] != 9 || c2[n] != 9 {
			t.Fatalf("n %d wrote past the end", n)
		}
	}
}

// MinMax8 is the extremes at every length and alignment.
// TRLC-LINKS: REQ-SDS-017
func TestMinMax8(t *testing.T) {
	rng := rand.New(rand.NewSource(88))
	buf := make([]uint8, 3000)
	for k := 0; k < 2000; k++ {
		for i := range buf {
			buf[i] = uint8(100 + rng.Intn(20))
		}
		off, n := rng.Intn(64), rng.Intn(2900)
		s := buf[off : off+n]
		if n > 0 && k%3 == 0 {
			s[rng.Intn(n)] = uint8(rng.Intn(256))
			s[rng.Intn(n)] = uint8(rng.Intn(256))
		}
		var lo, hi uint8
		if n > 0 {
			lo, hi = s[0], s[0]
			for _, v := range s {
				lo, hi = min(lo, v), max(hi, v)
			}
		}
		if glo, ghi := MinMax8(s); glo != lo || ghi != hi {
			t.Fatalf("case %d (off %d n %d): %d %d, want %d %d", k, off, n, glo, ghi, lo, hi)
		}
	}
}

// TRLC-LINKS: REQ-SDS-017
func BenchmarkMinMax8(b *testing.B) {
	s := make([]uint8, 65536)
	for i := range s {
		s[i] = uint8(i * 37)
	}
	for b.Loop() {
		MinMax8(s)
	}
}

// TRLC-LINKS: REQ-SDS-017
func BenchmarkMinMax8Loop(b *testing.B) {
	s := make([]uint8, 65536)
	for i := range s {
		s[i] = uint8(i * 37)
	}
	for b.Loop() {
		lo, hi := s[0], s[0]
		for _, v := range s {
			if v < lo {
				lo = v
			} else if v > hi {
				hi = v
			}
		}
		sinkLo, sinkHi = lo, hi
	}
}

var sinkLo, sinkHi uint8
