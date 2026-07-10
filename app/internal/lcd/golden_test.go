package lcd

import (
	"bytes"
	"flag"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Golden-image harness for the lcd package: render a view into a MemSurface,
// PNG-encode it (the same EncodePNG the /api/screen.png endpoint uses) and
// compare PIXELS against a checked-in golden under testdata/golden/. Because
// the renderer is pure Go over a fixed bitmap font, renders are deterministic;
// comparison is still done on decoded pixels (never PNG bytes, which may vary
// across Go versions) with a small differing-pixel allowance for cross-arch
// float rounding at trace edges.
//
// Regenerating goldens after an INTENDED render change (one-liner; commit the
// PNGs with the change that caused them):
//
//	go test ./internal/lcd -run Golden -update-golden
var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/golden/*.png from the current render")

// goldenMaxDiffPixels: pixels allowed to differ before the comparison fails.
// Benign cross-platform float rounding moves a trace edge by ≤ a pixel at a
// handful of columns; a real regression (colour, layout, missing element)
// flips hundreds to thousands.
const goldenMaxDiffPixels = 64

func goldenAssert(t *testing.T, name string, sf *MemSurface) {
	t.Helper()
	got := EncodePNG(sf)
	path := filepath.Join("testdata", "golden", name+".png")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden rewritten: %s", path)
		return
	}
	wantBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (%v) — generate it with:\n  go test ./internal/lcd -run Golden -update-golden", path, err)
	}
	want, err := png.Decode(bytes.NewReader(wantBytes))
	if err != nil {
		t.Fatalf("corrupt golden %s: %v", path, err)
	}
	gotImg, err := png.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("EncodePNG output undecodable: %v", err)
	}
	if want.Bounds() != gotImg.Bounds() {
		t.Fatalf("golden %s bounds %v != render bounds %v", name, want.Bounds(), gotImg.Bounds())
	}
	diff := 0
	firstX, firstY := -1, -1
	b := want.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			wr, wg, wb, _ := want.At(x, y).RGBA()
			gr, gg, gb, _ := gotImg.At(x, y).RGBA()
			if wr != gr || wg != gg || wb != gb {
				if diff == 0 {
					firstX, firstY = x, y
				}
				diff++
			}
		}
	}
	if diff > goldenMaxDiffPixels {
		rej := filepath.Join(t.TempDir(), name+".got.png")
		if err := os.WriteFile(rej, got, 0o644); err == nil {
			t.Logf("rendered image saved to %s", rej)
		}
		t.Fatalf("golden %s: %d pixels differ (allowance %d), first at (%d,%d).\nIf the render change is intended, regenerate with:\n  go test ./internal/lcd -run Golden -update-golden",
			name, diff, goldenMaxDiffPixels, firstX, firstY)
	}
	if diff > 0 {
		t.Logf("golden %s: %d pixels differ (within the %d-pixel allowance)", name, diff, goldenMaxDiffPixels)
	}
}

// goldenSuperresHUD is the fixed synthetic dataset behind the super-res review
// golden: a harmonic-rich C1 stack (square-ish: 5 MHz + 3rd/5th harmonics) on
// a ×16 fine grid with a gap band, a C2 tone, review focus, and the Task-2
// device URL on the top bar — all deterministic.
func goldenSuperresHUD() HUD {
	const K, nb = 16, 4096
	const sampleS = 2e-9
	fineDt := sampleS / K
	h := srHUD(nb, K, sampleS,
		func(b int) float64 {
			ts := float64(b) * fineDt
			v := 128.0
			for _, m := range []float64{1, 3, 5} {
				v += (55 / m) * math.Sin(2*math.Pi*5e6*m*ts)
			}
			return v
		},
		func(b int) float64 { return 128 + 70*math.Sin(2*math.Pi*2e6*float64(b)*fineDt) })
	for b := 1500; b < 1650; b++ {
		h.SRMean[b] = -1 // a gap band: the review must lift the pen, not bridge
	}
	h.SRBits = 3.2
	h.SRStatus = "done: 512 stacked"
	h.URL = "http://192.168.1.42:8080"
	return h
}

// TestGoldenSuperresReview pins the whole super-res review Y-T screen —
// graticule, stacked trace with its gap, SR status line, top-bar HUD with the
// device URL — against testdata/golden/superres_review_yt.png.
func TestGoldenSuperresReview(t *testing.T) {
	h := goldenSuperresHUD()
	h.ViewMode = 0
	sf := NewMemSurface()
	Render(sf, nil, h, true) // review renders from the HUD stack; no live frame
	goldenAssert(t, "superres_review_yt", sf)
}

// TestGoldenSuperresReviewFFT pins the review's stacked-FFT screen (both
// channels + the fine-Nyquist axis label) — a second consumer proving the
// harness generalises across views.
func TestGoldenSuperresReviewFFT(t *testing.T) {
	h := goldenSuperresHUD()
	h.ViewMode = 2
	// Frequency-zoom onto the low band (the tones live at 2–25 MHz; the fine
	// grid's Nyquist is 4 GHz) so the golden pins real spectral content.
	h.Zoom, h.ZoomOff = 64, -0.5
	sf := NewMemSurface()
	Render(sf, nil, h, true)
	goldenAssert(t, "superres_review_fft", sf)
}
