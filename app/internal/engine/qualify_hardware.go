// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"fmt"
	"math"

	"github.com/labeth/open-dso-core/decode"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The pulse, slope and video triggers are the FPGA's (ADR-HARDWARE-QUALIFIERS):
// the qualifier image measures a pulse's width or a slope's time, or counts
// video lines, and fires on the event. The engine plans it from the trigger
// settings and the trigger channel's extent learned from the captures (the
// levels are fractions of it, kept in volts so a V/div or offset change
// re-expresses them). Software never decides one of these triggers; it only
// places the trigger point on the event.

// imageQualify is the image with the hardware qualifiers.
const imageQualify = "qualify"

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

// Video line numbers: the first line sync after a field's vertical sync and
// its post-equalizing pulses is line 10 in NTSC (525) and line 6 in PAL (625).
const (
	videoFirstLineNTSC = 10
	videoFirstLinePAL  = 6
)

// planQualifier is the trigger type's hardware qualifier on channel ch at the
// current V/div and offset, the trigger level its edges fire at, and why it
// has none ("" when it does). rising is the trigger slope: a high pulse, a
// rising slope.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) planQualifier(tp core.TrigParams, ch int, rising bool) (sramcapture.QualifierConfig, uint8, string) {
	ch &= 1
	if !e.hardwareQualifier {
		if e.images != nil && e.images.HasQualify() && e.protocolImage != imageQualify {
			return sramcapture.QualifierConfig{}, 0, "loading the qualifier FPGA image"
		}
		return sramcapture.QualifierConfig{}, 0, "this FPGA image has no pulse, slope or video trigger"
	}
	if !e.qualExt.Have[ch].Load() {
		return sramcapture.QualifierConfig{}, 0, fmt.Sprintf("learning the signal levels on C%d", ch+1)
	}
	off := math.Float64frombits(e.trigOffV[ch].Load())
	vdiv := e.chVdivV(ch)
	code := func(v float64) float64 { return 128 + (v+off)*25/vdiv }
	lo := code(math.Float64frombits(e.qualExt.Lo[ch].Load()))
	hi := code(math.Float64frombits(e.qualExt.Hi[ch].Load()))
	span := hi - lo
	if span < core.FlatRejectSpan {
		return sramcapture.QualifierConfig{}, 0, fmt.Sprintf("no signal swing on C%d", ch+1)
	}
	clamp := func(v float64) uint8 { return uint8(math.Round(math.Max(1, math.Min(254, v)))) }
	q := sramcapture.QualifierConfig{Enabled: true, Channel: uint8(ch), Cond: uint8(tp.PulseCond)} //nolint:gosec // G115: the channel is 0 or 1, the condition 0..3 (SetPulseParams)
	level := clamp(lo + tp.PulseLvlFrac*span)
	minNs, maxNs := tp.PulseWMinNs, tp.PulseWMaxNs
	switch tp.Typ {
	case TrigPulse:
		q.Kind, q.Negative = sramcapture.QualPulse, !rising
	case TrigSlope:
		q.Kind, q.Negative, q.Cond = sramcapture.QualSlope, !rising, uint8(tp.SlopeCond) //nolint:gosec // G115: the condition is 0..3 (SetSlopeParams)
		q.LevelA, q.LevelB = clamp(lo+tp.SlopeLoFrac*span), clamp(lo+tp.SlopeHiFrac*span)
		level, minNs, maxNs = q.LevelA, tp.SlopeTMinNs, tp.SlopeTMaxNs
	case TrigVideo:
		q.Kind, q.Negative, q.Cond = sramcapture.QualVideo, tp.VideoNeg, sramcapture.QualAny
		level = clamp(lo + 0.3*span) // the sync slice, 30 % from the sync tip
		if !tp.VideoNeg {
			level = clamp(hi - 0.3*span)
		}
		first := videoFirstLinePAL
		if tp.VideoStd == 1 {
			first = videoFirstLineNTSC
		}
		if tp.VideoLine > 0 {
			q.Line = uint16(max(1, min(1023, tp.VideoLine-first+1)))
		}
		minNs, maxNs = 0, 0
	case TrigEdge:
		return sramcapture.QualifierConfig{}, 0, "an edge trigger has no qualifier"
	default:
		return sramcapture.QualifierConfig{}, 0, "the trigger type has no qualifier"
	}
	q.Prescale, q.MinTicks, q.MaxTicks = qualTicks(minNs, maxNs)
	return q, level, ""
}

// qualTicks expresses the limits in ticks of the smallest power-of-two
// prescale P (1..65536 clocks) that holds both. The FPGA counts an interval
// whose edges are W clocks apart as floor((W-1)/P) ticks: it is at least m
// ticks from W = m*P+1 clocks and over M ticks from W = (M+1)*P+1, so a
// minimum of W clocks is (W-1)/P ticks and a maximum W/P-1, exact at P 1.
// TRLC-LINKS: REQ-SDS-011
func qualTicks(minNs, maxNs float64) (prescale, minTicks, maxTicks uint32) {
	longest := math.Max(math.Max(minNs, maxNs), 0)
	prescale = 1
	for prescale < sramcapture.QualMaxPrescale && longest/float64(sramcapture.QualTickNs*int(prescale)) > sramcapture.QualMaxTicks {
		prescale *= 2
	}
	ticks := func(t float64) uint32 { return uint32(math.Min(sramcapture.QualMaxTicks, math.Max(math.Round(t), 0))) }
	p := float64(prescale)
	minW, maxW := math.Max(minNs, 0)/sramcapture.QualTickNs, math.Max(maxNs, 0)/sramcapture.QualTickNs
	return prescale, ticks((minW - 1) / p), ticks(maxW/p - 1)
}

