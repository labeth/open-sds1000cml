// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-204
func TestTriggerPositionEndpointsJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "trigger_position.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("trigger position: %v\n%s", err, out)
	}
}
