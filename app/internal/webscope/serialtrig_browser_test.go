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

// TestSerialTrigBrowser drives serialtrig_browser.mjs against a live server:
// the serial-trigger panel reveals per-protocol config rows and ARM pushes the
// config + arms the engine over the real API. Skips without node/Playwright (fails under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-180
func TestSerialTrigBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const N = 2048
	var seq uint64
	gen := func() *sample.Frame {
		seq++
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := range N {
			c1[i] = uint8(128 + 60*math.Sin(2*math.Pi*8*float64(i)/N))
			c2[i] = uint8(128 + 40*math.Sin(2*math.Pi*5*float64(i)/N))
		}
		return &sample.Frame{
			Seq:       seq,
			WinPoints: N,
			TriggerAt: N / 2,
			SampleS:   800e-9,
			Snapshot:  sample.Settings{TdivS: 500e-6, DisplayedS: 500e-6},
			Traces:    []sample.Trace{{Codes8: c1, Valid: N}, {Ch: 1, Codes8: c2, Valid: N}},
		}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	srv := httptest.NewServer(newServer(fs, nil, newDecodePanel(fs, nil), nil).Handler())
	defer srv.Close()

	out, err := exec.CommandContext(t.Context(), "node", "serialtrig_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("serialtrig_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		testenv.SkipBrowser(t, "browser unavailable")
	}
	if err != nil {
		t.Fatalf("serial-trigger e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("serial-trigger e2e did not pass")
	}
}
