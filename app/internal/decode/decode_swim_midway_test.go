// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "testing"

// A record that opens amid a transaction (a trigger on a byte of it) reads
// its bytes as bytes, not as commands whose parity fails, and the unit an
// end of the record cuts as cut ("?"), not as a framing error.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func TestDecodeSWIMMidway(t *testing.T) {
	sr, T := 25e6, 1/363636.0
	w := newTimeline(sr)
	w.add(1, 3*T)
	for _, v := range []int{0x00, 0x50, 0x0A} {
		swimFrame(w, 0, 8, v, T, 1, false, 3*T)
	}
	swimFrame(w, 1, 8, 0x5A, T, 1, false, 3*T)
	swimFrame(w, 0, 3, 1, T, 1, false, 3*T) // ROTF
	swimFrame(w, 0, 8, 2, T, 1, false, 3*T)
	codes := w.codes()
	codes = codes[int(4.5*T*sr):]          // open within the first byte
	codes = codes[:len(codes)-int(5*T*sr)] // and end within the count
	r := DecodeSWIM(codes, 1/sr, LineCfg{})
	if want := "? W50 W0A R5A ROTF ?"; !r.OK || r.Text != want {
		t.Fatalf("%q (%s), want %q", r.Text, r.Error, want)
	}
}
