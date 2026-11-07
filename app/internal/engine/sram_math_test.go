// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"fmt"
	"testing"

	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// An average's block keeps to the same settings' captures, not to the record's
// split around the trigger, which a serial trigger moves by each match's
// measured shift (keyed on the split, the block restarts on almost every
// capture).
// TRLC-LINKS: REQ-SDS-012
func TestAverageBlockIgnoresTheTriggerSplit(t *testing.T) {
	c := sramcapture.Config{DecimationLog2: 4, PreWords: 7259, PostWords: 7258, TriggerLevel: 80}
	moved := c
	moved.PreWords, moved.PostWords = 7260, 7257
	if fmt.Sprint(averageCfg(c)) != fmt.Sprint(averageCfg(moved)) {
		t.Fatal("a moved trigger split started a new block")
	}
	level := c
	level.TriggerLevel = 90
	if fmt.Sprint(averageCfg(c)) == fmt.Sprint(averageCfg(level)) {
		t.Fatal("another trigger level kept the block")
	}
}
