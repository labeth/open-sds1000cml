// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-203
func TestMathCalibrationJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "math_calibration.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("math calibration: %v\n%s", err, out)
	}
}
