// Package measure computes oscilloscope auto-measurements over a raw 8-bit
// sample record. It is shared by the web frame path and the on-device LCD so
// both surfaces report identical numbers from one implementation.
//
// The vertical set (Vpp/Vmax/Vmin/Vmean/Vrms/Vtop/Vbase/Vampl and over/preshoot)
// is span/level based; the timing set (Freq/Period/Duty/rise/fall/±width) is
// derived from interpolated threshold crossings so it is accurate to a fraction
// of a sample. Timing is reported only when the record has a real, resolvable
// edge (a minimum amplitude and at least one full cycle).
package measure

import "math"

// Result is the full auto-measurement set: volts, seconds and Hz. HasTiming is
// false when the record has no resolvable edge (flat/DC or sub-noise), in which
// case the timing fields are zero and callers should show them as "—".
type Result struct {
	Vpp   float64 `json:"vpp"`
	Vmax  float64 `json:"vmax"`
	Vmin  float64 `json:"vmin"`
	Vmean float64 `json:"vmean"`
	Vrms  float64 `json:"vrms"`
	Vtop  float64 `json:"vtop"`
	Vbase float64 `json:"vbase"`
	Vampl float64 `json:"vampl"` // top − base (settled amplitude)

	Overshoot float64 `json:"overshoot"` // percent of amplitude above top
	Preshoot  float64 `json:"preshoot"`  // percent of amplitude below base

	Freq      float64 `json:"freq"`
	Period    float64 `json:"period"`
	Duty      float64 `json:"duty"` // percent high
	RiseS     float64 `json:"rise_s"`
	FallS     float64 `json:"fall_s"`
	PosWidthS float64 `json:"pos_width_s"`
	NegWidthS float64 `json:"neg_width_s"`

	HasTiming bool `json:"has_timing"`
}

// minAmplCodes is the smallest top−base span (in ADC codes) for which timing is
// considered resolvable; below it the record is treated as flat/noise.
const minAmplCodes = 8

// Clipped reports whether a trace is clipping against the ADC rails, calibrated
// to THIS hardware (device-measured): the low rail clamps dead-consistently at
// code 6, the high rail at 252–255 under overdrive. Crucially, a clean signal
// whose real level sits high on screen reaches code ~249 (3.9 divisions) — only
// a few codes below the moderate high clamp — so a high-code test alone
// false-flags it. The low rail (6) is the robust discriminator: a clean signal's
// low excursion is nowhere near it, and this symmetric front end rails BOTH ends
// on overdrive, so low-rail pileup catches it; the high side only flags at the
// hard clamp (>=253). >0.5 % of samples piled within 2 codes of the rail ⇒ clipped.
func Clipped(sig []uint8) bool {
	n := len(sig)
	if n == 0 {
		return false
	}
	mn, mx := 255, 0
	for _, v := range sig {
		iv := int(v)
		if iv < mn {
			mn = iv
		}
		if iv > mx {
			mx = iv
		}
	}
	if mn > 6 && mx < 253 { // neither extreme is against a rail
		return false
	}
	hi, lo := 0, 0
	for _, v := range sig {
		iv := int(v)
		if iv >= mx-2 {
			hi++
		}
		if iv <= mn+2 {
			lo++
		}
	}
	return (mn <= 6 && lo*200 > n) || (mx >= 253 && hi*200 > n)
}

