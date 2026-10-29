// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package decodedlines

import (
	"encoding/binary"
	"testing"

	"open-sds/app/internal/sramcapture"
)

// Any event stream the fabric might send, for any protocol, builds lines of
// at most 16 units that format and travel (EncodeLines, DecodeLines) whole.
// TRLC-LINKS: REQ-SDS-018
func FuzzTranscript(f *testing.F) {
	f.Add(uint8(6), []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11})
	f.Add(uint8(2), []byte{1, 0, 0, 0, 0x55, 1, 0, 0, 0, 0xaa, 2, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, proto uint8, b []byte) {
		tr := New(int(proto%64), 64)
		var ev []sramcapture.DecodedEvent
		sample := uint64(HistorySamples)
		for len(b) >= 6 {
			sample += uint64(binary.LittleEndian.Uint16(b[1:]))
			ev = append(ev, sramcapture.DecodedEvent{Kind: b[0] % 8, Sample: sample, Value: binary.LittleEndian.Uint32(append(b[2:5:5], b[5]))})
			b = b[6:]
		}
		tr.Add(ev)
		tr.Flush()
		for i := 0; i < tr.Count(); i++ {
			if l := tr.At(i); int(l.N) > len(l.Vals) {
				t.Fatalf("line %d holds %d units", i, l.N)
			}
			_ = tr.Format(i)
		}
		buf := make([]byte, 1<<16)
		n := tr.EncodeLines(0, buf)
		bt, err := DecodeLines(buf[:n])
		if err != nil {
			t.Fatalf("its own batch: %v", err)
		}
		if len(bt.Lines) > tr.Count() {
			t.Fatalf("%d lines back of %d", len(bt.Lines), tr.Count())
		}
		_, _ = DecodeLines(b) // and any bytes as a batch: no panic
	})
}
