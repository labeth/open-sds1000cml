// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "testing"

// symbolIndexRef is the bit-by-bit index the RTL's priority encoder gives.
// TRLC-LINKS: REQ-SDS-013
func symbolIndexRef(level bool, count uint32) int {
	if count > SymbolMaxCount {
		count = SymbolMaxCount
	}
	e, sh := 0, 0
	if count >= 64 {
		top := 0
		for b := 6; b < 21; b++ {
			if count>>b&1 != 0 {
				top = b
			}
		}
		e, sh = top-5, top-6
	}
	idx := e<<6 | int(count>>sh&63)
	if level {
		idx |= 1024
	}
	return idx
}

// SymbolIndex agrees with the bit-by-bit reference on every count.
// TRLC-LINKS: REQ-SDS-013
func TestSymbolIndexMatchesRef(t *testing.T) {
	for c := uint32(0); c < 1<<22; c++ {
		for _, l := range []bool{false, true} {
			if got, want := SymbolIndex(l, c), symbolIndexRef(l, c); got != want {
				t.Fatalf("SymbolIndex(%v, %d) = %d, want %d", l, c, got, want)
			}
		}
	}
	for _, c := range []uint32{1<<32 - 1, 1 << 31, 1<<22 + 5} {
		if got, want := SymbolIndex(true, c), symbolIndexRef(true, c); got != want {
			t.Fatalf("SymbolIndex(true, %d) = %d, want %d", c, got, want)
		}
	}
}
