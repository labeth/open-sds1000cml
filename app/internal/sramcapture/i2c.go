// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "fmt"

// TRLC-LINKS: REQ-SDS-013
type I2CTriggerConfig struct {
	Enabled      bool   `json:"enabled"`
	ClockChannel uint8  `json:"clock_channel"`
	Inverted     bool   `json:"inverted"`
	Address      int    `json:"address"`   // -1 accepts any seven-bit address
	Direction    uint8  `json:"direction"` // 0 write, 1 read, 2 either
	Pattern      uint32 `json:"pattern"`
	Length       uint8  `json:"length"`
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SupportsI2C() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	revision, err := c.read(13)
	if err != nil || revision < 10 {
		return false, err
	}
	capability, err := c.read(53)
	return capability == 0x4901 && err == nil, err
}

// Called with the capture lock held and the fabric idle.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) configureI2C(revision uint16, p I2CTriggerConfig) error {
	capability := uint16(0)
	if revision >= 10 {
		var err error
		capability, err = c.read(53)
		if err != nil {
			return err
		}
	}
	if capability != 0x4901 {
		if p.Enabled {
			return fmt.Errorf("sramcapture: fabric lacks hardware I2C trigger")
		}
		return nil
	}
	control := uint16(0)
	if p.Enabled {
		control = 1 | uint16(p.ClockChannel)<<1 | uint16(p.Direction)<<3 | uint16(p.Length)<<6
		if p.Inverted {
			control |= 4
		}
		if p.Address < 0 {
			control |= 32
		}
		for _, r := range []struct{ sel, value uint16 }{{54, uint16(p.Address) & 127}, {55, uint16(p.Pattern)}, {56, uint16(p.Pattern >> 16)}} {
			if err := c.write(r.sel, r.value); err != nil {
				return err
			}
		}
	}
	return c.write(53, control)
}
