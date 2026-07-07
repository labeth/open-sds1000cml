package measure

import (
	"encoding/json"
	"math"
	"math/rand"
	"testing"
)

// Measurement fuzz: degenerate records must produce FINITE, JSON-encodable
// results — a single NaN/Inf field poisons the whole frame reply at the
// encoding layer (json.Marshal errors on NaN), taking the UI down with it.
func TestMeasureFuzzFinite(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	mk := func(n int, f func(i int) uint8) []uint8 {
		s := make([]uint8, n)
		for i := range s {
			s[i] = f(i)
		}
		return s
	}
	cases := [][]uint8{
		{},       // empty
		{128},    // one sample
		{0, 255}, // two rails
		mk(4096, func(i int) uint8 { return 128 }),                  // flat
		mk(4096, func(i int) uint8 { return 0 }),                    // rail low
		mk(4096, func(i int) uint8 { return 255 }),                  // rail high
		mk(4096, func(i int) uint8 { return uint8(i % 2 * 255) }),   // nyquist toggle
		mk(4096, func(i int) uint8 { return uint8(rng.Intn(256)) }), // noise
		mk(3, func(i int) uint8 { return uint8(i * 100) }),          // tiny ramp
		mk(4096, func(i int) uint8 { // single glitch on flat
			if i == 2000 {
				return 255
			}
			return 10
		}),
	}
	for i := 0; i < 300; i++ { // random walks, bursts, spikes
		n := 1 + rng.Intn(5000)
		v := 128.0
		cases = append(cases, mk(n, func(int) uint8 {
			v += rng.NormFloat64() * float64(rng.Intn(40))
			if v < 0 {
				v = 0
			}
			if v > 255 {
				v = 255
			}
			return uint8(v)
		}))
	}
	for ci, sig := range cases {
		for _, sampleS := range []float64{0, 800e-9, 1e-12} {
			r := Compute(sig, 0.03125, 0, sampleS)
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatalf("case %d sampleS=%g: unencodable result: %v", ci, sampleS, err)
			}
			var back map[string]any
			if json.Unmarshal(b, &back) != nil {
				t.Fatalf("case %d: roundtrip failed", ci)
			}
			for k, v := range back {
				if f, ok := v.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0)) {
					t.Fatalf("case %d sampleS=%g: %s is %v", ci, sampleS, k, f)
				}
			}
		}
	}
}
