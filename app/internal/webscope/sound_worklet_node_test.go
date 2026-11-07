// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"os/exec"
	"testing"

	"github.com/labeth/open-dso-core/testenv"
)

// The Listen player plays through replies that now and then come 160 ms
// late (the scope's core busy with the LCD's SOUND view): its jitter
// buffer's target grows before the buffer runs dry, where a fixed 120 ms
// ran dry at every late reply.
// TRLC-LINKS: REQ-SDS-265
func TestSoundPlayerRidesLateReplies(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "sound_worklet.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("sound player: %v\n%s", err, out)
	}
}
