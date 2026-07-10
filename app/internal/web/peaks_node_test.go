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
