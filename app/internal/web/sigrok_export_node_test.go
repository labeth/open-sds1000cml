package web

import (
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// TestSigrokExportJS runs the sigrok export encoders (sigrok_export.js) under
// node: CRC32 known-answer vectors, stored-ZIP structure via an independent
// central-directory extraction, srzip/VCD/WAV byte layouts against what
// libsigrok's readers parse, and the frame→series calibration contract shared
// with the CSV export. Skips if node is unavailable (a hard failure under
// CI_REQUIRE_BROWSER=1).
func TestSigrokExportJS(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")
	out, err := exec.Command(node, "sigrok_export.test.cjs").CombinedOutput()
	t.Logf("sigrok_export.test.cjs output:\n%s", out)
	if err != nil {
		t.Fatalf("sigrok_export.js test failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("sigrok_export.js test did not report ALL PASS")
	}
}
