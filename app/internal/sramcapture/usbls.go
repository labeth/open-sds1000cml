// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "fmt"

// USBLSTriggerConfig matches payload within one single-ended USB packet.
// BitTicksQ8 is the bit period in 125 MHz clocks with eight fractional bits.
// CRC bytes are payload; PID and observed in-packet stuffing are checked.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
type USBLSTriggerConfig struct {
	Enabled    bool   `json:"enabled"`
	Channel    uint8  `json:"channel"`
	Inverted   bool   `json:"inverted"`
	BitTicksQ8 uint32 `json:"bit_ticks_q8"`
	Pattern    uint32 `json:"pattern"`
	Length     uint8  `json:"length"`
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (p USBLSTriggerConfig) validate() error {
	if p.Enabled && (p.Channel > 1 || p.BitTicksQ8 < 2048 || p.BitTicksQ8 > 0xffffff || p.Length > 4) {
		return fmt.Errorf("sramcapture: invalid hardware USB configuration")
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SupportsUSBLS() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.read(97)
	return v == 0x5501 && err == nil, err
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) configureUSBLS(revision uint16, p USBLSTriggerConfig) error {
	var capability uint16
	if revision >= 10 {
		v, err := c.read(97)
		if err != nil {
			return err
		}
		capability = v
	}
	if capability != 0x5501 {
		if p.Enabled {
			return fmt.Errorf("sramcapture: fabric lacks hardware USB trigger")
		}
		return nil
	}
	var control uint16
	if p.Enabled {
		control = 1 | uint16(p.Channel)<<1 | uint16(p.Length)<<3
		if p.Inverted {
			control |= 4
		}
		for _, r := range []struct{ sel, value uint16 }{{98, uint16(p.BitTicksQ8)}, {99, uint16(p.BitTicksQ8 >> 16)}, {100, uint16(p.Pattern)}, {101, uint16(p.Pattern >> 16)}} {
			if err := c.write(r.sel, r.value); err != nil {
				return err
			}
		}
	}
	return c.write(97, control)
}
