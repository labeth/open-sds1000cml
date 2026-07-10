package web

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/testenv"
)

// TestAcceptanceBrowser drives the shared page-object acceptance suite
// (acceptance_browser.mjs) over the REAL ui.html against httptest+fakeScope,
// covering the paths not owned by the decode/fft/deepmem drivers: boot/liveness,
// acquire, trigger, vertical/horizontal, cursors, view modes + panel visibility,
// and export. Skips (via the driver) without node/Playwright (fails under CI_REQUIRE_BROWSER=1).
func TestAcceptanceBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const N = 2048
	c1 := make([]uint8, N)
	c2 := make([]uint8, N)
	for i := 0; i < N; i++ {
		c1[i] = uint8(128 + 40*math.Sin(2*math.Pi*8*float64(i)/N))
		c2[i] = uint8(128 + 30*math.Sin(2*math.Pi*13*float64(i)/N))
	}
	gen := func() *engine.Frame {
		return &engine.Frame{
			C1: c1, C2: c2, Seq: 7, Valid: N, WinCols: N, EdgeX: N / 2,
			TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9,
		}
	}
	// TrigCode 31434 ≈ 0 V (the level marker lands mid-screen so it's draggable).
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5, TrigCode: 31434}}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.Command("node", "acceptance_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("acceptance_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		testenv.SkipBrowser(t, "browser unavailable")
	}
	if err != nil {
		t.Fatalf("acceptance e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("acceptance e2e did not pass")
	}
}
