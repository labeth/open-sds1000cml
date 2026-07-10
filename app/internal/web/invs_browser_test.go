package web

import (
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/testenv"
)

// TestINVSBrowser drives invs_browser.mjs over the REAL ui.html + WebGL draw
// path: the display-level INVS flags (st.inv1/st.inv2, fed by the SCPI shadow
// via /api/status in production) must mirror the rendered trace about the
// display centre, per channel, leaving the other channel untouched. The
// served frame is deliberately asymmetric: C1 flat at code 168 (above
// centre), C2 flat at code 88 (below). Self-skips when node/Playwright is
// absent (hard failure on the CI browser lane). Served: C1 flat at code 178
// (above centre), C2 flat at code 88 (below).
func TestINVSBrowser(t *testing.T) {
	testenv.NeedNode(t)
	// Flat codes chosen so no upright row collides with any inverted row
	// (178↔77, 88↔167 — every pair ≥39 px apart on screen): the per-colour
	// classifier then sees each trace in isolation.
	const N = 2048
	c1 := make([]uint8, N)
	c2 := make([]uint8, N)
	for i := 0; i < N; i++ {
		c1[i], c2[i] = 178, 88
	}
	gen := func() *engine.Frame {
		return &engine.Frame{
			C1: c1, C2: c2, Seq: 7, Valid: N, WinCols: N, EdgeX: -1, // flat → centre
			TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9,
		}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.Command("node", "invs_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("invs_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		testenv.SkipBrowser(t, "browser unavailable")
	}
	if err != nil {
		t.Fatalf("INVS e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("INVS e2e did not pass")
	}
}
