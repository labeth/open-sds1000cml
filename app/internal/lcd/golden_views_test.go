package lcd

import (
	"math"
	"testing"

	"open-sds/app/internal/engine"
)

// Golden coverage for the core LCD views (see golden_test.go for the harness).
// Every fixture is fully synthetic and deterministic: fixed waveform math (no
// randomness, no clock), and Frame.Seq left at 0 — a hand-built frame bypasses
// the time-based measurement cache in measFor, so the HUD/MEASURE readouts are
// recomputed from the fixture on every render instead of possibly serving
// another test's cached Result within the measRefresh window. On-screen text
// that could be environment-dependent (the device URL) is pinned in the HUD.

// goldenClamp converts a generator value to an ADC code.
func goldenClamp(v float64) uint8 {
	if v < 0 {
		v = 0
	}
	if v > 255 {
		v = 255
	}
	return uint8(v)
}

// goldenFrame builds a render-ready dual-channel frame from two per-sample
// generators, windowed across the whole record and centred on its middle.
func goldenFrame(n int, g1, g2 func(i int) float64) *engine.Frame {
	f := &engine.Frame{
		C1: make([]uint8, n), C2: make([]uint8, n),
		Valid: n, WinCols: n, EdgeX: float64(n) / 2,
		Ptp: 144, TdivS: 500e-6, SampleS: 800e-9, Trigd: true,
	}
	for i := 0; i < n; i++ {
		f.C1[i] = goldenClamp(g1(i))
		f.C2[i] = goldenClamp(g2(i))
	}
	return f
}

// goldenYTFrame is the bread-and-butter Y-T fixture: a square wave on C1 with
// a rising edge exactly at EdgeX (=n/2), and a sine on C2.
func goldenYTFrame() *engine.Frame {
	const n = 2048
	return goldenFrame(n,
		func(i int) float64 { // 512-sample period square, rising edge at 1024
			if (i/256)%2 == 0 {
				return 200
			}
			return 56
		},
		func(i int) float64 { return 128 + 60*math.Sin(2*math.Pi*float64(i-n/2)/512) })
}

// goldenYTHUD is the matching HUD: both channels shown, distinct V/div, real
// offsets (ground arrows off-centre), trigger level + position markers, and
// the fixed device URL on the top bar.
func goldenYTHUD() HUD {
	h := defaultHUD()
	h.ShowC1, h.ShowC2 = true, true
	h.C2VdivV = 0.5
	h.TrigLvlDiv = 1.2
	h.TrigPosFrac = 0.5
	h.OffC1V, h.OffC2V = 1.0, -0.25
	h.Trigd = true
	h.URL = "http://192.168.1.42:8080"
	return h
}

// TestGoldenYTDual pins the core dual-trace Y-T screen: graticule, both
// traces, trigger level line + position pointer, per-channel ground arrows,
// top-bar readouts (V/div, t/div, trigger state, URL) and the bottom
// Vpp/frequency line.
func TestGoldenYTDual(t *testing.T) {
	sf := NewMemSurface()
	Render(sf, goldenYTFrame(), goldenYTHUD(), true)
	goldenAssert(t, "yt_dual", sf)
}

// goldenUARTWave synthesizes an 8N1 LSB-first UART record (idle-high, codes
// 40/210, spb samples per bit), padded with trailing idle to length n — the
// same shape the decode package's own tests use.
func goldenUARTWave(bytes []int, spb, n int) []uint8 {
	w := make([]uint8, 0, n)
	push := func(bit, k int) {
		v := uint8(40)
		if bit == 1 {
			v = 210
		}
		for j := 0; j < k; j++ {
			w = append(w, v)
		}
	}
	push(1, spb*8) // lead idle
	for _, b := range bytes {
		push(0, spb) // start
		for c := 0; c < 8; c++ {
			push((b>>c)&1, spb) // LSB first
		}
		push(1, spb) // stop
		push(1, spb) // inter-byte idle
	}
	for len(w) < n { // trail idle
		w = append(w, 210)
	}
	return w[:n]
}

