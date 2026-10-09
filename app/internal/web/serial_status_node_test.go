// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-203
func TestSerialStatusJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "serial_status.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("serial status: %v\n%s", err, out)
	}
}
