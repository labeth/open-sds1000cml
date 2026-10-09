// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-203
func TestWideFormatJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "wide_format.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("wide format: %v\n%s", err, out)
	}
}
