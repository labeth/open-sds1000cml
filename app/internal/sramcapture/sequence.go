// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "fmt"

// Event kinds a sequence element can require; SequenceAny matches any kind.
const (
	SequenceAny   uint8 = 0
	SequenceStart uint8 = 1
	SequenceData  uint8 = 2
	SequenceEnd   uint8 = 3
	SequenceError uint8 = 4
)

// SequenceMax is the event-sequence trigger's element capacity.
const SequenceMax = 32

// SequenceElement matches one decoder event: its kind, and its 32-bit value
// under Mask. Values carry protocol metadata beside the data where the
// decoder publishes it: I2C address flag bit 8 and NACK bit 9, the MIL
// command flag bit 16, the SENT nibble index in bits 8..14.
// TRLC-LINKS: REQ-SDS-013
type SequenceElement struct {
	Kind  uint8  `json:"kind"`
	Value uint32 `json:"value"`
	Mask  uint32 `json:"mask"`
}

// SequenceTriggerConfig drives the event-sequence trigger of the protocol
// images (ADR-PROTOCOL-SEQUENCE-TRIGGER). Length elements must match
// contiguous events of the enabled hardware decoder; its match then replaces
// the decoder's own. Qualify holds a hit until the frame's END and drops it on
// ERROR or START; EndBadValue treats a nonzero END value as a bad frame. The
// array keeps Config comparable.
// TRLC-LINKS: REQ-SDS-013
type SequenceTriggerConfig struct {
	Length      uint8                        `json:"length"`
	Qualify     bool                         `json:"qualify"`
	EndBadValue bool                         `json:"end_bad_value"`
	Elements    [SequenceMax]SequenceElement `json:"elements"`
}

// TRLC-LINKS: REQ-SDS-013
func (p SequenceTriggerConfig) validate() error {
	if p.Length > SequenceMax {
		return fmt.Errorf("sramcapture: a sequence trigger holds at most %d elements", SequenceMax)
	}
	for _, e := range p.Elements[:p.Length] {
		if e.Kind > SequenceError {
			return fmt.Errorf("sramcapture: invalid sequence element kind %d", e.Kind)
		}
	}
	return nil
}

// SupportsSequence reports the event-sequence trigger. The stacking image
// uses the same registers, so the protocol images' record identity is required.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SupportsSequence() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sequenceCapability()
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) sequenceCapability() (bool, error) {
	revision, err := c.read(13)
	if err != nil || revision != 10 {
		return false, err
	}
	identity, err := c.read(73)
	if err != nil || identity != 0x5201 {
		return false, err
	}
	v, err := c.read(118)
	return v == 0x5351, err
}

// SequenceOnly reports an image whose decoders ignore their own pattern
// registers, so every pattern needs the sequence trigger.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SequenceOnly() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if present, err := c.sequenceCapability(); err != nil || !present {
		return false, err
	}
	v, err := c.read(47)
	return v == 0x534f, err
}

// SequenceOverflows reads the count of events dropped at a full queue.
// TRLC-LINKS: REQ-SDS-013
func (c *Capture) SequenceOverflows() (uint16, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.read(120)
}

// TRLC-LINKS: REQ-SDS-013
func (c *Capture) configureSequence(p SequenceTriggerConfig) error {
	present, err := c.sequenceCapability()
	if err != nil {
		return err
	}
	if !present {
		if p.Length != 0 {
			return fmt.Errorf("sramcapture: fabric lacks the sequence trigger")
		}
		return nil
	}
	// Disable before rewriting the element RAM.
	if err := c.write(118, 0); err != nil || p.Length == 0 {
		return err
	}
	for k, e := range p.Elements[:p.Length] {
		for _, r := range []struct{ sel, value uint16 }{
			{119, uint16(k)}, {120, uint16(e.Value)}, {121, uint16(e.Value >> 16)},
			{122, uint16(e.Mask)}, {123, uint16(e.Mask >> 16)}, {124, uint16(e.Kind)},
		} {
			if err := c.write(r.sel, r.value); err != nil {
				return err
			}
		}
	}
	control := uint16(p.Length)
	if p.Qualify {
		control |= 64
	}
	if p.EndBadValue {
		control |= 128
	}
	return c.write(118, control)
}
