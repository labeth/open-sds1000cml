// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"testing"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
)

// couplingFrontEnd is a front end at 1 V/div with an offset, C1's
// coupling as set.
type couplingFrontEnd struct {
	hal.FrontEnd
	cpl *hal.Coupling
}

func (f couplingFrontEnd) Get(ch int) hal.Channel {
	c := hal.Channel{On: true, VoltsDiv: 1, OffsetV: 2, Probe: 1}
	if ch == 0 {
		c.Coupling = *f.cpl
	}
	return c
}

// meanFrame is a Main frame whose C1 codes average mean.
func meanFrame(mean uint8) *sample.Frame {
	f := RecordFrame(1000)
	for k := range f.Traces[0].Codes8 {
		f.Traces[0].Codes8[k] = mean - 10 + uint8(20*(k&1))
	}
	return f
}

// An AC-coupled channel's level is codes above the channel's mean, as the
// AC trace shows it: a 0 V level on C1 programs the fabric at C1's mean
// code, the frames state the same scale (the owner's placement finds the
// crossing where the fabric fired), and a mean that moved a whole code
// plans the trigger again at the next arm. A DC channel keeps its offset.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-096
func TestACLevelFollowsTheMean(t *testing.T) {
	b, _, _ := newTestBoard()
	cpl := hal.DC
	b.SetFrontEnd(couplingFrontEnd{cpl: &cpl})
	plan, err := b.Planner().Plan(hal.Request{Channels: []int{0, 1}, TimePerDivS: 1e-6})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Acquirer().Configure(plan); err != nil {
		t.Fatal(err)
	}
	spec := hal.TriggerSpec{Stages: []hal.TrigStage{{Node: hal.NodeEdge, Source: hal.Source{Kind: hal.Analog}, Levels: hal.Levels{High: 0, Slope: hal.Rising}}}}
	program := func() uint8 {
		t.Helper()
		if err := b.Trigger().Program(spec); err != nil {
			t.Fatal(err)
		}
		return b.Staged().TriggerLevel
	}
	b.noteMeans(meanFrame(100))
	dc := program()
	if want := uint8(sample.Mid8 + 2*CodesPerDiv); dc != want {
		t.Fatalf("DC: 0 V at code %d, want %d (the offset applied)", dc, want)
	}
	cpl = hal.AC
	if got := program(); got != 100 {
		t.Fatalf("AC: 0 V at code %d, want the mean 100", got)
	}
	if s := b.levelScale(0); s.Volts(100) != 0 || s.OffsetV != 0 {
		t.Fatalf("AC frames' scale %+v", s)
	}
	for range 30 {
		b.noteMeans(meanFrame(140))
	}
	if err := b.Acquirer().Arm(); err != nil {
		t.Fatal(err)
	}
	if got := b.Staged().TriggerLevel; got < 139 || got > 140 {
		t.Fatalf("the mean moved to 140: the armed level is code %d", got)
	}
}
