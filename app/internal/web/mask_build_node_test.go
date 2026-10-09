// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-014
func TestMaskBuildJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "mask_build.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("mask build: %v\n%s", err, out)
	}
}
