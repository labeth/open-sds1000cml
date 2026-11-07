// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"reflect"
	"testing"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/hal/prototrig"
)

// keptTestSpec is a spec with every part the board keeps set: a qualifier,
// a video line, zones and a bus request with bytes and a slicer.
func keptTestSpec() hal.TriggerSpec {
	return hal.TriggerSpec{Stages: []hal.TrigStage{{
		Node: hal.NodeProtocol, Source: hal.Source{Kind: hal.Analog}, Levels: hal.Levels{High: .5, Slope: hal.Rising},
		Qual:  &hal.Qualifier{Op: hal.Inside, MinS: 1e-6, MaxS: 2e-6},
		Video: &hal.Video{Standard: "pal", Line: 5},
		Zones: []hal.Zone{{Source: hal.Source{Kind: hal.Analog}, FromS: -1e-6, ToS: 1e-6, Low: -1, High: 1}},
		Proto: &prototrig.Bus{Request: core.SerialParams{Proto: core.SerUART, Baud: 9600, Bytes: []int{0x41}}, ARINCSlicer: []float64{1, 2, 3, 4}},
	}}}
}

// bump changes v to another value of its type: a pointer gets a changed
// copy (a nil one a zero value), a slice one more element, an interface
// another bus.
func bump(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() { //nolint:exhaustive // the kinds of the trigger spec's fields; any other fails the test
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(v.Uint() + 1)
	case reflect.Float64:
		v.SetFloat(v.Float() + 1)
	case reflect.String:
		v.SetString(v.String() + "x")
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() && v.Field(i).Type().Size() != 0 {
				bump(t, v.Field(i))
				return
			}
		}
		t.Fatalf("%v: no field to change", v.Type())
	case reflect.Pointer:
		n := reflect.New(v.Type().Elem())
		if !v.IsNil() {
			n.Elem().Set(v.Elem())
			bump(t, n.Elem())
		}
		v.Set(n)
	case reflect.Slice:
		v.Set(reflect.Append(v, reflect.Zero(v.Type().Elem())))
	case reflect.Interface:
		v.Set(reflect.ValueOf(&prototrig.Bus{Levels: [2]float64{9}}))
	default:
		t.Fatalf("%v: kind %v not covered by the test", v.Type(), v.Kind())
	}
}

// The kept spec must see every field of the spec and its stage: a change
// it missed would leave the trigger staged for the old spec armed.
// TRLC-LINKS: REQ-SDS-011
func TestKeptSpecSeesEveryField(t *testing.T) {
	b, _, _ := newTestBoard()
	in := b.trigInputs()
	var k keptSpec
	base := keptTestSpec()
	k.keep(&base, &in)
	if s := keptTestSpec(); !k.matches(&s, &in) {
		t.Fatal("the same spec is not known")
	}
	specT := reflect.TypeFor[hal.TriggerSpec]()
	for i := range specT.NumField() {
		s := keptTestSpec()
		bump(t, reflect.ValueOf(&s).Elem().Field(i))
		if k.matches(&s, &in) {
			t.Errorf("TriggerSpec.%s: a change is not seen", specT.Field(i).Name)
		}
	}
	stageT := reflect.TypeFor[hal.TrigStage]()
	for i := range stageT.NumField() {
		s := keptTestSpec()
		bump(t, reflect.ValueOf(&s.Stages[0]).Elem().Field(i))
		if k.matches(&s, &in) {
			t.Errorf("TrigStage.%s: a change is not seen", stageT.Field(i).Name)
		}
	}
	busOfSpec := func(s *hal.TriggerSpec) *prototrig.Bus {
		bus, _ := s.Stages[0].Proto.(*prototrig.Bus)
		return bus
	}
	s := keptTestSpec()
	busOfSpec(&s).Request.Bytes[0] = 0x42
	if k.matches(&s, &in) {
		t.Error("a bus request's byte is not seen")
	}
	// The kept copy is the board's own: the owner reuses its scratch.
	base.Stages[0].Qual.MinS = 5
	base.Stages[0].Zones[0].Low = 7
	busOfSpec(&base).Request.Bytes[0] = 0x43
	busOfSpec(&base).ARINCSlicer[0] = 9
	if s := keptTestSpec(); !k.matches(&s, &in) {
		t.Error("the kept spec changed with the caller's")
	}
	for name, change := range map[string]func(*trigInputs){
		"scale":   func(in *trigInputs) { in.scale[1].PerCode *= 2 },
		"caps":    func(in *trigInputs) { in.caps.UART = !in.caps.UART },
		"offsets": func(in *trigInputs) { in.haveOffsets = true },
	} {
		other := in
		change(&other)
		if s := keptTestSpec(); k.matches(&s, &other) {
			t.Errorf("a change of the board's %s is not seen", name)
		}
	}
}

// Programming and configuring what is staged changes nothing; a change of
// the spec or of the front end it is converted at stages again, and a
// draft of the staged capture is known as it.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-011
func TestRestagingTheStagedChangesNothing(t *testing.T) {
	b, _, _ := newTestBoard()
	fe := offsetFrontEnd{vdiv: [2]float64{1, 1}}
	b.SetFrontEnd(fe)
	src := hal.Source{Kind: hal.Analog}
	edge := func(v float64) hal.TriggerSpec {
		return hal.TriggerSpec{Stages: []hal.TrigStage{{Node: hal.NodeEdge, Source: src, Levels: hal.Levels{High: v, Slope: hal.Rising}}}}
	}
	plan, err := b.Planner().Plan(hal.Request{Channels: []int{0, 1}, TimePerDivS: 1e-3, PretrigFrac: .25})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Trigger().Program(edge(.5)); err != nil {
		t.Fatal(err)
	}
	if err := b.Acquirer().Configure(plan); err != nil {
		t.Fatal(err)
	}
	gen, staged := b.StageGen(), b.Staged()
	if err := b.Trigger().Program(edge(.5)); err != nil {
		t.Fatal(err)
	}
	if err := b.Acquirer().Configure(plan); err != nil {
		t.Fatal(err)
	}
	if b.StageGen() != gen || b.Staged() != staged {
		t.Fatalf("restaged: generation %d -> %d", gen, b.StageGen())
	}
	d := b.Draft()
	if err := d.Program(edge(.5)); err != nil {
		t.Fatal(err)
	}
	if err := d.Configure(plan); err != nil {
		t.Fatal(err)
	}
	if !d.Staged() || d.Config() != staged {
		t.Fatal("the draft of the staged capture is not known as it")
	}
	d.Reset()
	if err := d.Program(edge(.6)); err != nil {
		t.Fatal(err)
	}
	if err := d.Configure(plan); err != nil {
		t.Fatal(err)
	}
	if d.Staged() || d.Config().TriggerLevel == staged.TriggerLevel {
		t.Fatal("a draft of another level is taken for the staged capture")
	}
	if b.StageGen() != gen {
		t.Fatal("a draft staged")
	}
	fe.vdiv[0] = 2
	b.SetFrontEnd(fe)
	if err := b.Trigger().Program(edge(.5)); err != nil {
		t.Fatal(err)
	}
	if b.StageGen() == gen || b.Staged().TriggerLevel == staged.TriggerLevel {
		t.Fatalf("a V/div change left the level at code %d", b.Staged().TriggerLevel)
	}
}
