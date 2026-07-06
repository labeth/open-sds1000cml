package web

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// TestStackPerfBrowser guards the superres-view redraw hot path: with a large
// multi-tone frame, many selected FFT peaks, and the residual on, a COLD redraw
// fits every tone while WARM redraws must hit the component + math memoization.
// stackperf_browser.mjs asserts warm ≫ cheaper than cold (the ratio collapses to
// ~1 if the memoization regresses, e.g. recomputing every fit per pan/zoom). The
// fakeScope here only needs to serve a basic frame so the page loads — the mjs
// installs its own synthetic stack in-page.
func TestStackPerfBrowser(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	const N = 2048
	n := uint64(0)
	gen := func() *engine.Frame {
		n++
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := 0; i < N; i++ {
			c1[i] = uint8(128 + 50*math.Sin(2*math.Pi*float64(i)/256))
			c2[i] = uint8(128 + 30*math.Sin(2*math.Pi*float64(i)/512))
		}
		return &engine.Frame{
			C1: c1, C2: c2, Seq: n, Valid: N, WinCols: N, EdgeX: N / 2,
			TdivS: 5e-9, DisplayedS: 5e-9, SampleS: 2e-9, Trigd: true,
		}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5, BandKind: "native-fast"}}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.Command("node", "stackperf_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("stackperf_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		t.Skip("browser unavailable")
	}
	if err != nil {
		t.Fatalf("stack perf e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("stack perf e2e did not pass")
	}
}
