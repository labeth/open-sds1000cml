// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"reflect"
	"testing"

	"github.com/labeth/open-dso-core/engine/fanout"
	"github.com/labeth/open-dso-core/render"
	"github.com/labeth/open-dso-core/uimodel/panel"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/scpi"
)

// hudFieldsDiffer names the fields in which a and b differ.
// TRLC-LINKS: REQ-SDS-168
func hudFieldsDiffer(a, b render.HUD) []string {
	var out []string
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	for i := range va.NumField() {
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			out = append(out, va.Type().Field(i).Name)
		}
	}
	return out
}

// The core's HUD over the instrument model alone (HALScope's controls,
// the codes kept beside the model) is the HUD over the device's engine in
// the states both compositions are set to alike.
// TRLC-LINKS: REQ-SDS-168
func TestCoreHUDOverTheModel(t *testing.T) {
	rigs := [2]setupRig{engineRig(t), modelRig(t)}
	for _, r := range rigs {
		r.pc.SetInverter(panel.Inverter2(scpi.New(scopeSource{r.ctl, fanout.NewFrames()}, r.afe, r.levels, r.pc, nil, t.Logf)))
	}
	steps := []struct {
		name string
		do   func(r setupRig)
	}{
		{"power-on", func(setupRig) {}},
		{"level set", func(r setupRig) { r.levels.SetTrigLevel(0.4, 0) }},
		{"C2 at 10x, 0.5 V/div, offset, source C2", func(r setupRig) {
			r.afe.SetProbe(1, 10)
			_ = r.afe.SetVdivV(1, 0.5)
			r.levels.SetOffset(1, 0.3)
			r.ctl.SetTrigSource(1)
		}},
		{"zoomed detent, AC", func(r setupRig) {
			_ = r.afe.SetVdivV(0, 0.002)
			_ = r.afe.SetCoupling(0, 1)
		}},
		{"level stepped", func(r setupRig) { r.levels.StepTrigLevel(-12, 1) }},
		{"NORMAL slope trigger", func(r setupRig) {
			r.ctl.SetNorm(true)
			r.ctl.SetTrigType(2)
			r.ctl.SetTrigSource(1)
		}},
		{"serial trigger armed", func(r setupRig) {
			se, ok := r.ctl.(interface {
				SetSerialParams(engine.SerialParams)
				SetSerialMode(int)
			})
			if !ok {
				t.Fatal("no serial trigger on the controls")
			}
			se.SetSerialParams(engine.SerialParams{Pattern: []engine.PatternElem{{Kind: 2, Value: 0xA5, Mask: 0xFF}}})
			se.SetSerialMode(1)
		}},
		{"stopped", func(r setupRig) { r.ctl.SetRunning(false) }},
	}
	for _, s := range steps {
		var huds [2]render.HUD
		for i, r := range rigs {
			s.do(r)
			huds[i] = r.pc.HUD(2)
		}
		if d := hudFieldsDiffer(huds[0], huds[1]); len(d) > 0 {
			t.Errorf("%s: over the model the HUD differs in %v", s.name, d)
		}
		if want, ok := modelShows[s.name]; ok && !want(huds[1]) {
			t.Errorf("%s: the state did not reach the HUD: %+v", s.name, huds[1])
		}
	}
}

// modelShows is what each step's HUD over the model must show.
var modelShows = map[string]func(render.HUD) bool{
	"level set": func(h render.HUD) bool { return h.TrigLvlV != 0 },
	"C2 at 10x, 0.5 V/div, offset, source C2": func(h render.HUD) bool {
		return h.Ch[1].Probe == 10 && h.Ch[1].OffV != 0 && h.TrigSrc == 1
	},
	"zoomed detent, AC":    func(h render.HUD) bool { return h.Ch[0].Zoom > 1 && h.Ch[0].Cpl == 1 },
	"NORMAL slope trigger": func(h render.HUD) bool { return h.Norm && h.TrigType == 2 && h.TrigSrc == 1 },
	"serial trigger armed": func(h render.HUD) bool { return h.SerialArmed && h.SerialMatch == "A5" },
	"stopped":              func(h render.HUD) bool { return !h.Running },
}
