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
