// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

// PanelConfig drives the front-panel serial scan (ADR-PANEL-SERIAL-SCAN):
// J2 loads the panel's shift chain, F2 clocks 64 bits out and they arrive on
// J1. Half is the F2 half period in GPMC-clock cycles, Gap the idle half
// periods between frames.
// TRLC-LINKS: REQ-SDS-022
type PanelConfig struct {
	Enable   bool   `json:"enable"`
	LoadHigh bool   `json:"load_high"`
	Late     bool   `json:"late"`
	IdleHigh bool   `json:"idle_high"`
	F1Mode   uint8  `json:"f1_mode"`
	Half     uint16 `json:"half"`
	Gap      uint16 `json:"gap"`
}

// PanelFrame is one read of the scan: the last frame's 64 bits (first
// shifted bit at Words[0] bit 0), the eight wrapping knob counts (knob 2i in
// Knobs[i] bits 7..0, knob 2i+1 in bits 15..8), the frame and changed-frame
// counters, and the live pins {J1, F1, J2, F2} in bits 3..0.
// TRLC-LINKS: REQ-SDS-022
type PanelFrame struct {
	Words   [4]uint16 `json:"words"`
	Knobs   [4]uint16 `json:"knobs"`
	Frames  uint16    `json:"frames"`
	Changes uint16    `json:"changes"`
	Pins    uint16    `json:"pins"`
}

// PanelDefault is the bench-settled scan: J2 high loads, F2 idles low at
// about 300 kHz from the 50 MHz GPMC clock, J1 sampled before the rising
// edge, and F1 pulsed low on each changed frame (the ARM key interrupt).
// TRLC-LINKS: REQ-SDS-022
var PanelDefault = PanelConfig{Enable: true, LoadHigh: true, F1Mode: 2, Half: 83, Gap: 200}

// SupportsPanel reports the panel scan block.
// TRLC-LINKS: REQ-SDS-022
func (c *Capture) SupportsPanel() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.read(31)
	return v == 0x5041, err
}

// ConfigurePanel writes the scan settings; the config word goes last so a
// running scan never sees a half-written rate.
// TRLC-LINKS: REQ-SDS-022
func (c *Capture) ConfigurePanel(p PanelConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg := uint16(p.F1Mode&3) << 10
	for _, f := range []struct {
		on  bool
		bit uint16
	}{{p.Enable, 1 << 15}, {p.LoadHigh, 1 << 14}, {p.Late, 1 << 13}, {p.IdleHigh, 1 << 12}} {
		if f.on {
			cfg |= f.bit
		}
	}
	half := p.Half
	if half == 0 {
		half = 1
	}
	for _, r := range []struct{ sel, value uint16 }{{32, half}, {33, p.Gap}, {31, cfg}} {
		if err := c.write(r.sel, r.value); err != nil {
			return err
		}
	}
	return nil
}

// ReadPanel reads the last frame and the counters.
// TRLC-LINKS: REQ-SDS-022
func (c *Capture) ReadPanel() (PanelFrame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var f PanelFrame
	for i := uint16(0); i < 11; i++ {
		if err := c.write(34, i); err != nil {
			return f, err
		}
		v, err := c.read(44)
		if err != nil {
			return f, err
		}
		switch {
		case i < 4:
			f.Words[i] = v
		case i < 8:
			f.Knobs[i-4] = v
		case i == 8:
			f.Frames = v
		case i == 9:
			f.Changes = v
		default:
			f.Pins = v
		}
	}
	return f, nil
}
