// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/labeth/open-dso-core/instrument"
)

// The trigger keeps one source, slope and level across the types over the
// instrument model, as the engine's one comparator kept them
// (ADR-CML-ONE-TRIGGER-SOURCE): every type picked after a source, slope or
// level change reads back the same in both compositions.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-098
func TestOneTriggerSourceAcrossTypes(t *testing.T) {
	rigs := []setupRig{modelRig(t)}
	type readBack struct {
		Type, Src  int
		Rising, OK bool
		Level      float64
	}
	raw, err := os.ReadFile("testdata/trigger.json")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]readBack{}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	read := func(r setupRig) readBack {
		st := r.ctl.Snapshot()
		v, ok := r.levels.TrigLevel(st.TrigSource)
		return readBack{st.TrigType, st.TrigSource, st.TrigRising, ok, v}
	}
	steps := []struct {
		name string
		do   func(r setupRig)
	}{
		{"pulse before any change", func(r setupRig) { r.ctl.SetTrigType(1) }},
		{"edge again", func(r setupRig) { r.ctl.SetTrigType(0) }},
		{"source C2, falling, level", func(r setupRig) {
			r.ctl.SetTrigSource(1)
			r.ctl.SetTrigSlope(false)
			r.levels.SetTrigLevel(0.3, 1)
		}},
		{"pulse", func(r setupRig) { r.ctl.SetTrigType(1) }},
		{"slope", func(r setupRig) { r.ctl.SetTrigType(2) }},
		{"source C1 on slope", func(r setupRig) { r.ctl.SetTrigSource(0) }},
		{"video", func(r setupRig) { r.ctl.SetTrigType(3) }},
		{"rising on video", func(r setupRig) { r.ctl.SetTrigSlope(true) }},
		{"edge", func(r setupRig) { r.ctl.SetTrigType(0) }},
		{"level stepped on edge", func(r setupRig) { r.levels.StepTrigLevel(5, 0) }},
		{"pulse after the step", func(r setupRig) { r.ctl.SetTrigType(1) }},
	}
	for _, s := range steps {
		var got [2]readBack
		for _, r := range rigs {
			s.do(r)
			got[1] = read(r)
		}
		var ok bool
		got[0], ok = want[s.name]
		if !ok {
			t.Fatalf("no recorded trigger for %q", s.name)
		}
		if got[0] != got[1] {
			t.Errorf("%s: engine %+v, model %+v", s.name, got[0], got[1])
		}
	}
	// the model keeps the shared values in the running type's own settings
	st := rigs[0].m.State()
	if src := st.Trigger.Source().Index; src != 0 || st.Trigger.Type != instrument.TrigPulse {
		t.Fatalf("the model's running type %v on source %d", st.Trigger.Type, src)
	}
}
