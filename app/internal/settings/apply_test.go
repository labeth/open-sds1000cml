// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS

package settings_test

import (
	"math"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/instrument"
	core "github.com/labeth/open-dso-core/settings"
	"github.com/labeth/open-dso-core/uimodel/panel"
	scope "github.com/labeth/open-sds1000cml/app"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/board"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	cmlpanel "github.com/labeth/open-sds1000cml/app/internal/panel"
	engine "github.com/labeth/open-sds1000cml/app/internal/scopeapi"
	"github.com/labeth/open-sds1000cml/app/internal/settings"
)

// nullSPI satisfies analog.Transport so the real FrontEnd (with its per-tier
// offset law, ladder and emit tracking) runs against no hardware.
// TRLC-LINKS: REQ-SDS-072
type nullSPI struct{ relays, gains int }

// TRLC-LINKS: REQ-SDS-072
func (n *nullSPI) WriteRelay(_ uint32) error { n.relays++; return nil }

// TRLC-LINKS: REQ-SDS-072
func (n *nullSPI) WriteGain(_, _ uint8) error { n.gains++; return nil }

// fixtureAnalog projects the real front end's changes as the app's controls do.
type fixtureAnalog struct {
	*analog.FrontEnd
	m      *instrument.Model
	levels *board.Levels
}

func (a *fixtureAnalog) project(ch int) {
	c := a.Get(ch)
	_ = a.m.Change(func(s *instrument.State) error { s.Channels[ch] = c; return nil })
	if a.levels != nil {
		a.levels.Reproject()
	}
}

func (a *fixtureAnalog) SetVdivV(ch int, v float64) error {
	err := a.FrontEnd.SetVdivV(ch, v)
	a.project(ch)
	return err
}
func (a *fixtureAnalog) SetProbe(ch int, v float64) { a.FrontEnd.SetProbe(ch, v); a.project(ch) }
func (a *fixtureAnalog) SetCoupling(ch, v int) error {
	err := a.FrontEnd.SetCoupling(ch, v)
	a.project(ch)
	return err
}

func (a *fixtureAnalog) SetBWL(ch int, on bool) error {
	err := a.FrontEnd.SetBWL(ch, on)
	a.project(ch)
	return err
}

func (a *fixtureAnalog) SetOffset(ch int, v float64) uint16 {
	got := a.FrontEnd.SetOffset(ch, v)
	a.project(ch)
	return got
}

// settingsEngine is the HAL controls with the optional settings capabilities.
type settingsEngine interface {
	panel.Engine
	MemDepth() int
	SetPrecisionRate(float64) float64
	SetRollStream(bool)
	SetBodeMode(bool, int, int)
	SetSerialParams(engine.SerialParams)
	SerialConfig() engine.SerialParams
	SetSerialMode(int)
}
type fixtureEngine struct {
	settingsEngine
	codes *board.ModelCodes
	m     *instrument.Model
}

func (e *fixtureEngine) Snapshot() engine.Stats {
	st := e.settingsEngine.Snapshot()
	codes := e.codes.Snapshot()
	st.TrigCode, st.OffC1, st.OffC2 = codes.TrigCode, codes.OffC1, codes.OffC2
	return st
}

func rig(tb testing.TB) (*fixtureEngine, *fixtureAnalog, *panel.Controller, *nullSPI) {
	tb.Helper()
	m := instrument.New(board.PowerOn(2), nil)
	m.SetTimebases(engine.SupportedTdivs())
	codes := board.NewModelCodes(m)
	spi := &nullSPI{}
	fe := &fixtureAnalog{FrontEnd: analog.New(spi, func(time.Duration) {}, nil), m: m}
	fe.OnOffset(codes.SetOffsetDAC)
	b := acq.New(acq.Config{})
	p, err := scope.Profile(scope.Identity())
	if err != nil {
		tb.Fatal(err)
	}
	b.SetProfile(p)
	b.SetFrontEnd(fe)
	ctl, ok := panel.NewHALScope(m, b, "").Engine().(settingsEngine)
	if !ok {
		tb.Fatal("HAL controls lack settings capabilities")
	}
	e := &fixtureEngine{ctl, codes, m}
	lv := board.New(codes, fe)
	lv.Project(m)
	fe.levels = lv
	fe.project(0)
	fe.project(1)
	pc := panel.New(e, fe, lv, cmlpanel.Hardware{}, engine.SupportedTdivs(), 500e-6, tb.Logf)
	return e, fe, pc, spi
}

