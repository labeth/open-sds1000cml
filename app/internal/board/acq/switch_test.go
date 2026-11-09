// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/superres"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// levelWrites are the comparator DAC codes the panel plane latched (lane
// A's high byte write closes a code).
func levelWrites(p *cs3Log) []uint16 {
	var out []uint16
	var lo uint16
	for _, w := range p.writes {
		switch w[0] {
		case cs3LevelALo:
			lo = w[1]
		case cs3LevelAHi:
			out = append(out, w[1]<<8|lo)
		}
	}
	return out
}

// The board stages the comparator DAC from the controllers' level when it
// programs a trigger: nothing while the level was never set (the boot
// comparator stays), the first code once set, then only a code that moved,
// whatever the trigger type, as the engine staged its one comparator.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-098
func TestLevelDACFromTheModelsLevel(t *testing.T) {
	b, _, p := newTestBoard("qualifier")
	code, set := uint16(0), false
	b.SetLevelDAC(func() (uint16, bool) { return code, set })
	edge := hal.TriggerSpec{Stages: []hal.TrigStage{{Node: hal.NodeEdge, Source: hal.Source{Kind: hal.Analog}, Levels: hal.Levels{High: .5, Slope: hal.Rising}}}}
	program := func(s hal.TriggerSpec) {
		t.Helper()
		if err := b.Trigger().Program(s); err != nil {
			t.Fatal(err)
		}
		b.Platform().Service()
	}
	program(edge)
	if w := levelWrites(p); len(w) != 0 {
		t.Fatalf("a level never set latched %v", w)
	}
	code, set = 30000, true
	program(edge)
	program(edge)
	pulse := edge
	pulse.Stages = []hal.TrigStage{{Node: hal.NodePulse, Source: hal.Source{Kind: hal.Analog}, Levels: hal.Levels{Slope: hal.Rising}, Qual: &hal.Qualifier{Op: hal.Longer, MinS: 1e-6}}}
	program(pulse)
	code = 30100
	program(pulse)
	if w := levelWrites(p); !slices.Equal(w, []uint16{30000, 30100}) {
		t.Fatalf("latched %v, want the first code and the moved one", w)
	}
}

// A decoded stream's record is read as the engine's stopped loop read an
// adopted record: a full-rate record at 2 ns, a decimated one at its own
// rate and decimation, its screen at the timebase now, on an edge.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-013
func TestAdoptedRecordIsReadAtItsOwnRate(t *testing.T) {
	b, _, _ := newTestBoard()
	p, err := b.Planner().Plan(hal.Request{Channels: []int{0, 1}, TimePerDivS: 1e-3})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Acquirer().Configure(p); err != nil {
		t.Fatal(err)
	}
	full := sramcapture.Metadata{Length: 1 << 18, TriggerIndex: 1 << 17, SamplesPerWord: 2, Triggered: true, Frozen: true, Ready: true}
	b.AdoptRecord(full, sramcapture.Config{})
	if a := b.armedPlan; a.SampleS != 2e-9 || a.AcqDecim != 1 || a.Depth != 1<<19 || a.WinSamples != 1<<19 || b.armedNode != hal.NodeEdge || !b.recStatus.Triggered {
		t.Fatalf("a full-rate record: %+v node %v status %+v", a, b.armedNode, b.recStatus)
	}
	dec := sramcapture.Metadata{Length: 1 << 16, TriggerIndex: 1 << 15, SamplesPerWord: 1, SampleRateHz: 500e6 / 256, Frozen: true, Ready: true}
	b.AdoptRecord(dec, sramcapture.Config{DecimationLog2: 8})
	want := RetainedSRAMPlan(SRAMPlan{Log: 8, Samples: 1 << 16, SampleS: 256 / 500e6}, 1e-3)
	if a := b.armedPlan; a.SampleS != want.SampleS || a.AcqDecim != 256 || a.Depth != want.Samples || a.WinSamples != want.Screen || b.Acquirer().State() != hal.Ready {
		t.Fatalf("a decimated record: %+v, want %+v", a, want)
	}
}

// The bus errors are counted on the board (the status's bus_errors), and
// ARINC 429's slicer the cycle learned is kept as the bus trigger
// programmed it, for a decoded stream's receiver.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-013
func TestBusErrorsAndARINCOnTheBoard(t *testing.T) {
	b, _, _ := newTestBoard()
	for range 3 {
		b.NoteBusError(errors.New("bus"))
	}
	if b.BusErrors() != 3 {
		t.Fatalf("%d bus errors", b.BusErrors())
	}
	if b.ARINCAux() != 0 {
		t.Fatal("a slicer before any was learned")
	}
	b.arincAux.Store(0x8d72a15c)
	if b.ARINCAux() != 0x8d72a15c {
		t.Fatal("the slicer is not kept")
	}
}

// A trigger waiting for an image says it as the engine said it, and the
// hold in BUS keeps the qualifier image only, while the owner's measuring
// keeps the fast image too.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-223
func TestImageNamesAndHolds(t *testing.T) {
	if imageCalled(ImageQualify) != "qualifier" || imageCalled(ImageBits) != "digital stream" || imageCalled(ImageGeneral) != ImageGeneralName || imageCalled(ImageStream) != ImageStream {
		t.Fatal("the images' called names")
	}
	b, _, _ := newTestBoard()
	edge := hal.TriggerSpec{Stages: []hal.TrigStage{{Node: hal.NodeEdge, Source: hal.Source{Kind: hal.Analog}}}}
	im := (*imagesFacet)(b)
	for _, c := range []struct {
		loaded          string
		measuring, held bool
		want            string
	}{
		{ImageFast, false, true, ImageGeneral},
		{ImageFast, true, false, ImageFast},
		{ImageQualify, false, true, ImageQualify},
		{ImageQualify, true, false, ImageQualify},
		{ImageQualify, false, false, ImageGeneral},
	} {
		b.imageLoaded = c.loaded
		if got := im.ChooseHeld(edge, c.measuring, c.held); !slices.Equal(got, []string{FeatureOf(c.want)}) {
			t.Errorf("%s loaded, measuring %v held %v: %v, want %s", c.loaded, c.measuring, c.held, got, c.want)
		}
	}
}

// An image load and a stacking session beat the owner's heartbeat between
// their steps: the halt, the configuration, the probes, the converters.
// TRLC-LINKS: REQ-SDS-025, REQ-SDS-005, REQ-SDS-141
func TestLongWorkBeats(t *testing.T) {
	b, fm, _ := newTestBoard()
	b.mem = &stackMem{fakeMem: fm}
	b.panel.bus = nil
	b.SetImages(&stackSwitcher{})
	b.SetConverterPort(ConverterPort{
		Snapshot: func(context.Context) ([10]uint8, error) { return [10]uint8{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, nil },
		Restart:  func() error { return nil },
	})
	beats := 0
	b.SetBeat(func() { beats++ })
	if err := b.loadFresh(ImageGeneral); err != nil {
		t.Fatal(err)
	}
	if beats < 4 {
		t.Fatalf("%d beats in an image load", beats)
	}
	beats = 0
	if _, err := b.StackSession(context.Background(), sramcapture.Config{}, superres.StackRequest{Records: 2}); err == nil || beats < 4 {
		t.Fatalf("%d beats in a stacking session (%v)", beats, err)
	}
}
