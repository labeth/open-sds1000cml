// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// Caps is what the loaded image implements, as its capability registers
// say.
type Caps struct {
	UART, I2C, SPI                   bool
	SampleSPI, SampleUART, SampleI2C bool // the receivers on the samples (the fast image)
	SENT, USBLS, MIL1553, Manchester bool
	Sequence, SequenceOnly           bool
	Envelope, Qualifier, Symbol      bool
	Packet                           uint16 // the packet receiver's protocol set
	RawOnly                          bool   // records raw samples only
	BoxDecim, PickDecim, PickAlt     bool   // decimation by box averages, by picking, picking on asking
}

// ProbeImage reads the loaded image's capabilities, in the order the
// registers are read after every load; a register that fails to answer
// counts as absent and is logged. OWNER.
// TRLC-LINKS: REQ-SDS-013
func (b *Board) ProbeImage(logf func(string, ...any)) Caps {
	var c Caps
	for _, probe := range []struct {
		name string
		dst  *bool
		fn   func() (bool, error)
	}{
		{"UART", &c.UART, b.mem.SupportsUART},
		{"I2C", &c.I2C, b.mem.SupportsI2C},
		{"SPI", &c.SPI, b.mem.SupportsSPI},
		{"SPI on the samples", &c.SampleSPI, b.mem.SupportsSampleSPI},
		{"UART on the samples", &c.SampleUART, b.mem.SupportsSampleUART},
		{"I2C on the samples", &c.SampleI2C, b.mem.SupportsSampleI2C},
		{"SENT", &c.SENT, b.mem.SupportsSENT},
		{"USB", &c.USBLS, b.mem.SupportsUSBLS},
		{"MIL-STD-1553", &c.MIL1553, b.mem.SupportsMIL1553},
		{"Manchester", &c.Manchester, b.mem.SupportsManchester},
		{"sequence", &c.Sequence, b.mem.SupportsSequence},
		{"envelope", &c.Envelope, b.mem.SupportsEnvelope},
		{"qualifier", &c.Qualifier, b.mem.SupportsQualifier},
		{"symbol", &c.Symbol, b.mem.SupportsSymbol},
		{"sequence-only", &c.SequenceOnly, b.mem.SequenceOnly},
	} {
		var err error
		if *probe.dst, err = probe.fn(); err != nil {
			logf("hardware %s capability: %v; using software", probe.name, err)
		}
	}
	var err error
	if c.Packet, err = b.mem.PacketProtocols(); err != nil {
		logf("hardware packet capability: %v; using software", err)
	}
	if c.RawOnly, err = b.mem.RawOnly(); err != nil {
		logf("decimation capability: %v", err)
	}
	if c.BoxDecim, err = b.mem.BoxDecimation(); err != nil {
		logf("box decimation capability: %v", err)
	}
	if c.PickDecim, err = b.mem.PickDecimation(); err != nil {
		logf("pick decimation capability: %v", err)
	}
	c.BoxDecim = c.BoxDecim || c.PickDecim
	if c.PickAlt, err = b.mem.PickOnAsking(); err != nil {
		logf("pick-on-asking capability: %v", err)
	}
	b.trig.caps = c
	return c
}

// Serial reports a matcher for any bus.
// TRLC-LINKS: REQ-SDS-013
func (c Caps) Serial() bool {
	return c.UART || c.I2C || c.SPI || c.SENT || c.MIL1553 || c.USBLS || c.Manchester || c.Packet != 0 || c.Symbol
}

// ImageCaps is the loaded image's capabilities, as ProbeImage read them.
// ANY from the owner's view, as State.
// TRLC-LINKS: REQ-SDS-013
func (b *Board) ImageCaps() Caps { return b.trig.caps }

// SetImageCaps takes capabilities probed elsewhere, for a board whose
// fabric is a model (the engine's tests). OWNER.
// TRLC-LINKS: REQ-SDS-013
func (b *Board) SetImageCaps(c Caps) { b.trig.caps = c }

// trigCaps is the loaded image's trigger nodes.
// TRLC-LINKS: REQ-SDS-013
func (b *Board) trigCaps() hal.TrigCaps {
	c := b.trig.caps
	tc := hal.TrigCaps{
		Nodes:     map[hal.TrigNode]hal.NodeCaps{hal.NodeEdge: {Slopes: []hal.Slope{hal.Rising, hal.Falling}}},
		MaxStages: 1,
		Sources:   []hal.SourceKind{hal.Analog},
	}
	if c.Qualifier {
		ops := []hal.QualOp{hal.Shorter, hal.Longer, hal.Inside}
		tc.Nodes[hal.NodePulse] = hal.NodeCaps{Slopes: []hal.Slope{hal.Rising, hal.Falling}, QualOps: ops, MinS: sramcapture.QualTickNs * 1e-9}
		tc.Nodes[hal.NodeSlope] = hal.NodeCaps{Slopes: []hal.Slope{hal.Rising, hal.Falling}, QualOps: ops, MinS: sramcapture.QualTickNs * 1e-9}
		tc.Nodes[hal.NodeVideo] = hal.NodeCaps{}
		tc.Nodes[hal.NodeZone] = hal.NodeCaps{}
	}
	for _, p := range []struct {
		on   bool
		name string
	}{
		{c.UART || c.SampleUART, "uart"},
		{c.I2C || c.SampleI2C, "i2c"},
		{c.SPI || c.SampleSPI, "spi"},
		{c.SENT, "sent"},
		{c.USBLS, "usb"},
		{c.MIL1553, "mil1553"},
		{c.Manchester, "manchester"},
		{c.Packet != 0, "packet"},
	} {
		if p.on {
			tc.Protocols = append(tc.Protocols, p.name)
		}
	}
	if len(tc.Protocols) != 0 {
		tc.Nodes[hal.NodeProtocol] = hal.NodeCaps{}
	}
	if c.Symbol {
		tc.Nodes[hal.NodeSymbol] = hal.NodeCaps{}
		tc.Nodes[hal.NodeFrameStart] = hal.NodeCaps{}
	}
	if c.Sequence {
		tc.Nodes[hal.NodeSequence] = hal.NodeCaps{}
	}
	return tc
}
