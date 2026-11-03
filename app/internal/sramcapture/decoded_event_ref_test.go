// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE

package sramcapture

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
