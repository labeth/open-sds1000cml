// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"os/exec"
	"testing"

	"github.com/labeth/open-dso-core/testenv"
)

// TRLC-LINKS: REQ-SDS-204
func TestRunToggleAfterSingleJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "run_toggle.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("run toggle: %v\n%s", err, out)
	}
}
