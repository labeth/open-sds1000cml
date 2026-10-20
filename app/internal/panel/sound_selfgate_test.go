// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// A self-gated envelope (Gate = the signal) of a note that repeats over a
// source floor above 3 % of its peak (a DAC's ripple) ends after the note
// dies away, not at the 15 s bound: the floor is learned while armed, as
// with a gate channel.
// TRLC-LINKS: REQ-SDS-268
func TestSelfGatedEnvelopeEndsAboveTheFloor(t *testing.T) {
	const a, d, s, off, r = 0.05, 0.03, 0.5, 0.8, 0.065
	// TRLC-LINKS: REQ-SDS-268
	env := func(t float64) float64 {
		t = math.Mod(t, 2)
		switch {
		case t < a:
			return t / a
		case t < off:
			return s + (1-s)*math.Exp(-(t-a)/d)
		}
		return (s + (1-s)*math.Exp(-(off-a)/d)) * math.Exp(-(t-off)/r)
	}
	e := newSoundEng([2]func(float64) float64{
		func(t float64) float64 {
			return 2*env(t)*math.Sin(2*math.Pi*330*t) + 0.16*math.Sin(2*math.Pi*7000*t) // floor 8 % of the peak
		},
		func(t float64) float64 { return 0 },
	}, 20)
	c := New(e, &fakeFE{idx: [2]int{8, 8}}, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	c.SetSoundSettings(SoundSettings{A4: 440, Ref: "dbu", Cycles: 2, Gate: -1, GateV: 0.5, Page: soundPageADSR})
	now := time.Now()
	e.next = int(1.2 * e.fs) // between two notes
	e.run(c, 300*time.Millisecond, &now)
	if err := c.SoundAction("adsr_arm"); err != nil {
		t.Fatal(err)
	}
	e.run(c, 4*time.Second, &now)
	rep := c.SoundReport(false, false)
	if rep.ADSR != adsrDone || !rep.ADSRRes.OK {
		t.Fatalf("capture %s %s %+v", rep.ADSR, rep.ADSRWhy, rep.ADSRRes)
	}
	if res := rep.ADSRRes; res.End > 2 {
		t.Fatalf("reading %+v", res)
	}
}
