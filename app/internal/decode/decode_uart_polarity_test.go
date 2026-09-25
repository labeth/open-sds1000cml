// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"bytes"
	"reflect"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018
func TestUARTInvertedPolarity(t *testing.T) {
	want := []int{0x48, 0x69, 0x20, 0x55, 0xaa, 0x0f, 0xf0, 0x0a}
	var samples []uint8
	bit := func(v int) {
		code := byte(190)
		if v != 0 {
			code = 40
		}
		samples = append(samples, bytes.Repeat([]byte{code}, 20)...)
	}
	for i := 0; i < 4; i++ {
		bit(1)
	}
	for _, v := range want {
		bit(0)
		for i := 0; i < 8; i++ {
			bit((v >> i) & 1)
		}
		bit(1)
	}
	for i := 0; i < 4; i++ {
		bit(1)
	}
	original := append([]byte(nil), samples...)
	for _, explicit := range []bool{false, true} {
		r := DecodeUART(samples, 1.0/2_000_000, UARTCfg{Baud: 100000, Inverted: true, HaveThr: explicit, Threshold: 100})
		if !r.OK || !reflect.DeepEqual(r.Bytes, want) {
			t.Fatalf("explicit=%t: %+v", explicit, r)
		}
		for _, span := range r.Spans {
			if span.Kind != "data" {
				t.Fatalf("unexpected span: %+v", span)
			}
		}
	}
	if !bytes.Equal(samples, original) {
		t.Fatal("decoder mutated acquisition samples")
	}
}
