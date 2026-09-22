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
	return compute(sig, voltsPerCode, offV, sampleS, 1)
}

// ComputeQ8 preserves fractional acquisition codes in every measurement.
func ComputeQ8(sig []uint16, voltsPerCode, offV, sampleS float64) *Result {
	return compute(sig, voltsPerCode/256, offV, sampleS, 256)
}

// ComputeAcquisition chooses the precision record when available. Coupling
// 1 removes its mean in the measurement domain, without quantizing samples;
// coupling 2 grounds the displayed input. The legacy path is already coupled.
func ComputeAcquisition(raw []uint8, q []uint16, voltsPerCode, offV, sampleS float64, coupling int, guard ...int) *Result {
	if len(q) != len(raw) || len(q) == 0 {
		return Compute(raw, voltsPerCode, offV, sampleS)
	}
	if len(guard) > 0 && guard[0] > 0 && 2*guard[0] < len(raw) {
		q = q[guard[0] : len(q)-guard[0]]
	}
	if coupling == 2 {
		return &Result{}
	}
	r := ComputeQ8(q, voltsPerCode, offV, sampleS)
	if coupling == 1 {
		mean := r.Vmean
		r.Vmin -= mean
		r.Vmax -= mean
		r.Vtop -= mean
		r.Vbase -= mean
		r.Vmean = 0
	}
	return r
}

func compute[T ~uint8 | ~uint16](sig []T, voltsPerCode, offV, sampleS, scale float64) *Result {
	n := len(sig)
	if n == 0 {
		return nil
	}
	cmin, cmax := int(sig[0]), int(sig[0])
	// Center moments on an exact sample code. Subtracting DC-sized moments
	// otherwise loses small Q8.8 ripple when the record is nearly constant.
	reference := int(sig[0])
	var sum, sum2 int64
	hist := make([]int, int(256*scale))
	for _, v := range sig {
		iv := int(v)
		if iv < cmin {
			cmin = iv
		}
		if iv > cmax {
			cmax = iv
		}
		delta := int64(iv - reference)
		sum += delta
		sum2 += delta * delta
		hist[iv]++
	}
	meanDelta := float64(sum) / float64(n)
	mean := float64(reference) + meanDelta
	variance := float64(sum2)/float64(n) - meanDelta*meanDelta
	if variance < 0 {
		variance = 0
	}
	toV := func(code float64) float64 { return (code-128*scale)*voltsPerCode - offV }

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
	if float64(topCode-baseCode) < minAmplCodes*scale || sampleS <= 0 {
		return r
	}
	base, top := float64(baseCode), float64(topCode)
	lo10 := base + 0.10*(top-base)
	hi90 := base + 0.90*(top-base)
	mid50 := base + 0.50*(top-base)

	// 50% crossings drive period/duty (interpolated for sub-sample accuracy).
	var riseIdx, fallIdx []float64
	// Confirm a complete transition through a Schmitt band; small midpoint
	// recrossings must not turn a single noisy edge into multiple periods.
	hyst := math.Max(scale, .05*(top-base))
	midCode := int(math.Ceil(mid50))
	lowCode := int(math.Floor(mid50 - hyst))
	highCode := int(math.Ceil(mid50 + hyst))
	state := 0
	candidate := -1.0
	if int(sig[0]) <= lowCode {
		state = -1
	}
	if int(sig[0]) >= highCode {
		state = 1
	}
	for i := 1; i < n; i++ {
		a, b := int(sig[i-1]), int(sig[i])
		if state == 0 {
			if b <= lowCode {
				state = -1
			}
			if b >= highCode {
				state = 1
			}
			continue
		}
		if state < 0 {
			if a < midCode && b >= midCode {
				candidate = interp(float64(a), float64(b), i, mid50)
			}
			if b >= highCode && candidate >= 0 {
				riseIdx = append(riseIdx, candidate)
				state = 1
				candidate = -1
			}
		} else {
			if a >= midCode && b < midCode {
				candidate = interp(float64(a), float64(b), i, mid50)
			}
			if b <= lowCode && candidate >= 0 {
				fallIdx = append(fallIdx, candidate)
				state = -1
				candidate = -1
			}
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
// `to` crossing after it (0 if none pair up). Used for ± pulse width. Both
// lists must be ascending (they are: Compute builds them scanning the record
// forward), which makes the matching `to` index non-decreasing across `from` —
// a single merge pass instead of a rescan per crossing (a deep 20480-sample
// record with many cycles made the rescan quadratic).
func avgWidth(from, to []float64) float64 {
	if len(from) == 0 || len(to) == 0 {
		return 0
	}
	var sum float64
	var cnt int
	j := 0
	for _, f := range from {
		for j < len(to) && to[j] <= f {
			j++
		}
		if j == len(to) {
			break
		}
		sum += to[j] - f
		cnt++
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
func firstEdge[T ~uint8 | ~uint16](sig []T, first, second float64, rising bool) (float64, float64, bool) {
	n := len(sig)
	firstUp, firstDown := int(math.Ceil(first)), int(math.Floor(first))
	secondUp, secondDown := int(math.Ceil(second)), int(math.Floor(second))
	for i := 1; i < n; i++ {
		a, b := int(sig[i-1]), int(sig[i])
		crossedFirst := (rising && a < firstUp && b >= firstUp) || (!rising && a > firstDown && b <= firstDown)
		if !crossedFirst {
			continue
		}
		t1 := interp(float64(a), float64(b), i, first)
		// Start at j=i so an edge faster than one sample (both thresholds crossed
		// in the same interval) still yields an interpolated, sub-sample rise/fall.
		for j := i; j < n; j++ {
			c, d := int(sig[j-1]), int(sig[j])
			// Abort this candidate if the edge reverses before reaching second.
			if rising && d < firstUp {
				break
			}
			if !rising && d > firstDown {
				break
			}
			crossedSecond := (rising && c < secondUp && d >= secondUp) || (!rising && c > secondDown && d <= secondDown)
			if crossedSecond {
				return t1, interp(float64(c), float64(d), j, second), true
			}
		}
	}
	return 0, 0, false
}
