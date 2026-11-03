// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// glyphs5x7 are spec 07 §10.0's glyphs for the letters, digits and marks the
// DONE screen uses: five column bytes, bit 0 the top row.
var glyphs5x7 = map[rune][5]byte{
	' ': {0, 0, 0, 0, 0}, '-': {0x08, 0x08, 0x08, 0x08, 0x08}, '.': {0x00, 0x60, 0x60, 0x00, 0x00},
	':': {0x00, 0x36, 0x36, 0x00, 0x00}, '/': {0x20, 0x10, 0x08, 0x04, 0x02}, '(': {0x00, 0x1C, 0x22, 0x41, 0x00},
	')': {0x00, 0x41, 0x22, 0x1C, 0x00}, ',': {0x00, 0x50, 0x30, 0x00, 0x00}, '_': {0x40, 0x40, 0x40, 0x40, 0x40},
	'0': {0x3E, 0x51, 0x49, 0x45, 0x3E}, '1': {0x00, 0x42, 0x7F, 0x40, 0x00}, '2': {0x42, 0x61, 0x51, 0x49, 0x46},
	'3': {0x21, 0x41, 0x45, 0x4B, 0x31}, '4': {0x18, 0x14, 0x12, 0x7F, 0x10}, '5': {0x27, 0x45, 0x45, 0x45, 0x39},
	'6': {0x3C, 0x4A, 0x49, 0x49, 0x30}, '7': {0x01, 0x71, 0x09, 0x05, 0x03}, '8': {0x36, 0x49, 0x49, 0x49, 0x36},
	'9': {0x06, 0x49, 0x49, 0x29, 0x1E}, 'A': {0x7E, 0x11, 0x11, 0x11, 0x7E}, 'B': {0x7F, 0x49, 0x49, 0x49, 0x36},
	'C': {0x3E, 0x41, 0x41, 0x41, 0x22}, 'D': {0x7F, 0x41, 0x41, 0x22, 0x1C}, 'E': {0x7F, 0x49, 0x49, 0x49, 0x41},
	'F': {0x7F, 0x09, 0x09, 0x09, 0x01}, 'G': {0x3E, 0x41, 0x49, 0x49, 0x7A}, 'H': {0x7F, 0x08, 0x08, 0x08, 0x7F},
	'I': {0x00, 0x41, 0x7F, 0x41, 0x00}, 'J': {0x20, 0x40, 0x41, 0x3F, 0x01}, 'K': {0x7F, 0x08, 0x14, 0x22, 0x41},
	'L': {0x7F, 0x40, 0x40, 0x40, 0x40}, 'M': {0x7F, 0x02, 0x0C, 0x02, 0x7F}, 'N': {0x7F, 0x04, 0x08, 0x10, 0x7F},
	'O': {0x3E, 0x41, 0x41, 0x41, 0x3E}, 'P': {0x7F, 0x09, 0x09, 0x09, 0x06}, 'Q': {0x3E, 0x41, 0x51, 0x21, 0x5E},
	'R': {0x7F, 0x09, 0x19, 0x29, 0x46}, 'S': {0x46, 0x49, 0x49, 0x49, 0x31}, 'T': {0x01, 0x01, 0x7F, 0x01, 0x01},
	'U': {0x3F, 0x40, 0x40, 0x40, 0x3F}, 'V': {0x1F, 0x20, 0x40, 0x20, 0x1F}, 'W': {0x3F, 0x40, 0x38, 0x40, 0x3F},
	'X': {0x63, 0x14, 0x08, 0x14, 0x63}, 'Y': {0x07, 0x08, 0x70, 0x08, 0x07}, 'Z': {0x61, 0x51, 0x49, 0x45, 0x43},
}

// banner is a picture of the DONE screen: a coloured box with a big word
// and small lines under it, in RGB565.
type banner struct {
	w, h int
	px   []uint16
}

