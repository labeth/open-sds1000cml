// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"os/exec"
	"testing"

	"open-sds/app/internal/testenv"
)

// The Listen player plays through replies that now and then come 160 ms
// late (the scope's core busy with the LCD's SOUND view): its jitter
// buffer's target grows before the buffer runs dry, where a fixed 120 ms
// ran dry at every late reply.
// TRLC-LINKS: REQ-SDS-265
func TestSoundPlayerRidesLateReplies(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "sound_worklet.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("sound player: %v\n%s", err, out)
	}
}
