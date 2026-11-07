// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/testenv"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// TestAcceptanceBrowser drives the shared page-object acceptance suite
// (acceptance_browser.mjs) over the REAL ui.html against httptest+fakeScope,
// covering the paths not owned by the decode/fft/deepmem drivers: boot/liveness,
// acquire, trigger, vertical/horizontal, cursors, view modes + panel visibility,
// and export. Skips (via the driver) without node/Playwright (fails under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-180
func TestAcceptanceBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const N = 2048
	c1 := make([]uint8, N)
	c2 := make([]uint8, N)
	for i := range N {
		c1[i] = uint8(128 + 40*math.Sin(2*math.Pi*8*float64(i)/N))
		c2[i] = uint8(128 + 30*math.Sin(2*math.Pi*13*float64(i)/N))
	}
	gen := func() *sample.Frame {
		return &sample.Frame{
			Seq:       7,
			WinPoints: N,
			TriggerAt: N / 2,
			SampleS:   800e-9,
			Snapshot:  sample.Settings{TdivS: 500e-6, DisplayedS: 500e-6},
			Traces:    []sample.Trace{{Codes8: c1, Valid: N}, {Ch: 1, Codes8: c2, Valid: N}},
		}
	}
	// TrigCode 31434 ≈ 0 V (the level marker lands mid-screen so it's draggable).
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5, TrigCode: 31434}}
	srv := httptest.NewServer(newServer(fs, nil, &refsOnlyPanel{frames: fs.WithFrame}, nil).Handler())
	defer srv.Close()

	out, err := exec.CommandContext(t.Context(), "node", "acceptance_browser.mjs", srv.URL).CombinedOutput()
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
