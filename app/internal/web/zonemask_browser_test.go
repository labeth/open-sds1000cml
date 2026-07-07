package web

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// TestZoneMaskBrowser drives zonemask_browser.mjs: real-browser coverage of
// the zone/mask card — armed zone drawing on the scope canvas (edge-anchored
// transform), the zone-trigger toggle, the raw-frame mask build + upload, the
// AC-coupling guard, and the failure gallery (click -> frozen failing frame
// with the violation marked). Server-side effects are asserted here after the
// browser run. Self-skips when node/Playwright is absent.
func TestZoneMaskBrowser(t *testing.T) {
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
			c1[i] = uint8(128 + 60*math.Sin(2*math.Pi*8*float64(i)/N))
			c2[i] = uint8(128 + 40*math.Sin(2*math.Pi*5*float64(i)/N))
		}
		return &engine.Frame{
			C1: c1, C2: c2, Seq: n, Valid: N, WinCols: N, EdgeX: N / 2,
			TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9, Trigd: true, Coherent: true, Ptp: 120,
		}
	}
	ringC1 := make([]uint8, N)
	for i := range ringC1 {
		ringC1[i] = 100
	}
	ringC1[900] = 250 // the captured violation
	fs := &fakeScope{
		frameGen: gen,
		stats: engine.Stats{
			Running: true, TrigPosFrac: 0.5, WinCols: N,
			MaskMode: 1, MaskPass: 10, MaskFail: 1, MaskRing: 1,
		},
		maskRing: []engine.MaskFail{{
			C1: ringC1, Valid: N, Seq: 77, EdgeX: N / 2, SampleS: 800e-9,
			WinCols: N, FailCol: 900 - N/2 + N/2, FailCode: 250, FailSample: 900,
		}},
	}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.Command("node", "zonemask_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("zonemask_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		t.Skip("browser unavailable")
	}
	if err != nil {
		t.Fatalf("zonemask e2e failed: %v", err)
	}
	// server-side effects of the browser actions
	if len(fs.zones) != 1 {
		t.Fatalf("zone POST: got %d zones", len(fs.zones))
	}
	z := fs.zones[0]
	if z.DtLoS <= 0 || z.DtHiS <= z.DtLoS || z.CodeHi <= z.CodeLo {
		t.Fatalf("zone geometry wrong: %+v", z)
	}
	if fs.zoneMode != 1 {
		t.Fatalf("zone trig toggle: zoneMode=%d", fs.zoneMode)
	}
	if fs.mask == nil || fs.mask.WinCols != N {
		t.Fatalf("mask upload missing: %+v", fs.mask)
	}
	for j := 0; j < N; j++ {
		if fs.mask.Lo[j] > fs.mask.Hi[j] {
			t.Fatalf("mask bounds inverted at col %d: [%d,%d]", j, fs.mask.Lo[j], fs.mask.Hi[j])
		}
	}
}
