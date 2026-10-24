// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math/bits"
	"testing"
)

// recip divides as the divide instruction does wherever its bound holds:
// every divisor ERES and Average use, every dividend up to the bound's edge.
// TRLC-LINKS: REQ-SDS-012
func TestRecipExact(t *testing.T) {
	for d := uint32(1); d <= 4096; d++ {
		r := newRecip(d)
		limit := uint64(1) << (17 + bits.Len32(d-1)) // the bound
		check := func(x uint64) {
			if x >= limit || x > 1<<32-1 {
				return
			}
			if got, want := r.div(uint32(x)), uint32(x)/d; got != want {
				t.Fatalf("%d/%d = %d, want %d", x, d, got, want)
			}
		}
		for k := uint64(0); k < 64; k++ {
			check(k)
			check(limit - 1 - k)
			check(uint64(d)*k*1031 + k)
		}
		for x := uint64(d) * 65535; x < uint64(d)*65535+uint64(d); x++ { // the sums' top
			check(x)
		}
	}
	if testing.Short() {
		return
	}
	for _, d := range []uint32{3, 7, 63, 255, 256, 257} { // every dividend under the bound
		r := newRecip(d)
		limit := uint32(1) << (17 + bits.Len32(d-1))
		for x := uint32(0); x < limit; x++ {
			if r.div(x) != x/d {
				t.Fatalf("%d/%d", x, d)
			}
		}
	}
}
