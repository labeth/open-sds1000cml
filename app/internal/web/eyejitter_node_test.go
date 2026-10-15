// ENGMODEL-OWNER-UNIT: FU-APP-WEB
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
// a mid-run bit-rate change is rejected). Skips if node is absent (fails under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-181
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

// TestEyeFullRateJS runs eye_fullrate.test.cjs: the web eye folds full-rate raw
// records only - the live envelope, a decimated record and an averaged one fed
// between them leave the eye as it is, and it says when it holds.
// TRLC-LINKS: REQ-SDS-215
func TestEyeFullRateJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "eye_fullrate.test.cjs").CombinedOutput(); err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("eye full-rate: %v\n%s", err, out)
	}
}
