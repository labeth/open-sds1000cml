package decode

// oracle_dump_spi_test.go — ORACLE HARNESS for the in-fabric SPI decoder.
// Dumps machine-readable CLK/DATA codes + expected Bytes so the iverilog
// testbench (and bench Verify agent) can replay identical stimulus and assert
// FPGA-drained words == DecodeSPI().Bytes. Asserts nothing; it DUMPS.
//
//   cd app && go test ./internal/decode -run TestSPIOracleDump -v
// Env: CPOL/CPHA/MSB (0/1), RATE (bit rate), BYTES (comma hex list).

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func dumpCodes(t *testing.T, name string, c []uint8) {
	var b strings.Builder
	for i, v := range c {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.Itoa(int(v)))
	}
	t.Logf("ORACLE %s=%s", name, b.String())
}

func TestSPIOracleDump(t *testing.T) {
	const sr = 1_000_000
	rate := float64(envDefaultInt("RATE", 62_500)) // 16 samples/bit
	cpol := envDefaultInt("CPOL", 0) != 0
	cpha := envDefaultInt("CPHA", 0) != 0
	msb := envDefaultInt("MSB", 1) != 0
	payload := parseByteList(envDefaultStr("BYTES",
		"0x00,0xFF,0xA5,0x5A,0x0F,0xF0,0x81,0x7E"))

	clk, mosi := oracleSPIBits(sr, rate, cpol, cpha, msb, spiOWords(2, payload...))
	r := DecodeSPI(bitsToCodes(clk), bitsToCodes(mosi), 1.0/sr,
		SPICfg{CPOL: cpol, CPHA: cpha, MSB: msb})
	if !r.OK {
		t.Fatalf("DecodeSPI failed: %s", r.Error)
	}
	thr8 := int(math.Ceil(r.Thr))
	gapFloor := int(math.Floor(1.5 * r.SPB))
	gapRound := int(math.Round(1.5 * r.SPB))
	t.Logf("ORACLE SPI CPOL=%d CPHA=%d MSB=%d CLKTHR8=%d DATATHR8=%d SPB=%.4f GAPRESET_FLOOR=%d GAPRESET_ROUND=%d",
		b2iSpi(cpol), b2iSpi(cpha), b2iSpi(msb), thr8, thr8, r.SPB, gapFloor, gapRound)
	t.Logf("ORACLE SPI NCODES=%d", len(clk))
	t.Logf("ORACLE SPI BYTES=% X", intsToBytes(r.Bytes))
	t.Logf("ORACLE SPI PAYLOAD=% X", intsToBytes(payload))
	dumpCodes(t, "SPI_CLK", bitsToCodes(clk))
	dumpCodes(t, "SPI_DATA", bitsToCodes(mosi))
}

func b2iSpi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// dumpSPIWords dumps a custom spiOWord stream (for gap/fractional/mid-word
// vectors the plain byte list cannot express).
func dumpSPIWords(t *testing.T, name string, sr, rate float64, cpol, cpha, msb bool, words []spiOWord) {
	clk, mosi := oracleSPIBits(sr, rate, cpol, cpha, msb, words)
	r := DecodeSPI(bitsToCodes(clk), bitsToCodes(mosi), 1.0/sr,
		SPICfg{CPOL: cpol, CPHA: cpha, MSB: msb})
	if !r.OK {
		t.Fatalf("%s DecodeSPI failed: %s", name, r.Error)
	}
	n := len(clk)
	if len(mosi) < n {
		n = len(mosi)
	}
	t.Logf("ORACLE SPI CPOL=%d CPHA=%d MSB=%d CLKTHR8=128 DATATHR8=128 SPB=%.4f GAPRESET_FLOOR=%d GAPRESET_ROUND=%d",
		b2iSpi(cpol), b2iSpi(cpha), b2iSpi(msb), r.SPB,
		int(math.Floor(1.5*r.SPB)), int(math.Round(1.5*r.SPB)))
	t.Logf("ORACLE SPI NCODES=%d", n)
	t.Logf("ORACLE SPI BYTES=% X", intsToBytes(r.Bytes))
	dumpCodes(t, "SPI_CLK", bitsToCodes(clk))
	dumpCodes(t, "SPI_DATA", bitsToCodes(mosi))
}

func TestSPIOracleDumpGap(t *testing.T) {
	const sr = 1_000_000
	const rate = 62_500
	switch envDefaultStr("VEC", "frac") {
	case "frac": // 13.717 cols/bit, jittery 13/14 gaps
		dumpSPIWords(t, "frac", sr, 72_900, false, false, true,
			spiOWords(2, 0x13, 0x37, 0xC0, 0xDE, 0xA5))
	case "idle": // 40-bit idle gaps on byte boundaries (no-op reset)
		dumpSPIWords(t, "idle", sr, rate, false, false, true, []spiOWord{
			{v: 0x9A, gapBits: 40}, {v: 0x02}, {v: 0x40, gapBits: 40},
			{v: 0xF1}, {v: 0x1F, gapBits: 40}, {v: 0x55}})
	case "midpause": // 1.4-bit pause mid-word: must NOT reframe -> A5 3C
		dumpSPIWords(t, "midpause", sr, rate, false, false, true, []spiOWord{
			{v: 0xA0, bits: 4, gapBits: 0.4}, {v: 0x50, bits: 4}, {v: 0x3C}})
	case "midgap": // 40-bit mid-word gap: MUST reframe orphan -> A5 3C
		dumpSPIWords(t, "midgap", sr, rate, false, false, true, []spiOWord{
			{v: 0xF0, bits: 4, gapBits: 40}, {v: 0xA5}, {v: 0x3C}})
	}
}