// TestGoldenYTDecodeUART pins the Y-T screen with the decode strip active:
// drawDecode runs the real internal/decode UART decoder on the fixture wave
// ("OK!" at an explicit baud), so the golden pins the span bars, byte text and
// the "UART  3 bytes" label produced by real decode spans.
func TestGoldenYTDecodeUART(t *testing.T) {
	const n, spb = 1024, 16
	const sampleS = 1e-6
	f := &engine.Frame{
		C1:    goldenUARTWave([]int{0x4F, 0x4B, 0x21}, spb, n), // "OK!"
		C2:    make([]uint8, n),
		Valid: n, WinCols: n, EdgeX: float64(n) / 2,
		Ptp: 170, TdivS: 100e-6, SampleS: sampleS, Trigd: true,
	}
	for i := range f.C2 {
		f.C2[i] = 210 // idle-high second channel
	}
	h := defaultHUD()
	h.TdivS, h.SampleS = 100e-6, sampleS
	// TrigPosFrac must be a real panel value (0.5): drawDecode maps spans with
	// the raw HUD posFrac and, unlike drawTrace, does not default 0 to centre —
	// a 0 here would shift the strip half a screen off the trace.
	h.TrigPosFrac = 0.5
	h.DecProto = 2                       // UART
	h.DecBaud = int(1 / (spb * sampleS)) // 62500: explicit, no auto-inference
	h.DecChA, h.DecFormat = 0, 0         // C1, hex
	sf := NewMemSurface()
	Render(sf, f, h, true)
	goldenAssert(t, "yt_decode_uart", sf)
}

// TestGoldenFFTPeaks pins the FFT view with peak markers: three tones placed
// on exact FFT bins (205/614/1024 cycles over the 4096-sample record) so the
// spectrum has clean local maxima above the -40 dBc mark floor, plus the
// per-channel peak-frequency labels and the band label.
func TestGoldenFFTPeaks(t *testing.T) {
	const n = 4096
	const sampleS = 50e-9 // Nyquist 10 MHz: tones land at ~1/3/5 MHz
	f := goldenFrame(n,
		func(i int) float64 {
			th := 2 * math.Pi * float64(i) / n
			return 128 + 70*math.Sin(205*th) + 18*math.Sin(614*th) + 7*math.Sin(1024*th)
		},
		func(i int) float64 { return 128 + 50*math.Sin(2*math.Pi*410*float64(i)/n) })
	f.TdivS, f.SampleS = 20e-6, sampleS
	h := defaultHUD()
	h.TdivS, h.SampleS = 20e-6, sampleS
	h.ViewMode = 2
	sf := NewMemSurface()
	Render(sf, f, h, true)
	goldenAssert(t, "fft_peaks", sf)
}

// TestGoldenXY pins the X-Y (Lissajous) view: C1 and C2 at a 1:2 frequency
// ratio trace a figure-eight, plus the "X:C1  Y:C2" hint and the X-Y top-bar
// label.
func TestGoldenXY(t *testing.T) {
	const n = 2048
	f := goldenFrame(n,
		func(i int) float64 { return 128 + 90*math.Sin(2*math.Pi*float64(i)/1024) },
		func(i int) float64 { return 128 + 90*math.Sin(4*math.Pi*float64(i)/1024) })
	h := defaultHUD()
	h.ViewMode = 1
	sf := NewMemSurface()
	Render(sf, f, h, true)
	goldenAssert(t, "xy_lissajous", sf)
}

// TestGoldenMeasurePanel pins the MEASURE overlay with BOTH channel boxes: the
// square/sine fixture gives every voltage row plus the timing rows (freq,
// period, duty) deterministic values. Seq=0 keeps measFor cache-free.
func TestGoldenMeasurePanel(t *testing.T) {
	h := goldenYTHUD()
	h.ShowMeas = true
	sf := NewMemSurface()
	Render(sf, goldenYTFrame(), h, true)
	goldenAssert(t, "measure_panel", sf)
}

// TestGoldenEnvelope pins the envelope-band rendering (IsEnv + Env arrays):
// an amplitude-modulated C1 band around centre and a thin C2 band near the
// bottom rail, so both fills and their shapes are pinned.
func TestGoldenEnvelope(t *testing.T) {
	const cols = 800
	f := &engine.Frame{
		C1: make([]uint8, cols), C2: make([]uint8, cols),
		EnvMin: make([]uint8, cols), EnvMax: make([]uint8, cols),
		EnvMin2: make([]uint8, cols), EnvMax2: make([]uint8, cols),
		Valid: cols, WinCols: cols, EdgeX: -1,
		IsEnv: true, EnvCols: cols,
		Ptp: 130, TdivS: 100e-3, SampleS: 1e-3,
	}
	for i := 0; i < cols; i++ {
		amp := 40 + 25*math.Sin(2*math.Pi*3*float64(i)/cols) // C1: AM band
		f.EnvMin[i] = goldenClamp(128 - amp)
		f.EnvMax[i] = goldenClamp(128 + amp)
		a2 := 6 + 4*math.Sin(2*math.Pi*5*float64(i)/cols) // C2: thin low band
		f.EnvMin2[i] = goldenClamp(40 - a2)
		f.EnvMax2[i] = goldenClamp(40 + a2)
		f.C1[i], f.C2[i] = 128, 128 // benign codes: no CLIP flag, flat readouts
	}
	h := defaultHUD()
	h.TdivS, h.SampleS = 100e-3, 1e-3
	sf := NewMemSurface()
	Render(sf, f, h, true)
	goldenAssert(t, "envelope_band", sf)
}

