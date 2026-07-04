package web

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// TestTransportBrowser drives transport_browser.mjs: asserts the binary
// long-poll is the ACTIVE transport in a real browser (so a silent fallback
// can never fake-pass the other suites), that a dead /api/frame.bin degrades
// to the JSON poll with frames still advancing, and that ?transport=json
// forces the legacy path. Self-skips when node/Playwright is absent.
func TestTransportBrowser(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	const N = 2048
	n := uint64(0)
	gen := func() *engine.Frame {
		n++
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := 0; i < N; i++ {
			c1[i] = uint8(128 + 60*math.Sin(2*math.Pi*(8*float64(i)/N+float64(n)*0.1)))
			c2[i] = uint8(128 + 40*math.Sin(2*math.Pi*5*float64(i)/N))
		}
		return &engine.Frame{
			C1: c1, C2: c2, Seq: n, Valid: N, WinCols: N, EdgeX: N / 2,
			TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9,
		}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.Command("node", "transport_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("transport_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		t.Skip("browser unavailable")
	}
	if err != nil {
		t.Fatalf("transport e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("transport e2e did not pass")
	}
}