func ext(e *fixtureEngine, fe *fixtureAnalog) core.Extension {
	var front board.FrontEnd
	if fe != nil {
		front = fe
	}
	lv := board.New(e.codes, front)
	lv.Project(e.m)
	return board.SettingsExtension{Levels: lv}
}

// TRLC-LINKS: REQ-SDS-072
func TestApplyRestoreCollectFidelity(t *testing.T) {
	e, fe, pc, _ := rig(t)
	want := settings.Settings{
		Version: settings.Version,
		TdivS:   1e-3, // ladder detent — accepted (staged; stats reflect it only after a frame)
		Ch: [2]settings.Channel{
			{VdivV: 0.5, OffsetV: 1.5, OffsetSet: true, Coupling: 1, Probe: 10, BWL: true},
			{VdivV: 2, OffsetV: -2.5, OffsetSet: true, Coupling: 2, Probe: 100},
		},
		VertSet:  true,
		Trigger:  settings.Trigger{Qualifiers: engine.TrigQual{PulseLvl: .4, PulseMinNs: 105000, PulseMaxNs: 120000, PulseCond: 3, SlopeLo: .3, SlopeHi: .7, SlopeMinNs: 100, SlopeMaxNs: 1000, VideoStd: 1, VideoLine: 42, VideoNeg: false}, LevelCode: 30500, Rising: false, Source: 1, Type: 2, Norm: true, HoldoffS: 1e-3, PosFrac: 0.3},
		Acq:      settings.Acq{Mode: 1, AvgCount: 64, EresLen: 1, MemDepth: 16384, PrecisionRateHz: 15625000},
		Decode:   settings.Decode{Proto: 4, Baud: 9600, ChA: 1, ChB: 0, CPOL: true, CPHA: true, Format: 1},
		ViewMode: 4,
		// The workspace UI: SPECTRUM on its spectrogram, BUS on the eye,
		// MEASURE on all values with two readouts, the master depth.
		UI: settings.UI{
			Set: true, Workspace: 2, Views: [5]int{1, 0, 4, 0, 0}, BusEye: true, MeasAll: true,
			Meas: [12]int{1*32 + 0 + 1, 0*32 + 1 + 1}, FFTSource: 1, MasterDepth: 128, RemoteNotices: true,
			// the mask build and super-res's stop
			MaskFrames: 64, MaskTol: 3, SrCh: 2, SrGrid: 16, SrStop: 3, SrTarget: 120,
		},
		// The serial trigger's match, kept while disarmed (a trigger type
		// other than Serial is set above); not I2C, so any address.
		Serial: settings.Serial{Set: true, Match: "55 AA", Addr: -1, RW: 2},
		// The screen saver's times, not the factory ones.
		Screen: settings.Screen{Set: true, SaverMin: 5, OffMin: 120},
		// The volts cursors, the zoom (no inverter in this rig: invert stays
		// off) and the frequency response's channels swapped.
		Display: settings.Display{
			Set: true, CurMode: 2, CurX: [2]float64{0.25, 0.75}, CurY: [2]float64{0.4, 0.55},
			CurSel: 1, Zoom: 10, ZoomOff: -0.2,
		},
		Bode: settings.Bode{Set: true, Ref: 1, Dut: 0},
		// SOUND: tuned on C2 at A4 = 442 Hz, meters on Eurorack's 10 Vpp,
		// the harmonics page, four cycles, a gate on C1 for the envelope.
		// The drift log is left out: it never comes back on (it runs only
		// while SOUND shows).
		Sound: settings.Sound{
			Set: true, A4: 442, Ch: 1, Ref: "euro", Range: "bass", Slow: true, Page: 3, Cycles: 4, ABIn: 1,
			EnvCV: true, Gate: 0, GateV: 1.5, VOctAuto: true, PreS: 2.5, PostS: 6, TrigGate: true,
		},
	}
	core.Apply(want, e, fe, pc, ext(e, fe), t.Logf)
	got := core.Collect(e, fe, pc, ext(e, fe))

	// The model reports the requested timebase immediately.
	if got.TdivS != want.TdivS {
		t.Fatalf("timebase: got %g, want %g", got.TdivS, want.TdivS)
	}
	// The cursors come back anchored to the signal (an older file's screen
	// places at the scales restored): their screen places as they were.
	gd := got.Display
	if !gd.CurAt || math.Abs(gd.CurX[0]-0.25) > 1e-9 || math.Abs(gd.CurX[1]-0.75) > 1e-9 ||
		math.Abs(gd.CurY[0]-0.4) > 1e-9 || math.Abs(gd.CurY[1]-0.55) > 1e-9 {
		t.Fatalf("cursors: %+v", gd)
	}
	got.Display.CurX, got.Display.CurY = want.Display.CurX, want.Display.CurY
	got.Display.CurAt, got.Display.CurT, got.Display.CurV, got.Display.CurVCh = false, [2]float64{}, [2]float64{}, 0
	if got != want {
		t.Fatalf("restore→collect mismatch:\n got %+v\nwant %+v", got, want)
	}

	// Spot-check the values really landed in the owners, not just Collect.
	st := e.Snapshot()
	if st.TrigCode != 30500 || st.TrigRising || st.TrigSource != 1 || st.TrigType != 2 || !st.Norm {
		t.Fatalf("trigger not applied through engine setters: %+v", st)
	}
	if st.AcqMode != 1 || st.AvgCount != 64 || st.PrecisionRateHz != 15625000 || st.TrigQual != want.Trigger.Qualifiers {
		t.Fatalf("acq not applied: mode=%d avg=%d", st.AcqMode, st.AvgCount)
	}
	if st.OffC1 == 0 || st.OffC2 == 0 {
		t.Fatal("offsets were not staged to the engine DAC shadow")
	}
	if !fe.Emitted() {
		t.Fatal("V/div restore did not drive the front end")
	}
	if fe.VdivV(0) != 0.5 || fe.VdivV(1) != 2 {
		t.Fatalf("vdiv wrong: %v %v", fe.VdivV(0), fe.VdivV(1))
	}
	if fe.Coupling(0) != 1 || fe.Coupling(1) != 2 || fe.ProbeFactor(0) != 10 || fe.ProbeFactor(1) != 100 {
		t.Fatal("coupling/probe not applied")
	}
	v := pc.SettingsView()
	if v.ViewMode != 4 || v.Decode.Proto != 4 || v.Decode.Baud != 9600 || !v.Decode.CPOL || !v.Decode.CPHA {
		t.Fatalf("controller view/decode not applied: %+v", v)
	}
}

