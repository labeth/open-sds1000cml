// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "testing"

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
