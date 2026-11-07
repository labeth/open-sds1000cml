// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// averageCfg is the part of a capture's configuration an average's block
// keeps to: the same settings' captures, not the record's split around the
// trigger. A serial trigger moves the split by each match's measured shift;
// keyed on it, the block would restart on almost every capture and Average
// never get past a few - a master on a serial trigger would never finish. The
// block aligns each capture on its edge anyway.
// TRLC-LINKS: REQ-SDS-012
func averageCfg(c sramcapture.Config) sramcapture.Config {
	c.PreWords, c.PostWords = 0, 0
	return c
}
