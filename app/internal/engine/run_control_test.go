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
