// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"testing"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/hal"
)

// The run control lives in the instrument model and the engine follows
// it: SINGLE arms a shot in NORMAL, every press a fresh one; a spent shot
// stops the run in the model too; RUN brings back the mode the shot
// replaced, unless the user chose one meanwhile.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-011
func TestRunControlOnTheModel(t *testing.T) {
	e := &Engine{}
	e.SetRunning(true)
	e.SetSingle()
	gen := e.singleGen.Load()
	st := e.inst().State()
	if !st.Running || !st.Single || st.Sweep != hal.NormalSweep || !e.normNow() || !e.singleArmed.Load() {
		t.Fatalf("SINGLE from AUTO: %+v norm %v", st, e.normNow())
	}
	e.SetSingle()
	if e.singleGen.Load() != gen+1 {
		t.Fatalf("a second press is no fresh shot: %d after %d", e.singleGen.Load(), gen)
	}
	(&sramCycle{e: e}).SingleDone(core.Verdict{})
	if st := e.inst().State(); st.Running || st.Single || e.running.Load() || e.singleArmed.Load() {
		t.Fatalf("a spent shot: %+v", st)
	}
	e.SetRunning(true)
	if st := e.inst().State(); !st.Running || st.Sweep != hal.Auto || e.normNow() {
		t.Fatalf("RUN after the shot: %+v norm %v", st, e.normNow())
	}
	e.SetSingle()
	e.SetNorm(true)
	e.SetRunning(false)
	e.SetRunning(true)
	if !e.normNow() || e.inst().State().Sweep != hal.NormalSweep {
		t.Fatal("RUN brought back the replaced mode over the user's NORMAL")
	}
}

// The instrument model holds the trigger and acquisition settings the
// engine applied, clamped as the engine clamps them.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-012
func TestModelHoldsTheEngineSettings(t *testing.T) {
	e := &Engine{}
	e.SetTrigType(7) // not this scope's: edge
	e.SetTrigSource(5)
	e.SetTrigSlope(false)
	e.SetHoldoff(30)
	e.SetPulseParams(1.5, 900, 100, 6)
	e.SetSlopeParams(.9, .1, 50, 0, 1)
	e.SetVideoParams(3, -2, true)
	e.SetAcqMode(1)
	e.SetAvgCount(1000)
	e.SetEresLen(8)
	e.SetSerialMode(1)
	e.SetSerialParams(SerialParams{Proto: 1, Baud: 9600, Bytes: []int{0x55}})
	s := e.inst().State()
	q := s.Qual
	switch {
	case s.Trigger.Type != 0, s.Trigger.Source().Index != 0, s.Trigger.Slope() != hal.Falling:
		t.Errorf("trigger %v source %v slope %v", s.Trigger.Type, s.Trigger.Source(), s.Trigger.Slope())
	case s.Trigger.Active().HoldoffS != 10:
		t.Errorf("holdoff %g", s.Trigger.Active().HoldoffS)
	case q.PulseLvl != 1 || q.PulseMinNs != 100 || q.PulseMaxNs != 900 || q.PulseCond != 2:
		t.Errorf("pulse %+v", q)
	case q.SlopeLo != .1 || q.SlopeHi != .9 || q.SlopeMinNs != 50 || q.SlopeCond != 1:
		t.Errorf("slope %+v", q)
	case q.VideoStd != 0 || q.VideoLine != 0 || !q.VideoNeg:
		t.Errorf("video %+v", q)
	case s.Mode != hal.Average || s.Averages != 256 || s.EresLen != 7:
		t.Errorf("mode %v averages %d eres %d", s.Mode, s.Averages, s.EresLen)
	case s.SerialMode != 1 || s.Serial.Baud != 9600 || len(s.Serial.Bytes) != 1:
		t.Errorf("serial %d %+v", s.SerialMode, s.Serial)
	}
}
