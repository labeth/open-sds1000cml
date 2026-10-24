// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import "testing"

// TRLC-LINKS: REQ-SDS-021
func BenchmarkDrawText(b *testing.B) {
	sf := NewMemSurface()
	for b.Loop() {
		DrawText(sf, 10, 100, "Time/div 200us  Trigger C1 Rise 1.65V", 0xffff, 1)
		DrawText(sf, 10, 200, "AUTO: UART 115200 8N1", 0xffff, 2)
		DrawText(sf, 790, 470, "clip", 0xffff, 2) // off the edge: clipped
	}
}

// benchPattern fills a surface like a persistence layer: mostly black, a
// band of trace pixels at varying brightness.
// TRLC-LINKS: REQ-SDS-021
func benchPattern() *MemSurface {
	m := NewMemSurface()
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			if (x*7+y*3)%11 < 3 {
				m.SetPixel(x, y, uint16(x*131+y*17))
			}
		}
	}
	return m
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkFadeToBlack(b *testing.B) {
	m := benchPattern()
	src := append([]byte(nil), m.Pix...)
	for b.Loop() {
		copy(m.Pix, src)
		m.FadeToBlack()
	}
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkBlitBright(b *testing.B) {
	src := benchPattern()
	d := NewMemSurface()
	for b.Loop() {
		d.BlitBright(src)
	}
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkEncodePNG(b *testing.B) {
	m := benchPattern()
	for b.Loop() {
		EncodePNG(m)
	}
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkDrawTraceSparse(b *testing.B) {
	sig := make([]uint8, 1000)
	for i := range sig {
		sig[i] = uint8(128 + 90*((i/37)%2*2-1) + i%13)
	}
	sf := NewMemSurface()
	for b.Loop() {
		drawTrace(sf, sig, 400, 500, false, 0xffe0, 0.5)
		drawTrace(sf, sig, 300, 500, true, 0x07ff, 0.5)
	}
}
