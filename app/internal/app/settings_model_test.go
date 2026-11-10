// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/instrument"
	core "github.com/labeth/open-dso-core/settings"
	"github.com/labeth/open-dso-core/uimodel/panel"
	scope "github.com/labeth/open-sds1000cml/app"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/board"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
	cmlpanel "github.com/labeth/open-sds1000cml/app/internal/panel"
	"github.com/labeth/open-sds1000cml/app/internal/settings"
)

// nullBus is a bus without hardware: the engine is built (never run) so
// its staging setters and snapshot answer.
type nullBus struct{}

// TRLC-LINKS: REQ-SDS-089
func (nullBus) Read(_ uint8, _ uint16) (uint16, error) { return 0, nil }

// TRLC-LINKS: REQ-SDS-089
func (nullBus) Write(_ uint8, _, _ uint16) error { return nil }

// TRLC-LINKS: REQ-SDS-089
func (nullBus) RawWrite(_, _ uint16) error { return nil }

// TRLC-LINKS: REQ-SDS-089
func (nullBus) BurstInto(_, _ []uint8, _ int) {}

// TRLC-LINKS: REQ-SDS-089
func (nullBus) PopWords(_ uint16, _ []uint16, _ int) {}

// TRLC-LINKS: REQ-SDS-089
func (nullBus) FastDrain() bool { return true }

// nullSPI is the front end's SPI without hardware.
type nullSPI struct{}

// TRLC-LINKS: REQ-SDS-089
func (nullSPI) WriteRelay(uint32) error { return nil }

// TRLC-LINKS: REQ-SDS-089
func (nullSPI) WriteGain(_, _ uint8) error { return nil }

// setupRig is a composition's controls, front end, levels, panel and
// board extension: what a setup is collected from and applied through.
type setupRig struct {
	m      *instrument.Model
	e      *engine.Engine // nil over the model alone
	ctl    panel.Engine
	afe    *modelAnalog
	levels *board.Levels
	pc     *panel.Controller
	x      core.Extension
}

// TRLC-LINKS: REQ-SDS-089
func (r setupRig) collect() settings.Settings { return core.Collect(r.ctl, r.afe, r.pc, r.x) }

// TRLC-LINKS: REQ-SDS-089
func (r setupRig) apply(s settings.Settings, logf func(string, ...any)) {
	core.Apply(s, r.ctl, r.afe, r.pc, r.x, logf)
}

// cmlControls are the engine's controls as the engine's composition
// drove them (1.2): staging for the engine's owner over the instrument
// model, the level's volts re-projected when the source changes. The
// compositions over the model alone are compared with it.
type cmlControls struct {
	engine.Controls
	levels *board.Levels
}

// SetTrigSource stages the trigger channel and re-projects the level onto
// it.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-098
func (c cmlControls) SetTrigSource(ch int) {
	c.Controls.SetTrigSource(ch)
	c.levels.Reproject()
}

// projected is fe projected into model m with the levels over eng.
// TRLC-LINKS: REQ-SDS-089
func projected(m *instrument.Model, fe *analog.FrontEnd, eng board.Engine) (*modelAnalog, *board.Levels) {
	afe := &modelAnalog{FrontEnd: fe, m: m}
	levels := board.New(eng, afe)
	levels.Project(m)
	afe.levels = levels
	afe.project(0)
	afe.project(1)
	return afe, levels
}

// engineRig is the device's composition as newBoard builds it: the
// engine's controls over the instrument model, the front end projected
// into the model, the level kept in the model as the comparator's code
// gives it.
// TRLC-LINKS: REQ-SDS-089
func engineRig(tb testing.TB) setupRig {
	tb.Helper()
	m := instrument.New(board.PowerOn(2), nil)
	e := engine.New(engine.Config{Bus: nullBus{}, Logf: tb.Logf, Instrument: m})
	fe := analog.New(nullSPI{}, func(time.Duration) {}, nil)
	fe.OnOffset(e.SetOffsetDAC)
	fe.OnOffsetV(e.SetChannelOffsetV)
	fe.OnVdiv(e.SetChannelVdiv)
	fe.OnCoupling(e.SetChannelCoupling)
	afe, levels := projected(m, fe, e.Controls())
	ctl := cmlControls{e.Controls(), levels}
	pc := panel.New(ctl, afe, levels, cmlpanel.Hardware{}, engine.SupportedTdivs(), uiStartTdiv, tb.Logf)
	return setupRig{m: m, e: e, ctl: ctl, afe: afe, levels: levels, pc: pc, x: board.SettingsExtension{Levels: levels}}
}

// modelRig is the composition over the instrument model alone, as the
// core's HAL cycle runs the scope: HALScope's controls over the board, the
// same front end projected into the model, and the levels over the codes
// kept beside the model.
// TRLC-LINKS: REQ-SDS-089
func modelRig(tb testing.TB) setupRig {
	tb.Helper()
	c := deviceComposition(tb)
	m := instrument.New(board.PowerOn(2), nil)
	m.SetTimebases(engine.SupportedTdivs())
	c.attach(m)
	ctl := panel.NewHALScope(m, c.acq, "").Engine()
	pc := panel.New(ctl, c.afe, c.levels, cmlpanel.Hardware{}, engine.SupportedTdivs(), uiStartTdiv, tb.Logf)
	return setupRig{m: m, ctl: ctl, afe: c.afe, levels: c.levels, pc: pc, x: board.SettingsExtension{Levels: c.levels}}
}

