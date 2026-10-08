// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "fmt"

// SPITriggerConfig selects two-wire, eight-bit SPI with explicit idle framing.
// GapTicks is the maximum interval between sampling edges at 125 MHz; use
// 1.5 clock periods. No chip-select input is inferred from the two analog inputs.
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
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SupportsSPI() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.read(64)
	return v == 0x5301 && err == nil, err
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
	if capability != 0x5301 {
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
		for _, r := range []struct{ sel, value uint16 }{{65, uint16(p.GapTicks)}, {66, uint16(p.GapTicks >> 16)}, {67, uint16(p.Pattern)}, {68, uint16(p.Pattern >> 16)}} {
			if err := c.write(r.sel, r.value); err != nil {
				return err
			}
		}
	}
	return c.write(64, control)
}
