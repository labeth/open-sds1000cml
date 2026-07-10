package web

import (
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// TestEyejitterJS runs the eye-diagram/jitter engine (eyejitter.js) under node
// against synthetic PRBS7 records with GROUND-TRUTH jitter: exact UI recovery,
// TIE noise floor, injected square-wave jitter (rms, dual-Dirac DJ, spectral
// peak frequency AND fundamental amplitude vs the analytic (4/π)·A value),
// plus negative controls (sine must not fabricate DJ; noise must not lock;
// a mid-run bit-rate change is rejected). Skips if node is absent.
func TestEyejitterJS(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")
	out, err := exec.Command(node, "eyejitter.test.cjs").CombinedOutput()
	t.Logf("eyejitter.test.cjs output:\n%s", out)
	if err != nil {
		t.Fatalf("eyejitter.js test failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("eyejitter.js test did not report ALL PASS")
	}
}