// Compute returns the measurement set for a record. voltsPerCode maps an ADC
// code step to volts (already probe-scaled); offV is the input-referred offset
// (v = (code-128)·voltsPerCode − offV); sampleS is per-sample seconds. Returns
// nil for an empty record.
func Compute(sig []uint8, voltsPerCode, offV, sampleS float64) *Result {
	n := len(sig)
	if n == 0 {
		return nil
	}
	cmin, cmax := int(sig[0]), int(sig[0])
	var sum, sum2 float64
	var hist [256]int
	for _, v := range sig {
		iv := int(v)
		if iv < cmin {
			cmin = iv
		}
		if iv > cmax {
			cmax = iv
		}
		sum += float64(iv)
		sum2 += float64(iv) * float64(iv)
		hist[iv]++
	}
	mean := sum / float64(n)
	variance := sum2/float64(n) - mean*mean
	if variance < 0 {
		variance = 0
	}
	toV := func(code float64) float64 { return (code-128)*voltsPerCode - offV }

	// Top/base via histogram modes either side of the midpoint — robust against
	// overshoot ringing (which max/min would capture). Falls back to max/min for
	// a signal with no clear two-level structure (e.g. a sine).
	mid := (cmin + cmax) / 2
	topCode := modeInRange(hist[:], mid+1, cmax)
	baseCode := modeInRange(hist[:], cmin, mid)
	if topCode < 0 {
		topCode = cmax
	}
	if baseCode < 0 {
		baseCode = cmin
	}

	r := &Result{
		Vpp:   float64(cmax-cmin) * voltsPerCode,
		Vmax:  toV(float64(cmax)),
		Vmin:  toV(float64(cmin)),
		Vmean: toV(mean),
		Vrms:  math.Sqrt(variance) * voltsPerCode,
		Vtop:  toV(float64(topCode)),
		Vbase: toV(float64(baseCode)),
		Vampl: float64(topCode-baseCode) * voltsPerCode,
	}
	// Over/preshoot are the excursions beyond the settled levels, as a percent
	// of amplitude (0 when amplitude is degenerate).
	if amp := topCode - baseCode; amp > 0 {
		r.Overshoot = float64(cmax-topCode) / float64(amp) * 100
		r.Preshoot = float64(baseCode-cmin) / float64(amp) * 100
	}

	// Timing needs a resolvable two-level edge.
	if topCode-baseCode < minAmplCodes || sampleS <= 0 {
		return r
	}
	base, top := float64(baseCode), float64(topCode)
	lo10 := base + 0.10*(top-base)
	hi90 := base + 0.90*(top-base)
	mid50 := base + 0.50*(top-base)

	// 50% crossings drive period/duty (interpolated for sub-sample accuracy).
	var riseIdx, fallIdx []float64
	for i := 1; i < n; i++ {
		a, b := float64(sig[i-1]), float64(sig[i])
		if a < mid50 && b >= mid50 {
			riseIdx = append(riseIdx, interp(a, b, i, mid50))
		} else if a >= mid50 && b < mid50 {
			fallIdx = append(fallIdx, interp(a, b, i, mid50))
		}
	}
	if len(riseIdx) >= 2 {
		period := (riseIdx[len(riseIdx)-1] - riseIdx[0]) / float64(len(riseIdx)-1) * sampleS
		if period > 0 {
			r.Period, r.Freq, r.HasTiming = period, 1/period, true
		}
	}
	// Positive width: a rising 50% crossing to the next falling one; negative:
	// the reverse. Averaged over the record for stability.
	if pw := avgWidth(riseIdx, fallIdx); pw > 0 {
		r.PosWidthS = pw * sampleS
	}
	if nw := avgWidth(fallIdx, riseIdx); nw > 0 {
		r.NegWidthS = nw * sampleS
	}
	if r.HasTiming && r.Period > 0 {
		r.Duty = r.PosWidthS / r.Period * 100
	}
	// Rise/fall over the first clean 10→90 % edge.
	if t10, t90, ok := firstEdge(sig, lo10, hi90, true); ok {
		r.RiseS = (t90 - t10) * sampleS
	}
	if t90, t10, ok := firstEdge(sig, hi90, lo10, false); ok {
		r.FallS = (t10 - t90) * sampleS
	}
	return r
}

// interp returns the fractional sample index where a segment [a,b] straddling
// samples (i-1,i) crosses level. a and b are the sample values.
func interp(a, b float64, i int, level float64) float64 {
	if b == a {
		return float64(i)
	}
	return float64(i-1) + (level-a)/(b-a)
}

// modeInRange returns the most-populated code in [lo,hi] (inclusive), or -1 if
// the range is empty.
func modeInRange(hist []int, lo, hi int) int {
	if lo < 0 {
		lo = 0
	}
	if hi > 255 {
		hi = 255
	}
	best, bestN := -1, 0
	for c := lo; c <= hi; c++ {
		if hist[c] > bestN {
			best, bestN = c, hist[c]
		}
	}
	return best
}

// avgWidth returns the mean sample-count from each `from` crossing to the next
// `to` crossing after it (0 if none pair up). Used for ± pulse width.
func avgWidth(from, to []float64) float64 {
	if len(from) == 0 || len(to) == 0 {
		return 0
	}
	var sum float64
	var cnt int
	for _, f := range from {
		for _, t := range to {
			if t > f {
				sum += t - f
				cnt++
				break
			}
		}
	}
	if cnt == 0 {
		return 0
	}
	return sum / float64(cnt)
}

// firstEdge finds the first clean transition crossing `first` then `second`
// without reversing between them, returning both interpolated crossing indices.
// rising=true looks for an upward edge (first=lo10, second=hi90); rising=false
// a downward edge (first=hi90, second=lo10).
func firstEdge(sig []uint8, first, second float64, rising bool) (float64, float64, bool) {
	n := len(sig)
	for i := 1; i < n; i++ {
		a, b := float64(sig[i-1]), float64(sig[i])
		crossedFirst := (rising && a < first && b >= first) || (!rising && a > first && b <= first)
		if !crossedFirst {
			continue
		}
		t1 := interp(a, b, i, first)
		// Start at j=i so an edge faster than one sample (both thresholds crossed
		// in the same interval) still yields an interpolated, sub-sample rise/fall.
		for j := i; j < n; j++ {
			c, d := float64(sig[j-1]), float64(sig[j])
			// Abort this candidate if the edge reverses before reaching second.
			if rising && d < first {
				break
			}
			if !rising && d > first {
				break
			}
			crossedSecond := (rising && c < second && d >= second) || (!rising && c > second && d <= second)
			if crossedSecond {
				return t1, interp(c, d, j, second), true
			}
		}
	}
	return 0, 0, false
}
