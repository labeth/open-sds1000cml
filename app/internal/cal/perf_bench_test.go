// ENGMODEL-OWNER-UNIT: FU-APP-CAL
package cal

import (
	"encoding/binary"
	"testing"
)

// benchBlob is a valid scrambled calibration file.
// TRLC-LINKS: REQ-SDS-016
func benchBlob() []byte {
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	Scramble(payload)
	raw := make([]byte, fileSize)
	copy(raw[4:], payload)
	binary.LittleEndian.PutUint32(raw, Checksum(payload))
	return raw
}

// TRLC-LINKS: REQ-SDS-016
func BenchmarkParse(b *testing.B) {
	raw := benchBlob()
	for b.Loop() {
		if _, err := Parse(raw); err != nil {
			b.Fatal(err)
		}
	}
}

// byteSum is the plain sum of the bytes at every length.
// TRLC-LINKS: REQ-SDS-016
func TestByteSumIsTheSum(t *testing.T) {
	b := make([]byte, 3000)
	for i := range b {
		b[i] = byte(255 - i%7)
	}
	for n := 0; n <= len(b); n += 37 {
		var want uint32
		for _, v := range b[:n] {
			want += uint32(v)
		}
		if got := byteSum(b[:n]); got != want {
			t.Fatalf("n %d: %d, want %d", n, got, want)
		}
	}
}
