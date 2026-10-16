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
	if math.IsNaN(p.SPIDelayNs) || math.IsInf(p.SPIDelayNs, 0) {
		return sramcapture.SPITriggerConfig{}
	}
	if !sample && math.Abs(p.SPIDelayNs)+8 >= 0.5e9/rate {
		// the level receiver reads the data at the edge to a tick: a delay
		// the eye does not hold around that needs the receiver on the samples
		return sramcapture.SPITriggerConfig{}
	}
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
		c.Delay = int8(spiDelay16(p.SPIDelayNs, p.ChA))
	}
	for _, b := range p.Bytes {
		if b < 0 || b > 255 {
			return sramcapture.SPITriggerConfig{}
		}
		c.Pattern = c.Pattern<<8 | uint32(b)
	}
	return c
}

// spiDelay16 is the receiver on the samples' data delay register for a
// delay of ns after the sampling edge with the clock on clockCh: the
// channels' phase and the delay, in sixteenths of a nanosecond, within the
// register's -128..127.
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func spiDelay16(ns float64, clockCh int) int {
	phase := spiPhase16
	if clockCh == 0 {
		phase = -spiPhase16
	}
	return max(-128, min(127, phase+int(math.Round(ns*16))))
}

// SPIDelayFPGA is the delay after the sampling edge the receiver on the
// samples reads SPI's data at for a delay of ns asked, with the clock on
// clockCh: its register holds -8 to 8 ns with the channels' phase.
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func SPIDelayFPGA(ns float64, clockCh int) float64 {
	phase := spiPhase16
	if clockCh == 0 {
		phase = -spiPhase16
	}
	return float64(spiDelay16(ns, clockCh)-phase) / 16
}
