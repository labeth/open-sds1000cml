// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// The SOUND readout boxes' steady figures (sound_steady.test.cjs).
// TRLC-LINKS: REQ-SDS-270
func TestSoundSteadyJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "sound_steady.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("sound steady: %v\n%s", err, out)
	}
}
