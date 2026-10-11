// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"open-sds/app/internal/testenv"
)

// The eye/jitter parity vectors: clean PRBS and UART-like NRZ at several
// samples per UI, with random, deterministic and periodic jitter, ringing,
// fractional (Q8.8) codes, plus the negative and guard cases (noise only, a
// bit-rate change, an edge-starved signal) and non-default engine sizes.

// ejVector is one parity vector: records fed in order to one engine.
// TRLC-LINKS: REQ-SDS-215
type ejVector struct {
	Name    string         `json:"name"`
	SampleS float64        `json:"sampleS"`
	Opts    map[string]int `json:"opts,omitempty"`
	Records [][]float64    `json:"records"`
	opts    EyeJitterOptions
	hint    float64 // the bit period a decoder would report (s), for the LCD eye
	stream  bool    // one continuous bit grid: the LCD eye must give the web's figures
}

// ejWebResult is ejparity.cjs's answer for one vector (nil: undefined in JS).
// TRLC-LINKS: REQ-SDS-215
type ejWebResult struct {
	Name        string      `json:"name"`
	Status      []string    `json:"status"`
	Records     int         `json:"records"`
	Rejected    int         `json:"rejected"`
	Edges       int         `json:"edges"`
	LastErr     string      `json:"lastErr"`
	UI          *float64    `json:"ui"`
	UISeconds   *float64    `json:"uiSeconds"`
	BitRate     *float64    `json:"bitRate"`
	TIEHpHz     *float64    `json:"tieHpHz"`
	TIERms      *float64    `json:"tieRms"`
	TIEPp       *float64    `json:"tiePp"`
	RJ          *float64    `json:"rj"`
	DJ          *float64    `json:"dj"`
	PeriodJRms  *float64    `json:"periodJRms"`
	C2CJRms     *float64    `json:"c2cJRms"`
	TIEWindow   *float64    `json:"tieWindow"`
	Spectrum    []float64   `json:"spectrum"`
	SpecDf      *float64    `json:"specDf"`
	SpecPeakHz  *float64    `json:"specPeakHz"`
	SpecPeakAmp *float64    `json:"specPeakAmp"`
	Eye         *ejWebEye   `json:"eye"`
	Digest      ejWebDigest `json:"digest"`
}

// TRLC-LINKS: REQ-SDS-215
type ejWebEye struct {
	Height float64 `json:"height"`
	Width  float64 `json:"width"`
	Cross  float64 `json:"cross"`
}

// TRLC-LINKS: REQ-SDS-215
type ejWebDigest struct {
	Sum      float64 `json:"sum"`
	Weighted float64 `json:"weighted"`
	Cells    int     `json:"cells"`
}

