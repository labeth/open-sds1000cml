// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
)

// spiPhase16 is how far after the clock line's crossing the receiver on the
// samples takes the data line when the clock is on C2 (as far before it on
// C1), in sixteenths of a nanosecond: the channels' sampling phase, so the
// data is taken at the crossing itself, the middle of a bit whose data
// changes on the other edge.
const spiPhase16 = decode.ChannelPhase16

// planHardwareSPI plans the image's SPI receiver: on the samples (sample) to
// a 125 MHz clock, on the 125 MHz line levels to about 20 MHz.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func planHardwareSPI(p SerialParams, sample bool) sramcapture.SPITriggerConfig {
	if p.Proto != serSPI || !p.HaveThr || p.SPIClockHz <= 0 || p.ChA < 0 || p.ChA > 1 || p.ChB != 1-p.ChA || len(p.Bytes) > 4 || math.IsNaN(p.Threshold) || math.IsInf(p.Threshold, 0) || p.Threshold < 0 || p.Threshold > 255 {
		return sramcapture.SPITriggerConfig{}
	}
	rate := float64(p.SPIClockHz)
	ticks := math.Round(1.5 * 125e6 / rate)
	if sample {
		// One sampling edge a 125 MHz receiver clock.
		if rate > 125e6 {
			return sramcapture.SPITriggerConfig{}
		}
		ticks = float64(decode.SampleSPIGap(rate))
	} else if ticks < 9 {
		return sramcapture.SPITriggerConfig{}
	}
	if ticks > 0xffffff {
		return sramcapture.SPITriggerConfig{}
	}
	c := sramcapture.SPITriggerConfig{Enabled: true, ClockChannel: uint8(p.ChA), Inverted: p.Inverted, CPOL: p.CPOL, CPHA: p.CPHA, MSB: p.MSB, GapTicks: uint32(ticks), Length: uint8(len(p.Bytes))}
	if sample {
		c.Delay = spiPhase16
		if p.ChA == 0 {
			c.Delay = -spiPhase16
		}
	}
	for _, b := range p.Bytes {
		if b < 0 || b > 255 {
			return sramcapture.SPITriggerConfig{}
		}
		c.Pattern = c.Pattern<<8 | uint32(b)
	}
	return c
}
