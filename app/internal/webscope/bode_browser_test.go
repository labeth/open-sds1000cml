// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/testenv"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// TestBodeBrowser drives the real web BODE/FRA card in headless Chromium: ARM
// posts bodemode, the render path fetches /api/bode (synthetic points) and
// draws, the enlarge opens, CLEAR runs — no page errors. Exercises the
// app_views.js bode wiring this refactor relocated. Skips without node/browser (fails under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-125
func TestBodeBrowser(t *testing.T) {
	testenv.NeedNode(t)
	var seq atomic.Int64
	gen := func() *sample.Frame {
		n := seq.Add(1)
		const N = 2048
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := range N {
			c1[i] = uint8(128 + 80*math.Sin(2*math.Pi*20*float64(i)/N))
			c2[i] = uint8(128 + 40*math.Sin(2*math.Pi*20*float64(i)/N))
		}
		return &sample.Frame{
			Seq:       uint64(n),
			WinPoints: N,
			TriggerAt: -1,
			SampleS:   800e-9,
			Snapshot:  sample.Settings{TdivS: 500e-6, DisplayedS: 500e-6},
			Status:    sample.Status{Triggered: true},
			Traces:    []sample.Trace{{Codes8: c1, Valid: N}, {Ch: 1, Codes8: c2, Valid: N}},
		}
	}
	fs := &fakeScope{
		frameGen: gen,
		// the engine's channels: the reference on C1, the DUT on C2
		stats: engine.Stats{Running: true, TrigPosFrac: 0.5, BodeDut: 1},
		bodePts: []engine.BodePoint{
			{FreqHz: 1e6, GainDB: -6, PhaseDeg: -45},
			{FreqHz: 2e6, GainDB: -12, PhaseDeg: -90},
		},
	}
	srv := httptest.NewServer(newServer(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.CommandContext(t.Context(), "node", "bode_browser.mjs", srv.URL).CombinedOutput()
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
