// ENGMODEL-OWNER-UNIT: FU-APP-SCPI
package scpi

import "testing"

// TRLC-LINKS: REQ-SDS-024
func BenchmarkHandleIDN(b *testing.B) {
	h, _ := newH(b)
	line := []byte("*IDN?\n")
	for b.Loop() {
		h.HandleLine(line)
	}
}

// TRLC-LINKS: REQ-SDS-024
func BenchmarkHandleCompound(b *testing.B) {
	h, _ := newH(b)
	line := []byte("CHDR OFF;TDIV?;C1:TRA?;TRMD?;MSIZ?\r\n")
	for b.Loop() {
		h.HandleLine(line)
	}
}

// TRLC-LINKS: REQ-SDS-024
func BenchmarkPAVAAll(b *testing.B) {
	h, _ := newH(b)
	line := []byte("C1:PAVA? ALL\n")
	for b.Loop() {
		h.HandleLine(line)
	}
}

// TRLC-LINKS: REQ-SDS-024
func BenchmarkFindEdges(b *testing.B) {
	c := make([]uint8, 5000)
	for i := range c {
		c[i] = 60
		if i/25%2 == 1 {
			c[i] = 190
		}
		c[i] += uint8(i * 7 % 5)
	}
	for b.Loop() {
		findEdges(c)
	}
}

// TRLC-LINKS: REQ-SDS-024
func BenchmarkDat2(b *testing.B) {
	h, fs := newH(b)
	for b.Loop() {
		h.dat2(0, fs.frame)
	}
}
