// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
)

// planHardwareUART plans the image's UART receiver: on the 125 MHz line
// levels, 8N1 within generalUARTReach; or on the samples (sample), 5 to 9
// data bits with no, even or odd parity, a bit of 16 ns (62.5 MBd) to about
// a millisecond, every edge in a frame re-timing the bits after it.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func planHardwareUART(p SerialParams, sample bool) sramcapture.UARTTriggerConfig {
	// Hardware must not silently substitute the edge-trigger level for the
	// decoder's automatic threshold. Auto slicing remains a software request.
	if !p.HaveThr || p.Proto != serUART || p.Baud <= 0 || len(p.Bytes) > 4 || p.ChA < 0 || p.ChA > 1 ||
		math.IsNaN(p.Threshold) || math.IsInf(p.Threshold, 0) || p.Threshold < 0 || p.Threshold > 255 {
		return sramcapture.UARTTriggerConfig{}
	}
	bits := p.Bits
	if bits == 0 {
		bits = 8
	}
	parity := map[string]uint8{"": 0, "none": 0, "even": 1, "odd": 2}
	par, ok := parity[p.Parity]
	if !ok {
		return sramcapture.UARTTriggerConfig{}
	}
	c := sramcapture.UARTTriggerConfig{Enabled: true, Channel: uint8(p.ChA), Inverted: p.Inverted, Length: uint8(len(p.Bytes))}
	if sample {
		per := decode.UARTPeriod16(float64(p.Baud))
		if per < decode.SampleUARTMinPeriod16 && 50*per >= 49*decode.SampleUARTMinPeriod16 {
			// a rate measured up to 2 % past 62.5 MBd: the receiver re-times
			// on every edge
			per = decode.SampleUARTMinPeriod16
		}
		if per < decode.SampleUARTMinPeriod16 || per > 0xffffff || bits < 5 || bits > 9 {
			return sramcapture.UARTTriggerConfig{}
		}
		c.Period16, c.Bits, c.Parity, c.Resync = uint32(per), uint8(bits), par, true
	} else {
		if bits != 8 || par != 0 || !generalUARTReach(float64(p.Baud), 10) {
			return sramcapture.UARTTriggerConfig{}
		}
		c.BitTicks = uint32(math.Round(125e6 / float64(p.Baud)))
	}
	for _, b := range p.Bytes {
		if b < 0 || b > 255 || b >= 1<<bits {
			return sramcapture.UARTTriggerConfig{}
		}
		c.Pattern = c.Pattern<<8 | uint32(b)
	}
	return c
}

// generalUARTReach reports whether the general image's UART receiver reads
// a frame of frameBits at baud from a transmitter within 2 % of it: the
// receiver counts whole 125 MHz ticks a bit from a start edge seen to a
// tick and never re-times, so its sampling point drifts by the bit's
// rounding and the transmitter's error over the frame. The drift to the
// stop bit's centre, and a tick each for the start edge and the line's
// slicing, must stay inside half a bit: 8N1 to 5 MBd. (On the bench's exact
// rates it read 10 and 25 MBd, which 2 % breaks; 50 MBd, 4 ticks for 2.5,
// not at all.)
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func generalUARTReach(baud, frameBits float64) bool {
	if !(baud > 0) {
		return false
	}
	bit := 125e6 / baud
	ticks := math.Round(bit)
	return ticks >= 4 && ticks <= 0xffffff && (frameBits-0.5)*(math.Abs(ticks-bit)+0.02*bit)+2 <= bit/2
}

// sampleUARTMatchLagS is how long after the matched frame's stop-bit
// centre the FPGA's trigger lands with the UART receiver on the samples:
// the receiver's latency (decode.SampleUARTLatency receiver clocks of 8 ns)
// and the trigger path's eight acquisition words of 4 ns.
const sampleUARTMatchLagS = (decode.SampleUARTLatency*8 + 8*4) * 1e-9

// generalI2CReachHz is the fastest I2C clock the general image's receiver
// takes: it samples SCL and SDA as 125 MHz levels, 8 ns apart, which holds
// fast-mode plus (1 MHz, data set up 50 ns before SCL rises) but not
// high-speed mode's 10 ns; a measured clock past it (with margin for the
// measurement) runs on the fast image's receiver on the samples.
const generalI2CReachHz = 1_500_000
