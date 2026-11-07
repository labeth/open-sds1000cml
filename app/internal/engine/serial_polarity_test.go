// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"testing"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
)

// TRLC-LINKS: REQ-SDS-013
func TestInvertedTwoWireSerialTriggers(t *testing.T) {
	for _, proto := range []int{core.SerI2C, core.SerSPI} {
		var a, b []uint8
		if proto == core.SerI2C {
			a, b = i2cWave(0x24, 0, []int{0x55, 0xaa}, 20)
		} else {
			a, b = spiWave([]int{0x55, 0xaa}, 20)
		}
		for i := range a {
			a[i] = 255 - a[i]
			b[i] = 255 - b[i]
		}
		f := &sample.Frame{SampleS: 2e-7, Traces: []sample.Trace{{Codes8: a, Valid: len(a)}, {Ch: 1, Codes8: b, Valid: len(a)}}}
		for _, manual := range []bool{false, true} {
			e := &Engine{}
			p := SerialParams{Proto: proto, ChA: 0, ChB: 1, Inverted: true, MSB: true, Addr: 0x24, RW: 0, Bytes: []int{0x55, 0xaa}, HaveThr: manual, Threshold: 155}
			e.SetSerialParams(p)
			if ok, anchor := e.serialQualify(f, f.Len(), f.SampleS); !ok || anchor < 0 {
				t.Fatalf("proto=%d manual=%t did not match", proto, manual)
			}
			p.Bytes = []int{0xde, 0xad}
			e.SetSerialParams(p)
			if ok, _ := e.serialQualify(f, f.Len(), f.SampleS); ok {
				t.Fatalf("proto=%d matched absent bytes", proto)
			}
		}
	}
}
