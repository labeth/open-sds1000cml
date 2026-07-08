package web

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// TestSerialTrigBrowser drives serialtrig_browser.mjs against a live server:
// the serial-trigger panel reveals per-protocol config rows and ARM pushes the
// config + arms the engine over the real API. Self-skips without node/Playwright.
func TestSerialTrigBrowser(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	const N = 2048
	var seq uint64
	gen := func() *engine.Frame {
		seq++
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := 0; i < N; i++ {
			c1[i] = uint8(128 + 60*math.Sin(2*math.Pi*8*float64(i)/N))
			c2[i] = uint8(128 + 40*math.Sin(2*math.Pi*5*float64(i)/N))
		}
		return &engine.Frame{C1: c1, C2: c2, Seq: seq, Valid: N, WinCols: N, EdgeX: N / 2,
			TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.Command("node", "serialtrig_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("serialtrig_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		t.Skip("browser unavailable")
	}
	if err != nil {
		t.Fatalf("serial-trigger e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("serial-trigger e2e did not pass")
	}
}
