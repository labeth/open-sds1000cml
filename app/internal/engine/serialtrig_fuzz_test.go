package engine

import (
	"math"
	"testing"
)

// TestSerialQualifyFuzz hammers serialQualify with hostile SerialParams + frames
// and asserts it never panics, hangs, or returns an out-of-range anchor. A pure,
// deterministic PRNG loop (no wall-clock seed) so it is reproducible.
func TestSerialQualifyFuzz(t *testing.T) {
	rng := uint64(0x9e3779b97f4a7c15)
	next := func() uint64 { rng ^= rng << 13; rng ^= rng >> 7; rng ^= rng << 17; return rng }
	ni := func(n int) int {
		if n <= 0 {
			return 0
		}
		return int(next() % uint64(n))
	}

	sampleSs := []float64{0, -1, 1e-6, 2e-7, math.NaN(), math.Inf(1), math.Inf(-1), 1e30, -1e-30}
	e := &Engine{}
	for iter := 0; iter < 4000; iter++ {
		// hostile params
		nb := ni(6)
		bytes := make([]int, nb)
		for i := range bytes {
			bytes[i] = ni(600) - 50 // includes <0 and >255
		}
		p := SerialParams{
			Proto: ni(6) - 1, // -1..4 (out of range included)
			ChA:   ni(4) - 1, // -1..2
			ChB:   ni(4) - 1,
			Baud:  ni(500000) - 1000,
			CPOL:  next()&1 == 0, CPHA: next()&1 == 0, MSB: next()&1 == 0,
			Addr:  ni(300) - 20, // includes <0 and >127
			RW:    ni(5) - 1,    // -1..3
			Bytes: bytes,
		}
		e.SetSerialParams(p)

		// hostile frame
		n := ni(4096)
		var c1, c2 []uint8
		if next()&7 != 0 { // sometimes nil channels
			c1 = make([]uint8, n)
			for i := range c1 {
				c1[i] = uint8(next())
			}
		}
		if next()&7 != 0 {
			c2 = make([]uint8, ni(4096))
			for i := range c2 {
				c2[i] = uint8(next())
			}
		}
		f := &Frame{C1: c1, C2: c2, IsEnv: next()&15 == 0}
		valid := ni(5000) - 200 // includes negative + > len
		sampleS := sampleSs[ni(len(sampleSs))]

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC iter=%d params=%+v n=%d validArg=%d sampleS=%v: %v", iter, p, n, valid, sampleS, r)
				}
			}()
			matched, anchor := e.serialQualify(f, valid, sampleS)
			if matched && anchor != -1 {
				lim := valid
				if lim > len(c1) {
					lim = len(c1)
				}
				if anchor < 0 || (lim > 0 && anchor >= lim) {
					// anchor may reference either channel; bound-check against the max plausible.
					maxLen := len(c1)
					if len(c2) > maxLen {
						maxLen = len(c2)
					}
					if anchor < 0 || anchor >= maxLen {
						t.Fatalf("iter=%d anchor %d out of [0,%d) params=%+v", iter, anchor, maxLen, p)
					}
				}
			}
		}()
	}
}
