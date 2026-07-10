package web

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/testenv"
)

// TestSpectrogramBrowser drives the real web SPECTROGRAM card in headless
// Chromium against a synthetic live signal, asserting the app_views.js wiring
// (arm → accumulate rows → paint → enlarge → clear) works end to end with no
// page errors. Skips (never fails) without node or a Playwright browser.
func TestSpectrogramBrowser(t *testing.T) {
	testenv.NeedNode(t)
	var seq atomic.Int64
	gen := func() *engine.Frame {
		n := seq.Add(1)
		const N = 2048
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := 0; i < N; i++ {
			v := 128 + 90*math.Sin(2*math.Pi*40*float64(i)/N) // a clean tone
			c1[i] = uint8(v)
			c2[i] = uint8(v)
		}
		return &engine.Frame{
			C1: c1, C2: c2, Seq: uint64(n), Valid: N, WinCols: N,
			EdgeX: -1, TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9, Trigd: true,
		}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.Command("node", "spectrogram_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("spectrogram_browser.mjs:\n%s", out)
	if strings.Contains(string(out), "SKIP:") {
		testenv.SkipBrowser(t, "browser driver skipped: %s", firstLine(out))
	}
	if err != nil {
		t.Fatalf("browser e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("browser e2e did not report ALL PASS")
	}
}