// TRLC-LINKS: REQ-SDS-072
func TestApplyHostileValuesClampThroughSetters(t *testing.T) {
	e, fe, pc, _ := rig(t)
	hostile := settings.Settings{
		Version: settings.Version,
		TdivS:   123.456, // not in ladder → skipped
		Ch: [2]settings.Channel{
			{VdivV: 0.33, OffsetV: 1e12, OffsetSet: true, Coupling: 99, Probe: 7},
			{VdivV: -2, OffsetV: 0, OffsetSet: false, Coupling: -1, Probe: 0},
		},
		VertSet:  true,
		Trigger:  settings.Trigger{LevelCode: 99999999, Source: 5, Type: -3, HoldoffS: 400},
		Acq:      settings.Acq{Mode: 77, AvgCount: 100000, EresLen: 10},
		Decode:   settings.Decode{Proto: -9, Baud: 99999999, ChA: -3, ChB: 42, Format: 47},
		ViewMode: -7,
		UI:       settings.UI{Set: true, Workspace: 77, Views: [5]int{-3, 9, 99, -1, 5}, Meas: [12]int{-5, 9999, 33}, FFTSource: -3, MasterDepth: 7},
	}
	core.Apply(hostile, e, fe, pc, ext(e, fe), t.Logf)

	st := e.Snapshot()
	if st.TrigCode != 0 {
		t.Fatalf("out-of-range level code must not stage over the boot comparator: %d", st.TrigCode)
	}
	if st.TrigSource != 0 || st.TrigType != 0 {
		t.Fatalf("trigger source/type not clamped: src=%d typ=%d", st.TrigSource, st.TrigType)
	}
	if st.HoldoffS != 10 {
		t.Fatalf("holdoff not clamped to 10s: %g", st.HoldoffS)
	}
	if st.AcqMode != 0 || st.AvgCount != 256 || st.EresLen != 9 {
		t.Fatalf("acq not clamped: mode=%d avg=%d eres=%d", st.AcqMode, st.AvgCount, st.EresLen)
	}
	// C1 vdiv is not a detent → skipped; C1 offset volts are recorded but the
	// staged DAC code obeys the per-tier clamp (never a wild 16-bit value).
	if st.OffC1 == 0 {
		t.Fatal("C1 offset should have staged (clamped), it was requested")
	}
	if fe.Coupling(0) != 0 || fe.Coupling(1) != 0 {
		t.Fatal("out-of-domain coupling must be skipped")
	}
	if fe.ProbeFactor(0) != 1 || fe.ProbeFactor(1) != 1 {
		t.Fatal("out-of-domain probe must be skipped")
	}
	v := pc.SettingsView()
	if v.Decode.Proto != 0 || v.Decode.Baud != 115200 {
		t.Fatalf("hostile decode not defaulted: %+v", v.Decode)
	}
	if v.Decode.ChA != 1 || v.Decode.Format != 2 {
		t.Fatalf("decode chan/format not clamped into domain: %+v", v.Decode)
	}
	if v.ViewMode < 0 || v.ViewMode > 5 {
		t.Fatalf("view mode out of domain: %d", v.ViewMode)
	}
	u := v.UI
	if u.Workspace < 0 || u.Workspace > 4 || u.FFTSource != 1 || u.MasterDepth != 64 || u.Meas[0] != 33 || u.Meas[1] != 0 {
		t.Fatalf("hostile workspace UI not clamped into domain: %+v", u)
	}
	for _, view := range u.Views {
		if view < 0 || view > 5 {
			t.Fatalf("a workspace view out of domain: %+v", u.Views)
		}
	}
}

