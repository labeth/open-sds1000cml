// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "fmt"

// MIL1553TriggerConfig accepts one complete 16-bit word after odd parity.
// BitTicks is the explicit bit period in 125 MHz receiver clocks.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
type MIL1553TriggerConfig struct {
	Enabled  bool   `json:"enabled"`
	Channel  uint8  `json:"channel"`
	Inverted bool   `json:"inverted"`
	BitTicks uint32 `json:"bit_ticks"`
	Pattern  uint16 `json:"pattern"`
	MatchAny bool   `json:"match_any"`
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (p MIL1553TriggerConfig) validate() error {
	if p.Enabled && (p.Channel > 1 || p.BitTicks < 8 || p.BitTicks > 0x3fffff) {
		return fmt.Errorf("sramcapture: invalid hardware MIL-STD-1553 configuration")
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SupportsMIL1553() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.read(93)
	return v == 0x4d01 && err == nil, err
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) configureMIL1553(revision uint16, p MIL1553TriggerConfig) error {
	var capability uint16
	if revision >= 10 {
		v, err := c.read(93)
		if err != nil {
			return err
		}
		capability = v
	}
	if capability != 0x4d01 {
		if p.Enabled {
			return fmt.Errorf("sramcapture: fabric lacks hardware MIL-STD-1553 trigger")
		}
		return nil
	}
	var control uint16
	if p.Enabled {
		control = 1 | uint16(p.Channel)<<1
		if p.Inverted {
			control |= 4
		}
		if p.MatchAny {
			control |= 8
		}
		for _, r := range []struct{ sel, value uint16 }{{94, uint16(p.BitTicks)}, {95, uint16(p.BitTicks >> 16)}, {96, p.Pattern}} {
			if err := c.write(r.sel, r.value); err != nil {
				return err
			}
		}
	}
	return c.write(93, control)
}
