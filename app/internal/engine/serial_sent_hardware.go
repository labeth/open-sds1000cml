// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"open-sds/app/internal/sramcapture"
)

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func planHardwareSENT(p SerialParams) sramcapture.SENTTriggerConfig {
	if p.Proto != serSENT || !p.HaveThr || p.ChA < 0 || p.ChA > 1 || len(p.Bytes) > 4 || math.IsNaN(p.Threshold) || math.IsInf(p.Threshold, 0) || p.Threshold < 0 || p.Threshold > 255 || math.IsNaN(p.TickNs) || math.IsInf(p.TickNs, 0) {
		return sramcapture.SENTTriggerConfig{}
	}
	ticks := math.Round(p.TickNs / 8)
	n := p.Nibbles
	if n == 0 {
		n = 8
	}
	if ticks < 4 || ticks > 0xffffff || n < 1 || n > 64 {
		return sramcapture.SENTTriggerConfig{}
	}
	c := sramcapture.SENTTriggerConfig{Enabled: true, Channel: uint8(p.ChA), Inverted: p.Inverted, TickTicks: uint32(ticks), Nibbles: uint8(n), Length: uint8(len(p.Bytes))}
	for _, b := range p.Bytes {
		if b < 0 || b > 15 {
			return sramcapture.SENTTriggerConfig{}
		}
		c.Pattern = c.Pattern<<8 | uint32(b)
	}
	return c
}
