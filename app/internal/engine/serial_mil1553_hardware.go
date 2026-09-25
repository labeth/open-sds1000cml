// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"open-sds/app/internal/sramcapture"
)

// Each repository MIL sync-delimited packet contains one word. A multiword
// pattern cannot match that packet contract and is not sent to this matcher.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func planHardwareMIL1553(p SerialParams) sramcapture.MIL1553TriggerConfig {
	if p.Proto != serMIL1553 || !p.HaveThr || p.ChA < 0 || p.ChA > 1 || len(p.Bytes) > 1 || p.Baud <= 0 || math.IsNaN(p.Threshold) || math.IsInf(p.Threshold, 0) || p.Threshold < 0 || p.Threshold > 255 {
		return sramcapture.MIL1553TriggerConfig{}
	}
	ticks := math.Round(125e6 / float64(p.Baud))
	if ticks < 8 || ticks > 0x3fffff {
		return sramcapture.MIL1553TriggerConfig{}
	}
	c := sramcapture.MIL1553TriggerConfig{Enabled: true, Channel: uint8(p.ChA), Inverted: p.Inverted, BitTicks: uint32(ticks), MatchAny: len(p.Bytes) == 0}
	if len(p.Bytes) == 1 {
		if p.Bytes[0] < 0 || p.Bytes[0] > 65535 {
			return sramcapture.MIL1553TriggerConfig{}
		}
		c.Pattern = uint16(p.Bytes[0])
	}
	return c
}
