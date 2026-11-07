// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"testing"

	"github.com/labeth/open-dso-core/decode"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// TRLC-LINKS: REQ-SDS-013
func TestPlanSequence(t *testing.T) {
	long := make([]int, 12)
	for i := range long {
		long[i] = i * 17
	}
	c := PlanSequence(core.SerialParams{Proto: core.SerCAN, Bytes: long})
	if c.Length != 12 || !c.Qualify || c.EndBadValue || c.Elements[11] != (sramcapture.SequenceElement{Kind: 2, Value: 187, Mask: 0xff}) {
		t.Fatalf("CAN long %+v", c)
	}
	// Errors and wildcards turn qualification off; the error element matches any error.
	c = PlanSequence(core.SerialParams{Proto: core.SerCAN, Pattern: []core.PatternElem{{Kind: 2, Value: 0xaa, Mask: 0xff}, {Kind: 2, Value: 0, Mask: 0}, {Kind: 4, Value: 0, Mask: 0}}})
	if c.Length != 3 || c.Qualify || c.Elements[1] != (sramcapture.SequenceElement{Kind: 2}) || c.Elements[2] != (sramcapture.SequenceElement{Kind: 4}) {
		t.Fatalf("CAN tokens %+v", c)
	}
	c = PlanSequence(core.SerialParams{Proto: core.SerARINC, Bytes: []int{0x2abcd, 0x15a3f}})
	if c.Length != 2 || !c.EndBadValue || c.Elements[0] != (sramcapture.SequenceElement{Kind: 2, Value: 0x2abcd << 10, Mask: 0x7ffff << 10}) {
		t.Fatalf("ARINC %+v", c)
	}
	c = PlanSequence(core.SerialParams{Proto: core.SerI2C, Bytes: []int{1, 2, 3, 4, 5}})
	if c.Length != 5 || c.Qualify || c.Elements[0] != (sramcapture.SequenceElement{Kind: 2, Value: 1, Mask: 0x1ff}) {
		t.Fatalf("I2C %+v", c)
	}
	c = PlanSequence(core.SerialParams{Proto: core.SerManchester, Bits: 16, Pattern: []core.PatternElem{{Kind: 2, Value: 0xA50A, Mask: 0xFF0F}}})
	if c.Length != 1 || c.Elements[0] != (sramcapture.SequenceElement{Kind: 2, Value: 0xA50A, Mask: 0xFF0F}) || !c.EndBadValue {
		t.Fatalf("Manchester %+v", c)
	}
	for _, bad := range []core.SerialParams{
		{Proto: core.SerUART, Bytes: make([]int, 33)},
		{Proto: core.SerUART, Bytes: []int{0x100}},
		{Proto: core.SerSENT, Bytes: []int{16}},
		{Proto: core.SerUART, Pattern: []core.PatternElem{{Kind: 3, Value: 0, Mask: 0}}},
	} {
		if c := PlanSequence(bad); c.Length != 0 {
			t.Fatalf("planned %+v from %+v", c, bad)
		}
	}
	if core.SequenceNeeded(core.SerialParams{Proto: core.SerUART, Bytes: []int{1, 2, 3, 4}}) || !core.SequenceNeeded(core.SerialParams{Proto: core.SerUART, Bytes: []int{1, 2, 3, 4, 5}}) ||
		core.SequenceNeeded(core.SerialParams{Proto: core.SerCAN, Bytes: make([]int, 8)}) || !core.SequenceNeeded(core.SerialParams{Proto: core.SerMIL1553, Bytes: []int{1, 2}}) {
		t.Fatal("native limits")
	}
}

// A frame-start element plans the hardware's START event, so [start, any]
// fires on a frame's first unit; a MIL-1553 unit may require the command
// sync flag beside its sixteen bits. The software form matches the same.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-223
func TestFrameStartSequence(t *testing.T) {
	c := PlanSequence(core.SerialParams{Proto: core.SerSPI, Pattern: []core.PatternElem{{Kind: core.PatternStart}, {Kind: core.PatternData}}})
	if c.Length != 2 || c.Qualify || c.Elements[0] != (sramcapture.SequenceElement{Kind: sramcapture.SequenceStart}) ||
		c.Elements[1] != (sramcapture.SequenceElement{Kind: sramcapture.SequenceData}) {
		t.Fatalf("SPI frame start %+v", c)
	}
	c = PlanSequence(core.SerialParams{Proto: core.SerMIL1553, Pattern: []core.PatternElem{{Kind: core.PatternData, Value: core.MILCommand, Mask: core.MILCommand}}})
	if c.Length != 1 || c.Elements[0] != (sramcapture.SequenceElement{Kind: sramcapture.SequenceData, Value: core.MILCommand, Mask: core.MILCommand}) {
		t.Fatalf("MIL command word %+v", c)
	}
	if c := PlanSequence(core.SerialParams{Proto: core.SerCAN, Pattern: []core.PatternElem{{Kind: core.PatternData, Value: core.MILCommand, Mask: core.MILCommand}}}); c.Length != 0 {
		t.Fatalf("the command flag on CAN: %+v", c)
	}
	sp := []decode.Span{
		{I0: 0, I1: 9, Kind: "data", Val: 1},
		{I0: 20, I1: 20, Kind: "start"},
		{I0: 21, I1: 29, Kind: "data", Val: 2},
		{I0: 30, I1: 39, Kind: "data", Val: 3},
	}
	if ok, at := core.MatchSequence(sp, core.SerialParams{Proto: core.SerCAN, Pattern: []core.PatternElem{{Kind: core.PatternStart}, {Kind: core.PatternData}}}); !ok || at != 20 {
		t.Fatalf("frame start matched %v at %d, want the frame opening at 20", ok, at)
	}
	mil := []decode.Span{
		{I0: 0, I1: 2, Kind: "start", Text: "dsync"},
		{I0: 3, I1: 19, Kind: "data", Val: 0xAAAA},
		{I0: 40, I1: 42, Kind: "start", Text: "csync"},
		{I0: 43, I1: 59, Kind: "data", Val: 0x1234},
	}
	if ok, at := core.MatchSequence(mil, core.SerialParams{Proto: core.SerMIL1553, Pattern: []core.PatternElem{{Kind: core.PatternData, Value: core.MILCommand, Mask: core.MILCommand}}}); !ok || at != 43 {
		t.Fatalf("command word matched %v at %d, want 43", ok, at)
	}
}
