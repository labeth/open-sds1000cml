// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package web

import (
	"os/exec"
	"testing"

	"github.com/labeth/open-dso-core/testenv"
)

// TRLC-LINKS: REQ-SDS-014
func TestMaskBuildJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "mask_build.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("mask build: %v\n%s", err, out)
	}
}
