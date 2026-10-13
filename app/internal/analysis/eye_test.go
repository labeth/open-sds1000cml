// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"math"
	"testing"
)

const eyeSampleS = 2e-9

// feedEye folds records into a fresh 384x170 eye (the LCD's 2 px cells).
// TRLC-LINKS: REQ-SDS-215
func feedEye(recs [][]float64, sampleS, hintS float64) (*Eye, EyeResult) {
	e := NewEye(384, 170)
	for _, r := range recs {
		e.Feed(r, sampleS, hintS)
	}
	return e, e.Result()
}

// prbsRecords renders nrec PRBS7 records of n samples at ui samples per bit.
// TRLC-LINKS: REQ-SDS-215
func prbsRecords(nrec, n int, ui float64, w wave, tie func(r, k int) float64) [][]float64 {
	bits := prbs7(8000)
	var recs [][]float64
	for r := 0; r < nrec; r++ {
		var tf func(int) float64
		if tie != nil {
			rr := r
			tf = func(k int) float64 { return tie(rr, k) }
		}
		w.n, w.seed, w.startHigh = n, uint64(r+1)*31, bits[0] == 1
		recs = append(recs, w.render(nrzEdges(bits, ui, 20+math.Mod(float64(r)*37.3, ui), tf)))
	}
	return recs
}

