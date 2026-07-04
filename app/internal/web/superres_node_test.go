package web

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSuperresJS runs the super-resolution stacker math (superres.js) under
// node against synthetic jittered/noisy/glitchy frames with known ground
// truth: sub-sample alignment accuracy, peak-locking uniformity, sqrt(N)
// noise reduction, lucky-frame rejection, drift normalization and the
// sum-of-sinusoids model fit. Skips if node is absent.
func TestSuperresJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; skipping superres.js test")
	}
	out, err := exec.Command(node, "superres.test.cjs").CombinedOutput()
	t.Logf("superres.test.cjs output:\n%s", out)
	if err != nil {
		t.Fatalf("superres.js test failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("superres.js test did not report ALL PASS")
	}
}
