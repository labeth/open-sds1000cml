// ENGMODEL-OWNER-UNIT: FU-APP-VXI11SRV
package vxi11srv

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TRLC-LINKS: REQ-SDS-024
func BenchmarkReadRecord(b *testing.B) {
	msg := make([]byte, 4+1024)
	binary.BigEndian.PutUint32(msg, 0x80000000|1024)
	r := bytes.NewReader(msg)
	for b.Loop() {
		r.Reset(msg)
		if _, err := readRecord(r); err != nil {
			b.Fatal(err)
		}
	}
}

// A record of several fragments reads as their bytes in order.
// TRLC-LINKS: REQ-SDS-024
func TestReadRecordFragments(t *testing.T) {
	var in []byte
	var want []byte
	for i, n := range []int{3, 0, 700, 5} {
		mark := uint32(n)
		if i == 3 {
			mark |= 0x80000000
		}
		in = binary.BigEndian.AppendUint32(in, mark)
		for k := 0; k < n; k++ {
			in = append(in, byte(i*31+k))
			want = append(want, byte(i*31+k))
		}
	}
	got, err := readRecord(bytes.NewReader(in))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%v: %d bytes, want %d", err, len(got), len(want))
	}
}
