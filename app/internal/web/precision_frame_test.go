package web

import (
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestPrecisionBinaryPayload(t *testing.T) {
	rep := frameReply{Seq: 1, Cols: 2, Depth: 2, C1: []int16{100, 255}, C2: []int16{101, 0}, Q1: []uint16{25601, 65535}, Q2: []uint16{25728, 0}, FractionBits: 8}
	b := encodeBinFrame(rep)
	if b[1]&0x20 == 0 {
		t.Fatal("precision flag missing")
	}
	n := int(binary.LittleEndian.Uint32(b[4:8]))
	var hdr map[string]any
	if err := json.Unmarshal(b[8:8+n], &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr["fraction_bits"] != float64(8) {
		t.Fatal(hdr)
	}
	pay := b[8+n:]
	if len(pay) != 8 {
		t.Fatal(len(pay))
	}
	for i, want := range []uint16{25601, 65535, 25728, 0} {
		if got := binary.LittleEndian.Uint16(pay[2*i:]); got != want {
			t.Fatalf("word %d=%d want %d", i, got, want)
		}
	}
}
