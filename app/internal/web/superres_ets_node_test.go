package web

import (
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// TestSuperresEtsJS runs the phase-coherent equivalent-time reconstruction
// (superres_ets.js) under node: synthetic FREE-RUN frames of a near-Nyquist
// clock at random phase + noise must recover the frequency, reconstruct the
// period at the correct amplitude, gain measured ENOB, keep a square's
// harmonics, and reject a pure-noise frame. Skips if node is absent.
func TestSuperresEtsJS(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")
	out, err := exec.Command(node, "superres_ets.test.cjs").CombinedOutput()
	t.Logf("superres_ets.test.cjs output:\n%s", out)
	if err != nil {
		t.Fatalf("superres_ets.js test failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("superres_ets.js test did not report ALL PASS")
	}
}