// ejVectors builds the shared vectors.
// TRLC-LINKS: REQ-SDS-215
func ejVectors() []ejVector {
	const sampleS, n = 2e-9, 8192
	bits := prbs7(4000)
	var vs []ejVector
	// nrz renders nrec records of a bit stream, each at another phase
	nrz := func(nrec int, src []int, ui float64, w wave, tie func(r, k int) float64) [][]float64 {
		var recs [][]float64
		for r := 0; r < nrec; r++ {
			phase := 20 + math.Mod(float64(r)*37.3, ui)
			var tf func(k int) float64
			if tie != nil {
				rr := r
				tf = func(k int) float64 { return tie(rr, k) }
			}
			w.n, w.seed, w.startHigh = n, uint64(r+1)*7919+uint64(ui*1000), src[0] == 1
			recs = append(recs, w.render(nrzEdges(src, ui, phase, tf)))
		}
		return recs
	}
	add := func(name string, ui float64, stream bool, recs [][]float64) {
		vs = append(vs, ejVector{Name: name, SampleS: sampleS, Records: recs, hint: ui * sampleS, stream: stream})
	}
	base := wave{lo: 60, hi: 200, noise: 0.7}
	w := func(rise float64) wave { b := base; b.rise = rise; return b }

	add("prbs-ui100", 100, true, nrz(8, bits, 100, w(9), nil))
	add("prbs-ui20", 20, true, nrz(8, bits, 20, w(3), nil))
	add("prbs-ui8", 8, true, nrz(8, bits, 8, w(1.5), nil))
	add("prbs-ui4.3-sharp", 4.3, true, nrz(8, bits, 4.3, w(0), nil))
	rj := func(r, k int) float64 { g := rng{s: uint64(r*100003 + k*7 + 1)}; return 0.4 * g.gauss() }
	add("prbs-ui12.5-rj", 12.5, true, nrz(8, bits, 12.5, w(2), rj))
	dj := func(r, k int) float64 { // the FPGA's square TIE: 0 / 6 samples every 16 bits
		if int(math.Floor(float64(k)+math.Mod(float64(r)*53.7, 32))/16)%2 == 1 {
			return 6
		}
		return 0
	}
	add("prbs-ui50-dj", 50, true, nrz(8, bits, 50, w(5), dj))
	pj := func(r, k int) float64 { return 2.5 * math.Sin(2*math.Pi*float64(k)/23+float64(r)) }
	add("prbs-ui30-pj", 30, true, nrz(8, bits, 30, w(3), pj))
	ringW := w(2)
	ringW.ring, ringW.ringPer, ringW.ringTau = 0.15, 6, 4
	add("prbs-ui25-ring", 25, true, nrz(8, bits, 25, ringW, nil))
	q88 := w(4)
	q88.frac = 0.38671875
	add("prbs-q88-ui40", 40, true, nrz(8, bits, 40, q88, nil))

	msg := []byte("Hello, scope! \x55\xaa\x0f\xf0\x00\xff 0123456789 The quick brown fox jumps over the lazy dog.")
	var long []byte
	for len(long) < 1200 {
		long = append(long, msg...)
	}
	add("uart-b2b-ui16", 16, true, nrz(8, uartBits(long, nil), 16, w(2), nil))
	add("uart-idle-ui10", 10, false, nrz(8, uartBits(long, []int{2, 3, 5}), 10, w(1.5), nil))
	add("uart-b2b-ui3.6-sharp", 3.6, true, nrz(6, uartBits(long, nil), 3.6, w(0), nil))
	{
		var recs [][]float64
		msgs := [][]byte{[]byte("Hi U\xaa"), []byte("status=OK\r\n"), []byte("\x01\x02\x03")}
		for r := 0; r < 8; r++ {
			uw := w(1)
			uw.n, uw.seed, uw.startHigh = n, uint64(r+11), true
			gaps := []float64{173.37 + float64(r), 291.81, 88.5 + 3.3*float64(r)}
			recs = append(recs, uw.render(asyncUART(msgs, 9.6, 50+7.7*float64(r), gaps)))
		}
		add("uart-async-ui9.6", 9.6, false, recs)
	}
	{
		clock := make([]int, 400)
		for i := range clock {
			clock[i] = i & 1
		}
		dcd := func(r, k int) float64 { // falling edges 4 samples late
			if k%2 == 0 {
				return 4
			}
			return 0
		}
		add("clock-dcd-ui40", 40, true, nrz(8, clock, 40, w(4), dcd))
	}
	{
		sparse := make([]int, 600)
		for i := 3; i < len(sparse); i += 19 {
			sparse[i] = 1
		}
		add("sparse-ui30", 30, true, nrz(8, sparse, 30, w(4.5), nil))
	}
	{
		var recs [][]float64
		for r := 0; r < 4; r++ {
			fw := base
			fw.lo, fw.hi, fw.n, fw.seed, fw.noise = 128, 128, n, uint64(r+3), 2
			recs = append(recs, fw.render(nil))
		}
		add("noise-only", 0, false, recs)
	}
	{
		recs := append(nrz(3, bits, 40, w(4), nil), nrz(3, bits, 20, w(2), nil)...)
		add("rate-change-40-20", 40, false, recs)
	}
	{
		var recs [][]float64
		for r := 0; r < 10; r++ {
			sb := make([]int, 14)
			s := 0x5a + r
			for k := range sb {
				sb[k] = (s >> (k % 7)) & 1
			}
			sw := w(4.5)
			sw.n, sw.seed, sw.startHigh = n, uint64(r+5), sb[0] == 1
			recs = append(recs, sw.render(nrzEdges(sb, 700, 300, nil)))
		}
		add("edge-starved-ui700", 700, true, recs)
	}
	vs = append(vs,
		ejVector{Name: "tiecap64-ui25", SampleS: sampleS, Records: nrz(8, bits, 25, w(2.5), rj),
			Opts: map[string]int{"tieCap": 64}, opts: EyeJitterOptions{TIECap: 64}},
		ejVector{Name: "small-eye-ui16", SampleS: sampleS, Records: nrz(8, bits, 16, w(2), nil),
			Opts: map[string]int{"eyeW": 64, "eyeH": 32, "fftN": 256}, opts: EyeJitterOptions{EyeW: 64, EyeH: 32, FFTN: 256}})
	return vs
}

