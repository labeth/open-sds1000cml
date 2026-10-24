// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"testing"

	"open-sds/app/internal/streamview"
)

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

// TRLC-LINKS: REQ-SDS-021
func BenchmarkFmtLabels(b *testing.B) {
	for b.Loop() {
		_ = vdivLabel(0.2, 10)
		_ = vdivLabel(0.05, 1)
		_ = fmtTdiv(-999.9e-9)
		_ = fmtFreq(1234567)
	}
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkDrawLoading(b *testing.B) {
	sf := NewMemSurface()
	for b.Loop() {
		DrawLoading(sf, "Shifting image", 1234567, 2000000)
	}
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkDeviceURL(b *testing.B) {
	DeviceURL()
	for b.Loop() {
		DeviceURL()
	}
}

// TRLC-LINKS: REQ-SDS-221
func BenchmarkDrawSaver(b *testing.B) {
	sf := NewMemSurface()
	f := goldenYTFrame()
	h := goldenYTHUD()
	h.MaskMode = 1
	h.MaskPass, h.MaskFail = 1234, 5
	step := 0
	for b.Loop() {
		DrawSaver(sf, f, h, step)
		step++
	}
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkFindModule(b *testing.B) {
	for b.Loop() {
		findModule("no-such-module.ko")
	}
}

// TRLC-LINKS: REQ-SDS-018
func BenchmarkStreamStatus(b *testing.B) {
	v := streamview.View{Label: "UART 115200", State: streamview.Streaming, Units: 123456, UnitsPerSecond: 11520, Errors: 2, Lost: 1, Mode: "hex"}
	for b.Loop() {
		_ = streamStatus(v)
	}
}

// TRLC-LINKS: REQ-SDS-214
func BenchmarkDrawMatchEditor(b *testing.B) {
	toks := make([]string, 32)
	for i := range toks {
		toks[i] = [...]string{"48", "65", "6C", "?F", "??", "ERR", "20"}[i%7]
	}
	m := MatchView{Open: true, Set: "Hex", Proto: "UART", Toks: toks, Cur: 30,
		Keys: [][]string{{"0", "1", "2", "3", "4", "5"}, {"6", "7", "8", "9", "A", "B"}, {"C", "D", "E", "F", "?", "??"}}, Row: 1, Col: 4}
	sf := NewMemSurface()
	for b.Loop() {
		drawMatchEditor(sf, m)
	}
}
