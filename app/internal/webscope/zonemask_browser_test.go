// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"math"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/testenv"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// TestZoneMaskBrowser drives zonemask_browser.mjs: real-browser coverage of
// the zone/mask card — armed zone drawing on the scope canvas (edge-anchored
// transform), the zone-trigger toggle, the raw-frame mask build + upload, the
// AC-coupling guard, and the failure gallery (click -> frozen failing frame
// with the violation marked). Server-side effects are asserted here after the
// browser run. Skips when node/Playwright is absent (fails under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-180
func TestZoneMaskBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const N = 2048
	var n atomic.Uint64
	gen := func() *sample.Frame {
		seq := n.Add(1)
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
			SourcePtp: 120,
			Snapshot:  sample.Settings{TdivS: 500e-6, DisplayedS: 500e-6},
			Status:    sample.Status{Triggered: true, Coherent: true},
			Traces:    []sample.Trace{{Codes8: c1, Valid: N}, {Ch: 1, Codes8: c2, Valid: N}},
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
	srv := httptest.NewServer(newServer(fs, nil, nil, nil).Handler())
	defer srv.Close()

	out, err := exec.CommandContext(t.Context(), "node", "zonemask_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("zonemask_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		testenv.SkipBrowser(t, "browser unavailable")
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
	if fs.lastMask == nil || fs.lastMask.WinCols != N {
		t.Fatalf("mask upload missing: %+v", fs.lastMask)
	}
	for j := range N {
		if fs.lastMask.Lo[j] > fs.lastMask.Hi[j] {
			t.Fatalf("mask bounds inverted at col %d: [%d,%d]", j, fs.lastMask.Lo[j], fs.lastMask.Hi[j])
		}
	}
	if fs.mask != nil {
		t.Fatal("the mask cleared elsewhere was installed again")
	}
}