// newBanner draws word (scale 12) and lines (scale 3, upper-cased) on a box
// of colour bg, the text white.
// TRLC-LINKS: REQ-SDS-292
func newBanner(w, h int, bg uint16, word string, lines []string) *banner {
	b := &banner{w: w, h: h, px: make([]uint16, w*h)}
	for i := range b.px {
		b.px[i] = bg
	}
	b.text((w-len(word)*6*12)/2, h/2-130, word, 12)
	y := h/2 + 0
	for _, l := range lines {
		l = strings.ToUpper(l)
		if n := (w - 20) / 18; len(l) > n {
			l = l[:n]
		}
		b.text((w-len(l)*6*3)/2, y, l, 3)
		y += 30
	}
	return b
}

// text draws s at x, y, each set glyph bit a scale×scale white block.
// TRLC-LINKS: REQ-SDS-292
func (b *banner) text(x, y int, s string, scale int) {
	for _, r := range s {
		g := glyphs5x7[r]
		for col := range 5 {
			for row := range 7 {
				if g[col]>>row&1 == 0 {
					continue
				}
				for dy := range scale {
					for dx := range scale {
						px, py := x+col*scale+dx, y+row*scale+dy
						if px >= 0 && py >= 0 && px < b.w && py < b.h {
							b.px[py*b.w+px] = 0xffff
						}
					}
				}
			}
		}
		x += 6 * scale
	}
}

// RGB565 colours of the DONE screen.
const (
	doneGreen = 0x0400
	failedRed = 0xa000
)

// showBanner writes b to every page of fb0 (16 bpp) and turns the backlight
// on (gpio7, spec 08 §10.1).
// TRLC-LINKS: REQ-SDS-292
func showBanner(b *banner) error {
	g, err := readFBGeom("/sys/class/graphics/fb0")
	if err != nil {
		return err
	}
	if g.BPP != 16 || g.W != b.w {
		return nil
	}
	_, vh := 0, 0
	if f := strings.Split(strings.TrimSpace(readStr("/sys/class/graphics/fb0/virtual_size")), ","); len(f) == 2 {
		vh, _ = strconv.Atoi(f[1])
	}
	pages := max(1, vh/g.H)
	f, err := os.OpenFile("/dev/fb0", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	row := make([]byte, g.Stride)
	for p := range pages {
		for y := range min(b.h, g.H) {
			for x := range b.w {
				v := b.px[y*b.w+x]
				row[2*x], row[2*x+1] = byte(v&0xff), byte(v>>8)
			}
			if _, err := f.WriteAt(row, int64(p*g.H+y)*int64(g.Stride)); err != nil {
				return err
			}
		}
	}
	_ = writeSys("/sys/class/gpio/gpio7/value", "1")
	return nil
}

// holdBanner shows b, and again every second until hold has passed while
// someone else (the factory app) may draw over it.
// TRLC-LINKS: REQ-SDS-292
func holdBanner(b *banner, hold time.Duration, others bool) {
	_ = showBanner(b)
	if !others {
		return
	}
	for end := time.Now().Add(hold); time.Now().Before(end); {
		time.Sleep(time.Second)
		_ = showBanner(b)
	}
}

// doneScreen syncs the stick and shows DONE (or FAILED) with lines on the
// scope's screen: once when the stick took the scope over, else kept over
// the factory app's screen for c.hold.
// TRLC-LINKS: REQ-SDS-292
func (c *collection) doneScreen(ok bool, lines []string) {
	// the archive and the status file are on the stick before the screen says
	// it can be switched off
	syscall.Sync()
	g, err := readFBGeom("/sys/class/graphics/fb0")
	if err != nil || g.BPP != 16 {
		return
	}
	word, bg := "DONE", uint16(doneGreen)
	if !ok {
		word, bg = "FAILED", failedRed
	}
	holdBanner(newBanner(g.W, g.H, bg, word, lines), c.hold, !c.tookOver)
}
