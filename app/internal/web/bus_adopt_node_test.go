// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"os/exec"
	"testing"

	"open-sds/app/internal/testenv"
)

// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func TestPageFollowsTheScopesDecodeJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "bus_adopt.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("the page follows the scope's decode: %v\n%s", err, out)
	}
}
