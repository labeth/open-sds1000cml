// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"math"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// liveSRAMWindow selects a contiguous preview anchored at the trigger position. At timebases
// needing more samples, retain the whole visible span instead of aliasing it.
// TRLC-LINKS: REQ-SDS-010
func liveSRAMWindow(m sramcapture.Metadata, screen int, posFrac float64) (uint32, uint32) {
	return acq.LiveSRAMWindowPre(m, screen, posFrac, 0)
}

// softAnchor bounds a software qualifier to the anchors the display can
// centre a full screen on: posFrac of the screen before, the rest after. It
// prefers the event nearest the hardware edge. A window no larger than the
// screen leaves only the hardware edge's own neighbourhood.
// TRLC-LINKS: REQ-SDS-011
func softAnchor(valid, screen int, posFrac, hwEdge float64) core.AnchorSpan {
	if !(posFrac >= 0 && posFrac <= 1) {
		posFrac = .5
	}
	lo := int(math.Ceil(float64(screen) * posFrac))
	hi := valid - int(math.Ceil(float64(screen)*(1-posFrac)))
	target := int(hwEdge)
	if hwEdge < 0 || hwEdge >= float64(valid) {
		target = (lo + hi) / 2
	}
	if lo > hi { // the window cannot hold a screen: anywhere, as before
		return core.AnchorSpan{Target: target, Lo: 0, Hi: valid}
	}
	return core.AnchorSpan{Target: target, Lo: lo, Hi: hi}
}

// offRecord is how many of a screen's winCols columns, centred on anchor at
// posFrac, fall outside a record of valid samples; zero when the record
// cannot hold a screen at all (then nothing better exists).
// TRLC-LINKS: REQ-SDS-013
func offRecord(valid, winCols int, posFrac float64, anchor int) int {
	if winCols <= 0 || winCols > valid || anchor < 0 {
		return 0
	}
	if !(posFrac >= 0 && posFrac <= 1) {
		posFrac = .5
	}
	left := anchor - int(math.Round(posFrac*float64(winCols)))
	right := left + winCols - valid
	return max(0, -left) + max(0, right)
}
