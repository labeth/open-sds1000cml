package web

import (
	"os/exec"
	"strings"
	"testing"
)

// TestPeaksJS runs the browser FFT peak-detection/selection logic (peaks.js)
// under node against synthetic frames — the closest thing to an end-to-end test
// of the client code without a headless browser. Skips if node is unavailable
// (e.g. CI images without it) so `go test ./...` stays green everywhere.
func TestPeaksJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; skipping peaks.js e2e test")
	}
	out, err := exec.Command(node, "peaks.test.cjs").CombinedOutput()
	t.Logf("peaks.test.cjs output:\n%s", out)
	if err != nil {
		t.Fatalf("peaks.js e2e test failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("peaks.js e2e test did not report ALL PASS")
	}
}
