// ENGMODEL-OWNER-UNIT: FU-APP-MEASURE
package measure

import (
	"math/rand"
	"testing"
)

// benchSquare is a noisy 0-3.3 V square of 40-sample cycles, raw and Q8.8.
// TRLC-LINKS: REQ-SDS-017
func benchSquare(n int) ([]uint8, []uint16) {
	r := rand.New(rand.NewSource(3))
	raw := make([]uint8, n)
	q := make([]uint16, n)
	for i := range raw {
		v := 70.0
		if i/20%2 == 1 {
			v = 190
		}
		v += r.Float64()*4 - 2
		raw[i] = uint8(v + .5)
		q[i] = uint16(v * 256)
	}
	return raw, q
}

// TRLC-LINKS: REQ-SDS-017
func BenchmarkCompute(b *testing.B) {
	raw, _ := benchSquare(5000)
	for b.Loop() {
		Compute(raw, 0.02, 0, 2e-9)
	}
}

// TRLC-LINKS: REQ-SDS-017
func BenchmarkComputeQ8(b *testing.B) {
	_, q := benchSquare(5000)
	for b.Loop() {
		ComputeQ8(q, 0.02, 0, 2e-9)
	}
}

// benchEnvelope is a live peak-detect frame: 1400 (min,max) buckets of a
// noisy square with 56-bucket cycles and an edge bucket at each transition.
// TRLC-LINKS: REQ-SDS-021
func benchEnvelope() []uint8 {
	r := rand.New(rand.NewSource(4))
	env := make([]uint8, 2*1400)
	for i := 0; i < 1400; i++ {
		lv := 70.0
		if i/28%2 == 1 {
			lv = 190
		}
		lo, hi := lv-2+r.Float64()*2, lv+r.Float64()*2
		if i%28 == 0 {
			lo, hi = 68, 192
		}
		env[2*i], env[2*i+1] = uint8(lo), uint8(hi)
	}
	return env
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkEnvelopeTiming(b *testing.B) {
	env := benchEnvelope()
	r := Compute(env, 0.02, 0, 2e-7)
	for b.Loop() {
		r.WithEnvelopeTiming(env, 0.02, 0, 2e-7)
	}
}

// Every channel frame is checked for clipping and its envelope's swing read.
// TRLC-LINKS: REQ-SDS-021
func BenchmarkClippedSwing(b *testing.B) {
	raw, _ := benchSquare(5000)
	env := benchEnvelope()
	for b.Loop() {
		Clipped(raw)
		scanEnvelope(env)
	}
}
