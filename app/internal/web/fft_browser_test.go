package web

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"open-sds/app/internal/engine"
)

// TestFFTBrowser drives the real UI in headless Chromium (via Playwright) against
// a local server that serves a synthetic two-tone frame whose peak magnitudes
// swap dominance every poll. That reproduces the conditions of the reported FFT
// bugs — unreliable/jumping selection, inability to re-select, and the peak list
// lingering after a mode switch — and asserts they stay fixed. It is fully
// device-independent. Skips (never fails) when node or a Playwright browser is
// not available, so `go test ./...` stays green on machines without them.
func TestFFTBrowser(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}

	var seq atomic.Int64
	clamp8 := func(v float64) uint8 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v + 0.5)
	}
	gen := func() *engine.Frame {
		n := seq.Add(1)
		phase := float64(n) * 0.6
		// Both tones stay strong (28..52) so neither peak ever disappears; only
		// which one is STRONGER swaps — that's what reshuffles the ranking.
		ampA := 40 + 12*math.Sin(phase) // low tone
		ampB := 40 - 12*math.Sin(phase) // high tone, anti-correlated
		const N = 2048
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := 0; i < N; i++ {
			c1[i] = clamp8(128 +
				ampA*math.Sin(2*math.Pi*24*float64(i)/N) +
				ampB*math.Sin(2*math.Pi*61*float64(i)/N))
			// C2 carries DIFFERENT tones (37, 50 cycles) so the per-channel FFT
			// boxes have distinct peaks and independent selection.
			c2[i] = clamp8(128 +
				ampB*math.Sin(2*math.Pi*37*float64(i)/N) +
				ampA*math.Sin(2*math.Pi*50*float64(i)/N))
		}
		return &engine.Frame{
			C1: c1, C2: c2, Seq: uint64(n), Valid: N, WinCols: N,
			EdgeX: -1, TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9,
		}
	}

	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	// The driver self-skips (prints SKIP, exits 0) when the browser is absent.
	out, err := exec.Command("node", "fft_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("fft_browser.mjs:\n%s", out)
	if strings.Contains(string(out), "SKIP:") {
		t.Skipf("browser driver skipped: %s", firstLine(out))
	}
	if err != nil {
		t.Fatalf("browser e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("browser e2e did not report ALL PASS")
	}
}

func firstLine(b []byte) string {
	if i := strings.IndexByte(string(b), '\n'); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}
