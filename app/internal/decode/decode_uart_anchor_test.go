// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"strings"
	"testing"
)

// uartBackToBack is 8N1 UART with no idle between the characters (the bench
// line), messages repeated after idle bits of gap, at spb samples a bit.
// TRLC-LINKS: REQ-SDS-018
func uartBackToBack(msg []int, repeats, gap, spb int) []uint8 {
	var w []uint8
	push := func(bit, k int) {
		v := uint8(40)
		if bit == 1 {
			v = 210
		}
		for j := 0; j < k; j++ {
			w = append(w, v)
		}
	}
	for r := 0; r < repeats; r++ {
		push(1, gap*spb)
		for _, b := range msg {
			push(0, spb)
			for c := 0; c < 8; c++ {
				push((b>>c)&1, spb)
			}
			push(1, spb)
		}
	}
	push(1, gap*spb)
	return w
}

// A record that opens inside back-to-back characters has no idle to frame
// them by; the owner's serial trigger on AA at 20 us/div read "A5 ER ER ER"
// on a line carrying 20 55 AA 0F. The hardware's match fixes the framing:
// with its point (inside the matched character's stop bit) as the anchor,
// every character back to back with it reads as sent.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestUARTAnchorFramesBackToBackTraffic(t *testing.T) {
	msg := []int{0x48, 0x69, 0x20, 0x55, 0xAA, 0x0F, 0xF0, 0x0A}
	const spb = 20
	w := uartBackToBack(msg, 2, 30, spb)
	aaStart := 30*spb + 4*10*spb // the first message's AA
	for _, cut := range []int{2*10*spb + 3*spb, 2*10*spb + 7*spb, 3*10*spb + 1*spb, 3*10*spb + 5*spb} {
		rec := w[30*spb+cut : aaStart+5*10*spb]         // opens inside 20 or 55, ends inside 0A..
		for _, into := range []float64{9.2, 9.5, 9.9} { // the match's point within the stop bit
			anchor := float64(aaStart-(30*spb+cut)) + into*spb
			r := DecodeUART(rec, 1/(115200.0*spb), UARTCfg{Baud: 115200, Anchor: anchor, HasAnchor: true})
			got := strings.Fields(r.Text)
			want := []string{"55", "AA", "0F", "F0"} // 20 is cut by the record's start
			if cut >= 3*10*spb {
				want = want[1:] // and 55
			}
			joined := strings.Join(got, " ")
			if !strings.Contains(joined, strings.Join(want, " ")) || strings.Contains(joined, "!") {
				t.Fatalf("cut %d bits, anchor %.1f bits into the frame: read %q, want %v in sync", cut/spb, into, joined, want)
			}
		}
	}
	// without the anchor such a record misreads (the case the anchor is for)
	misread := 0
	for cut := 2 * 10 * spb; cut < 4*10*spb; cut += spb {
		r := DecodeUART(w[30*spb+cut:aaStart+5*10*spb], 1/(115200.0*spb), UARTCfg{Baud: 115200})
		if !strings.Contains(r.Text, "AA 0F F0") || strings.Contains(r.Text, "!") {
			misread++
		}
	}
	if misread == 0 {
		t.Fatalf("without the anchor every cut read in sync: the test no longer shows the case")
	}
	// an anchor where no clean frame is leaves the decode as it was
	plain := DecodeUART(w, 1/(115200.0*spb), UARTCfg{Baud: 115200})
	off := DecodeUART(w, 1/(115200.0*spb), UARTCfg{Baud: 115200, Anchor: 5, HasAnchor: true})
	if plain.Text != off.Text {
		t.Fatalf("an anchor on idle changed the decode: %q vs %q", off.Text, plain.Text)
	}
}
