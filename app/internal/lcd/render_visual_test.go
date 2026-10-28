// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"strings"
	"testing"

	"open-sds/app/internal/decode"
)

// TestVisualPictures: each bus's picture draws from its spans (a MIDI
// capture's pushes and releases, DMX's levels, LEDs, PS/2's keys, the
// readings, the frames), says what it shows, and paints the picture.
// TRLC-LINKS: REQ-SDS-281
func TestVisualPictures(t *testing.T) {
	sp := func(kind, text string, i0, val int) decode.Span {
		return decode.Span{Kind: kind, Text: text, I0: i0, I1: i0 + 10, Val: val}
	}
	data := func(i0 int, vals ...int) []decode.Span {
		out := []decode.Span{}
		for k, v := range vals {
			out = append(out, sp("data", "", i0+20*k, v))
		}
		return out
	}
	cases := []struct {
		proto string
		spans []decode.Span
		want  string // in the line beside the title
	}{
		{"midi", data(100, 0x90, 60, 100, 0x90, 64, 90, 0x80, 60, 0), "2 pushed (filled), 1 released"},
		{"dmx512", append([]decode.Span{sp("sof", "BRK", 0, 0)}, data(10, 0, 255, 128, 7)...), "3 slots"},
		{"ws281x", append(append([]decode.Span{sp("sof", "", 0, 0)}, data(10, 255, 0, 0, 0, 255, 0)...), sp("rw", "RST", 500, 0)), "2 LEDs"},
		{"ps2", data(0, 0x1c, 0xf0, 0x1c, 0xe0, 0x74), "3 key events"},
		{"irnec", []decode.Span{sp("sof", "LDR", 0, 0), sp("data", "A04", 10, 4), sp("data", "C16", 30, 0x16)}, "newest"},
		{"caliper", []decode.Span{sp("rw", "12.34mm", 0, 1234)}, "newest"},
		{"lin", []decode.Span{sp("sof", "BRK", 0, 0), sp("data", "ID02", 10, 0xc2), sp("data", "12", 20, 0x12), sp("data", "CKCB", 30, 0xcb)}, "1 identifier"},
		{"canfd", []decode.Span{sp("sof", "SOF", 0, 0), sp("id", "123", 5, 0x123), sp("data", "55", 10, 0x55)}, "1 identifier"},
		{"uart", data(0, 'H', 'i', '\n'), "text"},
	}
	for _, k := range cases {
		sf := NewMemSurface()
		a := visArea{x0: 0, y0: 30, w: 770, h: 380, n: 1000, dt: 1e-5}
		title, sub := visDrawBus(sf, a, decode.Result{OK: true, Proto: k.proto, Spans: k.spans})
		if title == "" || !strings.Contains(sub, k.want) {
			t.Fatalf("%s: %q / %q, want %q", k.proto, title, sub, k.want)
		}
		painted := 0
		for y := a.y0; y < a.y0+a.h; y++ {
			for x := 0; x < a.w; x++ {
				if sf.At(x, y) != 0 {
					painted++
				}
			}
		}
		if painted < 20 {
			t.Fatalf("%s: the picture painted %d pixels", k.proto, painted)
		}
	}
}