var (
	ejWebOnce sync.Once
	ejWebOut  []ejWebResult
	ejWebErr  error
	ejVecs    []ejVector
)

// ejWeb runs the vectors through eyejitter.js once per test binary.
// TRLC-LINKS: REQ-SDS-215
func ejWeb(t *testing.T) ([]ejVector, []ejWebResult) {
	t.Helper()
	testenv.NeedNode(t)
	ejWebOnce.Do(func() {
		ejVecs = ejVectors()
		node, _ := exec.LookPath("node")
		dir, err := os.MkdirTemp("", "ejparity")
		if err != nil {
			ejWebErr = err
			return
		}
		defer os.RemoveAll(dir)
		blob, err := json.Marshal(ejVecs)
		if err != nil {
			ejWebErr = err
			return
		}
		vf := filepath.Join(dir, "vectors.json")
		if ejWebErr = os.WriteFile(vf, blob, 0o644); ejWebErr != nil {
			return
		}
		cmd := exec.Command(node, filepath.Join("testdata", "ejparity.cjs"), vf, filepath.Join("..", "web"))
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			ejWebErr = err
			return
		}
		ejWebErr = json.Unmarshal(out, &ejWebOut)
	})
	if ejWebErr != nil {
		t.Fatalf("eyejitter.js run: %v", ejWebErr)
	}
	if len(ejWebOut) != len(ejVecs) {
		t.Fatalf("eyejitter.js answered %d of %d vectors", len(ejWebOut), len(ejVecs))
	}
	return ejVecs, ejWebOut
}

// sameF compares a Go figure with the JavaScript one within rel (relative to
// the larger) or abs; a nil JavaScript figure (undefined) wants a missing Go one.
// TRLC-LINKS: REQ-SDS-215
func sameF(t *testing.T, vec, name string, has bool, got float64, want *float64, rel, abs float64) {
	t.Helper()
	switch {
	case want == nil && !has:
		return
	case want == nil || !has:
		t.Errorf("%s: %s present in Go %v, in JS %v", vec, name, has, want != nil)
		return
	}
	d := math.Abs(got - *want)
	if d > abs && d > rel*math.Max(math.Abs(got), math.Abs(*want)) {
		t.Errorf("%s: %s Go %.17g JS %.17g", vec, name, got, *want)
	}
}

