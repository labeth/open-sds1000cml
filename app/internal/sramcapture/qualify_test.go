// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import "testing"

// qualBus is a fabric with (or without) the qualifier block: it answers the
// block's capability reads and keeps the last value written to each register.
type qualBus struct {
	block  bool
	writes map[uint16]uint16
	order  []uint16
}

func (b *qualBus) Read(_ uint8, s uint16) (uint16, error) {
	switch {
	case s == qualCapability && b.block:
		return 0x5154, nil
	case s == qualCapability+1 && b.block:
		return 0x000f, nil
	}
	return 0, nil
}

func (b *qualBus) RawWrite(s, v uint16) error {
	b.writes[s] = v
	b.order = append(b.order, s)
	return nil
}

// The qualifier and zone registers as fpga/common/top.v decodes them: the
// control word last (it enables the block with the limits already in place),
// limits split in 16-bit halves, the prescale less one; zones' codes {hi, lo},
// signed 11-bit windows, the control word {avoid, channel, enable} last.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-014
func TestQualifierAndZoneRegisters(t *testing.T) {
	b := &qualBus{block: true, writes: map[uint16]uint16{}}
	c := &Capture{bus: b}
	q := QualifierConfig{Enabled: true, Kind: QualSlope, Channel: 1, Negative: true, Cond: QualBetween,
		MinTicks: 0x2345, MaxTicks: 0x789a, Prescale: 8, LevelA: 0x40, LevelB: 0xc0}
	if err := q.validate(); err != nil {
		t.Fatal(err)
	}
	if err := c.configureQualifier(10, q); err != nil {
		t.Fatal(err)
	}
	want := map[uint16]uint16{21: 1 | 1<<1 | 8 | 3<<4 | 1<<6, 22: 0x2344, 24: 0x789a, 26: 0xc040, 27: 0, 28: 7}
	for s, v := range want {
		if b.writes[s] != v {
			t.Fatalf("register %d = %#x, want %#x", s, b.writes[s], v)
		}
	}
	if b.order[len(b.order)-1] != 21 {
		t.Fatalf("the control word was not written last: %v", b.order)
	}
	// a minimum of none sets min_zero, not a minimum less one
	if err := c.configureQualifier(10, QualifierConfig{Enabled: true, Kind: QualPulse, Cond: QualUnder, Prescale: 1}); err != nil {
		t.Fatal(err)
	}
	if b.writes[21] != 0x91 || b.writes[22] != 0 || b.writes[28] != 0 { // enable, under, min_zero
		t.Fatalf("minimum of none: control %#x, min %#x, prescale %#x", b.writes[21], b.writes[22], b.writes[28])
	}
	// a prescale that is not a power of two, or past 65536, is refused; 65536 is register 0xffff
	if (QualifierConfig{Enabled: true, Prescale: 3}).validate() == nil || (QualifierConfig{Enabled: true, Prescale: 1 << 17}).validate() == nil {
		t.Fatal("a prescale of 3 or 131072 clocks passed")
	}
	if err := c.configureQualifier(10, QualifierConfig{Enabled: true, Prescale: QualMaxPrescale}); err != nil || b.writes[28] != 0xffff {
		t.Fatalf("prescale 65536: %v, register %#x", err, b.writes[28])
	}

	z := ZoneConfig{Wait: 314, Bucket: 8}
	z.Zones[0] = ZoneWindow{Enabled: true, Lo: 180, Hi: 220, Start: -625, End: -156}
	z.Zones[3] = ZoneWindow{Enabled: true, Channel: 1, Lo: 0, Hi: 60, Start: 78, End: 313, Avoid: true}
	if err := z.validate(); err != nil {
		t.Fatal(err)
	}
	b.order = nil
	if err := c.configureZones(10, z); err != nil {
		t.Fatal(err)
	}
	want = map[uint16]uint16{84: 314, 85: 7, 86: 220<<8 | 180, 90: uint16(0x800 - 625), 70: uint16(0x800 - 156),
		89: 60 << 8, 69: 78, 73: 313, 83: 1 | 1<<3 | 1<<7 | 1<<11}
	for s, v := range want {
		if b.writes[s] != v {
			t.Fatalf("zone register %d = %#x, want %#x", s, b.writes[s], v)
		}
	}
	if b.order[len(b.order)-1] != 83 {
		t.Fatalf("the zones' control word was not written last: %v", b.order)
	}

	// a fabric without the block refuses an enabled qualifier or zones, and
	// takes their disabling quietly
	plain := &Capture{bus: &qualBus{writes: map[uint16]uint16{}}}
	if plain.configureQualifier(10, q) == nil || plain.configureZones(10, z) == nil {
		t.Fatal("a fabric without the qualifier block took a hardware qualifier")
	}
	if plain.configureQualifier(10, QualifierConfig{}) != nil || plain.configureZones(10, ZoneConfig{}) != nil {
		t.Fatal("disabling refused on a fabric without the block")
	}
}
