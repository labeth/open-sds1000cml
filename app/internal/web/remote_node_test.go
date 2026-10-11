// ENGMODEL-OWNER-UNIT: FU-WEB-REMOTE
package web

import (
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// TestRemoteCoreJS runs the phone remote's pure logic (remote_core.js) under
// node: numbers as the scope writes them, the value a detent reaches, typed
// entry and match-byte hints, search with everyday words, the change
// history's diff, merge and undo targets, and the knob-job line. Skips without
// node (a hard failure under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-220
func TestRemoteCoreJS(t *testing.T) {
	testenv.NeedNode(t)
	out, err := exec.Command("node", "remote.test.cjs").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("remote.test.cjs: %v\n%s", err, out)
	}
}
