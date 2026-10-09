// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-011, REQ-SDS-072
func TestQualifierControlSyncJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "qualifier_sync.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("qualifier controls: %v\n%s", err, out)
	}
}
