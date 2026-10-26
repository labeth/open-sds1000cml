// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"os"
	"path/filepath"
	"testing"

	"open-sds/app/internal/testenv"
)

// generalPrecisionStub stands in for the vendor-FIFO precision filter in the
// general image's simulation: a constant word, one a 16 raw words.
const generalPrecisionStub = "`timescale 1ns/1ps\n" +
	"module adc_precision #(parameter SHARED_TAIL=0)(input core,packclk,clk100,enable,input [4:0] decim_request,input [31:0] raw,input raw_valid,\n" +
	" output [31:0] data,output reg valid=0,output fault);\n" +
	" assign data=32'h12341234;assign fault=1'b0;\n" +
	" reg [3:0] n=0;\n" +
	" always @(posedge core)begin valid<=0;if(!enable)n<=0;else if(raw_valid)begin n<=n+1'b1;valid<=n==0;end end\n" +
	"endmodule\n"

// The general image's SPI trigger (its receiver on the line levels) on a
// two-line bus fires at the same word whether the record takes every word
// or one in 8 or 128 (decimated /16 and /256): it reads the raw samples.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-040
func TestGeneralImageDecimatedTopRTL(t *testing.T) {
	if testing.Short() {
		t.Skip("full top-level simulation")
	}
	needIverilog(t)
	dir := t.TempDir()
	stub := filepath.Join(dir, "precision_stub.v")
	if err := os.WriteFile(stub, []byte(generalPrecisionStub), 0o600); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(testenv.RTLDir(t), stub)
	if err != nil {
		t.Fatal(err)
	}
	general := compileWordsTop(t, dir, "general-decim", generalImageDefines+"`define PRECISION\n", "uart_trigger.v", "i2c_trigger.v", "spi_trigger.v", rel)
	p := spiBus{rateHz: 5e6, msb: true, clockCh: 1, frames: [][]byte{benchFrame(0)[:12], benchFrame(1)[:12], benchFrame(2)[:12]},
		idleS: 2e-6 + 3/5e6, riseS: 3e-9, noise: 1, seed: 7, shift: [2]float64{75, -60}}
	c := p.config()
	c.Pattern, c.Length = 0x0b0010, 3
	control := uint32(1 | c.ClockCh<<1 | int(b2u8(c.Inverted))<<2 | int(b2u8(c.CPOL))<<3 | int(b2u8(c.CPHA))<<4 | int(b2u8(c.MSB))<<5 | c.Length<<6)
	first := -1
	for _, log := range []uint32{0, 4, 8} {
		regs := [][2]uint32{{18, log}, {7, uint32(c.Level) | 13<<8}, {59, uint32(c.DataLevel)}, {58, uint32(c.Delay) & 0xff},
			{65, 38}, {66, 0}, {67, c.Pattern & 0xffff}, {68, c.Pattern >> 16}, {64, control}}
		_, _, trigger := runFastTop(t, dir, general, p.words(), regs, "+general", "+precision", "+pre=2", "+post=400")
		if first < 0 {
			first = trigger
		}
		if trigger < 0 || trigger != first {
			t.Fatalf("/%d: trigger at word %d, full rate %d", 1<<log, trigger, first)
		}
	}
}
