// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"reflect"
	"testing"

	"github.com/labeth/open-dso-core/engine/fanout"
	"github.com/labeth/open-dso-core/render"
	"github.com/labeth/open-dso-core/uimodel/panel"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/lcd"
	"github.com/labeth/open-sds1000cml/app/internal/scpi"
)

// hudComposition is the device's composition over r, with the SCPI
// shadow's display invert on the CH tabs as newBoard wires it.
// TRLC-LINKS: REQ-SDS-168
func hudComposition(t *testing.T, r setupRig) *composition {
	t.Helper()
	ctl, ok := r.ctl.(cmlControls)
	if !ok {
		t.Fatal("the engine's composition drives cmlControls")
	}
	c := &composition{e: r.e, ctl: ctl, fe: r.afe.FrontEnd, afe: r.afe, levels: r.levels, pc: r.pc}
	c.scpi = scpi.New(scopeSource{ctl, fanout.NewFrames()}, r.afe, r.levels, r.pc, nil, t.Logf)
	r.pc.SetInverter(panel.Inverter2(c.scpi))
	return c
}

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

// The core's HUD over the device's controls, front end and levels is the
// HUD the device's own hud builds, field by field, in every state a
// controller can set here: the scope can draw from the core's HUD.
// TRLC-LINKS: REQ-SDS-168
func TestCoreHUDIsTheDevicesHUD(t *testing.T) {
	r := engineRig(t)
	c := hudComposition(t, r)
	e, pc := r.e, r.pc
	view := func(v int) {
		vs := pc.SettingsView()
		vs.ViewMode, vs.UI.Set = v, false // the view alone, not the workspace's
		pc.ApplySettingsView(vs)
	}
	steps := []struct {
		name string
		do   func()
	}{
		{"power-on", func() {}},
		{"level set", func() { r.levels.SetTrigLevel(0.4, 0) }},
		{"C2 at 10x, 0.5 V/div, offset", func() {
			r.afe.SetProbe(1, 10)
			_ = r.afe.SetVdivV(1, 0.5)
			r.levels.SetOffset(1, 0.3)
			c.ctl.SetTrigSource(1)
		}},
		{"zoomed detent, AC", func() {
			_ = r.afe.SetVdivV(0, 0.002)
			_ = r.afe.SetCoupling(0, 1)
		}},
		{"NORMAL slope trigger", func() {
			c.ctl.SetNorm(true)
			c.ctl.SetTrigType(2)
		}},
		{"serial trigger armed", func() {
			c.ctl.SetSerialParams(engine.SerialParams{Pattern: []engine.PatternElem{{Kind: 2, Value: 0xA5, Mask: 0xFF}}})
			c.ctl.SetSerialMode(1)
		}},
		{"zones", func() {
			e.SetZones([]engine.Zone{{DtLoS: -1e-4, DtHiS: 1e-4, CodeLo: 100, CodeHi: 150}})
			e.SetZoneMode(1)
		}},
		{"INVS C2", func() { c.scpi.HandleLine([]byte("C2:INVS ON\n")) }},
		{"BODE view", func() { view(3) }},
		{"BODE view left", func() { view(0) }},
		{"stopped", func() { c.ctl.SetRunning(false) }},
	}
	for _, s := range steps {
		s.do()
		dev := c.hud()
		core := pc.HUD(2)
		core.URL = lcd.DeviceURL() // the display gives the address (Display.URL)
		if d := hudFieldsDiffer(dev, core); len(d) > 0 {
			t.Errorf("%s: the core's HUD differs in %v", s.name, d)
		}
		if want, ok := shows[s.name]; ok && !want(dev) {
			t.Errorf("%s: the state did not reach the HUD: %+v", s.name, dev)
		}
	}
}

// shows is what each step's HUD must show, so the comparison compares
// what the step set.
var shows = map[string]func(render.HUD) bool{
	"level set":                    func(h render.HUD) bool { return h.TrigLvlV != 0 && h.TrigLvlDiv != 0 },
	"C2 at 10x, 0.5 V/div, offset": func(h render.HUD) bool { return h.Ch[1].Probe == 10 && h.Ch[1].OffV != 0 && h.TrigSrc == 1 },
	"zoomed detent, AC":            func(h render.HUD) bool { return h.Ch[0].Zoom > 1 && h.Ch[0].Cpl == 1 },
	"NORMAL slope trigger":         func(h render.HUD) bool { return h.Norm && h.TrigType == 2 },
	"serial trigger armed":         func(h render.HUD) bool { return h.SerialArmed && h.SerialMatch == "A5" },
	"zones":                        func(h render.HUD) bool { return h.ZoneTrig && len(h.Zones) == 1 },
	"INVS C2":                      func(h render.HUD) bool { return h.Ch[1].Inv },
	"BODE view":                    func(h render.HUD) bool { return h.ViewMode == 3 },
	"stopped":                      func(h render.HUD) bool { return !h.Running },
}

// The core's HUD over the instrument model alone (HALScope's controls,
// the codes kept beside the model) is the HUD over the device's engine in
// the states both compositions are set to alike.
// TRLC-LINKS: REQ-SDS-168
func TestCoreHUDOverTheModel(t *testing.T) {
	rigs := [2]setupRig{engineRig(t), modelRig(t)}
	for _, r := range rigs {
		r.pc.SetInverter(panel.Inverter2(scpi.New(scopeSource{cmlControls{}, fanout.NewFrames()}, r.afe, r.levels, r.pc, nil, t.Logf)))
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
