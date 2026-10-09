// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-204
func TestARINCAutoParityErrorsJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "arinc_auto.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("ARINC auto: %v\n%s", err, out)
	}
}
