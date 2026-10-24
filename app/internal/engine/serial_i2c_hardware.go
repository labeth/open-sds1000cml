// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"open-sds/app/internal/sramcapture"
)

// planHardwareI2C plans the image's I2C receiver: on the 125 MHz line
// levels, or on the samples (sample), SDA read at SCL's interpolated
// crossing plus the channels' sampling phase.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func planHardwareI2C(p *SerialParams, sample bool) sramcapture.I2CTriggerConfig {
	if p.Proto != serI2C || !p.HaveThr || math.IsNaN(p.Threshold) || math.IsInf(p.Threshold, 0) || p.Threshold < 0 || p.Threshold > 255 ||
		p.ChA < 0 || p.ChA > 1 || p.ChB < 0 || p.ChB > 1 || p.ChA == p.ChB || p.Addr < -1 || p.Addr > 127 || p.RW < 0 || p.RW > 2 || len(p.Bytes) > 4 {
		return sramcapture.I2CTriggerConfig{}
	}
	c := sramcapture.I2CTriggerConfig{Enabled: true, ClockChannel: uint8(p.ChA), Inverted: p.Inverted, Address: p.Addr, Direction: uint8(p.RW), Length: uint8(len(p.Bytes))}
	if sample {
		c.Samples, c.Delay = true, spiPhase16
		if p.ChA == 0 {
			c.Delay = -spiPhase16
		}
	}
	for _, b := range p.Bytes {
		if b < 0 || b > 255 {
			return sramcapture.I2CTriggerConfig{}
		}
		c.Pattern = c.Pattern<<8 | uint32(b)
	}
	return c
}
