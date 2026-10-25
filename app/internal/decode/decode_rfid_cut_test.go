// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"strings"
	"testing"
)

// A record that opens amid a reader command marks that command cut ("?"),
// not as a coding error, and decodes the whole one after it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
func TestT55xxCommandCutByTheRecord(t *testing.T) {
	const sr = 1000000
	w := newTimeline(sr)
	f := rfidField{w, true}
	f.on(500 * rfidFC)
	t55xxCommand(f, t55xxBits(2, -1, 1, 0xDEADBEEF, 5))
	t55xxCommand(f, t55xxBits(2, -1, 0, 0x00148040, 1))
	codes := w.codes()[int((500+30+20*49)*rfidFC*sr):] // open twenty bits into the first
	r := DecodeT55xx(codes, 1.0/sr, LineCfg{})
	if !r.OK || r.FrameBits != 38 {
		t.Fatalf("%q (%s)", r.Text, r.Error)
	}
	for _, sp := range r.Spans {
		if sp.Kind == "frame-error" {
			t.Fatalf("a coding error in a record that opens amid a command: %q", r.Text)
		}
	}
	if !strings.Contains(r.Text, "?") || !strings.Contains(r.Text, "BL1") {
		t.Fatalf("%q: want the cut command marked and the whole one read", r.Text)
	}
}
