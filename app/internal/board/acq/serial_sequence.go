// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// PlanSequence maps the request into the hardware's event values: ARINC's
// data field sits at bits 10..28 of its word, and an I2C data unit requires
// the address flag (bit 8) clear. Length zero means unplannable.
// TRLC-LINKS: REQ-SDS-013
func PlanSequence(p core.SerialParams) sramcapture.SequenceTriggerConfig {
	var c sramcapture.SequenceTriggerConfig
	elems := core.PatternOf(p)
	if len(elems) == 0 || len(elems) > sramcapture.SequenceMax {
		return c
	}
	dataOnly := true
	for i, e := range elems {
		switch e.Kind {
		case core.PatternStart:
			dataOnly = false
			c.Elements[i] = sramcapture.SequenceElement{Kind: sramcapture.SequenceStart}
		case core.PatternError:
			dataOnly = false
			c.Elements[i] = sramcapture.SequenceElement{Kind: sramcapture.SequenceError}
		case core.PatternData:
			if allowed := core.UnitMask(p) | core.UnitFlags(p); e.Value < 0 || e.Mask < 0 || e.Value&^allowed != 0 || e.Mask&^allowed != 0 {
				return sramcapture.SequenceTriggerConfig{}
			}
			value, mask := uint32(e.Value&e.Mask), uint32(e.Mask) //nolint:gosec // G115: checked non-negative and within the unit mask above
			switch p.Proto {
			case core.SerARINC:
				value, mask = value<<10, mask<<10
			case core.SerI2C:
				mask |= 0x100
			}
			c.Elements[i] = sramcapture.SequenceElement{Kind: sramcapture.SequenceData, Value: value, Mask: mask}
		default:
			return sramcapture.SequenceTriggerConfig{}
		}
	}
	c.Length = uint8(len(elems)) //nolint:gosec // G115: checked to at most SequenceMax elements above
	c.Qualify = dataOnly && core.FramedProtocol(p.Proto)
	c.EndBadValue = p.Proto == core.SerManchester || p.Proto == core.SerARINC
	return c
}
