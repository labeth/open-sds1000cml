// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"encoding/binary"
	"fmt"
	"testing"
)

type eventBus struct {
	data    []byte
	cursor  int
	epoch   uint32
	enabled bool
	fail    int
}

// TRLC-LINKS: REQ-SDS-013
func (b *eventBus) Read(_ uint8, s uint16) (uint16, error) {
	switch s {
	case 57:
		if b.enabled {
			return 1, nil
		}
		return 0, nil
	case 58:
		if len(b.data) == 0 {
			return 0, nil
		}
		return 1 | uint16(b.cursor<<4), nil
	case 59:
		if b.cursor == b.fail {
			b.fail = -1
			return 0, fmt.Errorf("injected read failure")
		}
		v := binary.LittleEndian.Uint16(b.data[b.cursor*2:])
		b.cursor++
		if b.cursor == 16 {
			b.cursor = 0
			b.data = nil
		}
		return v, nil
	case 61:
		return uint16(b.epoch), nil
	case 62:
		return uint16(b.epoch >> 16), nil
	case 63:
		return 0x4501, nil
	}
	return 0, fmt.Errorf("unexpected read %d", s)
}

// TRLC-LINKS: REQ-SDS-013
func (b *eventBus) RawWrite(s, v uint16) error {
	switch s {
	case 57:
		if v == 1 && !b.enabled {
			b.epoch++
		}
		b.enabled = v == 1
	case 60:
		if v&1 != 0 {
			b.cursor = 0
		}
	default:
		return fmt.Errorf("unexpected write %d", s)
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-013
func TestDecodedEventHostRecovery(t *testing.T) {
	want := DecodedEvent{Epoch: 0x10002, Sequence: 7, Sample: 1 << 40, Protocol: 1, Kind: EventData, Value: 0xa5, Valid: true, Channels: 2}
	data, _ := want.MarshalBinary()
	b := &eventBus{data: data, epoch: want.Epoch, enabled: true, fail: 6}
	c := &Capture{bus: b}
	if _, ok, err := c.ReadDecodedEvent(want.Epoch); err == nil || ok {
		t.Fatal("partial failure accepted")
	}
	got, ok, err := c.ReadDecodedEvent(want.Epoch)
	if err != nil || !ok || got != want {
		t.Fatalf("rewind recovery: %+v %v %v", got, ok, err)
	}
	if _, ok, err := c.ReadDecodedEvent(want.Epoch); err != nil || ok {
		t.Fatalf("empty: %v %v", ok, err)
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestDecodedEventHostEpoch(t *testing.T) {
	b := &eventBus{epoch: 12, fail: -1}
	c := &Capture{bus: b}
	epoch, err := c.EnableDecodedEvents(true)
	if err != nil || epoch != 13 || !b.enabled {
		t.Fatalf("enable: %d %v", epoch, err)
	}
	if _, _, err := c.ReadDecodedEvent(12); err == nil {
		t.Fatal("accepted old reader epoch")
	}
	stale := DecodedEvent{Epoch: 12, Protocol: 1, Kind: EventData}
	b.data, _ = stale.MarshalBinary()
	if _, ok, err := c.ReadDecodedEvent(epoch); err == nil || ok {
		t.Fatal("accepted old event epoch")
	}
	if _, err := c.EnableDecodedEvents(false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.ReadDecodedEvent(epoch); err == nil {
		t.Fatal("accepted disabled stream")
	}
}

type dmaEventBus struct {
	*eventBus
	calls   int
	failDMA bool
}

// TRLC-LINKS: REQ-SDS-013
func (b *dmaEventBus) PopBytesChecked(selector uint16, dst []byte) error {
	b.calls++
	if selector != 59 || len(dst) != DecodedEventBytes {
		return fmt.Errorf("invalid event DMA")
	}
	copy(dst, b.data)
	b.data = nil // A failure may happen after the final pop; fallback is forbidden.
	if b.failDMA {
		return fmt.Errorf("injected DMA completion failure")
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-013
func TestDecodedEventDMAConsumption(t *testing.T) {
	want := DecodedEvent{Epoch: 7, Sequence: 3, Protocol: 2, Kind: EventData, Value: 0xaa, Valid: true, Channels: 3}
	for _, failure := range []bool{false, true} {
		data, _ := want.MarshalBinary()
		b := &dmaEventBus{eventBus: &eventBus{data: data, epoch: 7, enabled: true, fail: -1}, failDMA: failure}
		c := &Capture{bus: b}
		got, available, err := c.ReadDecodedEvent(7)
		if b.calls != 1 || len(b.data) != 0 {
			t.Fatal("DMA must consume one event exactly once")
		}
		if failure {
			if err == nil || available {
				t.Fatal("uncertain DMA accepted")
			}
		} else if err != nil || !available || got != want {
			t.Fatalf("DMA event: %+v %v %v", got, available, err)
		}
	}
}

type batchEventBus struct {
	*eventBus
	calls    int
	failDMA  bool
	reserved uint16
}

// TRLC-LINKS: REQ-SDS-013
func (b *batchEventBus) Read(plane uint8, s uint16) (uint16, error) {
	if s == 74 {
		return 0x4503, nil
	}
	if s == 75 {
		return b.reserved, nil
	}
	return b.eventBus.Read(plane, s)
}

// TRLC-LINKS: REQ-SDS-013
func (b *batchEventBus) RawWrite(s, v uint16) error {
	if s == 76 && v == 1 {
		b.reserved = uint16(len(b.data) / DecodedEventBytes)
		return nil
	}
	return b.eventBus.RawWrite(s, v)
}

// TRLC-LINKS: REQ-SDS-013
func (b *batchEventBus) PopBytesChecked(s uint16, dst []byte) error {
	b.calls++
	if s != 59 || len(dst) > len(b.data) || len(dst)%DecodedEventBytes != 0 {
		return fmt.Errorf("invalid reserved DMA")
	}
	copy(dst, b.data)
	b.data = b.data[len(dst):]
	if b.failDMA {
		return fmt.Errorf("injected completion failure")
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-013
func TestDecodedReservedBatch(t *testing.T) {
	for _, failure := range []bool{false, true} {
		b := &batchEventBus{eventBus: &eventBus{epoch: 7, enabled: true, fail: -1, cursor: 3}, failDMA: failure}
		for i := 0; i < 3; i++ {
			data, _ := (DecodedEvent{Epoch: 7, Sequence: uint32(i), Protocol: 2, Kind: EventData, Value: uint32(i + 85)}).MarshalBinary()
			b.data = append(b.data, data...)
		}
		c := &Capture{bus: b}
		got, err := c.ReadDecodedEvents(7, 2)
		if b.calls != 1 || len(b.data) != DecodedEventBytes || b.cursor != 0 {
			t.Fatal("batch did not reserve exactly two records after rewind")
		}
		if failure {
			if err == nil || len(got) != 0 {
				t.Fatal("uncertain batch was accepted")
			}
			continue
		}
		if err != nil || len(got) != 2 || got[0].Sequence != 0 || got[1].Sequence != 1 {
			t.Fatalf("batch: %+v %v", got, err)
		}
		owned := got
		storage := make([]DecodedEvent, 256)
		got, err = c.ReadDecodedEventsInto(7, storage)
		if err != nil || len(got) != 1 || got[0].Sequence != 2 {
			t.Fatalf("remaining batch: %+v %v", got, err)
		}
		if &got[0] != &storage[0] || owned[0].Sequence != 0 || owned[1].Sequence != 1 {
			t.Fatal("caller storage was ignored or an owned batch was overwritten")
		}
		if _, err = c.ReadDecodedEvents(8, 256); err == nil {
			t.Fatal("stale epoch accepted")
		}
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestDecodedCallerBufferDrainDoesNotAllocate(t *testing.T) {
	data, err := (DecodedEvent{Epoch: 7, Protocol: 2, Kind: EventData, Value: 85}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	b := &batchEventBus{eventBus: &eventBus{epoch: 7, enabled: true, fail: -1}}
	c := &Capture{bus: b}
	storage := make([]DecodedEvent, 512)
	allocations := testing.AllocsPerRun(100, func() {
		b.data = data
		got, err := c.ReadDecodedEventsInto(7, storage)
		if err != nil || len(got) != 1 || got[0].Value != 85 {
			t.Fatalf("drain: %v %v", got, err)
		}
	})
	if allocations != 0 {
		t.Fatalf("steady-state event drain allocated %g times", allocations)
	}
}
