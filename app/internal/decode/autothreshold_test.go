// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math/rand"
	"testing"
)

// AutoThreshold is the level the decoders slice at without one set, so the
// hardware matcher given it slices where the decode does; a line without two
// clear levels has none.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-013
func TestAutoThresholdIsTheDecoders(t *testing.T) {
	w := uartWave([]int{0x48, 0x69, 0x20, 0x55, 0xaa}, 20)
	for i := range w { // a little noise on both rails
		w[i] = uint8(int(w[i]) + (i*7)%5 - 2)
	}
	thr, ok := AutoThreshold(w)
	s := sliceChannel(w, 0, false)
	if !ok || !s.ok || thr != s.threshold {
		t.Fatalf("AutoThreshold %v %v, the decode slices at %v (ok %v)", thr, ok, s.threshold, s.ok)
	}
	flat := make([]uint8, 4096)
	for i := range flat {
		flat[i] = uint8(128 + i%7)
	}
	if _, ok := AutoThreshold(flat); ok {
		t.Fatal("a 6-code noise band has no threshold")
	}
}

// AutoThresholdExtent is AutoThreshold and Extent of the same codes.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-018
func TestAutoThresholdExtentIsBoth(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	for k := 0; k < 300; k++ {
		n := 4 + r.Intn(5000)
		codes := make([]uint8, n)
		lo, hi := r.Intn(256), r.Intn(256)
		for i := range codes {
			v := lo
			if r.Intn(1+k%40) == 0 {
				v = hi
			}
			codes[i] = uint8(min(255, max(0, v+r.Intn(5)-2)))
		}
		thr, ok := AutoThreshold(codes)
		elo, ehi, eok := Extent(codes)
		thr2, ok2, elo2, ehi2, eok2 := AutoThresholdExtent(codes)
		if thr != thr2 || ok != ok2 || elo != elo2 || ehi != ehi2 || eok != eok2 {
			t.Fatalf("case %d: %v %v %v %v %v, want %v %v %v %v %v", k, thr2, ok2, elo2, ehi2, eok2, thr, ok, elo, ehi, eok)
		}
	}
}

