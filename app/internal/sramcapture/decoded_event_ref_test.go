// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE

package sramcapture

// marshalBinary emits a versioned, little-endian record suitable for FIFO words.
// Flags distinguish a real record ID zero from an event without a retained record.
// TRLC-LINKS: REQ-SDS-013
func marshalBinary(e DecodedEvent) ([]byte, error) {
	b := make([]byte, DecodedEventBytes)
	if err := MarshalEvent(e, b); err != nil {
		return nil, err
	}
	return b, nil
}
