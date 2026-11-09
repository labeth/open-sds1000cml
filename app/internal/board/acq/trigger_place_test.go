// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"math"
	"testing"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// placeFrame is a record of a pulse: low, a rising edge at sample 400, high,
// its closing (falling) edge at sample 600, low again; the hardware's index
// at idx.
func placeFrame(idx, sampleS float64) *sample.Frame {
	n := 1000
	c := make([]uint8, n)
	for i := range c {
		c[i] = 60
		if i >= 400 && i < 600 {
			c[i] = 200
		}
	}
	f := &sample.Frame{TriggerAt: idx, SampleS: sampleS, Content: sample.RawFullRate, Traces: []sample.Trace{{Ch: 0, Codes8: c}}}
	f.SetLen(n)
	f.Status.Triggered = true
	return f
}

// A zone trigger's point moves back by the zones' fire delay in every frame
// of the record; a pulse qualifier's moves onto its closing edge's level
// crossing; an untriggered record, or a plain edge, keeps the index.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-014
func TestTriggerPlacement(t *testing.T) {
	b, _, _ := newTestBoard()
	ws := []hal.Window{{Role: sample.Main}, {Role: sample.Edge}}
	b.recStatus.Triggered = true

	b.armedPlace = triggerPlace{zoneDelayS: 100e-9}
	main, edge := placeFrame(700, 2e-9), placeFrame(300, 1e-9)
	b.placeTrigger(ws, []*sample.Frame{main, edge})
	if main.TriggerAt != 650 || edge.TriggerAt != 200 {
		t.Fatalf("zones: main at %g, edge at %g", main.TriggerAt, edge.TriggerAt)
	}

	qc := sramcapture.QualifierConfig{Enabled: true, Kind: sramcapture.QualPulse, Negative: true}
	b.armedPlace = triggerPlace{qual: qc, channel: 0, level: 130}
	late := float64(sramcapture.QualFireNs) * 1e-9 / 2e-9
	main = placeFrame(600+late+1, 2e-9) // the qualifier fires QualFireNs after the edge, in the word after it
	b.placeTrigger(ws[:1], []*sample.Frame{main})
	if math.Abs(main.TriggerAt-600) > 1 || main.TrigNode != QualName(sramcapture.QualPulse) {
		t.Fatalf("pulse: at %g node %q, want the closing edge near 600", main.TriggerAt, main.TrigNode)
	}

	b.recStatus.Triggered = false
	main = placeFrame(700, 2e-9)
	b.armedPlace = triggerPlace{zoneDelayS: 100e-9}
	b.placeTrigger(ws[:1], []*sample.Frame{main})
	b.recStatus.Triggered = true
	b.armedPlace = triggerPlace{}
	b.placeTrigger(ws[:1], []*sample.Frame{main})
	if main.TriggerAt != 700 {
		t.Fatalf("untriggered or plain: at %g", main.TriggerAt)
	}
}
