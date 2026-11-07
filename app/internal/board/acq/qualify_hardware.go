// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"math"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// ImageQualify is the image with the hardware qualifiers.
const ImageQualify = "qualify"

// Video line numbers: the first line sync after a field's vertical sync and
// its post-equalizing pulses is line 10 in NTSC (525) and line 6 in PAL (625).
const (
	VideoFirstLineNTSC = 10
	VideoFirstLinePAL  = 6
)

// QualTicks expresses the limits in ticks of the smallest power-of-two
// prescale P (1..65536 clocks) that holds both. The FPGA counts an interval
// whose edges are W clocks apart as floor((W-1)/P) ticks: it is at least m
// ticks from W = m*P+1 clocks and over M ticks from W = (M+1)*P+1, so a
// minimum of W clocks is (W-1)/P ticks and a maximum W/P-1, exact at P 1.
// TRLC-LINKS: REQ-SDS-011
func QualTicks(minNs, maxNs float64) (prescale, minTicks, maxTicks uint32) {
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

// QualName names a qualifier kind for the trigger backend.
// TRLC-LINKS: REQ-SDS-011
func QualName(kind uint8) string {
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

// PlaceQualified places a qualified trigger on its event: the closing edge's
// level crossing (a pulse's closing edge, a slope's second level), and for a
// video line its sync's leading edge, where the hardware fired at the sync's
// end. The search starts where an edge trigger on the closing edge would
// have fired (the qualifier fires sramcapture.QualFireNs later, a slope
// QualSlopeFireNs). The FPGA decided the trigger; only its position moves.
// TRLC-LINKS: REQ-SDS-011
func PlaceQualified(f *sample.Frame, disc []uint8, q []uint16, qc sramcapture.QualifierConfig, level uint8) float64 {
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
