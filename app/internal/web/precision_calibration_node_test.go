// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package web

import (
	"os/exec"
	"testing"

	"github.com/labeth/open-sds1000cml/app/internal/testenv"
)

// TRLC-LINKS: REQ-SDS-205
func TestPrecisionCaptureCalibrationJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "precision_calibration.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("precision calibration: %v\n%s", err, out)
	}
}