// qualName names a qualifier kind for the trigger backend.
// TRLC-LINKS: REQ-SDS-011
func qualName(kind uint8) string {
	switch kind {
	case sramcapture.QualPulse:
		return "pulse"
	case sramcapture.QualSlope:
		return "slope"
	case sramcapture.QualVideo:
		return "video"
	}
	return ""
}

// placeQualified places a qualified trigger on its event: the closing edge's
// level crossing (a pulse's closing edge, a slope's second level), and for a
// video line its sync's leading edge, where the hardware fired at the sync's
// end. The search starts where an edge trigger on the closing edge would
// have fired (the qualifier fires sramcapture.QualFireNs later, a slope
// QualSlopeFireNs). The FPGA decided the trigger; only its position moves.
// TRLC-LINKS: REQ-SDS-011
func placeQualified(f *sample.Frame, disc []uint8, q []uint16, qc sramcapture.QualifierConfig, level uint8) float64 {
	x := f.TriggerAt
	if !f.Status.Triggered || !(x >= 0) || f.Content == sample.PeakPairs {
		return x
	}
	if f.SampleS > 0 {
		late := float64(sramcapture.QualFireNs)
		if qc.Kind == sramcapture.QualSlope {
			late = sramcapture.QualSlopeFireNs
		}
		x = math.Max(x-late*1e-9/f.SampleS, 0)
	}
	switch qc.Kind {
	case sramcapture.QualPulse:
		return core.RefineTriggerEdge(disc, q, f.Len(), x, float64(level), qc.Negative)
	case sramcapture.QualSlope:
		if qc.Negative {
			return core.RefineTriggerEdge(disc, q, f.Len(), x, float64(qc.LevelA), false)
		}
		return core.RefineTriggerEdge(disc, q, f.Len(), x, float64(qc.LevelB), true)
	case sramcapture.QualVideo:
		end := core.RefineTriggerEdge(disc, q, f.Len(), x, float64(level), qc.Negative)
		// back to the sync's leading edge, at most a line sync's width before
		span := 8e-6 / f.SampleS
		for i := int(end); i > 0 && float64(int(end)-i) <= span; i-- {
			a, b := disc[i-1], disc[i]
			if (qc.Negative && a > level && b <= level) || (!qc.Negative && a < level && b >= level) {
				return core.CrossFrac(disc, i, int(level))
			}
		}
		return end
	}
	return x
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

// planZones is the zone trigger in the FPGA (ADR-HARDWARE-QUALIFIERS): each
// zone's window around the edge in buckets of the smallest power-of-two
// clocks that keep every window and the decision's wait inside the 1024
// buckets of history, the zones' codes as they are. delayS is how long after
// an edge trigger's the FPGA fires (the wait for the latest window's end and
// the fixed decision time); why says why there is no plan ("" when there is
// one).
// TRLC-LINKS: REQ-SDS-014
func (e *Engine) planZones() (z sramcapture.ZoneConfig, delayS float64, why string) {
	if !e.hardwareQualifier {
		if e.images != nil && e.images.HasQualify() && e.protocolImage != imageQualify {
			return z, 0, "loading the qualifier FPGA image"
		}
		return z, 0, "this FPGA image has no zone trigger"
	}
	e.zm.Mu.Lock()
	zones := append([]Zone(nil), e.zm.Zones...)
	e.zm.Mu.Unlock()
	if len(zones) > len(z.Zones) {
		return z, 0, "the FPGA takes four zones"
	}
	const clockS = sramcapture.ZoneClockNs * 1e-9
	for bucket := 1; bucket <= 4096; bucket *= 2 {
		bs := clockS * float64(bucket)
		minStart, maxEnd := 0, -1<<30
		var ws [4]sramcapture.ZoneWindow
		for i, zn := range zones {
			lo, hi := math.Min(zn.DtLoS, zn.DtHiS), math.Max(zn.DtLoS, zn.DtHiS)
			start, end := int(math.Floor(lo/bs)), int(math.Ceil(hi/bs))
			minStart, maxEnd = min(minStart, start), max(maxEnd, end)
			cl, ch := min(max(zn.CodeLo, 0), 255), min(max(zn.CodeHi, 0), 255)
			ws[i] = sramcapture.ZoneWindow{
				Enabled: true, Channel: uint8(zn.Ch & 1), Lo: uint8(min(cl, ch)), Hi: uint8(max(cl, ch)),
				Start: int16(max(start, -1024)), End: int16(min(end, 1024)), Avoid: zn.Avoid, //nolint:gosec // G115: a bucket is refused below unless both lie within ±1000
			}
		}
		wait := max(maxEnd+1, 1)
		if minStart < -1000 || maxEnd > 1000 || wait-minStart+1 > 1023 {
			continue // too fine a bucket for the windows: twice as long
		}
		z = sramcapture.ZoneConfig{Zones: ws, Wait: uint16(wait), Bucket: uint16(bucket)}
		return z, float64(wait)*bs + (sramcapture.ZoneDecideClocks*sramcapture.ZoneClockNs+sramcapture.ZoneFireNs)*1e-9, ""
	}
	return sramcapture.ZoneConfig{}, 0, "the zones span more time than the FPGA's history"
}
