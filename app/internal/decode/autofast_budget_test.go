// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math"
	"testing"
)

// sigmaDelta is a 1-bit sigma-delta tone switching every few samples (an
// audio bench source sampled at the full rate): a busy line that is no bus.
// TRLC-LINKS: REQ-SDS-214
func sigmaDelta(n int, f float64) []uint8 {
	out := make([]uint8, n)
	acc, v := 0.0, uint8(30)
	for i := range out {
		if i%5 == 0 {
			acc += 0.5 + 0.4*math.Sin(2*math.Pi*f*float64(i)*2e-9)
			if v = 30; acc >= 1 {
				acc, v = acc-1, 220
			}
		}
		out[i] = v
	}
	return out
}

// The bus search reads a busy record only up to its edge budget, and a
// sparse one whole: a sigma-delta line 1M samples long (200 k crossings) is
// cut to its first budget, where a fast SPI bus over the whole record is
// still named, and a UART record keeps every sample.
// TRLC-LINKS: REQ-SDS-214
func TestAutodetectEdgeBudget(t *testing.T) {
	a, b := sigmaDelta(1000000, 440), sigmaDelta(1000000, 110)
	if n := budgetEnd(a, b, autoEdgeBudget); n >= len(a)/2 {
		t.Fatalf("a busy line kept %d of %d samples", n, len(a))
	}
	if res, _ := AutodetectFast(a, b, 2e-9, "hex"); res.OK && res.Proto != "off" {
		t.Fatalf("a sigma-delta tone named %s", res.Proto)
	}
	u := uartWave([]int{0x48, 0x69, 0x0d, 0x0a}, 40)
	if n := budgetEnd(u, nil, autoEdgeBudget); n != len(u) {
		t.Fatalf("a UART record was cut to %d of %d", n, len(u))
	}
	var bytes []int
	for i := 0; i < 20000; i++ {
		bytes = append(bytes, (i*37+11)&0xff)
	}
	clk, data := spiWave(bytes, 3) // a fast SPI bus busy over the whole record
	if n := budgetEnd(clk, data, autoEdgeBudget); n >= len(clk) {
		t.Fatalf("a busy SPI record was not cut (%d samples)", len(clk))
	}
	if res, _ := AutodetectFast(clk, data, 2e-9, "hex"); res.Proto != "spi" || len(res.Bytes) < 1000 {
		t.Fatalf("the cut SPI record named %q with %d bytes", res.Proto, len(res.Bytes))
	}
}
