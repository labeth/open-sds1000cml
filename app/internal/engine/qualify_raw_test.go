// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"testing"
)

// The FPGA's pulse, slope and video qualifier fires on decimated records
// too (its fire held until the record takes it: TestQualifierDecimatedTopRTL),
// so a qualifier trigger plans as an edge does: 1ms/div decimated, no
// timebase cap, and Average, ERES and Precision on their display-sized
// decimated captures. An image that records raw samples only still caps the
// timebase at its record's screen.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-011
func TestQualifierTriggerPlansAsAnEdge(t *testing.T) {
	for _, typ := range []TrigType{TrigEdge, TrigPulse, TrigSlope, TrigVideo} {
		e := &Engine{hardwareQualifier: true, band: Band{TdivS: 1e-3}}
		e.tp.typ = typ
		e.trigPosFrac.Store(math.Float64bits(.5))
		_, p, tdiv, _, _ := e.sramConfig()
		if tdiv != 1e-3 || e.stats.TdivMaxS != 0 || p.Log == 0 {
			t.Fatalf("type %d at 1ms/div: timebase %g (most %g), log %d; want 1ms/div decimated", typ, tdiv, e.stats.TdivMaxS, p.Log)
		}
		for _, acq := range []int32{AcqAverage, AcqEres, AcqPrecision} {
			e := &Engine{hardwareQualifier: true, band: Band{TdivS: 20e-6}}
			e.tp.typ = typ
			e.acqMode.Store(acq)
			e.trigPosFrac.Store(math.Float64bits(.5))
			if _, p, _, _, _ := e.sramConfig(); p.Log == 0 {
				t.Fatalf("type %d, acquisition %d at 20us/div: the full rate", typ, acq)
			}
		}
	}
	e := &Engine{rawOnly: true, band: Band{TdivS: 1e-3}}
	e.trigPosFrac.Store(math.Float64bits(.5))
	if _, _, tdiv, _, _ := e.sramConfig(); tdiv != 200e-6 || e.stats.TdivMaxS != 200e-6 {
		t.Fatalf("a raw-only image at 1ms/div: %g (most %g), want 200us/div", tdiv, e.stats.TdivMaxS)
	}
}
