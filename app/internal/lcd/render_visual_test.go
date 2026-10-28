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

// TestVisualPicturesNeverBreak: random spans of every kind, any size of
// picture, a record with no samples: each picture draws without a panic and
// inside its rectangle.
// TRLC-LINKS: REQ-SDS-281
func TestVisualPicturesNeverBreak(t *testing.T) {
	protos := []string{"midi", "dmx512", "ws281x", "ps2", "irnec", "caliper", "wiegand", "pwm", "dcf77", "quad", "stepper", "lin", "canfd", "uart", "spi"}
	kinds := []string{"data", "data", "data", "sof", "rw", "id", "frame-error"}
	texts := []string{"", "A04", "C16", "F42", "C4660", "12.34mm", "26b", "50.0%", "MIN12", "HR3", "DAY9", "MON1", "YR26", "F12", "R-3", "ID02", "CK12", "!CK12", "RST", "H12"}
	seed := uint32(5)
	rnd := func(n int) int { seed = seed*1664525 + 1013904223; return int(seed>>8) % n }
	for it := 0; it < 3000; it++ {
		var spans []decode.Span
		for k, n := 0, rnd(400); k < n; k++ {
			spans = append(spans, decode.Span{Kind: kinds[rnd(len(kinds))], Text: texts[rnd(len(texts))], I0: rnd(5000), I1: rnd(5000), Val: rnd(1 << 20)})
		}
		sf := NewMemSurface()
		a := visArea{x0: 0, y0: 14 + rnd(200), w: 1 + rnd(770), h: 1 + rnd(450), n: rnd(3) * rnd(10000), dt: float64(rnd(3)) * 1e-6}
		a.h = min(a.h, H-a.y0)
		visDrawBus(sf, a, decode.Result{OK: true, Proto: protos[rnd(len(protos))], Spans: spans})
	}
}
