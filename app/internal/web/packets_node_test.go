// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// TestPacketsJS runs BUS's packet grouping and still-standing packet list
// (packets.js) under node on records decoded by the real UART, I2C and SPI
// decoders. Skips without node (a hard failure under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-214
func TestPacketsJS(t *testing.T) {
	testenv.NeedNode(t)
	out, err := exec.Command("node", "packets.test.cjs").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("packets.test.cjs: %v\n%s", err, out)
	}
}

// TestWorkspaceMathJS runs the workspaces' statistics, reference difference,
// master repeat gate and mask dilation (wsmath.js) under node.
// TRLC-LINKS: REQ-SDS-217, REQ-SDS-218
func TestWorkspaceMathJS(t *testing.T) {
	testenv.NeedNode(t)
	out, err := exec.Command("node", "wsmath.test.cjs").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("wsmath.test.cjs: %v\n%s", err, out)
	}
}
