// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"encoding/binary"
	"fmt"
)

// DecodedEventBytes is the fixed transport record size. This ABI is proposed
// until the general image advertises its event-stream capability.
const DecodedEventBytes = 32

const (
	EventStart uint8 = iota + 1
	EventData
	EventEnd
	EventError
	EventLoss
	EventTrigger
	EventRetained
)

// DecodedEvent carries acquisition-time facts, never browser column indices.
// Sample is an absolute sample ordinal within Epoch. Sequence counts all emitted
// events including loss markers. Record is meaningful only with RecordValid.
// Value preserves full decoder words (including ARINC's 19-bit data field).
// For EventLoss, Count is the number of lost events; zero means unknown count.
// For EventRetained, Sample is the first retained sample and Count its length.
// Channels is a bit mask: bit 0 C1, bit 1 C2. Protocol uses registry IDs 1..10;
// zero is reserved for transport-wide loss and retained-record metadata.
// TRLC-LINKS: REQ-SDS-013
type DecodedEvent struct {
	Epoch, Sequence uint32
	Sample          uint64
	Record, Value   uint32
	Count           uint32
	Protocol, Kind  uint8
	Channels        uint8
	Valid           bool
	RecordValid     bool
}

// MarshalBinary emits a versioned, little-endian record suitable for FIFO words.
// Flags distinguish a real record ID zero from an event without a retained record.
// TRLC-LINKS: REQ-SDS-013
func (e DecodedEvent) MarshalBinary() ([]byte, error) {
	b := make([]byte, DecodedEventBytes)
	if err := e.MarshalTo(b); err != nil {
		return nil, err
	}
	return b, nil
}

// TRLC-LINKS: REQ-SDS-013
func (e DecodedEvent) validate() error {
	if e.Kind < EventStart || e.Kind > EventRetained || e.Protocol > 10 || e.Channels > 3 {
		return fmt.Errorf("invalid decoded event header")
	}
	if e.Protocol == 0 && e.Kind != EventLoss && e.Kind != EventRetained {
		return fmt.Errorf("protocol required for decoded event")
	}
	if (e.Kind == EventLoss || e.Kind == EventError) && e.Valid {
		return fmt.Errorf("loss or error cannot be valid decoded data")
	}
	if e.Kind == EventRetained && (!e.RecordValid || e.Count == 0) {
		return fmt.Errorf("retained event requires a record and nonempty sample range")
	}
	return nil
}

// MarshalTo encodes into a caller-owned batch without a per-event allocation.
// TRLC-LINKS: REQ-SDS-013
func (e DecodedEvent) MarshalTo(b []byte) error {
	if len(b) != DecodedEventBytes {
		return fmt.Errorf("invalid decoded event output size")
	}
	if err := e.validate(); err != nil {
		return err
	}
	b[0], b[1], b[2], b[3] = 1, e.Kind, e.Protocol, e.Channels
	if e.Valid {
		b[3] |= 1 << 2
	}
	if e.RecordValid {
		b[3] |= 1 << 3
	}
	binary.LittleEndian.PutUint32(b[4:8], e.Epoch)
	binary.LittleEndian.PutUint32(b[8:12], e.Sequence)
	binary.LittleEndian.PutUint64(b[12:20], e.Sample)
	binary.LittleEndian.PutUint32(b[20:24], e.Record)
	binary.LittleEndian.PutUint32(b[24:28], e.Value)
	binary.LittleEndian.PutUint32(b[28:32], e.Count)
	return nil
}

// ParseDecodedEvent rejects incompatible versions and reserved flag bits rather
// than interpreting them as a different acquisition epoch or protocol.
// TRLC-LINKS: REQ-SDS-013
func ParseDecodedEvent(b []byte) (DecodedEvent, error) {
	if len(b) != DecodedEventBytes || b[0] != 1 || b[3]&0xf0 != 0 {
		return DecodedEvent{}, fmt.Errorf("invalid decoded event framing")
	}
	e := DecodedEvent{
		Kind: b[1], Protocol: b[2], Channels: b[3] & 3, Valid: b[3]&4 != 0, RecordValid: b[3]&8 != 0,
		Epoch: binary.LittleEndian.Uint32(b[4:8]), Sequence: binary.LittleEndian.Uint32(b[8:12]),
		Sample: binary.LittleEndian.Uint64(b[12:20]), Record: binary.LittleEndian.Uint32(b[20:24]),
		Value: binary.LittleEndian.Uint32(b[24:28]), Count: binary.LittleEndian.Uint32(b[28:32]),
	}
	if err := e.validate(); err != nil {
		return DecodedEvent{}, err
	}
	return e, nil
}