// TestEyeMatchesWebOnStreams: on a continuous bit stream the LCD eye is one
// burst, so its UI and TIE equal the web engine's exactly, and its eye height
// and width (the same rules on the LCD's line-drawn grid) agree closely.
// TRLC-LINKS: REQ-SDS-215
func TestEyeMatchesWebOnStreams(t *testing.T) {
	vecs, web := ejWeb(t)
	for i, v := range vecs {
		js := web[i]
		if !v.stream || js.Records != len(v.Records) || js.TIERms == nil {
			continue
		}
		for _, hint := range []float64{0, v.hint} {
			e := NewEye(384, 170)
			for _, rec := range v.Records {
				e.Feed(rec, v.SampleS, hint)
			}
			r := e.Result()
			if r.Records != js.Records {
				t.Errorf("%s hint %g: folded %d of %d (%s)", v.Name, hint, r.Records, js.Records, r.LastErr)
				continue
			}
			if d := math.Abs(r.UI-*js.UISeconds) / *js.UISeconds; d > 1e-9 {
				t.Errorf("%s: UI %g vs web %g", v.Name, r.UI, *js.UISeconds)
			}
			if d := math.Abs(r.TIERms-*js.TIERms) / *js.TIERms; d > 1e-9 {
				t.Errorf("%s: TIE rms %g vs web %g", v.Name, r.TIERms, *js.TIERms)
			}
			if d := math.Abs(r.TIEPkPk-*js.TIEPp) / *js.TIEPp; d > 1e-9 {
				t.Errorf("%s: TIE pk-pk %g vs web %g", v.Name, r.TIEPkPk, *js.TIEPp)
			}
			// the LCD shows the web's RJ/DJ and period jitter too
			if js.RJ != nil && (math.Abs(r.RJ-*js.RJ) > 1e-15+1e-9*math.Abs(*js.RJ) || math.Abs(r.DJ-*js.DJ) > 1e-15+1e-9*math.Abs(*js.DJ)) {
				t.Errorf("%s: RJ/DJ %g/%g vs web %g/%g", v.Name, r.RJ, r.DJ, *js.RJ, *js.DJ)
			}
			if js.RJ == nil && !math.IsNaN(r.RJ) {
				t.Errorf("%s: RJ %g where the web has none", v.Name, r.RJ)
			}
			if js.PeriodJRms != nil && *js.PeriodJRms > 0 && math.Abs(r.PeriodJ-*js.PeriodJRms) > 1e-9**js.PeriodJRms {
				t.Errorf("%s: period jitter %g vs web %g", v.Name, r.PeriodJ, *js.PeriodJRms)
			}
			if js.C2CJRms != nil && *js.C2CJRms > 0 && math.Abs(r.C2CJ-*js.C2CJRms) > 1e-9**js.C2CJRms {
				t.Errorf("%s: cycle-to-cycle jitter %g vs web %g", v.Name, r.C2CJ, *js.C2CJRms)
			}
			if js.Eye != nil && js.Eye.Height > 0 && math.Abs(r.EyeHeight-js.Eye.Height) > 4 {
				t.Errorf("%s: eye height %.1f vs web %.1f codes", v.Name, r.EyeHeight, js.Eye.Height)
			}
			if js.Eye != nil && js.Eye.Width > 0 && math.Abs(r.EyeWidth-js.Eye.Width) > 0.05 {
				t.Errorf("%s: eye width %.3f vs web %.3f UI", v.Name, r.EyeWidth, js.Eye.Width)
			}
			if hint == 0 {
				t.Logf("%-22s height %.1f (web %v)  width %.3f (web %v)", v.Name, r.EyeHeight,
					webF(js.Eye, true), r.EyeWidth, webF(js.Eye, false))
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-215
func webF(m *ejWebEye, height bool) any {
	switch {
	case m == nil:
		return "-"
	case height:
		return math.Round(m.Height*10) / 10
	}
	return math.Round(m.Width*1000) / 1000
}

// TestEyeFiguresClean checks the figures against the signal's ground truth:
// a 25-sample-UI PRBS with 4-sample edges and 140 codes of swing.
// TRLC-LINKS: REQ-SDS-215
func TestEyeFiguresClean(t *testing.T) {
	const ui = 25.0
	_, r := feedEye(prbsRecords(10, 8192, ui, wave{lo: 60, hi: 200, rise: 4, noise: 0.7}, nil), eyeSampleS, 0)
	if r.Records != 10 || r.Rejected != 0 {
		t.Fatalf("folded %d, rejected %d (%s)", r.Records, r.Rejected, r.LastErr)
	}
	near := func(name string, got, want, tol float64) {
		t.Helper()
		if !(math.Abs(got-want) <= tol) {
			t.Errorf("%s = %.6g, want %.6g +-%.3g", name, got, want, tol)
		}
	}
	near("bit rate", r.BitRate, 1/(ui*eyeSampleS), 1/(ui*eyeSampleS)*5e-4)
	near("samples/UI", r.SamplesPerUI, ui, 0.02)
	near("eye height (codes)", r.EyeHeight, 136, 5) // 140 codes less the noise tails
	near("eye width (UI)", r.EyeWidth, 0.93, 0.07)
	near("crossing", r.Crossing, 0.5, 0.05)
	near("rise (samples)", r.Rise/eyeSampleS, 4, 0.6)
	near("fall (samples)", r.Fall/eyeSampleS, 4, 0.6)
	near("levels lo", r.Lo, 60, 1)
	near("levels hi", r.Hi, 200, 1)
	if r.TIERms > 0.1*eyeSampleS || r.TIEPkPk > 0.6*eyeSampleS {
		t.Errorf("TIE %.3g rms %.3g pk-pk: want the noise floor", r.TIERms, r.TIEPkPk)
	}
	if r.SampleLimited || r.TIEBound || r.RiseBound || r.FallBound || r.WidthBound {
		t.Errorf("clean 25 samples/UI eye flagged as bounds: %+v", r)
	}
	if want := 10 * (8192 - 2*ui) / ui; math.Abs(r.UIs-want) > 0.05*want {
		t.Errorf("UIs folded %.0f, want about %.0f", r.UIs, want)
	}
	if !r.Open.HOK || !r.Open.VOK || r.Open.Left < 0.5 || r.Open.Right > 1.5 || r.Open.Lo < 60 || r.Open.Hi > 200 {
		t.Errorf("opening %+v outside the crossings and rails", r.Open)
	}
	// the grid: one UI twice, the crossings at a quarter and three quarters
	if r.Cols != 384 || r.Rows != 170 || len(r.Grid) != r.Cols*r.Rows || r.GridMax == 0 {
		t.Fatalf("grid %dx%d len %d max %d", r.Cols, r.Rows, len(r.Grid), r.GridMax)
	}
	mid := int(math.Round((EyeCodeHi - 130) / ((EyeCodeHi - EyeCodeLo) / 170)))
	best, bestV := 0, uint32(0)
	for c := 0; c < r.Cols/2; c++ {
		if v := r.Grid[mid*r.Cols+c]; v > bestV {
			best, bestV = c, v
		}
		if r.Grid[mid*r.Cols+c] != r.Grid[mid*r.Cols+c+r.Cols/2] {
			t.Fatalf("grid halves differ at column %d", c)
		}
	}
	if math.Abs(float64(best)-float64(r.Cols)/4) > 3 {
		t.Errorf("mid-level crossing density peaks at column %d, want %d", best, r.Cols/4)
	}
}

// TestEyeSampleLimitedBounds: sharp edges at 4.3 samples per UI fall between
// samples, so TIE, rise and fall are upper bounds, the width a lower bound,
// no crossing is given, and the eye is sample-limited (the review's rule).
// TRLC-LINKS: REQ-SDS-215
func TestEyeSampleLimitedBounds(t *testing.T) {
	_, r := feedEye(prbsRecords(8, 8192, 4.3, wave{lo: 60, hi: 200, noise: 0.5}, nil), eyeSampleS, 0)
	if r.Records == 0 {
		t.Fatalf("nothing folded: %s", r.LastErr)
	}
	if !r.SampleLimited || !r.TIEBound || !r.RiseBound || !r.FallBound || !r.WidthBound {
		t.Errorf("4.3 samples/UI, sharp edges: bounds not flagged %+v", r)
	}
	if !math.IsNaN(r.Crossing) {
		t.Errorf("crossing %.3f given for edges between samples", r.Crossing)
	}
	if r.Rise > 1.5*eyeSampleS || r.TIEPkPk > 1.2*eyeSampleS {
		t.Errorf("rise %.3g TIE pk-pk %.3g: an ideal step reads under a sample", r.Rise, r.TIEPkPk)
	}
	// the line-drawn grid still measures a (lower-bound) width across the X
	if !(r.EyeWidth > 0.5 && r.EyeWidth < 1) {
		t.Errorf("eye width %.3f", r.EyeWidth)
	}
}

// TestEyeJitterInjected recovers injected deterministic jitter: a 0/6-sample
// square TIE (the FPGA source's) on a 50-sample UI reads 3 samples rms and
// closes the eye by about 6/50 UI. Its pk-pk reads 6 to 8.4: each record's
// least-squares clock tilts to absorb part of jitter this slow (32 bits in a
// 164-bit record), exactly as in the web engine (the CDR corner, tieHpHz).
// TRLC-LINKS: REQ-SDS-215
func TestEyeJitterInjected(t *testing.T) {
	dj := func(r, k int) float64 {
		if int(math.Floor(float64(k)+math.Mod(float64(r)*53.7, 32))/16)%2 == 1 {
			return 6
		}
		return 0
	}
	_, r := feedEye(prbsRecords(10, 8192, 50, wave{lo: 60, hi: 200, rise: 5, noise: 0.7}, dj), eyeSampleS, 0)
	if pp := r.TIEPkPk / eyeSampleS; pp < 5.8 || pp > 9 {
		t.Errorf("TIE pk-pk %.2f samples, want 6 to 9", pp)
	}
	if rms := r.TIERms / eyeSampleS; rms < 2.6 || rms > 3.4 {
		t.Errorf("TIE rms %.2f samples, want about 3", rms)
	}
	if r.EyeWidth < 0.80 || r.EyeWidth > 0.90 {
		t.Errorf("eye width %.3f UI, want about 0.86", r.EyeWidth)
	}
}

// TestEyeSparsePulses: a lone pulse every 19 bits leaves every burst with two
// edges; the record is then fitted as one grid, as the web does.
// TRLC-LINKS: REQ-SDS-215
func TestEyeSparsePulses(t *testing.T) {
	sparse := make([]int, 600)
	for i := 3; i < len(sparse); i += 19 {
		sparse[i] = 1
	}
	var recs [][]float64
	for r := 0; r < 6; r++ {
		w := wave{n: 8192, lo: 60, hi: 200, rise: 4.5, noise: 0.7, seed: uint64(r + 1)}
		recs = append(recs, w.render(nrzEdges(sparse, 30, 20+7.3*float64(r), nil)))
	}
	_, r := feedEye(recs, eyeSampleS, 0)
	if r.Records != len(recs) {
		t.Fatalf("folded %d of %d (%s)", r.Records, len(recs), r.LastErr)
	}
	if math.Abs(r.SamplesPerUI-30) > 0.05 || r.TIERms > 0.1*eyeSampleS {
		t.Errorf("%.3f samples/UI, TIE %.3g s rms", r.SamplesPerUI, r.TIERms)
	}
}

// TestEyeAsyncUART: a UART message starts at any time, so one bit grid over
// the whole record cannot fit it; the web engine locks onto a smear (or not
// at all), the LCD eye fits each message on its own grid.
// TRLC-LINKS: REQ-SDS-215
func TestEyeAsyncUART(t *testing.T) {
	const ui = 9.6
	msgs := [][]byte{[]byte("Hi U\xaa"), []byte("status=OK\r\n"), []byte("\x01\x02\x03")}
	var recs [][]float64
	for r := 0; r < 8; r++ {
		w := wave{n: 8192, lo: 60, hi: 200, rise: 1, noise: 0.7, startHigh: true, seed: uint64(r + 11)}
		gaps := []float64{173.37 + float64(r), 291.81, 88.5 + 3.3*float64(r)}
		recs = append(recs, w.render(asyncUART(msgs, ui, 50+7.7*float64(r), gaps)))
	}
	web := NewEyeJitter(EyeJitterOptions{})
	for _, rec := range recs {
		web.Feed(rec, eyeSampleS)
	}
	wr := web.Result()
	for _, hint := range []float64{0, ui * eyeSampleS} {
		_, r := feedEye(recs, eyeSampleS, hint)
		if r.Records != len(recs) {
			t.Fatalf("hint %g: folded %d of %d (%s)", hint, r.Records, len(recs), r.LastErr)
		}
		if math.Abs(r.SamplesPerUI-ui) > 0.03 {
			t.Errorf("hint %g: %.3f samples/UI, want %.1f", hint, r.SamplesPerUI, ui)
		}
		if r.TIERms > 0.15*eyeSampleS || (wr.HasTIE && r.TIERms > wr.TIERms/5) {
			t.Errorf("hint %g: TIE %.3g s rms (one grid over the record: %.3g)", hint, r.TIERms, wr.TIERms)
		}
		if r.EyeWidth < 0.8 {
			t.Errorf("hint %g: eye width %.3f", hint, r.EyeWidth)
		}
	}
}

// TestEyeUndersampledUARTWithHint: at 3.6 samples per bit with sharp edges
// the intervals do not cluster well enough for the estimator alone; the
// decoder's baud seeds it and the eye folds, flagged sample-limited.
// TRLC-LINKS: REQ-SDS-215
func TestEyeUndersampledUARTWithHint(t *testing.T) {
	const ui = 3.6
	msg := []byte("The quick brown fox jumps over the lazy dog. 0123456789")
	var recs [][]float64
	for r := 0; r < 6; r++ {
		w := wave{n: 4096, lo: 60, hi: 200, noise: 0.5, startHigh: true, seed: uint64(r + 3)}
		recs = append(recs, w.render(nrzEdges(uartBits(msg, []int{1, 4}), ui, 10+0.37*float64(r), nil)))
	}
	_, r := feedEye(recs, eyeSampleS, ui*eyeSampleS)
	if r.Records == 0 {
		t.Fatalf("nothing folded with the decoder's bit period: %s", r.LastErr)
	}
	if math.Abs(r.SamplesPerUI-ui) > 0.05 {
		t.Errorf("%.3f samples/UI, want %.1f", r.SamplesPerUI, ui)
	}
	if !r.SampleLimited || !r.TIEBound || !r.WidthBound || !math.IsNaN(r.Crossing) {
		t.Errorf("undersampled eye not flagged: limited %v TIE %v width %v crossing %v",
			r.SampleLimited, r.TIEBound, r.WidthBound, r.Crossing)
	}
}

// TestEyeI2CFramingEdges folds I2C SDA at the SCL period: the START and STOP
// edges move SDA while SCL is high, half a bit off the data grid; they are left
// out of the TIE instead of reading as half a UI of jitter.
// TRLC-LINKS: REQ-SDS-215
func TestEyeI2CFramingEdges(t *testing.T) {
	const h = 20 // samples per SCL half period
	var recs [][]float64
	for r := 0; r < 6; r++ {
		var sda []float64
		seg := func(v float64, k int) {
			for i := 0; i < k; i++ {
				sda = append(sda, v)
			}
		}
		lo, hi := 40.0, 210.0
		bit := func(b int) float64 {
			if b != 0 {
				return hi
			}
			return lo
		}
		seg(hi, 3*h+r*7) // idle
		seg(lo, h)       // START: SDA falls while SCL is high
		for _, v := range []int{0x48, 0x55, 0xA3, 0x0F} {
			for k := 7; k >= 0; k-- {
				seg(bit((v>>k)&1), 2*h)
			}
			seg(lo, 2*h) // ACK
		}
		seg(lo, h)       // SCL low, SDA low
		seg(lo, h/2)     // SCL high
		seg(hi, 4*h+r*5) // STOP and idle
		recs = append(recs, sda)
	}
	_, r := feedEye(recs, eyeSampleS, 2*h*eyeSampleS)
	if r.Records != len(recs) {
		t.Fatalf("folded %d of %d (%s)", r.Records, len(recs), r.LastErr)
	}
	if math.Abs(r.SamplesPerUI-2*h) > 0.1 {
		t.Errorf("%.2f samples/UI, want %d", r.SamplesPerUI, 2*h)
	}
	if r.TIEPkPk > 1.5*eyeSampleS {
		t.Errorf("TIE pk-pk %.2f samples: the START/STOP edges were counted as jitter", r.TIEPkPk/eyeSampleS)
	}
}

// TestEyeEdgeStarvedWithHint: a capture holding one UART byte has too few
// edges to estimate a bit period; the decoder's period folds it.
// TRLC-LINKS: REQ-SDS-215
func TestEyeEdgeStarvedWithHint(t *testing.T) {
	const ui = 40.0
	w := wave{n: 1024, lo: 60, hi: 200, rise: 3, noise: 0.5, startHigh: true, seed: 9}
	rec := w.render(nrzEdges(uartBits([]byte{0x5A}, nil), ui, 100, nil))
	e := NewEye(256, 100)
	e.Feed(rec, eyeSampleS, 0)
	if r := e.Result(); r.Records != 0 || r.LastErr != EyeNoBitGrid {
		t.Fatalf("one byte without a hint: folded %d, last %q", r.Records, r.LastErr)
	}
	e.Feed(rec, eyeSampleS, ui*eyeSampleS)
	r := e.Result()
	if r.Records != 1 || r.LastErr != "" {
		t.Fatalf("one byte with the decoder's period: folded %d, last %q", r.Records, r.LastErr)
	}
	if math.Abs(r.SamplesPerUI-ui) > 0.5 {
		t.Errorf("%.2f samples/UI, want %.0f", r.SamplesPerUI, ui)
	}
}

// TestEyeRejectsAndReset covers the rejections and Reset.
// TRLC-LINKS: REQ-SDS-215
func TestEyeRejectsAndReset(t *testing.T) {
	e := NewEye(200, 100)
	good := prbsRecords(1, 4096, 20, wave{lo: 60, hi: 200, rise: 3, noise: 0.5}, nil)[0]
	e.Feed(good, eyeSampleS, 0)
	if r := e.Result(); r.Records != 1 {
		t.Fatalf("good record not folded: %s", r.LastErr)
	}
	for _, tc := range []struct {
		name    string
		codes   []float64
		sampleS float64
		want    string
	}{
		{"empty", nil, eyeSampleS, EyeNoRecord},
		{"short", good[:10], eyeSampleS, EyeNoRecord},
		{"no sample interval", good, 0, EyeNoRecord},
		{"NaN interval", good, math.NaN(), EyeNoRecord},
		{"NaN code", append(append([]float64{}, good[:100]...), math.NaN()), eyeSampleS, EyeNoRecord},
		{"flat", make([]float64, 4096), eyeSampleS, EyeNoSwing},
		{"another bit rate", prbsRecords(1, 4096, 10, wave{lo: 60, hi: 200, rise: 2, noise: 0.5}, nil)[0],
			eyeSampleS, EyeUIInconsistent},
	} {
		e.Feed(tc.codes, tc.sampleS, 0)
		if r := e.Result(); r.LastErr != tc.want {
			t.Errorf("%s: last error %q, want %q", tc.name, r.LastErr, tc.want)
		}
	}
	// the same signal at another sample interval is the same eye (UI in seconds)
	e.Feed(prbsRecords(1, 8192, 40, wave{lo: 60, hi: 200, rise: 6, noise: 0.5}, nil)[0], eyeSampleS/2, 0)
	if r := e.Result(); r.LastErr != "" || r.Records != 2 {
		t.Errorf("same UI at half the sample interval: %q, %d folded", r.LastErr, r.Records)
	}
	e.Reset()
	r := e.Result()
	if r.Records != 0 || r.Rejected != 0 || r.UIs != 0 || r.GridMax != 0 || !math.IsNaN(r.UI) ||
		!math.IsNaN(r.EyeHeight) || !math.IsNaN(r.TIERms) || r.Open.HOK || r.Open.VOK {
		t.Errorf("after Reset: %+v", r)
	}
	if r.Cols != 200 || r.Rows != 100 {
		t.Errorf("Reset changed the size to %dx%d", r.Cols, r.Rows)
	}
}

// TestEyeOpeningCursors moves the threshold and the sample point.
// TRLC-LINKS: REQ-SDS-215
func TestEyeOpeningCursors(t *testing.T) {
	e, r := feedEye(prbsRecords(10, 8192, 25, wave{lo: 60, hi: 200, rise: 8, noise: 0.7}, nil), eyeSampleS, 0)
	mid := (r.Lo + r.Hi) / 2
	centre := e.Opening(1, mid)
	nearRail := e.Opening(1, r.Lo+0.15*(r.Hi-r.Lo))
	if !centre.HOK || !nearRail.HOK || !(nearRail.Width() < centre.Width()) {
		t.Errorf("width at mid %.3f, near the rail %.3f: slow edges must narrow the eye toward the rails",
			centre.Width(), nearRail.Width())
	}
	if c := e.Opening(0.5, mid); !c.HOK || c.Width() != 0 {
		t.Errorf("sample point on the crossing: width %.3f, want a closed 0", c.Width())
	}
	if off := e.Opening(0.75, mid); !off.VOK || !(off.Height() < centre.Height()) {
		t.Errorf("height off centre %.1f, at the centre %.1f", off.Height(), centre.Height())
	}
	if b := e.Opening(math.NaN(), mid); b.HOK || b.VOK {
		t.Errorf("NaN sample point measured %+v", b)
	}
	// the half eye at the window's left edge is the same eye
	left := e.Opening(0, mid)
	if math.Abs(left.Width()-centre.Width()) > 1e-9 || math.Abs(left.Height()-centre.Height()) > 1e-9 {
		t.Errorf("left half eye %+v, centre %+v", left, centre)
	}
}

// TestEyeDegenerateSizes: tiny, odd and huge grid sizes are clamped.
// TRLC-LINKS: REQ-SDS-215
func TestEyeDegenerateSizes(t *testing.T) {
	rec := prbsRecords(1, 4096, 20, wave{lo: 60, hi: 200, rise: 3, noise: 0.5}, nil)[0]
	for _, sz := range [][2]int{{0, 0}, {1, 1}, {3, 3}, {-5, 7}, {101, 33}, {1 << 20, 1 << 20}} {
		e := NewEye(sz[0], sz[1])
		e.Feed(rec, eyeSampleS, 0)
		r := e.Result()
		if r.Cols < 2 || r.Cols%2 != 0 || r.Cols > eyeMaxCols || r.Rows < 2 || r.Rows > eyeMaxRows ||
			len(r.Grid) != r.Cols*r.Rows {
			t.Errorf("NewEye(%d, %d): %dx%d, grid %d", sz[0], sz[1], r.Cols, r.Rows, len(r.Grid))
		}
	}
}

// TestEyeGridHalving keeps the counts from overflowing on a long run.
// TRLC-LINKS: REQ-SDS-215
func TestEyeGridHalving(t *testing.T) {
	e := NewEye(64, 32)
	rec := prbsRecords(1, 4096, 20, wave{lo: 60, hi: 200, rise: 3, noise: 0.5}, nil)[0]
	e.Feed(rec, eyeSampleS, 0)
	before := e.Result().GridMax
	e.bound = eyeMaxCount + 1 // as after hours of captures
	e.Feed(rec, eyeSampleS, 0)
	after := e.Result().GridMax
	if !(after < 2*before) || after == 0 {
		t.Errorf("grid max %d -> %d: not halved", before, after)
	}
}

// BenchmarkEyeFeed: one capture into the eye, the LCD's per-frame cost.
// TRLC-LINKS: REQ-SDS-215
func BenchmarkEyeFeed(b *testing.B) {
	for _, bc := range []struct {
		name string
		n    int
		ui   float64
		rise float64
	}{
		{"2k-3.6spu", 2048, 3.6, 0},
		{"2k-20spu", 2048, 20, 3},
		{"10k-3.6spu", 10000, 3.6, 0},
		{"10k-20spu", 10000, 20, 3},
		{"10k-100spu", 10000, 100, 9},
	} {
		recs := prbsRecords(4, bc.n, bc.ui, wave{lo: 60, hi: 200, rise: bc.rise, noise: 0.7}, nil)
		b.Run(bc.name, func(b *testing.B) {
			e := NewEye(384, 170)
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				e.Feed(recs[i%len(recs)], eyeSampleS, bc.ui*eyeSampleS)
			}
		})
	}
}

// BenchmarkEyeResult: the figures and the grid, once per LCD frame.
// TRLC-LINKS: REQ-SDS-215
func BenchmarkEyeResult(b *testing.B) {
	e, _ := feedEye(prbsRecords(4, 10000, 20, wave{lo: 60, hi: 200, rise: 3, noise: 0.7}, nil), eyeSampleS, 0)
	b.ReportAllocs()
	for b.Loop() {
		e.Result()
	}
}
