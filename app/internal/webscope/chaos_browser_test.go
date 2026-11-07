// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/testenv"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// TestChaosBrowser drives chaos_browser.mjs: a seeded monkey pokes every
// interactive control and gesture path in a real browser; any pageerror is a
// reachable broken UI state. Three seeds per run keep it cheap but varied.
// Skips when node/Playwright is absent (fails under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-180
func TestChaosBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const N = 2048
	n := uint64(0)
	gen := func() *sample.Frame {
		n++
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := range N {
			c1[i] = uint8(128 + 60*math.Sin(2*math.Pi*8*float64(i)/N+float64(n)*0.05))
			c2[i] = uint8(128 + 40*math.Sin(2*math.Pi*3*float64(i)/N))
		}
		return &sample.Frame{
			Seq:       n,
			WinPoints: N,
			TriggerAt: N / 2,
			SampleS:   800e-9,
			SourcePtp: 120,
			Snapshot:  sample.Settings{TdivS: 500e-6, DisplayedS: 500e-6},
			Status:    sample.Status{Triggered: true, Coherent: true},
			Traces:    []sample.Trace{{Codes8: c1, Valid: N}, {Ch: 1, Codes8: c2, Valid: N}},
		}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5, WinCols: N}}
	srv := httptest.NewServer(newServer(fs, nil, nil, nil).Handler())
	defer srv.Close()

	for _, seed := range []int{1337, 99, 20260707} {
		out, err := exec.CommandContext(t.Context(), "node", "chaos_browser.mjs", srv.URL, strconv.Itoa(seed), "400").CombinedOutput()
		t.Logf("chaos seed %d:\n%s", seed, out)
		if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
			testenv.SkipBrowser(t, "browser unavailable")
		}
		if err != nil {
			t.Fatalf("chaos seed %d failed: %v", seed, err)
		}
	}
}
