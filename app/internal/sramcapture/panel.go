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
	return c.writeMany([2]uint16{32, half}, [2]uint16{33, p.Gap}, [2]uint16{31, cfg}) // one batch, the config last
}

// ReadPanel reads the last frame and the counters.
// TRLC-LINKS: REQ-SDS-022
func (c *Capture) ReadPanel() (PanelFrame, error) {
	var w [11]uint16
	var err error
	if iso, ok := c.bus.(interface {
		ReadIsolatedPanel() ([11]uint16, bool, error)
	}); ok {
		var owned bool
		c.mu.Lock() // as the reads themselves: no other sequence in between
		w, owned, err = iso.ReadIsolatedPanel()
		c.mu.Unlock()
		if !owned {
			w, err = c.PanelWords()
		}
	} else {
		w, err = c.PanelWords()
	}
	if err != nil {
		return PanelFrame{}, err
	}
	var f PanelFrame
	copy(f.Words[:], w[0:4])
	copy(f.Knobs[:], w[4:8])
	f.Frames, f.Changes, f.Pins = w[8], w[9], w[10]
	return f, nil
}

// PanelWords reads the panel's eleven words (ReadPanel) on this bus: the
// acquisition worker reads them for the app in one round trip.
// TRLC-LINKS: REQ-SDS-022
func (c *Capture) PanelWords() ([11]uint16, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var w [11]uint16
	for i := uint16(0); i < 11; i++ {
		if err := c.write(34, i); err != nil {
			return w, err
		}
		v, err := c.read(44)
		if err != nil {
			return w, err
		}
		w[i] = v
	}
	return w, nil
}
