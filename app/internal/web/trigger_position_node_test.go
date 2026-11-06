// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package web

import (
	"os/exec"
	"testing"

	"github.com/labeth/open-dso-core/testenv"
)

// TRLC-LINKS: REQ-SDS-204
func TestTriggerPositionEndpointsJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "trigger_position.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("trigger position: %v\n%s", err, out)
	}
}
