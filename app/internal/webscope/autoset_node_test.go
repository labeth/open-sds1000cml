// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"os/exec"
	"testing"

	"github.com/labeth/open-dso-core/testenv"
)

// TRLC-LINKS: REQ-SDS-204
func TestAutosetCommandOrderingJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "autoset.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("autoset ordering: %v\n%s", err, out)
	}
}
