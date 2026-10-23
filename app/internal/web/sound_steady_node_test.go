// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"os/exec"
	"testing"

	"open-sds/app/internal/testenv"
)

// The SOUND readout boxes' steady figures (sound_steady.test.cjs).
// TRLC-LINKS: REQ-SDS-270
func TestSoundSteadyJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "sound_steady.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("sound steady: %v\n%s", err, out)
	}
}
