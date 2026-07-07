package web

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// TestChaosBrowser drives chaos_browser.mjs: a seeded monkey pokes every
// interactive control and gesture path in a real browser; any pageerror is a
// reachable broken UI state. Three seeds per run keep it cheap but varied.
// Self-skips when node/Playwright is absent.
func TestChaosBrowser(t *testing.T) {
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
			c1[i] = uint8(128 + 60*math.Sin(2*math.Pi*8*float64(i)/N+float64(n)*0.05))
			c2[i] = uint8(128 + 40*math.Sin(2*math.Pi*3*float64(i)/N))
		}
		return &engine.Frame{
			C1: c1, C2: c2, Seq: n, Valid: N, WinCols: N, EdgeX: N / 2,
			TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9, Trigd: true, Coherent: true, Ptp: 120,
		}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5, WinCols: N}}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	for _, seed := range []int{1337, 99, 20260707} {
		out, err := exec.Command("node", "chaos_browser.mjs", srv.URL, strconv.Itoa(seed), "400").CombinedOutput()
		t.Logf("chaos seed %d:\n%s", seed, out)
		if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
			t.Skip("browser unavailable")
		}
		if err != nil {
			t.Fatalf("chaos seed %d failed: %v", seed, err)
		}
	}
}
