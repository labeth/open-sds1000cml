// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-205
func TestPrecisionCaptureCalibrationJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "precision_calibration.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("precision calibration: %v\n%s", err, out)
	}
}
