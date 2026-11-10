// ENGMODEL-OWNER-UNIT: FU-APP-ANALOG

package board

import (
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/instrument"
)

// ShareTrigger keeps one trigger source, slope and level across the trigger
// types (instrument.Model.OnTrigType): the SDS1000CML+ has one comparator,
// on one channel, at one level, with one slope, whatever the type, so a
// type picked takes them from the type before it (ADR-CML-ONE-TRIGGER-SOURCE)
// and every read-back is the comparator's. A level never set stays unset.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-098
func ShareTrigger(s *instrument.State, from instrument.TrigType) {
	before := s.Trigger
	before.Type = from
	src, slope, level := before.Source(), before.Slope(), before.LevelV()
	n := len(s.Channels)
	_ = s.Trigger.SetSource(src, n) // a type without one source keeps its own
	if s.Trigger.SetSlope(slope, n) != nil {
		KeepSlope(s, slope)
	}
	if s.LevelSet {
		_ = s.Trigger.SetLevel(level, n)
	}
}

// KeepSlope keeps the comparator's slope on the stages of a running type
// without a slope setting of its own (video; instrument.Model.OnTrigSlope),
// so it reads back and the type after it takes it on.
// TRLC-LINKS: REQ-SDS-011
func KeepSlope(s *instrument.State, slope hal.Slope) {
	c := s.Trigger.Running()
	for i := range c.Stages {
		c.Stages[i].Levels.Slope = slope
	}
}
