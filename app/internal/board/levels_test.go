// ENGMODEL-OWNER-UNIT: FU-APP-ANALOG

package board

import (
	"math"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/instrument"
	settings "github.com/labeth/open-dso-core/settings"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	engine "github.com/labeth/open-sds1000cml/app/internal/scopeapi"
)

// fakeEngine stages DAC codes as the engine does: the level clamped to its
// range, the offsets as given.
type fakeEngine struct{ st engine.Stats }

// TRLC-LINKS: REQ-SDS-098
func (e *fakeEngine) Snapshot() engine.Stats { return e.st }

// TRLC-LINKS: REQ-SDS-098
func (e *fakeEngine) SetTrigLevelCode(c uint16) uint16 {
	if c != 0 {
		e.st.TrigCode = min(max(c, engine.TrigCodeMin), engine.TrigCodeMax)
	}
	return e.st.TrigCode
}

// TRLC-LINKS: REQ-SDS-095
func (e *fakeEngine) SetOffsetDAC(ch int, c uint16) {
	if ch == 1 {
		e.st.OffC2 = c
	} else {
		e.st.OffC1 = c
	}
}

// The trigger level goes to the nearest DAC step of the front end's
// calibration and reads back as that step's volts; a knob step is one code;
// a kept level comes back to its code whatever the calibration did.
// TRLC-LINKS: REQ-SDS-098, REQ-SDS-213
func TestTriggerLevelInVolts(t *testing.T) {
	eng := &fakeEngine{}
	fe := analog.New(nil, func(time.Duration) {}, nil)
	l := New(eng, fe)
	if _, ok := l.TrigLevel(0); ok {
		t.Fatal("a boot comparator read as set")
	}
	v := l.SetTrigLevel(0.5, 0)
	want := math.Round(fe.TrigCode(0.5, 0))
	if float64(eng.st.TrigCode) != want || v != fe.TrigVolts(eng.st.TrigCode, 0) {
		t.Fatalf("0.5 V: code %d applied %v, want code %v", eng.st.TrigCode, v, want)
	}
	if got, ok := l.TrigLevel(0); !ok || got != v {
		t.Fatalf("read back %v %v, want %v", got, ok, v)
	}
	g := l.TrigLevelGrid(0)
	c0 := eng.st.TrigCode
	up := l.StepTrigLevel(3, 0)
	if eng.st.TrigCode != c0-3 || math.Abs(up-v-3*g.Step) > 1e-9 {
		t.Fatalf("3 steps up: code %d (from %d), level %v from %v, step %v", eng.st.TrigCode, c0, up, v, g.Step)
	}
	saved := l.SaveTrigLevel()
	l.SetTrigLevel(-2, 0)
	l.RestoreTrigLevel(saved)
	if eng.st.TrigCode != c0-3 || !saved.Set() {
		t.Fatalf("restored code %d, want %d", eng.st.TrigCode, c0-3)
	}
	l.SetTrigLevel(1e6, 0)
	if eng.st.TrigCode != engine.TrigCodeMin || l.TrigLevelGrid(0).Max != fe.TrigVolts(engine.TrigCodeMin, 0) {
		t.Fatalf("a level past the DAC's top: code %d", eng.st.TrigCode)
	}
	l.ResetTrigLevel()
	if eng.st.TrigCode != uint16(fe.TrigCode(0, 0)) {
		t.Fatalf("default setup's level: code %d", eng.st.TrigCode)
	}
}

