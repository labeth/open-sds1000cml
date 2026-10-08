// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "testing"

// A slow UART on a long record decodes the same through the strided copy,
// at the strided sample time, and the reported rate is unchanged.
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectFastUART(t *testing.T) {
	const spb = 4340 // 115200 Bd at 2 ns
	msg := []byte("Hi U, hello scope")
	var c1 []uint8
	idle := func(n int) {
		for i := 0; i < n; i++ {
			c1 = append(c1, 210)
		}
	}
	idle(3 * spb)
	for _, b := range msg {
		bits := []int{0}
		for k := 0; k < 8; k++ {
			bits = append(bits, int(b>>k)&1)
		}
		bits = append(bits, 1)
		for _, bit := range bits {
			v := uint8(128)
			if bit == 1 {
				v = 210
			}
			for i := 0; i < spb; i++ {
				c1 = append(c1, v)
			}
		}
	}
	idle(3 * spb)
	c2 := make([]uint8, len(c1))
	for i := range c2 {
		c2[i] = 128
	}
	res, st := AutodetectFast(c1, c2, 2e-9, "hex")
	if res.Proto != "uart" || res.Baud != 115200 || st <= 2e-9 || string(byteSlice(res.Bytes)) != string(msg) {
		t.Fatalf("proto %s baud %d st %g bytes %q", res.Proto, res.Baud, st, byteSlice(res.Bytes))
	}
}

// Glitches of a sample or two do not force full-rate decoding.
// TRLC-LINKS: REQ-SDS-018
func TestShortestPulseIgnoresGlitches(t *testing.T) {
	c := make([]uint8, 10000)
	for i := range c {
		c[i] = 128
		if (i/1000)%2 == 1 {
			c[i] = 210
		}
	}
	c[500] = 210 // one-sample glitch
	if r := shortestPulse(c); r < 900 {
		t.Fatalf("shortest pulse %d", r)
	}
}

// TRLC-LINKS: REQ-SDS-018
func byteSlice(v []int) []byte {
	out := make([]byte, len(v))
	for i, b := range v {
		out[i] = byte(b)
	}
	return out
}
