// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "fmt"

// SENTTriggerConfig uses 125 MHz clocks per nominal tick. Pattern contains up
// to four byte-sized nibbles, including status, and is accepted only at good CRC.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
type SENTTriggerConfig struct {
	Enabled    bool   `json:"enabled"`
	Channel    uint8  `json:"channel"`
	Inverted   bool   `json:"inverted"`
	PausePulse bool   `json:"pause_pulse"`
	TickTicks  uint32 `json:"tick_ticks"`
	Nibbles    uint8  `json:"nibbles"`
	Pattern    uint32 `json:"pattern"`
	Length     uint8  `json:"length"`
}

// TRLC-LINKS: REQ-SDS-013
func (p SENTTriggerConfig) validate() error {
	if !p.Enabled {
		return nil
	}
	if p.Channel > 1 || p.TickTicks < 4 || p.TickTicks > 0xffffff || p.Nibbles < 1 || p.Nibbles > 64 || p.Length > 4 {
		return fmt.Errorf("sramcapture: invalid hardware SENT configuration")
	}
	for i := uint8(0); i < p.Length; i++ {
		if (p.Pattern>>(8*i))&255 > 15 {
			return fmt.Errorf("sramcapture: SENT pattern values must be nibbles")
		}
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SupportsSENT() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.read(77)
	return v == 0x5e01 && err == nil, err
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) configureSENT(revision uint16, p SENTTriggerConfig) error {
	capability := uint16(0)
	if revision >= 10 {
		var err error
		capability, err = c.read(77)
		if err != nil {
			return err
		}
	}
	if capability != 0x5e01 {
		if p.Enabled {
			return fmt.Errorf("sramcapture: fabric lacks hardware SENT trigger")
		}
		return nil
	}
	control := uint16(0)
	if p.Enabled {
		control = 1 | uint16(p.Channel)<<1 | uint16(p.Length)<<4
		if p.Inverted {
			control |= 4
		}
		if p.PausePulse {
			control |= 8
		}
		for _, r := range []struct{ sel, value uint16 }{{78, uint16(p.TickTicks)}, {79, uint16(p.TickTicks >> 16)}, {80, uint16(p.Pattern)}, {81, uint16(p.Pattern >> 16)}, {82, uint16(p.Nibbles)}} {
			if err := c.write(r.sel, r.value); err != nil {
				return err
			}
		}
	}
	return c.write(77, control)
}
