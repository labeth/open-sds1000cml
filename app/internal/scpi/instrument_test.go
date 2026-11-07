// ENGMODEL-OWNER-UNIT: FU-APP-SCPI

package scpi

import (
	"testing"

	"github.com/labeth/open-dso-core/hal"
	core "github.com/labeth/open-dso-core/scpi"
)

// The core Instrument over this scope reads the engine, front end and
// levels at the probe tip, and stages changes through their setters.
// TRLC-LINKS: REQ-SDS-024
func TestInstrumentAdapter(t *testing.T) {
	h, fs, _ := newHFE(t)
	in := instrument{h}
	s := in.State()
	if len(s.Channels) != 2 || s.TimeDivS != 500e-6 || !s.Running || s.Trigger.Slope != hal.Rising {
		t.Fatalf("state %+v", s)
	}
	err := in.Change(func(s *core.State) error {
		s.TimeDivS = 1e-6
		s.Trigger.Slope = hal.Falling
		s.Trigger.Source = hal.Source{Kind: hal.Analog, Index: 1}
		s.Sweep = hal.NormalSweep
		s.Channels[0].Probe = 10
		s.Channels[0].VoltsDiv = 5
		s.Channels[1].Invert = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s = in.State()
	if s.TimeDivS != 1e-6 || s.Trigger.Slope != hal.Falling || s.Trigger.Source.Index != 1 || s.Sweep != hal.NormalSweep ||
		s.Channels[0].Probe != 10 || s.Channels[0].VoltsDiv != 5 || !s.Channels[1].Invert || !fs.stats.Norm {
		t.Fatalf("after change %+v", s)
	}
	if err := in.Change(func(s *core.State) error { s.Depth = 1; return nil }); err == nil {
		t.Fatal("a depth this scope has no setting for was staged")
	}
	if got := do(t, h, "TDIV?;TRSL?;C1:VDIV?;C2:INVS?"); got != "TDIV 1.00E-06s\nTRSL NEG\nC1:VDIV 5.00E+00V\nC2:INVS ON\n" {
		t.Fatalf("SCPI reads %q", got)
	}
	if in.Acquisition() != core.Triggered || in.Force() != nil {
		t.Fatal("acquisition")
	}
}
