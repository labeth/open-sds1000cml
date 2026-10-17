// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"reflect"
	"testing"

	"open-sds/app/internal/decode"
)

// The protocols that open each frame with a start marker take their packets
// from it: LIN's frame ID is the address, a bad checksum marks its byte and
// the packet; SWD's data word takes four bytes; a frame without the next
// start after it and little record left is cut.
// TRLC-LINKS: REQ-SDS-214
func TestPacketsOpenedFrames(t *testing.T) {
	lin := decode.Result{OK: true, Proto: "lin", SPB: 50, Spans: []decode.Span{
		{I0: 100, I1: 800, Text: "BRK", Kind: "sof"}, {I0: 850, I1: 1350, Text: "SYNC", Kind: "rw", Val: 0x55},
		{I0: 1400, I1: 1900, Text: "IDB2", Kind: "data", Val: 0xB2}, {I0: 2000, I1: 2500, Text: "A5", Kind: "data", Val: 0xA5},
		{I0: 2500, I1: 3000, Text: "!CK00", Kind: "frame-error", Val: 0},
		{I0: 5000, I1: 5700, Text: "BRK", Kind: "sof"}, {I0: 5750, I1: 6250, Text: "SYNC", Kind: "rw", Val: 0x55},
		{I0: 6300, I1: 6800, Text: "ID20", Kind: "data", Val: 0x20},
	}}
	ps := Packets(lin, 7000)
	if len(ps) != 2 || !ps[0].Complete || ps[1].Complete || ps[0].Addr != 0x32 || !ps[0].HasAddr ||
		!reflect.DeepEqual(ps[0].Bytes, []byte{0xB2, 0xA5, 0x00}) || !reflect.DeepEqual(ps[0].ByteErr, []bool{false, false, true}) ||
		ps[0].Err != FaultCRC {
		t.Fatalf("LIN packets %+v", ps)
	}
	swd := decode.Result{OK: true, Proto: "swd", SPB: 20, Spans: []decode.Span{
		{I0: 10, I1: 10, Kind: "sof"}, {I0: 10, I1: 170, Text: "RDP0", Kind: "data", Val: 0xA5},
		{I0: 190, I1: 250, Text: "OK", Kind: "data", Val: 1}, {I0: 250, I1: 900, Text: "2BA01477", Kind: "data", Val: 0x2BA01477},
	}}
	ps = Packets(swd, 100000)
	if len(ps) != 1 || !reflect.DeepEqual(ps[0].Bytes, []byte{0xA5, 0x01, 0x2B, 0xA0, 0x14, 0x77}) {
		t.Fatalf("SWD packets %+v", ps)
	}
}
