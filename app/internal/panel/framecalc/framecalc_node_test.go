// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package framecalc

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/testenv"
)

// TestSpectrumJSParity runs the LCD's spectrum (Spectrum) and the web's
// (peaks.js fftReduce) on the same records: the same points, block means,
// window and span, a decimated capture's ended at its filter band, so the two
// surfaces draw and list the same spectrum (no stride sized from another frame,
// nothing drawn past the filter). Skips without
// node (fails under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-070, REQ-SDS-216
func TestSpectrumJSParity(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")
	type vec struct {
		Name    string    `json:"name"`
		Samples []float64 `json:"samples"`
		Nyq     float64   `json:"nyq"`
		Band    float64   `json:"band"`
		MaxN    int       `json:"maxN"`
		f       *engine.Frame
	}
	square := func(n int, period, phase float64) []uint8 {
		s := make([]uint8, n)
		for i := range s {
			s[i] = 60
			if math.Mod(float64(i)+phase, period) < period/2 {
				s[i] = 196
			}
			s[i] += uint8(i * 7 % 5) // a little noise
		}
		return s
	}
	var vs []vec
	add := func(name string, c []uint8, sampleS, band float64) {
		f := &engine.Frame{C1: c, C2: make([]uint8, len(c)), Valid: len(c), SampleS: sampleS, BandwidthHz: band, Seq: uint64(len(vs) + 1)}
		g := make([]float64, len(c))
		for i, v := range c {
			g[i] = float64(v)
		}
		vs = append(vs, vec{Name: name, Samples: g, Nyq: 0.5 / sampleS, Band: band, MaxN: MaxFFT, f: f})
	}
	add("5us/div full rate (25000)", square(25000, 500, 3.3), 2e-9, 0)
	add("1us/div full rate (5000)", square(5000, 500, 7.1), 2e-9, 0)
	add("decimated 32 ns, filtered", square(1691, 31.25, 1.7), 32e-9, 0.1346/32e-9)
	add("long record (100000)", square(100000, 2000, 11), 2e-9, 0)
	add("band above Nyquist", square(4096, 64, 0), 32e-9, 1e9)
	add("too short", square(10, 4, 0), 2e-9, 0)

	dir := t.TempDir()
	blob, err := json.Marshal(vs)
	if err != nil {
		t.Fatal(err)
	}
	vf := filepath.Join(dir, "vectors.json")
	if err := os.WriteFile(vf, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, filepath.Join("testdata", "spectrumparity.cjs"), vf, filepath.Join("..", "..", "web"))
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("peaks.js run: %v", err)
	}
	var web []struct {
		Name   string    `json:"name"`
		Half   int       `json:"half"`
		Nyq    float64   `json:"nyq"`
		Fs     float64   `json:"fs"`
		Points int       `json:"points"`
		Peak   float64   `json:"peak"`
		Mags   []float64 `json:"mags"`
	}
	if err := json.Unmarshal(out, &web); err != nil {
		t.Fatal(err)
	}
	if len(web) != len(vs) {
		t.Fatalf("peaks.js answered %d of %d records", len(web), len(vs))
	}
	for i, v := range vs {
		js := web[i]
		sp, ok := Spectrum(v.f, 0, v.f.SampleS)
		if !ok {
			if js.Half != 0 {
				t.Errorf("%s: no LCD spectrum, the web has %d bins", v.Name, js.Half)
			}
			continue
		}
		rel := func(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(math.Abs(a), math.Abs(b)) }
		if len(sp.Mags) != js.Half || sp.N != js.Points || !rel(sp.SpanHz, js.Nyq) || !rel(2*sp.EffNyq, js.Fs) || !rel(sp.Peak, js.Peak) {
			t.Errorf("%s: LCD %d bins of %d, span %g, fs %g, peak %g; web %d of %d, %g, %g, %g", v.Name,
				len(sp.Mags), sp.N, sp.SpanHz, 2*sp.EffNyq, sp.Peak, js.Half, js.Points, js.Nyq, js.Fs, js.Peak)
			continue
		}
		for k, m := range sp.Mags {
			if math.Abs(m-js.Mags[k]) > 1e-9*math.Max(sp.Peak, 1) {
				t.Errorf("%s: bin %d LCD %g web %g", v.Name, k, m, js.Mags[k])
				break
			}
		}
		t.Logf("%-28s %5d bins of %5d, span %.4g Hz", v.Name, len(sp.Mags), sp.N/2, sp.SpanHz)
	}
}
