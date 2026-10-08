// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"open-sds/app/internal/sramcapture"
)

// TRLC-LINKS: REQ-SDS-013
func planHardwareSPI(p SerialParams) sramcapture.SPITriggerConfig {
	if p.Proto != serSPI || !p.HaveThr || p.SPIClockHz <= 0 || p.ChA < 0 || p.ChA > 1 || p.ChB != 1-p.ChA || len(p.Bytes) > 4 || math.IsNaN(p.Threshold) || math.IsInf(p.Threshold, 0) || p.Threshold < 0 || p.Threshold > 255 {
		return sramcapture.SPITriggerConfig{}
	}
	ticks := math.Round(1.5 * 125e6 / float64(p.SPIClockHz))
	if ticks < 9 || ticks > 0xffffff {
		return sramcapture.SPITriggerConfig{}
	}
	c := sramcapture.SPITriggerConfig{Enabled: true, ClockChannel: uint8(p.ChA), Inverted: p.Inverted, CPOL: p.CPOL, CPHA: p.CPHA, MSB: p.MSB, GapTicks: uint32(ticks), Length: uint8(len(p.Bytes))}
	for _, b := range p.Bytes {
		if b < 0 || b > 255 {
			return sramcapture.SPITriggerConfig{}
		}
		c.Pattern = c.Pattern<<8 | uint32(b)
	}
	return c
}
