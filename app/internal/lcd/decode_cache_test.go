// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"testing"

	"open-sds/app/internal/engine"
)

// Auto paces its full detection, but a newer frame inside the pacing window
// is decoded afresh, or the cached spans sit where the earlier frame's bytes
// had been (labels off the trace on a free-phase UART).
// TRLC-LINKS: REQ-SDS-018
func TestAutoDecodeFollowsNewFrame(t *testing.T) {
	msg := []int{0x48, 0x69, 0x20, 0x55, 0xAA, 0x0F, 0xF0, 0x0A}
	const n, spb = 4000, 20
	w1 := goldenUARTWave(msg, spb, n)
	shift := 7*spb + 3
	w2 := append(make([]uint8, 0, n), w1[shift:]...)
	for len(w2) < n {
		w2 = append(w2, 210)
	}
	hud := HUD{DecProto: 1}
	decode := func(w []uint8, seq uint64) int {
		f := &engine.Frame{C1: w, C2: make([]uint8, n), Valid: n, SampleS: 1e-6, Seq: seq}
		for i := range f.C2 {
			f.C2[i] = 128
		}
		ch := func(c int) []uint8 {
			if c == 1 {
				return f.C2
			}
			return f.C1
		}
		res := decodeFrame(f, hud, n, ch, "hex")
		for _, s := range res.Spans {
			if s.Kind == "data" {
				return s.I0
			}
		}
		t.Fatalf("frame %d: no data span (%s %q)", seq, res.Proto, res.Error)
		return 0
	}
	a := decode(w1, 101)
	b := decode(w2, 102)
	if d := a - b; d < shift-spb/2 || d > shift+spb/2 {
		t.Fatalf("first byte at %d then %d: moved %d, want the frame's shift %d", a, b, d, shift)
	}
}
