// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// TRLC-LINKS: REQ-SDS-010, REQ-SDS-021
func BenchmarkConditionView(b *testing.B) {
	bd := New(Config{})
	cal := &InterleaveCalibration{}
	for k := range cal.Offset[0] {
		cal.Offset[0][k], cal.Offset[1][k] = 0.3*float64(k)-0.5, -0.2*float64(k)
	}
	bd.SetInterleaveCalibration(cal)
	m := sramcapture.Metadata{SampleRateHz: 500e6, SamplesPerWord: 2, Decimation: 1}
	const n = 2048
	src := make([]uint8, n)
	for i := range src {
		src[i] = uint8(128 + 60*((i/97)%2) - 30)
	}
	f := &sample.Frame{Traces: []sample.Trace{{Codes8: make([]uint8, n), Valid: n}, {Ch: 1, Codes8: make([]uint8, n), Valid: n}}}
	for b.Loop() {
		copy(f.Codes8(0), src)
		copy(f.Codes8(1), src)
		bd.conditionView(m, 4, f)
	}
}
