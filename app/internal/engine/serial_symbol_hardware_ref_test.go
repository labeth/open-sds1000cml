// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"github.com/labeth/open-sds1000cml/app/internal/decode"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// symbolCfg is the Go model's form of a planned symbol trigger.
// TRLC-LINKS: REQ-SDS-013
func symbolCfg(c sramcapture.SymbolTriggerConfig) decode.SymbolCfg {
	d := decode.SymbolCfg{
		InvA: c.InvA, InvB: c.InvB, AndAB: c.AndAB, ClockFall: c.ClockFall, Mode: int(c.Mode), LeadLen: int(c.LeadLen),
		Prescale: int(c.Prescale), UnitQ8: c.UnitQ8, FrameUnits: int(c.FrameUnits), Period: int(c.Period), Phase0: int(c.Phase0),
		K: int(c.K), Pattern: c.Pattern, Mask: c.Mask, Table: c.Table,
	}
	return d
}
