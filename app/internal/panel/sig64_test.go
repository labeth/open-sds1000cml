// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"hash/fnv"
	"testing"
)

// sig64 hashes as hash/fnv's FNV-1a: strings, bytes and words alike.
// TRLC-LINKS: REQ-SDS-211
func TestSig64IsFNV1a(t *testing.T) {
	ref := fnv.New64a()
	h := newSig()
	for i, s := range []string{"", "trig.level", "bus.layer", "\x00\xff", "Ünïcode"} {
		ref.Write([]byte(s))
		h.str(s)
		v := uint64(i)*0x9E3779B97F4A7C15 + 7
		var b [8]byte
		for k := range b {
			b[k] = byte(v >> (8 * k))
		}
		ref.Write(b[:])
		h.u64(v)
		ref.Write([]byte{1, 2, 250})
		h.bytes([]byte{1, 2, 250})
		if uint64(h) != ref.Sum64() {
			t.Fatalf("after %q: sig64 %#x, fnv %#x", s, uint64(h), ref.Sum64())
		}
	}
}
