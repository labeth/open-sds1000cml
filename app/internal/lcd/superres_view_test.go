package lcd

import (
	"math"
	"testing"

	"open-sds/app/internal/engine"
)

// A synthetic super-res HUD: a K× fine-grid mean built from gen(fineBin), a
// second channel from gen2, review focus, and a window covering the whole grid.
func srHUD(nb, K int, sampleS float64, gen, gen2 func(b int) float64) HUD {
	m1 := make([]float32, nb)
	m2 := make([]float32, nb)
	for b := 0; b < nb; b++ {
		m1[b] = float32(gen(b))
		if gen2 != nil {
			m2[b] = float32(gen2(b))
		} else {
			m2[b] = -1
		}
	}
	h := defaultHUD()
	h.SRActive, h.SRFocus = true, 3
	h.SRMean, h.SRMean2 = m1, m2
	h.SRk, h.SRSampleS = K, sampleS
	h.SRGateLo, h.SRGateHi, h.SRN = 0, nb/K, nb/K
	h.SRWinLo, h.SRWinHi = 0, nb/K // review the whole grid
	h.SRPeriod = 0
	return h
}

// The super-res FFT axis must be honest on the FINE grid: a tone placed ABOVE
// the raw single-shot Nyquist (only reachable because the stack is K× finer)
// must be reported at its true frequency — proving the spectrum reaches past
// the raw Nyquist, which is the whole point of an FFT over the crunched stack.
func TestSuperresFFTAboveRawNyquist(t *testing.T) {
	const K = 8
	const nb = 4096
	const sampleS = 1e-9  // 1 GSa/s raw → raw Nyquist 500 MHz
	fineDt := sampleS / K // 0.125 ns → fine Nyquist 4 GHz
	const F = 800e6       // 800 MHz: ABOVE the 500 MHz raw Nyquist
	h := srHUD(nb, K, sampleS, func(b int) float64 {
		return 128 + 90*math.Sin(2*math.Pi*F*float64(b)*fineDt)
	}, nil)

	n, effNyq, kLo, kHi, ok := srFFTPlan(h)
	if !ok {
		t.Fatal("srFFTPlan not ok")
	}
	if effNyq < 2*(0.5/sampleS) {
		t.Errorf("fine Nyquist %g should be several× the raw Nyquist %g", effNyq, 0.5/sampleS)
	}
	samples, _ := srResampleArray(h.SRMean, n)
	if samples == nil {
		t.Fatal("resample returned nil")
	}
	peak := fftCore(NewMemSurface(), samples, colC1, effNyq, kLo, kHi)
	if math.Abs(peak-F) > 0.03*F {
		t.Errorf("SR FFT peak %.3g Hz, want ~%.3g Hz (fine Nyq %.3g)", peak, F, effNyq)
	}
}

// Regression for the review finding: a repetitive stack is stored as ONE period
// (SRPeriod>0) but the Y-T review tiles it across a WIDE on-screen window. The
// FFT must transform the one-period fine grid at its fine dt — keeping the full
// K× Nyquist and finding a tone above the raw Nyquist — REGARDLESS of how wide
// the tiled display window is (the old window-based resample aliased it away).
func TestSuperresFFTTiledWindowKeepsFineNyquist(t *testing.T) {
	const K = 16
	const nb = 4096 // one period on the fine grid → 256 raw samples
	const sampleS = 2e-9
	fineDt := sampleS / K // fine Nyquist = 0.5/fineDt = 4 GHz; raw Nyquist = 250 MHz
	const F = 700e6       // above the 250 MHz raw Nyquist
	h := srHUD(nb, K, sampleS, func(b int) float64 {
		return 128 + 90*math.Sin(2*math.Pi*F*float64(b)*fineDt)
	}, nil)
	// TILING: one period stored, but the review window spans 40 tiled periods.
	h.SRPeriod = nb / K
	h.SRWinLo, h.SRWinHi = 0, 40*nb/K
	h.SRGateLo, h.SRGateHi = 0, nb/K

	n, effNyq, kLo, kHi, ok := srFFTPlan(h)
	if !ok {
		t.Fatal("srFFTPlan not ok")
	}
	fineNyq := 0.5 / fineDt
	if math.Abs(effNyq-fineNyq) > 0.05*fineNyq {
		t.Errorf("effNyq %.3g should stay at the fine Nyquist %.3g regardless of the tiled window", effNyq, fineNyq)
	}
	samples, _ := srResampleArray(h.SRMean, n)
	peak := fftCore(NewMemSurface(), samples, colC1, effNyq, kLo, kHi)
	if math.Abs(peak-F) > 0.03*F {
		t.Errorf("tiled-window SR FFT peak %.3g Hz, want ~%.3g Hz (raw Nyq %.3g) — window must not alias it", peak, F, 0.5/sampleS)
	}
}