// TestEyeJitterJSParity runs the Go port and the web's eyejitter.js on the
// same vectors: every record's lock or rejection, the UI, the TIE statistics,
// RJ/DJ, period and cycle-to-cycle jitter, the TIE spectrum, the eye metrics
// and the eye density map must agree. Skips without node (fails under
// CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-215, REQ-SDS-199
func TestEyeJitterJSParity(t *testing.T) {
	vecs, web := ejWeb(t)
	const rel = 1e-12
	for i, v := range vecs {
		js := web[i]
		st := NewEyeJitter(v.opts)
		for k, rec := range v.Records {
			if got := st.Feed(rec, v.SampleS); got != js.Status[k] {
				t.Errorf("%s: record %d Go %q JS %q", v.Name, k, got, js.Status[k])
			}
		}
		r := st.Result()
		if r.Records != js.Records || r.Rejected != js.Rejected || r.Edges != js.Edges || r.LastErr != js.LastErr {
			t.Errorf("%s: counts Go %d/%d/%d %q JS %d/%d/%d %q", v.Name, r.Records, r.Rejected, r.Edges,
				r.LastErr, js.Records, js.Rejected, js.Edges, js.LastErr)
		}
		sameF(t, v.Name, "ui", true, r.UI, js.UI, rel, 0)
		sameF(t, v.Name, "uiSeconds", true, r.UISeconds, js.UISeconds, rel, 0)
		sameF(t, v.Name, "bitRate", true, r.BitRate, js.BitRate, rel, 0)
		sameF(t, v.Name, "tieHpHz", true, r.TIEHpHz, js.TIEHpHz, rel, 0)
		sameF(t, v.Name, "tieRms", r.HasTIE, r.TIERms, js.TIERms, rel, 0)
		sameF(t, v.Name, "tiePp", r.HasTIE, r.TIEPp, js.TIEPp, rel, 0)
		sameF(t, v.Name, "rj", r.HasRJDJ, r.RJ, js.RJ, rel, 0)
		sameF(t, v.Name, "dj", r.HasRJDJ, r.DJ, js.DJ, rel, 0)
		sameF(t, v.Name, "periodJRms", r.HasTIE, r.PeriodJRms, js.PeriodJRms, rel, 0)
		sameF(t, v.Name, "c2cJRms", r.HasTIE, r.C2CJRms, js.C2CJRms, rel, 0)
		sameF(t, v.Name, "tieWindow", r.HasTIE, float64(r.TIEWindow), js.TIEWindow, 0, 0)
		// the spectrum's Hann and FFT twiddles use cos/sin, whose last bit
		// may differ between Go and V8
		top := 0.0
		for _, m := range js.Spectrum {
			top = math.Max(top, m)
		}
		hasSpec := r.Spectrum != nil
		if hasSpec != (js.Spectrum != nil) || len(r.Spectrum) != len(js.Spectrum) {
			t.Errorf("%s: spectrum Go %d bins JS %d", v.Name, len(r.Spectrum), len(js.Spectrum))
		} else {
			for k := range r.Spectrum {
				if math.Abs(r.Spectrum[k]-js.Spectrum[k]) > 1e-9*top {
					t.Errorf("%s: spectrum[%d] Go %g JS %g", v.Name, k, r.Spectrum[k], js.Spectrum[k])
					break
				}
			}
		}
		sameF(t, v.Name, "specDf", hasSpec, r.SpecDf, js.SpecDf, rel, 0)
		sameF(t, v.Name, "specPeakHz", hasSpec, r.SpecPeakHz, js.SpecPeakHz, rel, 0)
		sameF(t, v.Name, "specPeakAmp", hasSpec, r.SpecPeakAmp, js.SpecPeakAmp, 1e-9, 0)
		if (r.Eye == nil) != (js.Eye == nil) {
			t.Errorf("%s: eye metrics Go %v JS %v", v.Name, r.Eye != nil, js.Eye != nil)
		} else if r.Eye != nil {
			sameF(t, v.Name, "eyeHeight", true, r.Eye.EyeHeightCodes, &js.Eye.Height, rel, 1e-12)
			sameF(t, v.Name, "eyeWidth", true, r.Eye.EyeWidthUI, &js.Eye.Width, rel, 0)
			sameF(t, v.Name, "crossTotal", true, r.Eye.CrossTotal, &js.Eye.Cross, 0, 0)
		}
		sum, weighted, cells := st.EyeDigest()
		if sum != js.Digest.Sum || weighted != js.Digest.Weighted || cells != js.Digest.Cells {
			t.Errorf("%s: eye map Go %v/%v/%d JS %v/%v/%d", v.Name, sum, weighted, cells,
				js.Digest.Sum, js.Digest.Weighted, js.Digest.Cells)
		}
		t.Logf("%-22s %d/%d locked  UI %.4g  TIE %s  eye %s", v.Name, r.Records, r.Records+r.Rejected, r.UI,
			fmtSeconds(r.HasTIE, r.TIERms), fmtEye(r.Eye))
	}
}

// TRLC-LINKS: REQ-SDS-215
func fmtSeconds(ok bool, v float64) string {
	if !ok {
		return "-"
	}
	return strconv.FormatFloat(v*1e12, 'g', 4, 64) + " ps rms"
}

// TRLC-LINKS: REQ-SDS-215
func fmtEye(m *EyeMetrics) string {
	if m == nil {
		return "-"
	}
	return strconv.FormatFloat(m.EyeHeightCodes, 'g', 4, 64) + " codes x " +
		strconv.FormatFloat(m.EyeWidthUI, 'g', 3, 64) + " UI"
}
