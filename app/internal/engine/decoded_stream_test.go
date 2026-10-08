// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"

	"open-sds/app/internal/sramcapture"
)

// The planner accepts the stream triggers' patterns: an error token, and a
// value within the unit; the manual element is out of every unit's range.
// TRLC-LINKS: REQ-SDS-018
func TestStreamTriggerPatternsPlan(t *testing.T) {
	p := SerialParams{Proto: serUART, Baud: 115200, ChA: 1, HaveThr: true, Threshold: 169}
	p.Pattern = []PatternElem{{Kind: patternError}}
	if c := planSequence(p); c.Length != 1 || c.Elements[0].Kind != sramcapture.SequenceError {
		t.Fatalf("error trigger: %+v", c)
	}
	p.Pattern = []PatternElem{{Kind: patternData, Value: 0x55, Mask: unitMask(p)}}
	if c := planSequence(p); c.Length != 1 || c.Elements[0].Value != 0x55 {
		t.Fatalf("value trigger: %+v", c)
	}
	if streamNever <= 0x1fffffff {
		t.Fatal("manual element inside a decoder unit's range")
	}
}
