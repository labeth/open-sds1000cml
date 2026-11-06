// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package web

import (
	"os/exec"
	"testing"

	"github.com/labeth/open-dso-core/testenv"
)

// TRLC-LINKS: REQ-SDS-201
func TestEyeOptimizeSRAMJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "eye_optimize.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("eye optimization: %v\n%s", err, out)
	}
}