// The crunched mean is FLOAT: feeding it to the FFT unquantised keeps a lower
// spectral noise floor than an 8-bit single capture of the same tone. If the
// view re-quantised the stack to uint8, this benefit would vanish — so lock it.
func TestSuperresFFTFloatFloorBeatsQuantised(t *testing.T) {
	const n = 2048
	const F = 37.0 // cycles across the record
	floorOf := func(samples []float64) float64 {
		re := make([]float64, n)
		im := make([]float64, n)
		var mean float64
		for i := 0; i < n; i++ {
			mean += samples[i]
		}
		mean /= float64(n)
		for i := 0; i < n; i++ {
			w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
			re[i] = (samples[i] - mean) * w
		}
		fftRadix2(re, im)
		half := n / 2
		mags := make([]float64, 0, half)
		peak := 1e-12
		for k := 1; k < half; k++ {
			m := math.Hypot(re[k], im[k])
			if m > peak {
				peak = m
			}
			mags = append(mags, m)
		}
		// median magnitude relative to the peak = the noise/quantisation floor
		var sum float64
		cnt := 0
		for _, m := range mags {
			sum += m / peak
			cnt++
		}
		return sum / float64(cnt)
	}
	fl := make([]float64, n)
	qz := make([]float64, n)
	for i := 0; i < n; i++ {
		v := 128 + 40*math.Sin(2*math.Pi*F*float64(i)/float64(n))
		fl[i] = v             // crunched float precision
		qz[i] = math.Round(v) // one 8-bit capture (quantised)
	}
	ffl := floorOf(fl)
	fqz := floorOf(qz)
	if ffl >= fqz {
		t.Errorf("float floor %.3e should be below quantised floor %.3e", ffl, fqz)
	}
}

// srResampleArray linearly resamples the fine-grid array, fills -1 gaps for the
// FFT, and flags those gap-touching output points invalid so X-Y can pen-up.
func TestSuperresResampleArray(t *testing.T) {
	// a ramp: resampling must stay monotone and mark every point valid (no gaps)
	const nb = 256
	m := make([]float32, nb)
	for b := range m {
		m[b] = float32(b) / 2 // 0..127.5, in code range
	}
	out, valid := srResampleArray(m, 512)
	if len(out) != 512 || len(valid) != 512 {
		t.Fatalf("want 512 out/valid, got %d/%d", len(out), len(valid))
	}
	for i := 1; i < len(out); i++ {
		if out[i] < out[i-1]-1e-9 {
			t.Fatalf("resampled ramp not monotone at %d: %g < %g", i, out[i], out[i-1])
		}
	}
	for i, v := range valid {
		if !v {
			t.Fatalf("no-gap ramp marked invalid at %d", i)
		}
	}
	// a gap region: the filled values interpolate over it (no NaN/-1), but the
	// output points that touch the gap are flagged invalid.
	for b := 100; b < 140; b++ {
		m[b] = -1
	}
	out2, valid2 := srResampleArray(m, 512)
	anyInvalid := false
	for i, v := range out2 {
		if math.IsNaN(v) || v < 0 {
			t.Fatalf("gap not filled at %d: %g", i, v)
		}
		if !valid2[i] {
			anyInvalid = true
		}
	}
	if !anyInvalid {
		t.Error("a 40-bin gap should flag some output points invalid (for X-Y pen-up)")
	}
}

// The behavioural fix: with the review active, selecting X-Y or FFT must render
// the STACKED data in that view — not silently fall back to the Y-T trace. Each
// view must draw its characteristic output and differ from Y-T.
func TestSuperresReviewRoutesByViewMode(t *testing.T) {
	const K, nb = 16, 8192
	const sampleS = 2e-9
	fineDt := sampleS / K
	// C1: a tone; C2: a different tone so X-Y is a real (non-diagonal) figure.
	h := srHUD(nb, K, sampleS,
		func(b int) float64 { return 128 + 80*math.Sin(2*math.Pi*3e6*float64(b)*fineDt) },
		func(b int) float64 { return 128 + 80*math.Sin(2*math.Pi*7e6*float64(b)*fineDt) },
	)
	h.ShowC1, h.ShowC2, h.TwoChan = true, true, true
	var f *engine.Frame // review ignores the live frame; nil must be safe

	yt := NewMemSurface()
	hyt := h
	hyt.ViewMode = 0
	Render(yt, f, hyt, true)

	// count the Y-T super-res trace ONLY in the trace band (the SR status line at
	// the bottom uses colSR too and is drawn in every view — exclude it).
	ytTrace := countColorIn(yt, colSR, 0, 0, W, H-20)
	if ytTrace == 0 {
		t.Error("Y-T review drew no super-res trace")
	}

	xy := NewMemSurface()
	hxy := h
	hxy.ViewMode = 1
	Render(xy, f, hxy, true)
	if countColor(xy, colMath) == 0 {
		t.Error("stacked X-Y drew no figure")
	}
	if countColorIn(xy, colSR, 0, 0, W, H-20) != 0 {
		t.Error("stacked X-Y should NOT draw the Y-T super-res trace")
	}

	fft := NewMemSurface()
	hf := h
	hf.ViewMode = 2
	Render(fft, f, hf, true)
	if countColor(fft, colC1) == 0 {
		t.Error("stacked FFT drew no C1 spectrum")
	}
	if countColor(fft, colC2) == 0 {
		t.Error("stacked FFT drew no C2 spectrum")
	}
	if countColorIn(fft, colSR, 0, 0, W, H-20) != 0 {
		t.Error("stacked FFT should NOT draw the Y-T super-res trace")
	}
}

// X-Y with no real second channel must degrade to the hint, not crash or draw
// a bogus diagonal.
func TestSuperresXYNeedsCH2(t *testing.T) {
	const K, nb = 8, 2048
	h := srHUD(nb, K, 2e-9, func(b int) float64 { return 128 + 50*math.Sin(float64(b)*0.05) }, nil)
	h.TwoChan = false
	h.ViewMode = 1
	sf := NewMemSurface()
	Render(sf, nil, h, true) // must not panic
	if countColor(sf, colMath) != 0 {
		t.Error("X-Y without CH2 should not draw a figure")
	}
}
