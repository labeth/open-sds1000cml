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

// TestBodeBrowser drives the real web BODE/FRA card in headless Chromium: ARM
// posts bodemode, the render path fetches /api/bode (synthetic points) and
// draws, the enlarge opens, CLEAR runs — no page errors. Exercises the
// app_views.js bode wiring this refactor relocated. Skips without node/browser.
func TestBodeBrowser(t *testing.T) {
	testenv.NeedNode(t)
	var seq atomic.Int64
	gen := func() *engine.Frame {
		n := seq.Add(1)
		const N = 2048
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := 0; i < N; i++ {
			c1[i] = uint8(128 + 80*math.Sin(2*math.Pi*20*float64(i)/N))
			c2[i] = uint8(128 + 40*math.Sin(2*math.Pi*20*float64(i)/N))
		}
		return &engine.Frame{C1: c1, C2: c2, Seq: uint64(n), Valid: N, WinCols: N,
			EdgeX: -1, TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9, Trigd: true}
	}
	fs := &fakeScope{
		frameGen: gen,
		stats:    engine.Stats{Running: true, TrigPosFrac: 0.5},
		bodePts: []engine.BodePoint{
			{FreqHz: 1e6, GainDB: -6, PhaseDeg: -45},
			{FreqHz: 2e6, GainDB: -12, PhaseDeg: -90},
		},
	}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.Command("node", "bode_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("bode_browser.mjs:\n%s", out)
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
