// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"testing"
)

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

// freeClock is a free-running square wave of n samples, hp samples a half-period.
func freeClock(n, hp int) []uint8 {
	ck := make([]uint8, n)
	for i := range ck {
		ck[i] = 40
		if (i/hp)%2 == 1 {
			ck[i] = 210
		}
	}
	return ck
}

// A UART beside a free-running clock it has nothing to do with is a UART:
// the bench's UART on C1 with a clock on C2 read as SPI on the web, and as
// I2C or nothing here, because a clocked pair suppressed UART (bug hunt
// 2026-10-11). Its edges keep no place in the clock's cycle; a clocked data
// line keeps one.
// TRLC-LINKS: REQ-SDS-018
func TestAutodetectUARTBesideFreeClock(t *testing.T) {
	want := []int{0x48, 0x69, 0x55, 0xAA, 0x41, 0x42, 0x43, 0x44}
	u := uartWave(want, 40)
	for _, hp := range []int{3, 7, 13, 29, 61, 400} {
		ck := freeClock(len(u), hp)
		for _, swap := range []bool{false, true} {
			c1, c2, src := u, ck, "C1"
			if swap {
				c1, c2, src = ck, u, "C2"
			}
			r := Autodetect(c1, c2, 1/(40*115200.0), "hex")
			if r.Proto != "uart" || r.Src != src || fmt.Sprint(r.Bytes) != fmt.Sprint(want) {
				t.Errorf("hp %d, UART on %s: %s %s %v (%s)", hp, src, r.Proto, r.Src, r.Bytes, r.Error)
			}
		}
		if share, n := phaseLock(clockScore(ck), sliceChannel(u, 0, false)); n >= 12 && share >= 0.6 {
			t.Errorf("hp %d: the UART locks to the free clock (%.2f of %d edges)", hp, share, n)
		}
	}
	clk, dat := spiWave([]int{0x48, 0x69, 0x55, 0xAA, 0x0F, 0xF0}, 20)
	if share, n := phaseLock(clockScore(clk), sliceChannel(dat, 0, false)); n < 12 || share < 0.9 {
		t.Errorf("SPI data is not locked to its clock: %.2f of %d edges", share, n)
	}
	scl, sda := i2cWave(0x24, 0, []int{0x55, 0xAA, 0x0F}, 20)
	if r := Autodetect(scl, sda, 2e-7, "hex"); r.Proto != "i2c" {
		t.Errorf("I2C detected as %q (%s)", r.Proto, r.Error)
	}
}
