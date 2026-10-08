// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"math"
)

// FPGARawSampleS is the stacking image's raw sample period (interleaved 500 MS/s).
const FPGARawSampleS = 2e-9

// FPGATemplateFloor is the strict per-point allowance for noise (~1 code rms)
// and the raw samples' uncorrected interleave core offsets (up to ~6 codes).
const FPGATemplateFloor = 6

// FPGATemplateLoose selects the loose tolerance in BuildFPGATemplate.
const FPGATemplateLoose = -1

// FPGATemplate is a template window for the FPGA qualifier
// (ADR-STACKING-TEMPLATE-QUALIFIER), in raw-record units.
type FPGATemplate struct {
	Values    []uint8
	Stride    int // raw samples between template points
	Pre       int // raw samples from the window start to the crossing
	Threshold int // largest accepted sum of absolute differences
}

// BuildFPGATemplate takes count points of sig (codes, one per sampleS) around
// the level crossing nearest edgeX, starting a quarter window before it. A
// decimated frame maps to a stride of sampleS/FPGARawSampleS raw samples.
// tolerance is the allowed mean |difference| per point in codes. 0 (the
// default) is strict: each point may differ only by what a true repeat of the
// same waveform can, FPGATemplateFloor codes of noise and uncorrected per-core
// offset plus a quarter of the local template slope, which bounds the error
// of placing the window to within half a template point. FPGATemplateLoose
// allows an eighth of the window's swing per point (at least 4 codes).
// TRLC-LINKS: REQ-SDS-141
func BuildFPGATemplate(sig []float64, sampleS, edgeX float64, level float64, falling bool, count int, tolerance float64) (FPGATemplate, error) {
	var t FPGATemplate
	if count < 2 || count > 1024 {
		return t, fmt.Errorf("template length %d out of 2..1024", count)
	}
	stride := int(math.Round(sampleS / FPGARawSampleS))
	if stride < 1 || stride > 255 {
		return t, fmt.Errorf("frame sample period %.3g s is not 1..255 raw samples; use a faster timebase", sampleS)
	}
	crosses := func(i int) bool {
		if falling {
			return sig[i] > level && sig[i+1] <= level
		}
		return sig[i] < level && sig[i+1] >= level
	}
	start := int(math.Round(edgeX))
	crossing := -1.0
	for d := 0; d < len(sig) && crossing < 0; d++ {
		for _, i := range []int{start - d, start + d} {
			if i >= 0 && i+1 < len(sig) && crosses(i) {
				crossing = float64(i) + (level-sig[i])/(sig[i+1]-sig[i])
				break
			}
		}
	}
	if crossing < 0 {
		return t, fmt.Errorf("no crossing of level %.0f near the trigger", level)
	}
	lead := count / 4
	first := int(math.Round(crossing)) - lead
	if first < 0 || first+count > len(sig) {
		return t, fmt.Errorf("template window leaves the frame")
	}
	t.Values = make([]uint8, count)
	lo, hi := 255.0, 0.0
	for k := range t.Values {
		v := math.Max(0, math.Min(255, math.Round(sig[first+k])))
		t.Values[k] = uint8(v)
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	t.Stride, t.Pre = stride, lead*stride
	switch {
	case tolerance > 0:
		t.Threshold = int(math.Round(tolerance * float64(count)))
	case tolerance == FPGATemplateLoose:
		t.Threshold = int(math.Round(math.Max(4, (hi-lo)/8) * float64(count)))
	default:
		allowed := 0.0
		for k := range t.Values {
			a, b := t.Values[max(k-1, 0)], t.Values[min(k+1, count-1)]
			allowed += FPGATemplateFloor + math.Abs(float64(b)-float64(a))/4
		}
		t.Threshold = int(math.Round(allowed))
	}
	return t, nil
}
