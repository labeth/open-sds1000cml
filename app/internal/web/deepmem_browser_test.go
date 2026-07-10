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

// TestDeepMemBrowser drives the real ui.html against a server serving a DEEP
// decimated frame (Valid=6144 > WinCols=2048, trigger edge mid-record). It
// checks that: the default window is the trigger-centered screen slice (edge at
// screen centre, like today), the navigator shows the whole record (window is a
// ~1/3 slice), wheel-zoom-out reaches the full record, dragging pans, and the
// deep record decodes/FFTs. Device-independent; skips without node/browser (fails under CI_REQUIRE_BROWSER=1).
func TestDeepMemBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const depth, winCols = 6144, 2048
	const edge = 2677.0 // mid-record trigger edge (as seen on the device)
	var seq atomic.Int64
	gen := func() *engine.Frame {
		n := seq.Add(1)
		c1 := make([]uint8, depth)
		c2 := make([]uint8, depth)
		// A 1 kHz-ish square on C1 across the whole deep record + a marker notch at
		// the trigger edge so the browser can verify centering.
		for i := 0; i < depth; i++ {
			if (i/64)%2 == 0 {
				c1[i] = 200
			} else {
				c1[i] = 56
			}
			c2[i] = uint8(128 + 40*math.Sin(2*math.Pi*float64(i)/128))
		}
		return &engine.Frame{
			C1: c1, C2: c2, Seq: uint64(n), Valid: depth, WinCols: winCols,
			EdgeX: edge, TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 500e-6 * 10 / winCols,
		}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.Command("node", "deepmem_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("deepmem_browser.mjs:\n%s", out)
	if strings.Contains(string(out), "SKIP:") {
		testenv.SkipBrowser(t, "browser driver skipped: %s", firstLine(out))
	}
	if err != nil {
		t.Fatalf("deep-memory browser e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("deep-memory browser e2e did not report ALL PASS")
	}
}
