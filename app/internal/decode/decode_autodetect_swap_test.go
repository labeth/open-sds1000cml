// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"slices"
	"testing"
)

// Neither channel is preferred: every capture the detection names - each
// protocol's, I2C and SPI in its four modes, and each record the scope read
// of a bench bus - is named the same with its channels swapped, its roles
// mirrored (a clock found on C2 is found on C1) and the same bytes decoded.
// A quadrature encoder's A and B swapped turn it the other way: named the
// same.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func TestAutodetectEitherChannel(t *testing.T) {
	type cap struct {
		name   string
		c1, c2 []uint8
		colT   float64
	}
	var caps []cap
	for _, tc := range append(protoDetectCases(), physDetectCases()...) {
		caps = append(caps, cap{tc.name, tc.c1, tc.c2, tc.colT})
	}
	// I2C: a few transfers with idle between; SPI in its four modes
	var scl, sda []uint8
	for i, a := range []int{0x24, 0x50, 0x3C, 0x68} {
		c, d := i2cWave(a, i&1, []int{0x55 + i, 0xAA - i, 0x0F}, 20)
		scl, sda = append(scl, c...), append(sda, d...)
		for k := 0; k < 200; k++ {
			scl, sda = append(scl, 210), append(sda, 210)
		}
	}
	caps = append(caps, cap{"I2C", scl, sda, 1e-7})
	for mode := 0; mode < 4; mode++ {
		clk, dat := spiModeWave([]int{0x0F, 0xF0, 0x3C, 0xC3, 0x12, 0x34, 0x56, 0x78, 0x01, 0x80}, 8, 0, mode&2 != 0, mode&1 != 0, true)
		caps = append(caps, cap{fmt.Sprintf("SPI mode %d", mode), clk, dat, 1e-7})
	}
	for _, sc := range scopeCaptures {
		c1, c2, s := loadGzJSON(t, "testdata/"+sc.file)
		caps = append(caps, cap{sc.file, c1, c2, s})
	}
	for _, c := range caps {
		a, _ := AutodetectFast(c.c1, c.c2, c.colT, "hex")
		b, _ := AutodetectFast(c.c2, c.c1, c.colT, "hex")
		two := a.Roles.ChB != a.Roles.ChA
		switch {
		case a.Proto != b.Proto:
			t.Errorf("%s: %q as captured, %q swapped", c.name, a.Proto, b.Proto)
		case a.Proto == "quad":
			// A and B swapped is the encoder turning the other way: the
			// detection names it on either channel; A stays C1 by convention
			t.Logf("%-38s %-10s either channel", c.name, a.Proto)
		case b.Roles.ChA != 1-a.Roles.ChA || (two && b.Roles.ChB != 1-a.Roles.ChB):
			t.Errorf("%s (%s): roles A C%d B C%d as captured, A C%d B C%d swapped", c.name, a.Proto,
				a.Roles.ChA+1, a.Roles.ChB+1, b.Roles.ChA+1, b.Roles.ChB+1)
		case !slices.Equal(a.Bytes, b.Bytes):
			t.Errorf("%s (%s): bytes differ swapped: %v / %v", c.name, a.Proto, firstUnits(a.Bytes), firstUnits(b.Bytes))
		default:
			t.Logf("%-38s %-10s A C%d -> C%d", c.name, a.Proto, a.Roles.ChA+1, b.Roles.ChA+1)
		}
	}
}

// firstUnits is a slice's first values, for a message.
// TRLC-LINKS: REQ-SDS-018
func firstUnits(v []int) []int { return v[:min(len(v), 12)] }
