// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"testing"
)

// spiModeWave is an SPI pair with sharp edges, h samples a half-period: the
// clock idles at CPOL, each bit is launched delay samples after its launch
// edge (CPHA 0: the trailing edge, the first bit set up at the burst start;
// CPHA 1: the leading edge) and is sampled on the other edge.
// TRLC-LINKS: REQ-SDS-018
func spiModeWave(bytes []int, h, delay int, cpol, cpha, msb bool) (clk, dat []uint8) {
	lo, hi := uint8(40), uint8(210)
	idle, act := lo, hi
	if cpol {
		idle, act = hi, lo
	}
	n := 8*h + len(bytes)*18*h
	clk, dat = make([]uint8, n), make([]uint8, n)
	for i := range clk {
		clk[i] = idle
	}
	type set struct {
		at int
		v  uint8
	}
	var sets []set
	t := 4 * h
	for _, b := range bytes {
		for j := 0; j < 8; j++ {
			k := 7 - j
			if !msb {
				k = j
			}
			v := lo
			if (b>>k)&1 == 1 {
				v = hi
			}
			at := t + delay
			if cpha {
				at += h
			}
			sets = append(sets, set{at, v})
			for i := t + h; i < t+2*h; i++ {
				clk[i] = act
			}
			t += 2 * h
		}
		t += 2 * h // idle between bytes
	}
	v, si := lo, 0
	for i := range dat {
		for si < len(sets) && sets[si].at <= i {
			v = sets[si].v
			si++
		}
		dat[i] = v
	}
	return clk, dat
}

// Auto decode reads every SPI mode and both bit orders on sharp edges, where
// a sample at either clock edge is a full rail: CPHA comes from the edge the
// data is launched on, and only text breaks the bit order (not CPHA 1 as mode
// 0, or binary payloads LSB first, as in 87 of 320 physically modelled
// captures).
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectSPIModes(t *testing.T) {
	binary := []int{0x0F, 0xF0, 0x3C, 0xC3, 0x12, 0x34, 0x56, 0x78, 0x01, 0x80}
	text := []int{'H', 'e', 'l', 'l', 'o', ' ', 'S', 'P', 'I', '!'}
	for _, cpol := range []bool{false, true} {
		for _, cpha := range []bool{false, true} {
			mode := 0
			if cpol {
				mode |= 2
			}
			if cpha {
				mode |= 1
			}
			for _, h := range []int{3, 4, 6, 10, 20} {
				for _, delay := range []int{0, 1} {
					for _, tc := range []struct {
						bytes []int
						msb   bool
					}{{binary, true}, {text, true}, {text, false}} {
						clk, dat := spiModeWave(tc.bytes, h, delay, cpol, cpha, tc.msb)
						order := map[bool]string{true: "MSB", false: "LSB"}[tc.msb]
						r := Autodetect(clk, dat, 1e-7, "hex")
						src := fmt.Sprintf("CLK C1 DATA C2 mode %d %s", mode, order)
						if r.Proto != "spi" || fmt.Sprint(r.Bytes) != fmt.Sprint(tc.bytes) || r.Src != src {
							t.Errorf("mode %d %s, %d samples a bit, delay %d: %s %q %v", mode, order, 2*h, delay, r.Proto, r.Src, r.Bytes)
						}
					}
				}
			}
		}
	}
}
