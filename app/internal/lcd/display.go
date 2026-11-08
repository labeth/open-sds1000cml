// ENGMODEL-OWNER-UNIT: FU-APP-LCD

package lcd

import (
	"encoding/binary"
	"image"
	"image/color"
	"sync"

	"github.com/labeth/open-dso-core/hal"
)

// Display is the panel as the HAL's display: the profile's 800×480 RGB565
// screen, its framebuffer once the panel is open (Attach), no hardware
// waveform renderer, and the GPIO 7 light, which is only on or off.
type Display struct {
	spec hal.DisplaySpec
	mu   sync.Mutex
	fb   *FB
}

var _ hal.Display = (*Display)(nil)

// NewDisplay is the panel with the profile's display spec, not open yet.
// TRLC-LINKS: REQ-SDS-021
func NewDisplay(spec hal.DisplaySpec) *Display { return &Display{spec: spec} }

// Attach gives the display the framebuffer the panel was opened on.
// TRLC-LINKS: REQ-SDS-021
func (d *Display) Attach(fb *FB) {
	d.mu.Lock()
	d.fb = fb
	d.mu.Unlock()
}

// open is the attached framebuffer; nil before the panel is open.
// TRLC-LINKS: REQ-SDS-021
func (d *Display) open() *FB {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fb
}

// Spec is the profile's display.
// TRLC-LINKS: REQ-SDS-021
func (d *Display) Spec() hal.DisplaySpec { return d.spec }

// Framebuffer is the panel's page-flipped framebuffer; nil before the
// panel is open.
// TRLC-LINKS: REQ-SDS-021
func (d *Display) Framebuffer() hal.Framebuffer {
	if fb := d.open(); fb != nil {
		return framebuffer{fb}
	}
	return nil
}

// Renderer is none: the CPU draws the traces.
// TRLC-LINKS: REQ-SDS-021
func (d *Display) Renderer() (hal.Renderer, bool) { return nil, false }

// Capture reads the page on show into dst, from its top-left corner.
// TRLC-LINKS: REQ-SDS-021
func (d *Display) Capture(dst *image.RGBA) error {
	fb := d.open()
	if fb == nil {
		return hal.Refuse("screen capture", "the panel is not open")
	}
	page := fb.mem[fb.cur*pageBytes : (fb.cur+1)*pageBytes]
	if !fb.panOK {
		page = fb.mem[:pageBytes]
	}
	r := dst.Bounds()
	for y := r.Min.Y; y < r.Max.Y && y-r.Min.Y < H; y++ {
		for x := r.Min.X; x < r.Max.X && x-r.Min.X < W; x++ {
			p := binary.LittleEndian.Uint16(page[2*((y-r.Min.Y)*W+x-r.Min.X):])
			dst.SetRGBA(x, y, rgb565(p))
		}
	}
	return nil
}

// rgb565 is an RGB565 pixel as an opaque colour, its channels widened by
// repeating their high bits.
// TRLC-LINKS: REQ-SDS-021
func rgb565(p uint16) color.RGBA {
	r, g, b := uint8(p>>11), uint8(p>>5&0x3f), uint8(p&0x1f)
	return color.RGBA{R: r<<3 | r>>2, G: g<<2 | g>>4, B: b<<3 | b>>2, A: 0xff}
}

// Backlight switches the light: any level above 0 is on, as GPIO 7 has no
// brightness.
// TRLC-LINKS: REQ-SDS-221
func (d *Display) Backlight(level float64) error { return SetBacklight(level > 0) }

// framebuffer is FB as the HAL's framebuffer: the page off show is the
// back buffer, and Present flips to it (or, without panning, the one page
// is drawn on show).
type framebuffer struct{ fb *FB }

// TRLC-LINKS: REQ-SDS-021
func (f framebuffer) Back() []byte {
	fb := f.fb
	if !fb.panOK {
		return fb.mem[:pageBytes]
	}
	next := 1 - fb.cur
	return fb.mem[next*pageBytes : (next+1)*pageBytes]
}

// TRLC-LINKS: REQ-SDS-021
func (f framebuffer) Stride() int { return W * 2 }

// TRLC-LINKS: REQ-SDS-021
func (f framebuffer) Present() error {
	fb := f.fb
	if !fb.panOK {
		return nil
	}
	next := 1 - fb.cur
	if err := fb.pan(next); err != nil {
		return err
	}
	fb.cur = next
	return nil
}