// TRLC-LINKS: REQ-SDS-072
func TestApplyVirginVerticalStaysUntouched(t *testing.T) {
	// VertSet=false (the saved session never drove the front end): restore
	// must preserve the seed-don't-emit boot rule — no relay/gain emission,
	// no offset staging (spec 06 §4.4).
	e, fe, pc, spi := rig(t)
	s := settings.Settings{
		Version: settings.Version,
		Ch: [2]settings.Channel{
			{VdivV: 0.5, OffsetV: 0, OffsetSet: false, Coupling: 0, Probe: 1},
			{VdivV: 0.5, OffsetV: 0, OffsetSet: false, Coupling: 0, Probe: 1},
		},
		VertSet: false,
	}
	core.Apply(s, e, fe, pc, ext(e, fe), t.Logf)
	if fe.Emitted() {
		t.Fatal("restore emitted onto a virgin front end")
	}
	if spi.relays != 0 || spi.gains != 0 {
		t.Fatalf("SPI writes on a virgin restore: relays=%d gains=%d", spi.relays, spi.gains)
	}
	st := e.Snapshot()
	if st.OffC1 != 0 || st.OffC2 != 0 {
		t.Fatal("offset staged though OffsetSet=false")
	}
}

// TRLC-LINKS: REQ-SDS-072
func TestApplyNilOwners(t *testing.T) {
	// fe==nil (no SPI front end) and pc==nil must be tolerated everywhere.
	e, _, _, _ := rig(t)
	core.Apply(sampleValid(), e, nil, nil, ext(e, nil), t.Logf)
	core.Apply(sampleValid(), nil, nil, nil, nil, nil)
	_ = core.Collect(e, nil, nil, ext(e, nil))
	_ = core.Collect(nil, nil, nil, nil)
}

// TRLC-LINKS: REQ-SDS-072
func sampleValid() settings.Settings {
	return settings.Settings{
		Version: settings.Version,
		TdivS:   500e-6,
		Trigger: settings.Trigger{LevelCode: 31000, Rising: true},
		Acq:     settings.Acq{Mode: 0, AvgCount: 16, EresLen: 1},
	}
}

// A setup keeps the frequency response's reference and DUT channels; a
// recall restores them and leaves the armed state as it is; an older file
// without the block keeps the channels.
// TRLC-LINKS: REQ-SDS-066, REQ-SDS-089
func TestApplyRestoresBodeChannels(t *testing.T) {
	e, fe, pc, _ := rig(t)
	e.SetBodeMode(true, 1, 0)
	s := core.Collect(e, fe, pc, ext(e, fe))
	if !s.Bode.Set || s.Bode.Ref != 1 || s.Bode.Dut != 0 {
		t.Fatalf("collected %+v", s.Bode)
	}
	e.SetBodeMode(false, 0, 1)
	core.Apply(s, e, fe, pc, ext(e, fe), t.Logf)
	if st := e.Snapshot(); st.BodeRef != 1 || st.BodeDut != 0 || st.BodeMode != 0 {
		t.Fatalf("restored ref %d dut %d mode %d, want 1 0 disarmed", st.BodeRef, st.BodeDut, st.BodeMode)
	}
	s.Bode = settings.Bode{}
	e.SetBodeMode(false, 1, 0)
	core.Apply(s, e, fe, pc, ext(e, fe), t.Logf)
	if st := e.Snapshot(); st.BodeRef != 1 || st.BodeDut != 0 {
		t.Fatalf("an older file moved the channels: ref %d dut %d", st.BodeRef, st.BodeDut)
	}
}
