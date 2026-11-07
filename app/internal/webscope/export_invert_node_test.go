// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"os/exec"
	"testing"

	"github.com/labeth/open-dso-core/testenv"
)

// The file exports follow the display invert; SCPI WF? keeps the acquired
// volts (export_invert.test.cjs).
// TRLC-LINKS: REQ-SDS-069
func TestExportFollowsTheDisplayInvertJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "export_invert.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("export invert: %v\n%s", err, out)
	}
}
