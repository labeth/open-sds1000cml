// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import "github.com/labeth/open-sds1000cml/app/internal/board/acq"

// setCaps changes the loaded image's capabilities on e's board, as a probe
// of another image would.
// TRLC-LINKS: REQ-SDS-013
func setCaps(e *Engine, set func(c *acq.Caps)) {
	c := e.caps()
	set(&c)
	e.hw().SetImageCaps(c)
}
