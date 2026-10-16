// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "fmt"

// SPITriggerConfig selects two-wire, eight-bit SPI with explicit idle framing.
// GapTicks is the maximum interval between sampling edges at 125 MHz; use
// 1.5 clock periods. No chip-select input is inferred from the two analog inputs.
// Delay is where a receiver on the samples (capability 0x5303, spi_sample.v)
// takes the data line after the clock line's crossing, in sixteenths of a
// nanosecond on the clock line's sample grid: the clock channel's sampling
// phase less the data channel's, plus any offset into the bit.
// TRLC-LINKS: REQ-SDS-013
type SPITriggerConfig struct {
	Enabled      bool   `json:"enabled"`
	ClockChannel uint8  `json:"clock_channel"`
	Inverted     bool   `json:"inverted"`
	CPOL         bool   `json:"cpol"`
	CPHA         bool   `json:"cpha"`
	MSB          bool   `json:"msb"`
	GapTicks     uint32 `json:"gap_ticks"`
	Pattern      uint32 `json:"pattern"`
	Length       uint8  `json:"length"`
	Delay        int8   `json:"delay,omitempty"`
}

// SPI receiver capabilities (register 64): on the 125 MHz line levels, or on
// the converters' samples with interpolated clock edges.
const (
	spiLevels  = 0x5301
	spiSamples = 0x5303
)

// regSampleDelay is the receivers on the samples' delay register: SPI's data
// line, I2C's SDA, after the first line's crossing (sample_bus.v).
const regSampleDelay = 58

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SupportsSPI() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.read(64)
	return (v == spiLevels || v == spiSamples) && err == nil, err
}

// SupportsSampleSPI reports the receiver on the samples, which decodes and
// triggers to a 125 MHz clock (spi_sample.v).
// TRLC-LINKS: REQ-SDS-229
func (c *Capture) SupportsSampleSPI() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.read(64)
	return v == spiSamples && err == nil, err
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) configureSPI(revision uint16, p SPITriggerConfig) error {
	capability := uint16(0)
	if revision >= 10 {
		var err error
		capability, err = c.read(64)
		if err != nil {
			return err
		}
	}
	if capability != spiLevels && capability != spiSamples {
		if p.Enabled {
			return fmt.Errorf("sramcapture: fabric lacks hardware SPI trigger")
		}
		return nil
	}
	control := uint16(0)
	if p.Enabled {
		control = 1 | uint16(p.ClockChannel)<<1 | uint16(p.Length)<<6
		if p.Inverted {
			control |= 4
		}
		if p.CPOL {
			control |= 8
		}
		if p.CPHA {
			control |= 16
		}
		if p.MSB {
			control |= 32
		}
		regs := []struct{ sel, value uint16 }{{65, uint16(p.GapTicks)}, {66, uint16(p.GapTicks >> 16)}, {67, uint16(p.Pattern)}, {68, uint16(p.Pattern >> 16)}}
		if capability == spiSamples {
			regs = append(regs, struct{ sel, value uint16 }{regSampleDelay, uint16(uint8(p.Delay))})
		}
		for _, r := range regs {
			if err := c.write(r.sel, r.value); err != nil {
				return err
			}
		}
	}
	return c.write(64, control)
}
