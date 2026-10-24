// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import "testing"

// layerBench applies every curated and random vector of the bus group.
// TRLC-LINKS: REQ-SDS-241
func layerBench(b *testing.B, group string) {
	var vs []layerVec
	for _, v := range append(layerCurated(), layerRandom(1)...) {
		g := busOf(v.Proto)
		if g != "spi" && g != "i2c" && g != "uart" {
			g = "misc"
		}
		if g == group {
			vs = append(vs, v)
		}
	}
	if len(vs) == 0 {
		b.Skip("no vectors")
	}
	for b.Loop() {
		for _, v := range vs {
			ApplyLayer(v.ID, v.Proto, v.Packets, v.Args)
		}
	}
}

// TRLC-LINKS: REQ-SDS-242
func BenchmarkLayerSPI(b *testing.B) { layerBench(b, "spi") }

// TRLC-LINKS: REQ-SDS-243
func BenchmarkLayerI2C(b *testing.B) { layerBench(b, "i2c") }

// TRLC-LINKS: REQ-SDS-244
func BenchmarkLayerUART(b *testing.B) { layerBench(b, "uart") }

// TRLC-LINKS: REQ-SDS-246
func BenchmarkLayerMisc(b *testing.B) { layerBench(b, "misc") }

// TRLC-LINKS: REQ-SDS-248
func BenchmarkHexToks(b *testing.B) {
	p := []byte{0x9f, 0x03, 0x00, 0x12, 0xab, 0x7f}
	for b.Loop() {
		hexToks(p)
		_ = hx(int64(0x1234), 6)
	}
}

// TRLC-LINKS: REQ-SDS-248
func BenchmarkLayerMatchSPI(b *testing.B) {
	p := mkSPI(0x03, 0x00, 0x12, 0x34, 0xff, 0xff)
	for b.Loop() {
		LayerMatch("spiflash", p)
	}
}

// TRLC-LINKS: REQ-SDS-241
func BenchmarkLayerTablesJS(b *testing.B) {
	for b.Loop() {
		_ = LayerTablesJS()
	}
}
