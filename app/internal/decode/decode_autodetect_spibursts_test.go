// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math/rand"
	"testing"
)

// SPI sent as short transfers - a few bytes, idle between - is named SPI in
// all four modes: its clock's bursts are whole bytes, where I2C clocks 9 a
// byte and an SWD transaction 46. In mode 3 the data changes while the
// clock rests high between transfers and read as I2C STARTs (39 of 50
// captures), and in every mode a short transfer read as an SWD request with
// an OK now and then.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func TestAutodetectShortSPITransfers(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var ok, total [4]int
	for trial := 0; trial < 400; trial++ {
		mode := trial % 4
		frames, n, h := 2+rng.Intn(8), 1+rng.Intn(5), 4+rng.Intn(12)
		var clk, dat []uint8
		for i := 0; i < frames; i++ {
			b := make([]int, n)
			for k := range b {
				b[k] = rng.Intn(256)
			}
			c, d := spiModeWave(b, h, 0, mode&2 != 0, mode&1 != 0, true)
			clk, dat = append(clk, c...), append(dat, d...)
		}
		r, _ := AutodetectFast(clk, dat, 1e-7, "hex")
		total[mode]++
		if r.Proto == "spi" {
			ok[mode]++
		}
	}
	for m := range ok {
		if ok[m]*10 < total[m]*9 {
			t.Errorf("SPI mode %d: named SPI on %d of %d captures", m, ok[m], total[m])
		}
	}
}

// An I2C transfer's clock rises 9 times a byte and once more for its STOP:
// seven bytes make a burst of 64, whole bytes for SPI. Transfers of that
// length stay I2C.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func TestAutodetectI2CBurstsOfWholeBytes(t *testing.T) {
	var scl, sda []uint8
	for i := 0; i < 4; i++ {
		c, d := i2cWave(0x50+i, 0, []int{0x10, 0x20 + i, 0x30, 0x40, 0x50, 0x60}, 20) // address + 6 bytes
		scl, sda = append(scl, c...), append(sda, d...)
		for k := 0; k < 400; k++ {
			scl, sda = append(scl, 210), append(sda, 210)
		}
	}
	if r, _ := AutodetectFast(scl, sda, 1e-7, "hex"); r.Proto != "i2c" {
		t.Fatalf("seven-byte I2C transfers named %q (%s)", r.Proto, r.Src)
	}
}
