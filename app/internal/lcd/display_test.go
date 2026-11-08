// ENGMODEL-OWNER-UNIT: FU-APP-LCD

package lcd

import (
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	"github.com/labeth/open-dso-core/hal"
)

// The panel as the HAL's display: the profile's spec, no framebuffer until
// the panel is open, no hardware renderer, and the page on show captured
// as RGBA.
// TRLC-LINKS: REQ-SDS-021
func TestDisplayAsHAL(t *testing.T) {
	spec := hal.DisplaySpec{W: W, H: H, Format: hal.RGB565, DivsH: 10, DivsV: 8}
	d := NewDisplay(spec)
	if d.Spec() != spec || d.Framebuffer() != nil {
		t.Fatalf("closed panel: %+v %v", d.Spec(), d.Framebuffer())
	}
	if _, ok := d.Renderer(); ok {
		t.Fatal("a hardware renderer")
	}
	dst := image.NewRGBA(image.Rect(0, 0, W, H))
	if _, ok := hal.AsRefusal(d.Capture(dst)); !ok {
		t.Fatal("captured a closed panel")
	}
	fb := &FB{fd: -1, mem: make([]byte, 2*pageBytes), pages: 2}
	d.Attach(fb)
	hfb := d.Framebuffer()
	if hfb == nil || hfb.Stride() != 2*W || len(hfb.Back()) != pageBytes {
		t.Fatalf("framebuffer %v", hfb)
	}
	binary.LittleEndian.PutUint16(hfb.Back()[2*(3*W+5):], 0xF800) // red at (5, 3)
	if err := hfb.Present(); err != nil {
		t.Fatal(err)
	}
	if err := d.Capture(dst); err != nil {
		t.Fatal(err)
	}
	if got := dst.RGBAAt(5, 3); got != (color.RGBA{R: 0xff, A: 0xff}) {
		t.Fatalf("pixel %+v", got)
	}
	if got := dst.RGBAAt(0, 0); got != (color.RGBA{A: 0xff}) {
		t.Fatalf("pixel %+v", got)
	}
}
