// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package web

import (
	"os/exec"
	"testing"

	"github.com/labeth/open-sds1000cml/app/internal/testenv"
)

// TRLC-LINKS: REQ-SDS-203
func TestMathCalibrationJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "math_calibration.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("math calibration: %v\n%s", err, out)
	}
}
