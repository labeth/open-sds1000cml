// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math/rand/v2"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018
func TestUnpackBits(t *testing.T) {
	raw := make([]byte, 4*257)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range raw {
		raw[i] = byte(r.Uint32())
	}
	for ch := range 2 {
		got := unpackBits([]uint8{7}, raw, ch)
		if len(got) != 1+16*257 || got[0] != 7 {
			t.Fatalf("ch %d: len %d", ch, len(got))
		}
		for i := range 257 {
			w := uint32(raw[4*i]) | uint32(raw[4*i+1])<<8 | uint32(raw[4*i+2])<<16 | uint32(raw[4*i+3])<<24
			for k := range 16 {
				want := uint8(0)
				if w>>(16*ch+k)&1 != 0 {
					want = 255
				}
				if g := got[1+16*i+k]; g != want {
					t.Fatalf("ch %d word %d period %d: %d want %d", ch, i, k, g, want)
				}
			}
		}
	}
}

// One second of a 2^9 digital stream (976,563 words) per op, one line, into
// a reused buffer.
// TRLC-LINKS: REQ-SDS-018
func BenchmarkUnpackBits(b *testing.B) {
	const words = 976563
	raw := make([]byte, 4*words)
	for i := range raw {
		raw[i] = byte(i * 37)
	}
	dst := make([]uint8, 0, 16*words)
	b.SetBytes(4 * words)
	for b.Loop() {
		dst = unpackBits(dst[:0], raw, 0)
	}
}
