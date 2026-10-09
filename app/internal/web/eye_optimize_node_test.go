// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-201
func TestEyeOptimizeSRAMJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "eye_optimize.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("eye optimization: %v\n%s", err, out)
	}
}