// deviceComposition is the device's composition as newBoard builds it,
// over a board without a fabric and a front end without SPI: the codes
// kept for the model, the front end staging its offsets through them, the
// levels over both, the profile. attach gives it its model.
// TRLC-LINKS: REQ-SDS-089
func deviceComposition(tb testing.TB) *composition {
	tb.Helper()
	b := acq.New(acq.Config{})
	c := &composition{acq: b, codes: board.NewModelCodes(nil)}
	c.fe = analog.New(nullSPI{}, func(time.Duration) {}, nil)
	c.fe.OnOffset(func(ch int, code uint16) {
		c.codes.SetOffsetDAC(ch, code)
		b.StageOffsetDAC(ch, code)
	})
	c.afe = &modelAnalog{FrontEnd: c.fe}
	c.levels = board.New(c.codes, c.afe)
	prof, err := scope.Profile(scope.Identity())
	if err != nil {
		tb.Fatal(err)
	}
	b.SetProfile(prof)
	b.SetFrontEnd(c.fe)
	b.SetLevelGrid(c.levels.TrigLevelGrid)
	return c
}

// goldenSettings is the settings file's golden (the settings package's).
const goldenSettings = "../settings/testdata/scope-settings.json.golden"

// saved is r's setup as the settings file's bytes.
// TRLC-LINKS: REQ-SDS-089
func saved(t *testing.T, r setupRig) []byte {
	t.Helper()
	path := settings.PathIn(t.TempDir())
	if err := core.SaveFile(path, r.collect()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The settings file is the same bytes whether the scope runs its engine
// over the instrument model or the model alone: the golden file loaded
// into either composition and saved again is the golden file, level_code
// and all (ADR-HAL-SETTINGS-FORMAT-KEPT), and a setup the one saves the
// other restores to the same file.
// TRLC-LINKS: REQ-SDS-089, REQ-SDS-072
func TestSettingsFileOverTheModel(t *testing.T) {
	want, err := os.ReadFile(goldenSettings)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := core.LoadFile(goldenSettings, t.Logf)
	if !ok {
		t.Fatal("the golden file does not load")
	}
	rigs := map[string]func(testing.TB) setupRig{"engine over the model": engineRig, "the model alone": modelRig}
	for name, mk := range rigs {
		r := mk(t)
		r.apply(s, t.Logf)
		if got := saved(t, r); string(got) != string(want) {
			t.Fatalf("%s: loaded and saved again:\n got %s\nwant %s", name, got, want)
		}
	}
	// Both ways, after a level step, a source change and a probe change
	// (the code stays where it is, its volts move): what the one saves the
	// other restores to the same file.
	for _, pair := range [][2]func(testing.TB) setupRig{{engineRig, modelRig}, {modelRig, engineRig}} {
		from := pair[0](t)
		from.apply(s, t.Logf)
		from.levels.StepTrigLevel(7, 1)
		from.ctl.SetTrigSource(0)
		from.afe.SetProbe(0, 10)
		raw := saved(t, from)
		mid, ok := core.LoadFile(writeTemp(t, raw), t.Logf)
		if !ok {
			t.Fatal("a saved file does not load")
		}
		to := pair[1](t)
		to.apply(mid, t.Logf)
		if got := saved(t, to); string(got) != string(raw) {
			t.Fatalf("restored across the compositions:\n got %s\nwant %s", got, raw)
		}
		if a, b := from.m.State().Trigger.LevelV(), to.m.State().Trigger.LevelV(); a != b {
			t.Fatalf("the model's level: %v, restored %v", a, b)
		}
	}
}

// writeTemp writes raw to a fresh file and returns its path.
// TRLC-LINKS: REQ-SDS-089
func writeTemp(t *testing.T, raw []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "setup.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The instrument model's power-on state is the engine's: the settings a
// controller reads back before a setup is restored.
// TRLC-LINKS: REQ-SDS-089
func TestPowerOnIsTheEngines(t *testing.T) {
	m := instrument.New(board.PowerOn(2), nil)
	m.SetTimebases(engine.SupportedTdivs())
	e := engine.New(engine.Config{Bus: nullBus{}, Logf: t.Logf, Instrument: instrument.New(board.PowerOn(2), nil)})
	a, b := e.Controls().Snapshot(), m.Snapshot()
	type set struct {
		Tdiv, Prec          float64
		Avg, Eres, Acq, Src int
		Running, Norm, Rise bool
		Qual                engine.TrigQual
	}
	of := func(st engine.Stats) set {
		return set{st.TdivS, st.PrecisionRateHz, st.AvgCount, st.EresLen, st.AcqMode, st.TrigSource, st.Running, st.Norm, st.TrigRising, st.TrigQual}
	}
	if of(a) != of(b) {
		t.Fatalf("power-on: engine %+v, model %+v", of(a), of(b))
	}
}
