// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"math"
	"slices"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// SRAMPlan retains the complete physical record. Reduction is the smallest
// supported power of two that fits a screen into that record, leaving the ARM
// all remaining samples for measurements and display reduction.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
type SRAMPlan struct {
	Log             uint8
	Samples, Screen int
	SampleS         float64
}

// FullRateTdiv is the slowest of tdivs whose SRAM record is sampled at the
// fastest rate (0 for none): where BUS detection looks again when a record
// hid the bus (decimated, or too short).
// TRLC-LINKS: REQ-SDS-214
func FullRateTdiv(tdivs []float64) float64 {
	if len(tdivs) == 0 {
		return 0
	}
	full, best := PlanSRAM(slices.Min(tdivs)).SampleS, 0.0
	for _, t := range tdivs {
		if PlanSRAM(t).SampleS <= full && t > best {
			best = t
		}
	}
	return best
}

// PlanSRAM plans the SRAM record for a timebase: the full record at 2 ns a
// sample while ten divisions fit, else the smallest decimation that fits them.
// TRLC-LINKS: REQ-SDS-214
func PlanSRAM(tdiv float64) SRAMPlan {
	p := SRAMPlan{Samples: int(sramcapture.SamplesPerChannel), SampleS: 2e-9}
	if !(tdiv > 0) || math.IsInf(tdiv, 0) {
		tdiv = 500e-6
	}
	if 10*tdiv > float64(p.Samples)*p.SampleS {
		p.Log = 4
		p.Samples = int(sramcapture.Words)
		p.SampleS = 32e-9
		for p.Log < 20 && 10*tdiv > float64(p.Samples)*p.SampleS {
			p.Log++
			p.SampleS *= 2
		}
	}
	p.Screen = int(math.Min(float64(p.Samples), math.Max(2, math.Round(10*tdiv/p.SampleS))))
	return p
}

// LiveSRAMPlan decimates p until its screen fits budget samples (or the
// deepest decimation), with a record of a few screens; ok is false when p's
// screen already reads in budget words.
// TRLC-LINKS: REQ-SDS-010
func LiveSRAMPlan(p SRAMPlan, budget int) (SRAMPlan, bool) {
	// A raw screen packs two samples a word: what bounds the readout is words.
	if words := p.Screen / (2 - BoolInt(p.Log != 0)); words <= budget {
		return p, false
	}
	span := float64(p.Screen) * p.SampleS
	if p.Log < 4 {
		p.Log, p.SampleS = 4, FinestDecimatedS
	}
	for p.Log < 20 && span/p.SampleS > float64(budget) {
		p.Log++
		p.SampleS *= 2
	}
	p.Screen = int(math.Max(2, math.Round(span/p.SampleS)))
	p.Samples = min(int(sramcapture.Words), max(4*p.Screen, 8192))
	return p, true
}

// PlanPrecisionSRAM keeps the selected sample rate fixed as the view changes.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-036
func PlanPrecisionSRAM(tdiv float64, log uint8) SRAMPlan {
	if !(tdiv > 0) || math.IsInf(tdiv, 0) {
		tdiv = 500e-6
	}
	if log < 4 {
		log = 4
	}
	if log > 20 {
		log = 20
	}
	p := SRAMPlan{Log: log, Samples: int(sramcapture.Words), SampleS: float64(uint64(1)<<log) / 500e6}
	p.Screen = int(math.Min(float64(p.Samples), math.Max(2, math.Round(10*tdiv/p.SampleS))))
	return p
}

// SecondLevel is the code SPI's data line or I2C's SDA is sliced at: its
// own threshold when planned (serialPlanParams), else the first line's.
// TRLC-LINKS: REQ-SDS-013
func SecondLevel(p core.SerialParams) uint8 {
	if p.HaveThrB && p.ThresholdB >= 0 && p.ThresholdB <= 255 {
		return uint8(math.Round(p.ThresholdB))
	}
	return uint8(math.Round(p.Threshold))
}

// SampleSPIFastGap is the idle gap of an SPI clock of 25 MHz and up (in
// receiver clocks), and sampleSPIFastBand the re-arm band its receiver on
// the samples takes (codes each side of the level).
const (
	SampleSPIFastGap  = 8
	SampleSPIFastBand = 6
)

// BoolInt is v as 1 or 0.
// TRLC-LINKS: REQ-SDS-040
func BoolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// LiveSRAMWindowPre is liveSRAMWindow holding extraPre more samples before the
// trigger, as many as the record holds beyond the screen: the screen's end
// stays (a T55xx command at 2ms/div lost its last 2 ms to a serial
// trigger's room).
// TRLC-LINKS: REQ-SDS-010
func LiveSRAMWindowPre(m sramcapture.Metadata, screen int, posFrac float64, extraPre int) (uint32, uint32) {
	per := int(m.SamplesPerWord)
	samples := max(2048, screen+512)
	if m.FractionBits == 8 {
		samples = max(256, screen+128)
	}
	margin := samples - screen
	extraPre = max(0, extraPre)
	if over := samples + extraPre - int(m.Length)*per; over > 0 {
		extraPre = max(0, extraPre-over)
	}
	samples += extraPre
	words := min(m.Length, uint32((samples+per-1)/per)) //nolint:gosec // G115: samples are bounded by the record
	// The trigger sits at posFrac of the screen, so the window holds
	// screen·posFrac samples before it plus half the margin. Centring the
	// window leaves a quarter of the screen as a flat extension at trigger
	// position 0.2 or 0.8 (1 µs/div).
	if !(posFrac >= 0 && posFrac <= 1) {
		posFrac = .5
	}
	pre := uint32((float64(screen)*posFrac + float64(margin)/2 + float64(extraPre)) / float64(per))
	pre = min(pre, words)
	offset := uint32(0)
	if m.TriggerIndex > pre {
		offset = m.TriggerIndex - pre
	}
	if offset+words > m.Length {
		offset = m.Length - words
	}
	return offset, words
}

// RetainedSRAMPlan is p at timebase tdiv after a stopped timebase change: it
// reuses the captured sample pitch and depth.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
func RetainedSRAMPlan(p SRAMPlan, tdiv float64) SRAMPlan {
	p.Screen = int(math.Min(float64(p.Samples), math.Max(2, math.Round(10*tdiv/p.SampleS))))
	return p
}
