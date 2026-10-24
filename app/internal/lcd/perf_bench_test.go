// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"math"
	"testing"

	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
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

// TRLC-LINKS: REQ-SDS-021
func BenchmarkRenderBode(b *testing.B) {
	h := defaultHUD()
	h.ViewMode = 3
	const fc = 30e3
	for i := 0; i < 25; i++ {
		f := 1e3 * math.Pow(10, 3*float64(i)/24)
		h.BodeFreq = append(h.BodeFreq, f)
		h.BodeGain = append(h.BodeGain, -10*math.Log10(1+(f/fc)*(f/fc)))
		h.BodePhase = append(h.BodePhase, -math.Atan(f/fc)*180/math.Pi)
	}
	benchRender(b, nil, h)
}

// TRLC-LINKS: REQ-SDS-070
func BenchmarkRenderFFT(b *testing.B) {
	const n = 4096
	f := goldenFrame(n,
		func(i int) float64 {
			th := 2 * math.Pi * float64(i) / n
			return 128 + 70*math.Sin(205*th) + 18*math.Sin(614*th) + 7*math.Sin(1024*th)
		},
		func(i int) float64 { return 128 + 50*math.Sin(2*math.Pi*410*float64(i)/n) })
	f.TdivS, f.SampleS = 20e-6, 50e-9
	h := defaultHUD()
	h.TdivS, h.SampleS = 20e-6, 50e-9
	h.ViewMode = 2
	benchRender(b, f, h)
}

// TRLC-LINKS: REQ-SDS-068
func BenchmarkSpectrogramPush(b *testing.B) {
	sg := NewSpectrogram()
	x := sgSine(4096, 25e6, 2e-9)
	for b.Loop() {
		sg.Push(x, 0, 0.5/2e-9)
	}
}

// TRLC-LINKS: REQ-SDS-068
func BenchmarkRenderSpectrogram(b *testing.B) {
	sg := NewSpectrogram()
	for s := 0; s < 8; s++ {
		for r := 0; r < 12; r++ {
			sg.Push(sgSine(4096, 10e6+15e6*float64(s), 2e-9), 0, 0.5/2e-9)
		}
	}
	h := defaultHUD()
	h.ViewMode = 4
	h.Spect = sg
	benchRender(b, nil, h)
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkRenderCursorsMeasure(b *testing.B) {
	h := goldenYTHUD()
	h.CurOn, h.CurType, h.CurSel = true, 0, 0
	h.CurX = [2]float64{0.3, 0.7}
	h.ShowMeas = true
	benchRender(b, goldenYTFrame(), h)
}

// TRLC-LINKS: REQ-SDS-070
func BenchmarkDrawSpectrum(b *testing.B) {
	const n = 4096
	x := make([]float64, n)
	for i := range x {
		x[i] = math.Round(128 + 90*math.Sin(2*math.Pi*205*float64(i)/n) + 5*math.Sin(2*math.Pi*1500*float64(i)/n))
	}
	mags, peak, _ := framecalc.Mags(x)
	sf := NewMemSurface()
	for b.Loop() {
		drawSpectrum(sf, fullPlot, mags, peak, colC1, 10e6, 1, len(mags)-1)
	}
}

// TRLC-LINKS: REQ-SDS-217
func BenchmarkDrawMeasPanel(b *testing.B) {
	f := goldenYTFrame()
	f.Seq = 7 // measured once, then cached: the panel's own cost
	h := goldenYTHUD()
	h.MeasItems = []string{"1:pkpk", "1:freq", "1:duty", "1:rise", "2:pkpk", "2:mean", "2:period", "2:ovsh"}
	sf := NewMemSurface()
	for b.Loop() {
		drawMeasPanel(sf, fullPlot, f, h)
	}
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkMathCodes(b *testing.B) {
	c1, c2 := make([]uint8, 20000), make([]uint8, 20000)
	for i := range c1 {
		c1[i], c2[i] = uint8(i*7), uint8(i*13+5)
	}
	h := goldenYTHUD()
	h.OffC1V, h.OffC2V = 0.1, -0.05
	for b.Loop() {
		for mode := 1; mode <= 4; mode++ {
			h.MathMode = mode
			mathCodes(c1, c2, h)
		}
	}
}

// TRLC-LINKS: REQ-SDS-273
func BenchmarkDrawSoundPane(b *testing.B) {
	s := goldenSoundPane("MODULATION", []wsview.Span{{Text: "TREMOLO (AM)", Col: wsview.Amber}, {Text: "depth 30.0%  5.4 dB", Col: wsview.Text}})
	p := plotOf(wsview.Layout{MenuW: 30, PlotTop: 262})
	sf := NewMemSurface()
	for b.Loop() {
		drawSoundPane(sf, p, s)
		drawSoundTrace(sf, p, s)
	}
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkDrawSuperresTrace(b *testing.B) {
	h := goldenSuperresHUD()
	sf := NewMemSurface()
	for b.Loop() {
		drawSuperresTrace(sf, fullPlot, h)
	}
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkSRResample(b *testing.B) {
	h := goldenSuperresHUD()
	for b.Loop() {
		srResampleArray(h.SRMean, 4096)
	}
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkDrawEnvelope(b *testing.B) {
	const cols = 800
	mn, mx := make([]uint8, cols), make([]uint8, cols)
	for i := range mn {
		a := 40 + 25*math.Sin(2*math.Pi*3*float64(i)/cols)
		mn[i], mx[i] = uint8(128-a), uint8(128+a)
	}
	sf := NewMemSurface()
	for b.Loop() {
		drawEnvelope(sf, fullPlot, mn, mx, cols, colC1, 1)
		drawEnvelope(sf, fullPlot, mn, mx, cols, colC2, 1)
	}
}
