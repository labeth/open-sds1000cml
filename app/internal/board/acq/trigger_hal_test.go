// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"testing"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/hal/prototrig"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// offsetFrontEnd is a front end at its own V/div and offset on each channel.
type offsetFrontEnd struct {
	hal.FrontEnd
	vdiv, off [2]float64
}

func (f offsetFrontEnd) Get(ch int) hal.Channel {
	return hal.Channel{On: true, VoltsDiv: f.vdiv[ch&1], OffsetV: f.off[ch&1], Probe: 1}
}

// A level the owner holds in codes reaches Program in volts (CodeVolts) and
// comes back as the same code at every V/div and offset, half codes of a
// learned bus threshold too.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-013
func TestLevelsRoundTripThroughVolts(t *testing.T) {
	src := hal.Source{Kind: hal.Analog, Index: 1}
	for _, fe := range []hal.FrontEnd{nil, offsetFrontEnd{vdiv: [2]float64{0.05, 0.5}, off: [2]float64{0.3, -1.7}}, offsetFrontEnd{vdiv: [2]float64{2, 20}, off: [2]float64{0, 37.5}}} {
		b, _, _ := newTestBoard("uart")
		b.SetFrontEnd(fe)
		for code := range 256 {
			spec := hal.TriggerSpec{Stages: []hal.TrigStage{{Node: hal.NodeEdge, Source: src, Levels: hal.Levels{High: b.CodeVolts(1, float64(code)), Slope: hal.Rising}}}}
			if err := b.Trigger().Program(spec); err != nil {
				t.Fatal(err)
			}
			if got := b.Staged().TriggerLevel; int(got) != code {
				t.Fatalf("front end %v: code %d came back %d", fe, code, got)
			}
		}
		for th := 1.0; th < 255; th += 0.5 {
			req := core.SerialParams{Proto: core.SerUART, ChA: 1, Baud: 115200, Bits: 8, Threshold: th, HaveThr: true, Addr: -1, RW: 2}
			bus := &prototrig.Bus{Request: req, Levels: [2]float64{b.CodeVolts(1, th), b.CodeVolts(0, 0)}}
			spec := hal.TriggerSpec{Stages: []hal.TrigStage{{Node: hal.NodeProtocol, Source: src, Proto: bus}}}
			if err := b.Trigger().Program(spec); err != nil {
				t.Fatal(err)
			}
			var want sramcapture.Config
			b.PlanSerial(&want, req, false, 0)
			if c := b.Staged(); c.UART != want.UART || c.TriggerLevel != want.TriggerLevel {
				t.Fatalf("front end %v: threshold %v planned %+v level %d, want %+v level %d", fe, th, c.UART, c.TriggerLevel, want.UART, want.TriggerLevel)
			}
		}
	}
}

// A qualifier's limits in seconds plan the same counter as the nanoseconds
// they were set in.
// TRLC-LINKS: REQ-SDS-011
func TestQualifierLimitsRoundTrip(t *testing.T) {
	for _, ns := range []float64{0, 2.5, 4, 10, 12, 100, 333.333, 1000, 4096, 1e5, 123456.789, 1e9, 1e10} {
		if got := nanos(ns / 1e9); got != ns {
			t.Fatalf("%v ns came back %v", ns, got)
		}
	}
	b, _, _ := newTestBoard("qualifier")
	src := hal.Source{Kind: hal.Analog}
	for _, c := range []struct {
		op         hal.QualOp
		cond       uint8
		minS, maxS float64
		minNs      float64 // the fabric's minimum register
		maxNs      float64
	}{
		{0, sramcapture.QualAny, 1e-6, 2e-6, 1000, 2000},
		{hal.Shorter, sramcapture.QualUnder, 3e-6, 10e-9, 10, 3000},
		{hal.Longer, sramcapture.QualOver, 10e-9, 3e-6, 3000, 10},
		{hal.Inside, sramcapture.QualBetween, 10e-9, 3e-6, 10, 3000},
	} {
		spec := hal.TriggerSpec{Stages: []hal.TrigStage{{Node: hal.NodePulse, Source: src, Levels: hal.Levels{Slope: hal.Rising}, Qual: &hal.Qualifier{Op: c.op, MinS: c.minS, MaxS: c.maxS}}}}
		if err := b.Trigger().Program(spec); err != nil {
			t.Fatal(err)
		}
		pre, lo, hi := QualTicks(c.minNs, c.maxNs)
		if q := b.Staged().Qualifier; q.Cond != c.cond || q.Prescale != pre || q.MinTicks != lo || q.MaxTicks != hi {
			t.Fatalf("op %d: %+v", c.op, q)
		}
	}
}

// A free-running trigger stages what the fabric takes of it, unarmed; the
// zones a stage asks are refused without the qualifier image, and kept as
// the record's room before the trigger with it. Program and Configure may
// come in either order.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-014
func TestFreeRunZonesAndOrder(t *testing.T) {
	src := hal.Source{Kind: hal.Analog}
	zone := []hal.Zone{{Source: src, FromS: -1e-6, ToS: 1e-6, Low: -1, High: 1}}
	edge := hal.TrigStage{Node: hal.NodeEdge, Source: src, Levels: hal.Levels{Slope: hal.Rising}, Zones: zone}
	b, _, _ := newTestBoard()
	if _, ok := hal.AsRefusal(b.Trigger().Program(hal.TriggerSpec{Stages: []hal.TrigStage{edge}})); !ok {
		t.Fatal("zones without the qualifier image: not refused")
	}
	if err := b.Trigger().Program(hal.TriggerSpec{Stages: []hal.TrigStage{edge}, FreeRun: true}); err != nil || b.Staged().Normal || b.Staged().Zones.Enabled() {
		t.Fatalf("free-running zones: %v %+v", err, b.Staged())
	}
	b, _, _ = newTestBoard("qualifier")
	plan := hal.Plan{Request: hal.Request{PretrigFrac: .5}, SampleS: 32e-9, Depth: 1 << 16, AcqDecim: 16}
	if err := b.Trigger().Program(hal.TriggerSpec{Stages: []hal.TrigStage{edge}}); err != nil {
		t.Fatal(err)
	}
	if err := b.Acquirer().Configure(plan); err != nil {
		t.Fatal(err)
	}
	c := b.Staged()
	_, delayS, _ := b.PlanZones(zone)
	if !c.Normal || !c.Zones.Enabled() || c.PreWords != 1<<15+uint32(delayS/32e-9) || c.PreWords+c.PostWords != 1<<16 {
		t.Fatalf("zones: %+v", c)
	}
	d := b.Draft()
	if err := d.Configure(plan); err != nil {
		t.Fatal(err)
	}
	if err := d.Program(hal.TriggerSpec{Stages: []hal.TrigStage{edge}}); err != nil || d.Config() != c {
		t.Fatalf("drafted the other way round: %v", err)
	}
	plan.Request.PretrigRoom = 100
	if err := d.Configure(plan); err != nil || d.Config().PreWords != c.PreWords+100 {
		t.Fatalf("room before the trigger: %v %d", err, d.Config().PreWords)
	}
}
