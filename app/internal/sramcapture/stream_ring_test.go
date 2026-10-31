// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"slices"
	"testing"
)

// ringFake serves the history ring's registers beside the stream's.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
type ringFake struct {
	*streamFake
	regs map[uint16]uint16
}

// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func (r *ringFake) Read(p uint8, s uint16) (uint16, error) {
	if s >= 61 && s <= 63 {
		return r.regs[s], nil
	}
	return r.streamFake.Read(p, s)
}

// The ring starts with the stream's decimation in register 21 and the
// ring's in 18, freezes on one stop with the newest word's time, and the
// stream ends on a second stop; an image without the ring refuses it.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func TestStreamRingStartFreezeStop(t *testing.T) {
	f := &ringFake{streamFake: streamBus(), regs: map[uint16]uint16{61: streamRingCapability, 62: 0x5678, 63: 0x0012}}
	f.staged[15] = 500
	c, err := New(f)
	if err != nil {
		t.Fatal(err)
	}
	if !c.StreamRingSupported() {
		t.Fatal("capability not seen")
	}
	if _, _, err := c.FreezeRing(context.Background()); err == nil {
		t.Fatal("froze a ring that was never started")
	}
	cfg := Config{Source: ADC, DecimationLog2: 9, PreWords: Words - 17, PostWords: 17}
	if err := c.StartStreamRing(context.Background(), cfg, 13); err != nil {
		t.Fatal(err)
	}
	if f.staged[21] != 13 || f.staged[19] != 1|2|4|32 || f.staged[18] != 9 {
		t.Fatalf("21=%d 19=%x 18=%d", f.staged[21], f.staged[19], f.staged[18])
	}
	f.commands = nil
	m, last, err := c.FreezeRing(context.Background())
	if err != nil || last != 0x125678 || !m.Frozen || m.Length == 0 {
		t.Fatalf("freeze: %+v last %x %v", m, last, err)
	}
	if !slices.Equal(f.commands, []uint16{5}) {
		t.Fatalf("freeze commands %v", f.commands)
	}
	f.commands = nil
	if err := c.StopStream(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.commands, []uint16{5, 5}) || f.staged[19] != 0 {
		t.Fatalf("stop commands %v, 19=%x", f.commands, f.staged[19])
	}
	// A plain stream afterwards stops once.
	f.commands = nil
	if err := c.StopStream(context.Background()); err != nil || !slices.Equal(f.commands, []uint16{5}) {
		t.Fatalf("plain stop %v %v", f.commands, err)
	}
	if err := c.StartStreamRing(context.Background(), cfg, 7); err == nil {
		t.Fatal("a stream decimation the digital words cannot take started")
	}
	f.regs[61] = 0
	if c.StreamRingSupported() {
		t.Fatal("an image without the ring reads as having it")
	}
	if err := c.StartStreamRing(context.Background(), cfg, 13); err == nil {
		t.Fatal("an image without the ring started one")
	}
}

// A ring frozen on a host trigger keeps its record through the recall's
// check although the fabric reports a stopped ring's trigger index.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240, REQ-SDS-033
func TestHostTriggerRecordStaysTheFrozenOne(t *testing.T) {
	f := frozenBus()
	c, err := New(f)
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.Status()
	if err != nil {
		t.Fatal(err)
	}
	m.TriggerIndex /= 3
	c.mu.Lock()
	_, err = c.sameFrozen(m)
	c.mu.Unlock()
	if err == nil {
		t.Fatal("a moved trigger index passed as the fabric's")
	}
	m.HostTrigger = true
	c.mu.Lock()
	_, err = c.sameFrozen(m)
	c.mu.Unlock()
	if err != nil {
		t.Fatalf("host-triggered record: %v", err)
	}
	m.Length--
	c.mu.Lock()
	_, err = c.sameFrozen(m)
	c.mu.Unlock()
	if err == nil {
		t.Fatal("a changed record passed")
	}
}
