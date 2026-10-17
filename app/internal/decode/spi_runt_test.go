package decode

import (
	"compress/gzip"
	"io"
	"os"
	"testing"
)

// loadSPIRunt is a 6.6 us stretch of a 104 MHz flash read on the scope (C1
// data, C2 clock, 2 ns samples, 10x probes): one high half of the clock
// sampled either side of its peak reads 36 69 68 47 against a hysteresis
// band to 69.1.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-229
func loadSPIRunt(t *testing.T) (c1, c2 []uint8) {
	t.Helper()
	f, err := os.Open("testdata/spi_flash104_runt.bin.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	return b[:len(b)/2], b[len(b)/2:]
}

// TestDecodeSPIKeepsAShortClockHalf: under six samples a clock period a
// sampled half that stops short of the band still clocks its bit, as in the
// FPGA's receiver; the band dropped it and every byte after it slipped a bit
// ("-SDS 0" read "-MA..", 68 bytes).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-229
func TestDecodeSPIKeepsAShortClockHalf(t *testing.T) {
	c1, c2 := loadSPIRunt(t)
	r := DecodeSPI(c2, c1, 2e-9, SPICfg{MSB: true, DataLag: SPIDataLag(1, 2e-9), DataDelay: 3.7})
	if !r.OK {
		t.Fatal(r.Error)
	}
	want := []int{0x0B, 0, 0x10, 0, 0xFF}
	for _, c := range "SPI FLASH 104MHZ OPEN-SDS 0123  " {
		want = append(want, int(c))
	}
	if len(r.Bytes) != 69 {
		t.Fatalf("%d bytes, want the frame's 69: %s", len(r.Bytes), r.Text)
	}
	for k, v := range r.Bytes {
		w := (r.Bytes[37] + k - 37) & 255
		if k < len(want) {
			w = want[k]
		}
		if v != w {
			t.Fatalf("byte %d %02X, want %02X: %s", k, v, w, r.Text)
		}
	}
}
