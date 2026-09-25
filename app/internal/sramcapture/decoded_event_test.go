// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"encoding/hex"
	"testing"
)

// TRLC-LINKS: REQ-SDS-013
func TestDecodedEventWireContract(t *testing.T) {
	// Literal wire vector: preserves a >32-bit sample ordinal and a 19-bit value.
	b, _ := hex.DecodeString("0102080d0403020108070605080706050403020100000000ffff070000000000")
	e, err := ParseDecodedEvent(b)
	if err != nil {
		t.Fatal(err)
	}
	if e.Sample != 0x0102030405060708 || e.Value != 0x7ffff || e.Record != 0 || !e.RecordValid || !e.Valid || e.Channels != 1 {
		t.Fatalf("lost event identity: %+v", e)
	}
	encoded, err := e.MarshalBinary()
	if err != nil || hex.EncodeToString(encoded) != hex.EncodeToString(b) {
		t.Fatal("wire encoding differs", err)
	}
	for _, offset := range []int{0, 1, 2, 3} {
		bad := append([]byte(nil), b...)
		bad[offset] = 255
		if _, err := ParseDecodedEvent(bad); err == nil {
			t.Fatalf("accepted malformed header byte %d", offset)
		}
	}
	if _, err := ParseDecodedEvent(b[:31]); err == nil {
		t.Fatal("accepted partial FIFO event")
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestDecodedLossAndRetention(t *testing.T) {
	for _, e := range []DecodedEvent{{Kind: EventLoss, Count: 17}, {Kind: EventRetained, RecordValid: true, Count: 100, Sample: 42}} {
		b, err := e.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParseDecodedEvent(b)
		if err != nil || got != e {
			t.Fatalf("metadata lost: %+v %v", got, err)
		}
	}
	for _, e := range []DecodedEvent{{Kind: EventLoss, Valid: true}, {Kind: EventRetained, Count: 100}, {Kind: EventRetained, RecordValid: true}} {
		if _, err := e.MarshalBinary(); err == nil {
			t.Fatalf("accepted invalid metadata: %+v", e)
		}
	}
}
