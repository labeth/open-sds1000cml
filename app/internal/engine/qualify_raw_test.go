// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"testing"
)

// The FPGA's pulse, slope and video qualifier fires only on full-rate
// records: on a decimated one (500us/div and slower, or Average, ERES and
// Precision's display-sized live captures) it never fired - Auto ran free,
// Normal waited forever. With a qualifier trigger the capture stays at the
// full rate: the timebase stops at the raw record's screen and the filtered
// modes read full-rate captures.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-011
func TestQualifierTriggerKeepsTheFullRate(t *testing.T) {
	for _, typ := range []TrigType{TrigEdge, TrigPulse, TrigSlope, TrigVideo} {
		e := &Engine{hardwareQualifier: true, band: Band{TdivS: 1e-3}}
		e.tp.typ = typ
		e.trigPosFrac.Store(math.Float64bits(.5))
		_, p, tdiv, _, _ := e.sramConfig()
		want, most := 1e-3, 0.0
		if typ != TrigEdge {
			want, most = 200e-6, 200e-6
		}
		if tdiv != want || e.stats.TdivMaxS != most {
			t.Fatalf("type %d at 1ms/div: timebase %g (most %g), want %g", typ, tdiv, e.stats.TdivMaxS, want)
		}
		if typ != TrigEdge && p.Log != 0 {
			t.Fatalf("type %d: a decimated record (log %d)", typ, p.Log)
		}
		for _, acq := range []int32{AcqAverage, AcqEres, AcqPrecision} {
			e := &Engine{hardwareQualifier: true, band: Band{TdivS: 20e-6}}
			e.tp.typ = typ
			e.acqMode.Store(acq)
			e.trigPosFrac.Store(math.Float64bits(.5))
			_, p, _, _, _ := e.sramConfig()
			if (typ != TrigEdge) != (p.Log == 0) {
				t.Fatalf("type %d, acquisition %d at 20us/div: decimation log %d", typ, acq, p.Log)
			}
		}
	}
}
