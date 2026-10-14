// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// TestPeaksJS runs the browser FFT peak-detection/selection logic (peaks.js)
// under node against synthetic frames — the closest thing to an end-to-end test
// of the client code without a headless browser. Skips if node is unavailable
// (a hard failure under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-070
func TestPeaksJS(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")
	out, err := exec.Command(node, "peaks.test.cjs").CombinedOutput()
	t.Logf("peaks.test.cjs output:\n%s", out)
	if err != nil {
		t.Fatalf("peaks.js e2e test failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("peaks.js e2e test did not report ALL PASS")
	}
}

// TestSpectrumLevelJS: a peak's dBV level (app_fft.js) comes from the
// transform's points when a decimated capture's band keeps fewer bins.
// TRLC-LINKS: REQ-SDS-216
func TestSpectrumLevelJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "spectrum_level.test.cjs").CombinedOutput(); err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("spectrum level: %v\n%s", err, out)
	}
}

// TestPickToggleJS: a click picks the peak, or unpicks the pick holding it,
// never a lost pick beside it (app_fft.js togglePeakCh).
// TRLC-LINKS: REQ-SDS-070
func TestPickToggleJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "pick_toggle.test.cjs").CombinedOutput(); err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("pick toggle: %v\n%s", err, out)
	}
}