// TestGoldenBode pins the Bode/FRA view: a single-pole low-pass response
// (fc = 30 kHz) sampled at 25 log-spaced points over 1 kHz..1 MHz — decade
// gridlines, dB/phase axes, both traces with point dots, and the point count.
// drawBode renders purely from the HUD arrays; no frame is needed.
func TestGoldenBode(t *testing.T) {
	h := defaultHUD()
	h.ViewMode = 3
	const fc = 30e3
	for i := 0; i < 25; i++ {
		f := 1e3 * math.Pow(10, 3*float64(i)/24) // 1 kHz .. 1 MHz
		h.BodeFreq = append(h.BodeFreq, f)
		h.BodeGain = append(h.BodeGain, -10*math.Log10(1+(f/fc)*(f/fc)))
		h.BodePhase = append(h.BodePhase, -math.Atan(f/fc)*180/math.Pi)
	}
	sf := NewMemSurface()
	Render(sf, nil, h, true)
	goldenAssert(t, "bode", sf)
}

// TestGoldenSpectrogram pins the spectrogram view: a deterministic frequency
// staircase (8 tones × 24 rows, oldest/lowest at the bottom) pushed through
// the real Push path, then blitted with the frequency axis, dB key and
// new/old arrows. The waterfall content depends only on the pushed frames;
// the untouched region below the staircase pins the scroll behaviour too.
func TestGoldenSpectrogram(t *testing.T) {
	const n = 4096
	const dt = 2e-9 // raw Nyquist 250 MHz; stride 2 → axis Nyquist 125 MHz
	sg := NewSpectrogram()
	for s := 0; s < 8; s++ {
		f := 10e6 + 15e6*float64(s) // 10..115 MHz staircase
		for r := 0; r < 24; r++ {
			sg.Push(sgSine(n, f, dt), 0, 0.5/dt)
		}
	}
	h := defaultHUD()
	h.ViewMode = 4
	h.Spect = sg
	sf := NewMemSurface()
	Render(sf, nil, h, true)
	goldenAssert(t, "spectrogram", sf)
}

// TestGoldenCursorsTime pins the time-cursor overlay: two dashed vertical
// cursors (active A highlighted) over the Y-T trace plus the dt / 1/dt
// readout box.
func TestGoldenCursorsTime(t *testing.T) {
	h := goldenYTHUD()
	h.CurOn, h.CurType, h.CurSel = true, 0, 0
	h.CurX = [2]float64{0.3, 0.7}
	sf := NewMemSurface()
	Render(sf, goldenYTFrame(), h, true)
	goldenAssert(t, "cursors_time", sf)
}

// TestGoldenCursorsVolts pins the volts-cursor variant: two dashed horizontal
// cursors (active B highlighted) and the per-channel ΔV readout.
func TestGoldenCursorsVolts(t *testing.T) {
	h := goldenYTHUD()
	h.CurOn, h.CurType, h.CurSel = true, 1, 1
	h.CurY = [2]float64{0.25, 0.7}
	sf := NewMemSurface()
	Render(sf, goldenYTFrame(), h, true)
	goldenAssert(t, "cursors_volts", sf)
}

// TestGoldenMenu pins the softkey menu overlay over a live trace: the title
// band, five slots aligned to the physical F1..F5 buttons, and the filled
// inverted highlight on the selected slot.
func TestGoldenMenu(t *testing.T) {
	h := goldenYTHUD()
	h.MenuOpen = true
	h.MenuTitle = "TRIGGER"
	h.MenuItems = []MenuItem{
		{"Mode", "AUTO"}, {"Slope", "Rise"}, {"Source", "C1"},
		{"Coupling", "DC"}, {"50%", "Set"},
	}
	h.MenuSel = 1
	sf := NewMemSurface()
	Render(sf, goldenYTFrame(), h, true)
	goldenAssert(t, "menu_trace", sf)
}
