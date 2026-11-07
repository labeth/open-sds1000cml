// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"math"

	"github.com/labeth/open-dso-core/decode"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
)

// The pulse, slope and video triggers are the FPGA's (ADR-HARDWARE-QUALIFIERS):
// the qualifier image measures a pulse's width or a slope's time, or counts
// video lines, and fires on the event. The engine plans it from the trigger
// settings and the trigger channel's extent learned from the captures (the
// levels are fractions of it, kept in volts so a V/div or offset change
// re-expresses them). Software never decides one of these triggers; it only
// places the trigger point on the event.

// learnQualExtentOf is learnQualExtent with the channels' extents taken
// from lv when it is given.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) learnQualExtentOf(f *sample.Frame, sc [2]core.ChScale, lv *core.FrameLevels) {
	if f == nil || f.Len() < 64 {
		return
	}
	for ch := range 2 {
		codes := f.Codes8(0)
		if ch == 1 {
			codes = f.Codes8(1)
		}
		if len(codes) < f.Len() || !(sc[ch].Vdiv > 0) {
			continue
		}
		var lo, hi int
		var ok bool
		if lv != nil {
			lv.Of(ch, codes[:f.Len()])
			lo, hi, ok = lv.ExtLo[ch], lv.ExtHi[ch], lv.ExtOK[ch]
		} else {
			lo, hi, ok = decode.Extent(codes[:f.Len()])
		}
		if !ok {
			continue
		}
		volts := func(code int) float64 { return (float64(code)-128)*sc[ch].Vdiv/25 - sc[ch].Off }
		lv, hv := volts(lo), volts(hi)
		if e.qualExt.Have[ch].Load() {
			ol := math.Float64frombits(e.qualExt.Lo[ch].Load())
			oh := math.Float64frombits(e.qualExt.Hi[ch].Load())
			if math.Abs(lv-ol)*25/sc[ch].Vdiv < 2 && math.Abs(hv-oh)*25/sc[ch].Vdiv < 2 {
				continue
			}
		}
		e.qualExt.Lo[ch].Store(math.Float64bits(lv))
		e.qualExt.Hi[ch].Store(math.Float64bits(hv))
		e.qualExt.Have[ch].Store(true)
	}
}

// learnLevels learns the serial trigger's levels and the qualifiers' extent
// from a capture armed under the front end's write generation feAtArm, only
// when no front-end write (relay, gain, offset) came since the previous
// capture was armed: the capture spanning a V/div change holds its
// transient, which widens the learned extent (at 10 V/div a pulse level is
// planned off the signal).
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-013
func (e *Engine) learnLevels(f *sample.Frame, sc [2]core.ChScale, feAtArm uint64) {
	if feAtArm == e.feGen.Load() && feAtArm == e.learnFE {
		var lv core.FrameLevels // one histogram a channel for both
		e.learnSerialLevelsOf(f, sc, &lv)
		e.learnQualExtentOf(f, sc, &lv)
	}
	e.learnFE = feAtArm
}

// zonesOn reports a zone trigger with zones to test.
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) zonesOn() bool {
	if e.zoneMode.Load() != ZoneTrigger {
		return false
	}
	e.zm.Mu.Lock()
	defer e.zm.Mu.Unlock()
	return len(e.zm.Zones) != 0
}
