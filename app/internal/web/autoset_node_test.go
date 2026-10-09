// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-204
func TestAutosetCommandOrderingJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "autoset.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("autoset ordering: %v\n%s", err, out)
	}
}
