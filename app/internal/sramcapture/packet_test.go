// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"testing"
)

// packetBus answers the packet image's capability registers.
type packetBus struct {
	*fakeBus
	packet, protocols, manchester, rawOnly uint16
}

// TRLC-LINKS: REQ-SDS-013
func (b packetBus) Read(p uint8, s uint16) (uint16, error) {
	if p == 1 {
		switch s {
		case 109:
			return b.packet, nil
		case 110:
			return b.protocols, nil
		case 102:
			return b.manchester, nil
		case 30:
			return b.rawOnly, nil
		}
	}
	return b.fakeBus.Read(p, s)
}

// TRLC-LINKS: REQ-SDS-013
func packetImage(f *fakeBus) packetBus { return packetBus{f, 0x5001, 0x0540, 0x4d01, 0x4e44} }

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestPacketTriggerArmAndDisable(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	c := client(t, f)
	c.bus = packetImage(f)
	if mask, err := c.PacketProtocols(); err != nil || mask != 0x0540 {
		t.Fatalf("mask %x %v", mask, err)
	}
	cfg := Config{Normal: true, PostWords: 16, DecodedEvents: false, Packet: PacketTriggerConfig{Enabled: true, Protocol: PacketCAN, Channel: 1,
		Ticks: 32000, Aux: 8000, Pattern: 0x0102030405060708, Length: 8}}
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for s, v := range map[uint16]uint16{109: 1 | 2 | 8<<3 | 6<<8, 110: 32000, 111: 0, 112: 8000, 113: 0, 114: 0x0708, 115: 0x0506, 116: 0x0304, 117: 0x0102, 102: 0} {
		if f.staged[s] != v {
			t.Fatalf("register %d=%x want %x", s, f.staged[s], v)
		}
	}
	f.flags = 4 | 16 | 32 | 64
	cfg.Packet = PacketTriggerConfig{}
	cfg.Manchester = ManchesterTriggerConfig{Enabled: true, IEEE: true, MSB: true, Bits: 16, BitTicks: 125, Pattern: 0xa55a, Length: 1}
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for s, v := range map[uint16]uint16{109: 0, 102: 1 | 8 | 16 | 16<<5 | 1<<10, 103: 125, 104: 0, 105: 0xa55a} {
		if f.staged[s] != v {
			t.Fatalf("register %d=%x want %x", s, f.staged[s], v)
		}
	}
	// The packet image refuses decimated records before touching registers.
	f.flags = 4 | 16 | 32 | 64
	cfg.DecimationLog2 = 4
	before := f.writes
	if err := c.Arm(context.Background(), cfg); err == nil || f.writes != before {
		t.Fatalf("decimation accepted on a raw-only image: %v", err)
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestPacketTriggerUnsupported(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	c := client(t, f)
	c.bus = packetBus{f, 0x5001, 0x0040, 0, 0x4e44} // CAN only, no Manchester
	for _, cfg := range []Config{
		{Normal: true, PostWords: 16, Packet: PacketTriggerConfig{Enabled: true, Protocol: PacketFlexRay, Ticks: 3200}},
		{Normal: true, PostWords: 16, Manchester: ManchesterTriggerConfig{Enabled: true, Bits: 8, BitTicks: 125}},
	} {
		f.flags = 4 | 16 | 32 | 64
		if err := c.Arm(context.Background(), cfg); err == nil {
			t.Fatalf("unsupported decoder accepted: %+v", cfg)
		}
		if f.staged[109] != 0 || f.staged[102] != 0 {
			t.Fatal("unsupported decoder was enabled")
		}
	}
	// The general image has no packet block: its registers are never written.
	g := frozenBus()
	g.revision = 10
	c = client(t, g)
	if err := c.Arm(context.Background(), Config{Normal: true, PostWords: 16}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []uint16{102, 109, 110} {
		if _, written := g.staged[s]; written {
			t.Fatalf("general image register %d written", s)
		}
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestPacketTriggerInvalidBeforeWrites(t *testing.T) {
	base := PacketTriggerConfig{Enabled: true, Protocol: PacketARINC, Ticks: 1250, Aux: 141<<24 | 114<<16 | 161<<8 | 92, Pattern: 0x1ffffc00<<32 | 0x2abcd<<10}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Packet.Protocol = 4 },
		func(c *Config) { c.Packet.Aux = 92<<24 | 114<<16 | 161<<8 | 92 },
		func(c *Config) { c.Packet.Length = 1 },
		func(c *Config) { c.Packet.Channel = 2 },
		func(c *Config) { c.Packet.Protocol, c.Packet.Ticks, c.Packet.Aux = PacketCAN, 32000, 0 },
		func(c *Config) {
			c.Packet.Protocol, c.Packet.Ticks, c.Packet.Aux, c.Packet.Length = PacketFlexRay, 3200, 0, 9
		},
		func(c *Config) { c.Normal = false },
		func(c *Config) { c.UART = UARTTriggerConfig{Enabled: true, BitTicks: 1085} },
		func(c *Config) { c.Manchester = ManchesterTriggerConfig{Enabled: true, Bits: 8, BitTicks: 125} },
		func(c *Config) {
			c.Packet.Enabled, c.Manchester = false, ManchesterTriggerConfig{Enabled: true, Bits: 17, BitTicks: 125}
		},
	} {
		f := frozenBus()
		c := client(t, f)
		cfg := Config{Normal: true, PostWords: 16, Packet: base}
		mutate(&cfg)
		if err := c.Arm(context.Background(), cfg); err == nil {
			t.Fatalf("invalid packet configuration accepted: %+v", cfg)
		}
		if f.writes != 0 {
			t.Fatal("invalid packet configuration reached hardware")
		}
	}
}

// sequenceBus adds the sequence block to a protocol image and records the
// element RAM writes, which reuse registers 119..124 per element.
type sequenceBus struct {
	packetBus
	elements map[uint16]SequenceElement
	index    uint16
	pending  SequenceElement
}

// TRLC-LINKS: REQ-SDS-013
func (b *sequenceBus) Read(p uint8, s uint16) (uint16, error) {
	if p == 1 && s == 118 {
		return 0x5351, nil
	}
	if p == 1 && s == 73 {
		return 0x5201, nil
	}
	return b.packetBus.Read(p, s)
}

// TRLC-LINKS: REQ-SDS-013
func (b *sequenceBus) RawWrite(s, v uint16) error {
	switch s {
	case 119:
		b.index = v
	case 120:
		b.pending.Value = b.pending.Value&0xffff0000 | uint32(v)
	case 121:
		b.pending.Value = b.pending.Value&0xffff | uint32(v)<<16
	case 122:
		b.pending.Mask = b.pending.Mask&0xffff0000 | uint32(v)
	case 123:
		b.pending.Mask = b.pending.Mask&0xffff | uint32(v)<<16
	case 124:
		b.pending.Kind = uint8(v)
		b.elements[b.index] = b.pending
	}
	return b.packetBus.fakeBus.RawWrite(s, v)
}

// TRLC-LINKS: REQ-SDS-013
func TestSequenceTriggerArm(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	c := client(t, f)
	b := &sequenceBus{packetBus: packetImage(f), elements: map[uint16]SequenceElement{}}
	c.bus = b
	var seq SequenceTriggerConfig
	seq.Length, seq.Qualify = 20, true
	for k := 0; k < 20; k++ {
		seq.Elements[k] = SequenceElement{Kind: SequenceData, Value: uint32(k * 7), Mask: 0xff}
	}
	cfg := Config{Normal: true, PostWords: 16, Sequence: seq, Packet: PacketTriggerConfig{Enabled: true, Protocol: PacketCAN, Ticks: 32000, Aux: 32000}}
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if f.staged[118] != 20|64 || len(b.elements) != 20 || b.elements[19] != (SequenceElement{2, 133, 0xff}) {
		t.Fatalf("control %x elements %v", f.staged[118], b.elements)
	}
	// Without a sequence the block is disabled.
	f.flags = 4 | 16 | 32 | 64
	cfg.Sequence = SequenceTriggerConfig{}
	if err := c.Arm(context.Background(), cfg); err != nil || f.staged[118] != 0 {
		t.Fatalf("sequence left enabled: %x %v", f.staged[118], err)
	}
	// A sequence alone, or longer than the RAM, is refused before any write.
	for _, bad := range []Config{
		{Normal: true, PostWords: 16, Sequence: seq},
		{Normal: true, PostWords: 16, Sequence: SequenceTriggerConfig{Length: 33}, Packet: cfg.Packet},
	} {
		g := frozenBus()
		c := client(t, g)
		if err := c.Arm(context.Background(), bad); err == nil || g.writes != 0 {
			t.Fatalf("invalid sequence accepted: %v writes %d", err, g.writes)
		}
	}
	// An image without the block never sees its registers written.
	g := frozenBus()
	g.revision = 10
	c = client(t, g)
	if err := c.Arm(context.Background(), Config{Normal: true, PostWords: 16}); err != nil {
		t.Fatal(err)
	}
	if _, written := g.staged[118]; written {
		t.Fatal("sequence register written on an image without the block")
	}
}
