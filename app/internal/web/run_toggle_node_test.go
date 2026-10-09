// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-204
func TestRunToggleAfterSingleJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "run_toggle.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("run toggle: %v\n%s", err, out)
	}
}
