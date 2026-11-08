// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"reflect"
	"testing"

	"github.com/labeth/open-dso-core/hal"
)

// A controller's settings through Controls read back at once exactly as
// the engine's own setters give them, reach the engine only on its owner,
// in order, and the instrument model holds them.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-011, REQ-SDS-012, REQ-SDS-013
func TestControlsStageOnTheOwner(t *testing.T) {
	direct, _ := newTestEngine(t, newFakeBus())
	staged, _ := newTestEngine(t, newFakeBus())
	ctl := staged.Controls()
	before := staged.Snapshot()

	set := func(c settingsSetter) {
		c.SetTrigType(7) // not this scope's: edge
		c.SetTrigType(2)
		c.SetTrigSource(5)
		c.SetTrigSlope(false)
		c.SetTrigPosFrac(.3)
		c.SetTrigPosFrac(.7)
		c.SetHoldoff(30)
		c.SetPulseParams(1.5, 900, 100, 6)
		c.SetSlopeParams(.9, .1, 50, 0, 1)
		c.SetVideoParams(3, -2, true)
		c.SetAcqMode(1)
		c.SetAvgCount(1000)
		c.SetEresLen(8)
		c.SetETS(true)
		c.SetSerialMode(1)
		c.SetSerialParams(SerialParams{Proto: 1, Baud: 9600, Bytes: []int{0x55}})
	}
	set(direct)
	set(ctl)
	if _, ok := direct.SetTdiv(1e-3); !ok {
		t.Fatal("1 ms/div is on the ladder")
	}
	if _, ok := ctl.SetTdiv(1e-3); !ok {
		t.Fatal("1 ms/div is on the ladder (staged)")
	}
	if _, ok := ctl.SetTdiv(3e-3); ok {
		t.Fatal("3 ms/div staged off the ladder")
	}

	if got := staged.Snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatal("a controller's setting reached the engine off its owner")
	}
	want := direct.Snapshot()
	if got := ctl.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("staged snapshot\n got %+v\nwant %+v", got, want)
	}
	if got, w := ctl.SerialConfig(), direct.SerialConfig(); !reflect.DeepEqual(got, w) {
		t.Fatalf("staged match %+v, want %+v", got, w)
	}

	staged.applyStaged()
	if got := staged.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("applied snapshot\n got %+v\nwant %+v", got, want)
	}
	if got := ctl.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatal("the controls' snapshot after the owner applied differs")
	}
	if staged.staged.pending.Load() != 0 {
		t.Fatal("staged settings left after the commit point")
	}

	m := staged.inst().State()
	switch {
	case m.Trigger.Type != 2, m.Trigger.Source().Index != 0, m.Trigger.Slope() != hal.Falling:
		t.Errorf("model trigger %v source %v slope %v", m.Trigger.Type, m.Trigger.Source(), m.Trigger.Slope())
	case m.TimeDivS != 1e-3, m.Mode != hal.Average, m.Averages != 256, m.EresLen != 7:
		t.Errorf("model timebase %g mode %v averages %d eres %d", m.TimeDivS, m.Mode, m.Averages, m.EresLen)
	case m.SerialMode != 1 || m.Serial.Baud != 9600:
		t.Errorf("model serial %d %+v", m.SerialMode, m.Serial)
	}
}

// settingsSetter is the setters the engine and its controls share.
type settingsSetter interface {
	SetTrigType(int)
	SetTrigSource(int)
	SetTrigSlope(bool)
	SetTrigPosFrac(float64)
	SetHoldoff(float64) float64
	SetPulseParams(float64, float64, float64, int)
	SetSlopeParams(float64, float64, float64, float64, int)
	SetVideoParams(int, int, bool)
	SetAcqMode(int)
	SetAvgCount(int)
	SetEresLen(int)
	SetETS(bool)
	SetSerialMode(int)
	SetSerialParams(SerialParams)
}
