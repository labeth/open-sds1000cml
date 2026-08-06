package settings_test

import (
	"testing"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/iface"
	"open-sds/app/internal/panel"
	"open-sds/app/internal/settings"
)

// nullBus satisfies bus.Bus (the owned interface) without any hardware: the
// engine is constructed (never Run) purely so Apply exercises the REAL staging
// setters and their clamps, and Snapshot reports what they stored.
type nullBus struct{}

func (nullBus) Read(plane iface.Plane, sel uint16) (uint16, error) { return 0, nil }
func (nullBus) Write(plane iface.Plane, sel, val uint16) error     { return nil }
func (nullBus) WriteSpare(sel, val uint16) error                   { return nil }
func (nullBus) BurstInto(c1, c2 []uint8, n int)                    {}
func (nullBus) BurstWordsInto(dst []uint16, n int)                 {}
func (nullBus) ChannelInto(sel uint16, dst []uint16, n int)        {}
func (nullBus) MmapDrain() bool                                    { return true }

// nullSPI satisfies analog.Transport so the real FrontEnd (with its per-tier
// offset law, ladder and emit tracking) runs against no hardware.
type nullSPI struct{ relays, gains int }

func (n *nullSPI) WriteRelay(word uint32) error   { n.relays++; return nil }
func (n *nullSPI) WriteGain(ch2, ch1 uint8) error { n.gains++; return nil }

// rig builds the production object graph exactly as cmd/app/main.go wires it
// (engine ← bus stub, front end ← SPI stub with the engine hooks, panel
// controller on top), without running any goroutine.
func rig(t *testing.T) (*engine.Engine, *analog.FrontEnd, *panel.Controller, *nullSPI) {
	t.Helper()
	e := engine.New(engine.Config{Bus: nullBus{}, Logf: t.Logf})
	spi := &nullSPI{}
	fe := analog.New(spi, func(time.Duration) {}, nil)
	fe.OnOffset(e.SetOffsetDAC)
	fe.OnVdiv(e.SetChannelVdiv)
	pc := panel.New(e, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	return e, fe, pc, spi
}

func TestApplyRestoreCollectFidelity(t *testing.T) {
	e, fe, pc, _ := rig(t)
	want := settings.Settings{
		Version: settings.Version,
		TdivS:   1e-3, // ladder detent — accepted (staged; stats reflect it only after a frame)
		Ch: [2]settings.Channel{
			{VdivV: 0.5, OffsetV: 1.5, OffsetSet: true, Coupling: 1, Probe: 10},
			{VdivV: 2, OffsetV: -2.5, OffsetSet: true, Coupling: 2, Probe: 100},
		},
		VertSet:  true,
		Trigger:  settings.Trigger{LevelCode: 30500, Rising: false, Source: 1, Type: 2, Norm: true, HoldoffS: 1e-3},
		Acq:      settings.Acq{Mode: 1, AvgCount: 64, EresLen: 1},
		Decode:   settings.Decode{Proto: 4, Baud: 9600, ChA: 1, ChB: 0, CPOL: true, CPHA: true, Format: 1},
		ViewMode: 4,
	}
	settings.Apply(want, e, fe, pc, t.Logf)
	got := settings.Collect(e, fe, pc)

	// The tdiv is staged for the next frame boundary; without running the
	// engine the stats keep the pre-restore value. Everything else must
	// round-trip exactly.
	got.TdivS, want.TdivS = 0, 0
	if got != want {
		t.Fatalf("restore→collect mismatch:\n got %+v\nwant %+v", got, want)
	}

	// Spot-check the values really landed in the owners, not just Collect.
	st := e.Snapshot()
	if st.TrigCode != 30500 || st.TrigRising || st.TrigSource != 1 || st.TrigType != 2 || !st.Norm {
		t.Fatalf("trigger not applied through engine setters: %+v", st)
	}
	if st.AcqMode != 1 || st.AvgCount != 64 {
		t.Fatalf("acq not applied: mode=%d avg=%d", st.AcqMode, st.AvgCount)
	}
	if st.OffC1 == 0 || st.OffC2 == 0 {
		t.Fatal("offsets were not staged to the engine DAC shadow")
	}
	idx, emitted := fe.Snapshot()
	if !emitted {
		t.Fatal("V/div restore did not drive the front end")
	}
	if analog.Detents[idx[0]].VdivV != 0.5 || analog.Detents[idx[1]].VdivV != 2 {
		t.Fatalf("vdiv detents wrong: %v", idx)
	}
	if fe.Coupling(0) != 1 || fe.Coupling(1) != 2 || fe.ProbeFactor(0) != 10 || fe.ProbeFactor(1) != 100 {
		t.Fatal("coupling/probe not applied")
	}
	v := pc.SettingsView()
	if v.ViewMode != 4 || v.Decode.Proto != 4 || v.Decode.Baud != 9600 || !v.Decode.CPOL || !v.Decode.CPHA {
		t.Fatalf("controller view/decode not applied: %+v", v)
	}
}

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
		Decode:   settings.Decode{Proto: -9, Baud: 1234567, ChA: -3, ChB: 42, Format: 47},
		ViewMode: -7,
	}
	settings.Apply(hostile, e, fe, pc, t.Logf)

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
	if v.ViewMode < 0 || v.ViewMode > 4 {
		t.Fatalf("view mode out of domain: %d", v.ViewMode)
	}
}

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
	settings.Apply(s, e, fe, pc, t.Logf)
	if _, emitted := fe.Snapshot(); emitted {
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

func TestApplyNilOwners(t *testing.T) {
	// fe==nil (no SPI front end) and pc==nil must be tolerated everywhere.
	e, _, _, _ := rig(t)
	settings.Apply(sampleValid(), e, nil, nil, t.Logf)
	settings.Apply(sampleValid(), nil, nil, nil, nil)
	_ = settings.Collect(e, nil, nil)
	_ = settings.Collect(nil, nil, nil)
}

func sampleValid() settings.Settings {
	return settings.Settings{
		Version: settings.Version,
		TdivS:   500e-6,
		Trigger: settings.Trigger{LevelCode: 31000, Rising: true},
		Acq:     settings.Acq{Mode: 0, AvgCount: 16, EresLen: 1},
	}
}
