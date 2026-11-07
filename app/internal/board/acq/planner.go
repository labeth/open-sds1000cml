// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"math"
	"math/bits"

	"github.com/labeth/open-dso-core/dsp"
	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

type plannerFacet Board

// Plan is the SRAM record for r: the full record at 2 ns a sample while
// ten divisions fit, else the smallest decimation that fits them (PlanSRAM),
// both channels always. A decimated record is filtered to Q8.8 precision
// words (or box averages, or picked samples, on images that do so).
// TRLC-LINKS: REQ-SDS-010
func (pl *plannerFacet) Plan(r hal.Request) (hal.Plan, error) {
	b := (*Board)(pl)
	switch {
	case r.Segments > 1:
		return hal.Plan{}, hal.Refuse("segmented memory", "the SRAM holds one record")
	case r.Mode == hal.RollMode || r.Mode == hal.Stream:
		return hal.Plan{}, hal.Refuse("roll and stream plans", "they run on the stream image through the Streamer")
	case r.Depth != 0 && r.Depth != int(sramcapture.SamplesPerChannel):
		return hal.Plan{}, hal.Refuse("record depth", "the SRAM record is always the full memory")
	}
	for _, c := range r.Channels {
		if c < 0 || c > 1 {
			return hal.Plan{}, hal.Refuse("channel", "the record holds C1 and C2")
		}
	}
	p := PlanSRAM(r.TimePerDivS)
	plan := hal.Plan{
		Request: r, Mode: r.Mode, SampleS: p.SampleS, Depth: p.Samples, AcqDecim: 1 << p.Log,
		ETSFactor: 1, Segments: 1, WinSamples: p.Screen,
		Content: sample.ContentSetOf(sample.RawFullRate, sample.PeakPairs),
	}
	if p.Log != 0 {
		plan.Content = sample.ContentSetOf(sample.RawDecimated, sample.PeakPairs)
		caps := b.trig.caps
		plan.Pick = caps.PickDecim
		if !caps.BoxDecim {
			plan.Precision = hal.PrecisionSpec{RateHz: 1 / p.SampleS, Guard: dsp.PrecisionGuard, PassbandHz: .075 / p.SampleS}
		}
	}
	if r.Mode == hal.PeakDetect && p.Log == 0 {
		plan.Note = "peak detect at the full rate reads the samples themselves"
	}
	return plan, nil
}

// planConfig is plan p as the capture configuration's plan fields: the
// decimation, picking, the record's words and those before the trigger.
// TRLC-LINKS: REQ-SDS-010
func planConfig(p hal.Plan) (planPart, error) {
	var pp planPart
	if p.AcqDecim < 1 || bits.OnesCount(uint(p.AcqDecim)) != 1 {
		return pp, hal.Refuse("decimation", "the fabric decimates by powers of two")
	}
	log := bits.TrailingZeros(uint(p.AcqDecim))
	if log != 0 && (log < 4 || log > 20) {
		return pp, hal.Refuse("decimation", "the fabric decimates by 16 to 2^20")
	}
	if p.Request.PretrigRoom < 0 {
		return pp, hal.Refuse("pretrigger room", "the room before the trigger is a count of samples")
	}
	pp.source, pp.decimLog2, pp.pick = sramcapture.ADC, uint8(log), p.Pick && log != 0 //nolint:gosec // G115: 0 or 4..20
	// A raw word packs two samples, a decimated one one: the divisions by
	// the samples a word are shifts (the Cortex-A8 has no divide
	// instruction, and the owner configures at every pass and look).
	shift := BoolInt(log == 0)
	pp.perWord = 1 + shift
	pp.words = uint32((p.Depth + shift) >> shift) //nolint:gosec // G115: the plan's samples fit the SRAM
	if pp.words == 0 || pp.words > sramcapture.Words {
		return planPart{}, hal.Refuse("record depth", "the SRAM holds one record of its whole memory")
	}
	frac := p.Request.PretrigFrac
	if !(frac >= 0 && frac <= 1) {
		frac = .5
	}
	pre := uint32(math.Floor(float64(pp.words)*frac)) + uint32(p.Request.PretrigRoom>>shift) //nolint:gosec // G115: a room of samples within the record
	pp.pre = min(pre, pp.words-1)
	pp.sampleS = p.SampleS
	return pp, nil
}
