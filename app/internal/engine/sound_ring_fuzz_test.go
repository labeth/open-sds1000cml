// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"encoding/binary"
	"testing"
	"time"
)

// FuzzSoundRing drives a small ring with the fuzzer's pushes (sizes, gaps,
// scale changes, marks) and reads (any ordinal, any count, as a web
// request may ask): a read holds what it says - samples First..First+N-1
// of what the ring still holds, the scale in force at First first, and only
// the gaps and marks inside it.
// TRLC-LINKS: REQ-SDS-265, REQ-SDS-272
func FuzzSoundRing(f *testing.F) {
	f.Add([]byte{10, 0, 0, 200, 1, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255, 255, 255, 255, 255, 255, 255, 255})
	f.Add([]byte{255, 3, 1, 5, 7, 9, 2, 80, 80, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15})
	f.Fuzz(func(t *testing.T, ops []byte) {
		r := newSoundRing(10) // 1024 samples a channel
		now := time.Unix(0, 0)
		vdiv := 1.0
		for len(ops) >= 3 {
			op, a, b := ops[0], ops[1], ops[2]
			ops = ops[3:]
			switch op % 4 {
			case 0, 1: // push: a words, a gap now and then, a scale change now and then
				raw := make([]byte, 4*(int(a)*int(1+b%8)))
				for i := range raw {
					raw[i] = byte(i) ^ b
				}
				if b&0x40 != 0 {
					vdiv *= 2
				}
				r.pushWords(raw, SoundScale{VdivV: [2]float64{vdiv, 1}}, b&0x80 != 0, now)
			case 2: // marks around the newest samples, some before the oldest
				r.mu.Lock()
				next := r.next
				r.mu.Unlock()
				at := next - min(next, uint64(a)*8)
				r.addMarks([]SoundMark{{At: at, Ch: int(b & 1), Rising: b&2 != 0}, {At: at + uint64(b), Ch: 0}})
			case 3: // a read from any ordinal
				var from uint64
				if len(ops) >= 8 {
					from = binary.LittleEndian.Uint64(ops)
					ops = ops[8:]
				}
				if a&1 == 0 {
					from %= 4096
				}
				max := int(int8(b)) * 37
				blk := r.read(from, max, nil, nil, 61035)
				r.mu.Lock()
				next, old := r.next, r.oldestLocked()
				r.mu.Unlock()
				if blk.N < 0 || len(blk.Q1) != blk.N || len(blk.Q2) != blk.N || blk.N > 1024 {
					t.Fatalf("read(%d, %d): N %d, %d and %d samples", from, max, blk.N, len(blk.Q1), len(blk.Q2))
				}
				if blk.N > 0 && (blk.First < old || blk.First+uint64(blk.N) > next) {
					t.Fatalf("read(%d, %d): %d..%d outside the ring's %d..%d", from, max, blk.First, blk.First+uint64(blk.N), old, next)
				}
				if blk.N > 0 && (len(blk.Scales) == 0 || blk.Scales[0].From > blk.First) {
					t.Fatalf("read(%d, %d) from %d: scales %+v", from, max, blk.First, blk.Scales)
				}
				for _, g := range blk.Gaps {
					if g <= blk.First || g >= blk.First+uint64(blk.N) {
						t.Fatalf("gap %d outside %d+%d", g, blk.First, blk.N)
					}
				}
				for _, m := range blk.Marks {
					if m.At < blk.First || m.At >= blk.First+uint64(blk.N) {
						t.Fatalf("mark %d outside %d+%d", m.At, blk.First, blk.N)
					}
				}
			}
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if len(r.marks) > 4096 || len(r.scales) > 1100 || len(r.gaps) > 1100 {
			t.Fatalf("held %d marks, %d scales, %d gaps", len(r.marks), len(r.scales), len(r.gaps))
		}
	})
}
