// ENGMODEL-OWNER-UNIT: FU-APP-MEASURE
package measure

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

// Captured CH1: 1 MHz triangle, 500 MS/s, Q8 calibrated, four-frame average.
// TRLC-LINKS: REQ-SDS-017
func TestHardwareTriangleFrequency(t *testing.T) {
	b, err := os.ReadFile("testdata/triangle-q8.bin")
	if err != nil {
		t.Fatal(err)
	}
	q := make([]uint16, len(b)/2)
	for i := range q {
		q[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	r := ComputeQ8(q, 1, 0, 2e-9)
	if !r.HasTiming || math.Abs(r.Freq/1e6-1) > .01 {
		t.Fatalf("hardware triangle: %+v", r)
	}
	t.Logf("hardware frequency %.3f Hz", r.Freq)
}
