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

// TestSpectrogramBrowser drives the real web SPECTROGRAM card in headless
// Chromium against a synthetic live signal, asserting the app_views.js wiring
// (arm → accumulate rows → paint → enlarge → clear) works end to end with no
// page errors. Skips without node or a Playwright browser (fails under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-068
func TestSpectrogramBrowser(t *testing.T) {
	testenv.NeedNode(t)
	var seq atomic.Int64
	gen := func() *sample.Frame {
		n := seq.Add(1)
		const N = 2048
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := range N {
			v := 128 + 90*math.Sin(2*math.Pi*40*float64(i)/N) // a clean tone
			c1[i] = uint8(v)
			c2[i] = uint8(v)
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
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	srv := httptest.NewServer(newServer(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.CommandContext(t.Context(), "node", "spectrogram_browser.mjs", srv.URL).CombinedOutput()
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