// Without a front end the level follows the measured global fit and the
// offset the boot detent's law.
// TRLC-LINKS: REQ-SDS-098, REQ-SDS-095
func TestLevelsWithoutFrontEnd(t *testing.T) {
	eng := &fakeEngine{}
	l := New(eng, nil)
	l.SetTrigLevel(1, 1)
	if eng.st.TrigCode != 31437-911 {
		t.Fatalf("1 V: code %d", eng.st.TrigCode)
	}
	if v := l.TrigLevelOr(0, 1); v != engine.TrigLevelVolts(31437-911) {
		t.Fatalf("level %v", v)
	}
	if v := l.StepTrigLevel(1, 0); eng.st.TrigCode != 31437-912 || v != engine.TrigLevelVolts(31437-912) {
		t.Fatalf("a step: code %d level %v", eng.st.TrigCode, v)
	}
	if l.Offset(1) != 0 {
		t.Fatal("an untouched offset read as set")
	}
	got := l.SetOffset(1, 0.25)
	if eng.st.OffC2 != analog.OffsetCode(1, 0.25) || got != analog.OffsetVolts(1, eng.st.OffC2) || l.Offset(1) != got {
		t.Fatalf("offset: code %d applied %v read %v", eng.st.OffC2, got, l.Offset(1))
	}
}

// With a front end the offset goes through it (it re-anchors on V/div
// changes) and reads back as the staged code's volts.
// TRLC-LINKS: REQ-SDS-095
func TestOffsetThroughTheFrontEnd(t *testing.T) {
	eng := &fakeEngine{}
	fe := analog.New(nil, func(time.Duration) {}, nil)
	fe.OnOffset(eng.SetOffsetDAC)
	l := New(eng, fe)
	got := l.SetOffset(0, -0.3)
	if eng.st.OffC1 != fe.OffsetCode(0, -0.3) || got != fe.OffsetVolts(0, eng.st.OffC1) || l.Offset(0) != got {
		t.Fatalf("offset: code %d applied %v", eng.st.OffC1, got)
	}
}

// The instrument model holds the comparator's level in volts, set from the
// first level written, moved with the source's calibration and probe while
// the code stays; the settings file's level_code from the model is the
// staged code exactly, on every detent, probe and source.
// TRLC-LINKS: REQ-SDS-089, REQ-SDS-098
func TestModelHoldsTheLevelCode(t *testing.T) {
	eng := &fakeEngine{}
	fe := analog.New(nil, func(time.Duration) {}, nil)
	m := instrument.New(instrument.State{Channels: make([]hal.Channel, 2), Running: true}, nil)
	l := New(eng, fe)
	l.Project(m)
	x := SettingsExtension{Levels: l}
	var tr settings.Trigger
	x.CollectTrigger(&tr)
	if m.State().LevelSet || tr.LevelCode != 0 {
		t.Fatalf("a boot comparator: set %v, level_code %d", m.State().LevelSet, tr.LevelCode)
	}
	for _, probe := range []float64{1, 10, 100} {
		for src := range 2 {
			eng.st.TrigSource = src
			fe.SetProbe(src, probe)
			for idx := range analog.Detents {
				if err := fe.SetVdiv(src, idx); err != nil {
					t.Fatal(err)
				}
				for c := engine.TrigCodeMin; c <= engine.TrigCodeMax; c += 997 {
					l.SetTrigLevelCode(uint16(c))
					x.CollectTrigger(&tr)
					s := m.State()
					if !s.LevelSet || tr.LevelCode != int(eng.st.TrigCode) {
						t.Fatalf("x%g C%d detent %d code %d: level_code %d set %v", probe, src+1, idx, c, tr.LevelCode, s.LevelSet)
					}
					if v := fe.TrigVolts(eng.st.TrigCode, src); s.Trigger.LevelV() != v {
						t.Fatalf("model level %v, the code's volts %v", s.Trigger.LevelV(), v)
					}
				}
			}
		}
	}
	code := eng.st.TrigCode
	if err := fe.SetVdiv(1, 2); err != nil {
		t.Fatal(err)
	}
	l.Reproject()
	if v := m.State().Trigger.LevelV(); eng.st.TrigCode != code || v != fe.TrigVolts(code, 1) {
		t.Fatalf("after a V/div change: code %d (was %d), model %v", eng.st.TrigCode, code, v)
	}
}
