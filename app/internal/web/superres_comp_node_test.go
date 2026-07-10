package web

import (
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// TestSuperresCompJS runs the analog-falloff compensation math
// (superres_comp.js) under node: FFT/IFFT round-trip, the filter figures
// (recovered -3 dB, bounded boost), restoration of an attenuated multi-tone
// toward flat, DC/offset preservation, gap-sentinel preservation and the
// non-power-of-two resample path. Skips if node is absent.
func TestSuperresCompJS(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")
	out, err := exec.Command(node, "superres_comp.test.cjs").CombinedOutput()
	t.Logf("superres_comp.test.cjs output:\n%s", out)
	if err != nil {
		t.Fatalf("superres_comp.js test failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("superres_comp.js test did not report ALL PASS")
	}
}
