// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "testing"

// pair builds a clock and a data line, the data changing `at` samples after
// each falling clock edge (hp samples per half-period).
func pair(hp, at, bits int) (clk, dat []uint8) {
	v := 1
	for b := 0; b < bits; b++ {
		v = (v*1103515245 + 12345) & 0x7fffffff
		bit := uint8(40 + 160*((v>>16)&1))
		prev := dat
		for k := 0; k < 2*hp; k++ {
			c := uint8(40)
			if k >= hp {
				c = 200
			}
			clk = append(clk, c)
			d := bit
			if k < at && len(prev) > 0 {
				d = prev[len(prev)-1]
			}
			dat = append(dat, d)
		}
	}
	return clk, dat
}

// A window of I2C without its START (SDA moving mid-SCL-low) is not SPI; a
// mode-0 SPI pair (data launched on the falling edge) still is (bug hunt #2:
// LCD Auto read the bench I2C at 20 us/div as "SPI mode 3, 3 bytes").
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectMidPhaseDataIsNotSPI(t *testing.T) {
	clk, dat := pair(20, 10, 60) // I2C-like: SDA changes a quarter period into SCL low
	if r := Autodetect(clk, dat, 1e-7, "hex"); r.Proto == "spi" {
		t.Fatalf("I2C-like pair detected as SPI: %s %s", r.Src, r.Text)
	}
	clk, dat = pair(20, 0, 64) // SPI mode 0: data set up on the falling edge
	if r := Autodetect(clk, dat, 1e-7, "hex"); r.Proto != "spi" {
		t.Fatalf("SPI pair detected as %q (%s)", r.Proto, r.Error)
	}
}
