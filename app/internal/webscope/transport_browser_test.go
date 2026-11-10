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
	engine "github.com/labeth/open-sds1000cml/app/internal/scopeapi"
)

// TestTransportBrowser drives transport_browser.mjs: asserts the binary
// long-poll is the ONE transport delivering Int16Array frames + header
// measurements in a real browser, and that a dead /api/frame.bin STOPS frames
// (no silent fallback that could fake-pass the other suites) yet self-heals on
// retry when the endpoint returns. Skips when node/Playwright is absent (fails under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-023, REQ-SDS-180
func TestTransportBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const N = 2048
	n := uint64(0)
	gen := func() *sample.Frame {
		n++
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := range N {
			c1[i] = uint8(128 + 60*math.Sin(2*math.Pi*(8*float64(i)/N+float64(n)*0.1)))
			c2[i] = uint8(128 + 40*math.Sin(2*math.Pi*5*float64(i)/N))
		}
		return &sample.Frame{
			Seq:       n,
			WinPoints: N,
			TriggerAt: N / 2,
			SampleS:   800e-9,
			Snapshot:  sample.Settings{TdivS: 500e-6, DisplayedS: 500e-6},
			Traces:    []sample.Trace{{Codes8: c1, Valid: N}, {Ch: 1, Codes8: c2, Valid: N}},
		}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	srv := httptest.NewServer(newServer(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.CommandContext(t.Context(), "node", "transport_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("transport_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		testenv.SkipBrowser(t, "browser unavailable")
	}
	if err != nil {
		t.Fatalf("transport e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("transport e2e did not pass")
	}
}
