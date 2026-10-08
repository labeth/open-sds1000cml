// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "fmt"

// Packet trigger protocols use the repository protocol IDs.
const (
	PacketCAN     uint8 = 6
	PacketARINC   uint8 = 8
	PacketFlexRay uint8 = 10
)

// PacketTriggerConfig drives the packet image's shared trigger block
// (ADR-PROTOCOL-PACKET-IMAGE); one of CAN / CAN FD, ARINC 429 or FlexRay runs.
// Ticks is the bit period in 125 MHz receiver clocks, with eight fractional
// bits for CAN and FlexRay. Aux is CAN's data-phase period in the same Q8
// form, or ARINC's slicer codes {exit_hi, exit_lo, hi, lo} from the high byte.
// Pattern holds Length contiguous payload bytes, the last in the low byte;
// ARINC compares the whole 32-bit word under the mask in Pattern's high half.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
type PacketTriggerConfig struct {
	Enabled  bool   `json:"enabled"`
	Protocol uint8  `json:"protocol"`
	Channel  uint8  `json:"channel"`
	Inverted bool   `json:"inverted"`
	Ticks    uint32 `json:"ticks"`
	Aux      uint32 `json:"aux"`
	Pattern  uint64 `json:"pattern"`
	Length   uint8  `json:"length"`
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (p PacketTriggerConfig) validate() error {
	if !p.Enabled {
		return nil
	}
	bad := p.Channel > 1 || p.Ticks > 0xffffff
	switch p.Protocol {
	case PacketCAN:
		bad = bad || p.Ticks < 2048 || p.Aux < 2048 || p.Aux > 0xffffff || p.Length > 8
	case PacketFlexRay:
		bad = bad || p.Ticks < 1024 || p.Aux != 0 || p.Length > 8
	case PacketARINC:
		lo, hi, exitLo, exitHi := uint8(p.Aux), uint8(p.Aux>>8), uint8(p.Aux>>16), uint8(p.Aux>>24)
		bad = bad || p.Ticks < 4 || p.Inverted || p.Length != 0 || !(lo < exitLo && exitLo <= exitHi && exitHi < hi)
	default:
		bad = true
	}
	if bad {
		return fmt.Errorf("sramcapture: invalid hardware packet trigger configuration")
	}
	return nil
}

// PacketProtocols returns the packet block's protocol mask (bit n for
// protocol ID n), or zero when the loaded image has no packet block.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) PacketProtocols() (uint16, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.packetProtocols()
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) packetProtocols() (uint16, error) {
	revision, err := c.read(13)
	if err != nil || revision != 10 {
		return 0, err
	}
	capability, err := c.read(109)
	if err != nil || capability != 0x5001 {
		return 0, err
	}
	return c.read(110)
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) configurePacket(p PacketTriggerConfig) error {
	mask, err := c.packetProtocols()
	if err != nil {
		return err
	}
	if mask == 0 {
		if p.Enabled {
			return fmt.Errorf("sramcapture: fabric lacks the hardware packet trigger")
		}
		return nil
	}
	var control uint16
	if p.Enabled {
		if mask&(1<<p.Protocol) == 0 {
			return fmt.Errorf("sramcapture: fabric lacks hardware protocol %d", p.Protocol)
		}
		control = 1 | uint16(p.Channel)<<1 | uint16(p.Length)<<3 | uint16(p.Protocol)<<8
		if p.Inverted {
			control |= 4
		}
		for _, r := range []struct{ sel, value uint16 }{
			{110, uint16(p.Ticks)}, {111, uint16(p.Ticks >> 16)}, {112, uint16(p.Aux)}, {113, uint16(p.Aux >> 16)},
			{114, uint16(p.Pattern)}, {115, uint16(p.Pattern >> 16)}, {116, uint16(p.Pattern >> 32)}, {117, uint16(p.Pattern >> 48)},
		} {
			if err := c.write(r.sel, r.value); err != nil {
				return err
			}
		}
	}
	return c.write(109, control)
}

// ManchesterTriggerConfig selects full-frame Manchester words (1..16 bits,
// Thomas or IEEE, either bit order) and up to four contiguous words, the last
// in Pattern's low 16 bits. BitTicks is whole 125 MHz receiver clocks.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
type ManchesterTriggerConfig struct {
	Enabled  bool   `json:"enabled"`
	Channel  uint8  `json:"channel"`
	Inverted bool   `json:"inverted"`
	IEEE     bool   `json:"ieee"`
	MSB      bool   `json:"msb"`
	Bits     uint8  `json:"bits"`
	BitTicks uint32 `json:"bit_ticks"`
	Pattern  uint64 `json:"pattern"`
	Length   uint8  `json:"length"`
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (p ManchesterTriggerConfig) validate() error {
	if p.Enabled && (p.Channel > 1 || p.Bits < 1 || p.Bits > 16 || p.BitTicks < 8 || p.BitTicks > 0x3fffff || p.Length > 4) {
		return fmt.Errorf("sramcapture: invalid hardware Manchester configuration")
	}
	return nil
}

// SupportsManchester reports the line image's Manchester block. MIL-STD-1553
// shares the capability word at its own register, so the revision is checked.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SupportsManchester() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.manchesterCapability()
	return v && err == nil, err
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) manchesterCapability() (bool, error) {
	revision, err := c.read(13)
	if err != nil || revision < 10 {
		return false, err
	}
	v, err := c.read(102)
	return v == 0x4d01, err
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) configureManchester(p ManchesterTriggerConfig) error {
	present, err := c.manchesterCapability()
	if err != nil {
		return err
	}
	if !present {
		if p.Enabled {
			return fmt.Errorf("sramcapture: fabric lacks hardware Manchester trigger")
		}
		return nil
	}
	var control uint16
	if p.Enabled {
		control = 1 | uint16(p.Channel)<<1 | uint16(p.Bits)<<5 | uint16(p.Length)<<10
		if p.Inverted {
			control |= 4
		}
		if p.IEEE {
			control |= 8
		}
		if p.MSB {
			control |= 16
		}
		for _, r := range []struct{ sel, value uint16 }{
			{103, uint16(p.BitTicks)}, {104, uint16(p.BitTicks >> 16)},
			{105, uint16(p.Pattern)}, {106, uint16(p.Pattern >> 16)}, {107, uint16(p.Pattern >> 32)}, {108, uint16(p.Pattern >> 48)},
		} {
			if err := c.write(r.sel, r.value); err != nil {
				return err
			}
		}
	}
	return c.write(102, control)
}

// RawOnly reports an image without precision decimation (register 30).
// TRLC-LINKS: REQ-SDS-010
func (c *Capture) RawOnly() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rawOnly()
}

// TRLC-LINKS: REQ-SDS-010
func (c *Capture) rawOnly() (bool, error) {
	revision, err := c.read(13)
	if err != nil || revision != 10 {
		return false, err
	}
	v, err := c.read(30)
	return v == 0x4e44, err
}
