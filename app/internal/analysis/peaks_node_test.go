// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"open-sds/app/internal/testenv"
)

// TestPeaksJSParity runs the web's peak search (peaks.js spectrum and
// detectPeaks) and SpectrumDB + Peaks on the same frames: the same peaks in
// the same order, at the same bins and frequencies, with the same bin levels.
// Skips without node (fails under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-216, REQ-SDS-070
func TestPeaksJSParity(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")
	type vec struct {
		Name    string    `json:"name"`
		Samples []float64 `json:"samples"`
		Nyq     float64   `json:"nyq"`
		Max     int       `json:"max"`
	}
	var vs []vec
	add := func(name string, s []float64, maxPeaks int) {
		n := 1
		for n*2 <= len(s) {
			n <<= 1
		}
		vs = append(vs, vec{Name: name, Samples: s, Nyq: 0.5 / peakSampleS, Max: maxPeaks})
	}
	add("two tones on bins", tones(1024, []comp{{52, 40, 0}, {123, 30, 0}}, 0.58, 1, true), 8)
	add("two tones off bins", tones(1024, []comp{{52.3, 40, 0.2}, {123.7, 30, 1}}, 0.58, 2, true), 8)
	add("square", tones(4096, squareComps(31.7, 80, 41), 0.5, 3, true), 8)
	add("square, 16 peaks", tones(4096, squareComps(31.7, 80, 41), 0.5, 3, true), 16)
	add("square, 3 peaks", tones(4096, squareComps(31.7, 80, 41), 0.5, 3, true), 3)
	add("triangle", tones(2048, triangleComps(23.4, 90, 31), 0.3, 4, true), 8)
	add("noise only", tones(2048, nil, 3, 5, true), 8)
	add("flat", make([]float64, 512), 8)
	add("3000 samples", tones(3000, []comp{{17.2, 60, 0}, {200.9, 10, 0}}, 0.5, 6, true), 8)
	add("16 samples", tones(20, []comp{{3, 60, 0}}, 0, 0, false), 8)
	add("too short", tones(10, []comp{{3, 60, 0}}, 0, 0, false), 8)
	add("Q8.8 codes", func() []float64 {
		s := tones(2048, []comp{{77.7, 50, 0}, {155.4, 5, 0}}, 0.5, 8, true)
		for i := range s {
			s[i] += float64(i%7) / 256
		}
		return s
	}(), 8)

	dir := t.TempDir()
	blob, err := json.Marshal(vs)
	if err != nil {
		t.Fatal(err)
	}
	vf := filepath.Join(dir, "vectors.json")
	if err := os.WriteFile(vf, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, filepath.Join("testdata", "peaksparity.cjs"), vf, filepath.Join("..", "web"))
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("peaks.js run: %v", err)
	}
	var web []struct {
		Name  string `json:"name"`
		Half  int    `json:"half"`
		Peaks []struct {
			K    int     `json:"k"`
			Freq float64 `json:"freq"`
			DB   float64 `json:"db"`
		} `json:"peaks"`
	}
	if err := json.Unmarshal(out, &web); err != nil {
		t.Fatal(err)
	}
	if len(web) != len(vs) {
		t.Fatalf("peaks.js answered %d of %d vectors", len(web), len(vs))
	}
	for i, v := range vs {
		js := web[i]
		db := SpectrumDB(v.Samples)
		if len(db) != js.Half {
			t.Errorf("%s: %d bins, JS %d", v.Name, len(db), js.Half)
			continue
		}
		binHz := 0.0
		if len(db) > 0 {
			binHz = v.Nyq / float64(len(db))
		}
		ps := Peaks(db, binHz, v.Max)
		if len(ps) != len(js.Peaks) {
			t.Errorf("%s: %d peaks, JS %d", v.Name, len(ps), len(js.Peaks))
			continue
		}
		for k, p := range ps {
			w := js.Peaks[k]
			if p.Bin != w.K || math.Abs(p.Freq-w.Freq) > 1e-6*binHz || math.Abs(db[p.Bin]-w.DB) > 1e-9 {
				t.Errorf("%s: peak %d Go bin %d %.9g Hz %.9g dB, JS bin %d %.9g Hz %.9g dB",
					v.Name, k, p.Bin, p.Freq, db[p.Bin], w.K, w.Freq, w.DB)
			}
		}
		t.Logf("%-20s %d peaks", v.Name, len(ps))
	}
}
