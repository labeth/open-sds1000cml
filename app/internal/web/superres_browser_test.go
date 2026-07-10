package web

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/testenv"
)

// TestSuperresBrowser drives superres_browser.mjs against a fakeScope whose
// generator emits a noisy sine with random sub-sample trigger jitter — the
// exact signal class the stacker exists for. Covers arm → accumulate →
// stats → review (frozen synthetic frame) → model fit → resume live.
func TestSuperresBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const N = 2048
	n := uint64(0)
	rng := uint64(0x5eed)
	rnd := func() float64 { // xorshift — deterministic jitter/noise
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		return float64(rng%1e6) / 1e6
	}
	gen := func() *engine.Frame {
		n++
		jitter := (rnd() - 0.5) * 6 // ±3 samples of trigger phase
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := 0; i < N; i++ {
			v := 128 + 55*math.Sin(2*math.Pi*(float64(i)-jitter)/256) + 2.5*(rnd()-0.5)*2
			if v < 0 {
				v = 0
			} else if v > 255 {
				v = 255
			}
			c1[i] = uint8(v)
			c2[i] = uint8(128 + 30*math.Sin(2*math.Pi*float64(i)/512))
		}
		return &engine.Frame{
			C1: c1, C2: c2, Seq: n, Valid: N, WinCols: N, EdgeX: N / 2,
			TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9, Trigd: true,
		}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5, BandKind: "native-fast"}}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.Command("node", "superres_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("superres_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		testenv.SkipBrowser(t, "browser unavailable")
	}
	if err != nil {
		t.Fatalf("superres e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("superres e2e did not pass")
	}
}
