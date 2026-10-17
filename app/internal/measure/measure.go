// Package measure computes oscilloscope auto-measurements over a raw 8-bit
// sample record. It is shared by the web frame path and the on-device LCD so
// both surfaces report identical numbers from one implementation.
//
// The vertical set (Vpp/Vmax/Vmin/Vmean/Vrms/Vtop/Vbase/Vampl and over/preshoot)
// is span/level based; the timing set (Freq/Period/Duty/rise/fall/±width) is
// derived from interpolated threshold crossings so it is accurate to a fraction
// of a sample. Timing is reported only when the record has a real, resolvable
// edge (a minimum amplitude and at least one full cycle).
// ENGMODEL-OWNER-UNIT: FU-APP-MEASURE
package measure

import (
	"math"
	"slices"
)

// Result is the full auto-measurement set: volts, seconds and Hz. HasTiming is
// false when the record has no resolvable edge (flat/DC or sub-noise), in which
// case the timing fields are zero and callers should show them as "—".
// TRLC-LINKS: REQ-SDS-017
type Result struct {
	Vpp    float64 `json:"vpp"`
	Vmax   float64 `json:"vmax"`
	Vmin   float64 `json:"vmin"`
	Vmean  float64 `json:"vmean"`
	Vrms   float64 `json:"vrms"`    // true RMS, DC included (SCPI RMS)
	VrmsAC float64 `json:"vrms_ac"` // RMS about the mean (standard deviation)
	Vtop   float64 `json:"vtop"`
	Vbase  float64 `json:"vbase"`
	Vampl  float64 `json:"vampl"` // top − base (settled amplitude)

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
	// EnvTiming marks timing measured on a peak-detect envelope: period,
	// frequency, duty and widths hold to a bucket, but edge shape (rise, fall,
	// over/preshoot) is unknown and reads 0 - unless EdgeShape.
	EnvTiming bool `json:"env_timing"`
	// EdgeShape marks rise, fall and over/preshoot taken from full-rate
	// windows around an envelope frame's edges (WithEdgeDetail).
	EdgeShape bool `json:"edge_shape"`
	// EnvAliased marks an envelope whose buckets mostly span the swing: the
	// signal is faster than a bucket, and the envelope's (min, max) pairs give
	// no mean or RMS (a pulse train's mean read its midpoint, 1.63 V for
	// 0.60 V) - Vmean, Vrms and AC rms are unknown unless the edge windows,
	// which then hold several periods, gave them.
	EnvAliased bool `json:"env_aliased"`
	// WindowTiming and WindowMean mark the timing (frequency, period, duty,
	// widths) and the mean and RMS taken from the edge windows: a signal the
	// envelope does not resolve (WithEdgeDetail). Like the edge shape, they
	// are carried to the frames between window reads.
	WindowTiming bool `json:"window_timing,omitempty"`
	WindowMean   bool `json:"window_mean,omitempty"`
}

// EdgeWindow is one channel's samples around an edge of an envelope frame's
// record, at the record's rate.
type EdgeWindow struct {
	Sig     []uint8
	Q       []uint16 // calibrated Q8.8, or nil
	SampleS float64
	// Shape: the window samples the full rate, so its edges are the signal's;
	// a decimated record's are its filter's step response, which serves the
	// mean and RMS but no rise, fall or over/preshoot.
	Shape bool
}

// WithEdgeDetail takes rise, fall, overshoot and preshoot from full-rate
// windows around an envelope frame's edges - the envelope's (min, max)
// buckets hold no edge shape - and keeps every other figure. A window without
// a clean edge of the channel adds nothing. A signal faster than the
// envelope's buckets resolve has no timing there, and an aliased mean; a
// window holding whole periods gives both.
// TRLC-LINKS: REQ-SDS-021
func (r *Result) WithEdgeDetail(voltsPerCode, offV float64, windows ...EdgeWindow) *Result {
	if r == nil || len(windows) == 0 {
		return r
	}
	c := *r
	var sum, sq float64 // an aliased envelope's mean and RMS from the windows
	var count int
	var period, pos, neg avg // timing the envelope does not resolve
	for _, w := range windows {
		if len(w.Sig) == 0 || !(w.SampleS > 0) {
			continue
		}
		t := ComputeAcquisition(w.Sig, w.Q, voltsPerCode, offV, w.SampleS, 0)
		if t != nil && r.EnvAliased {
			sum += t.Vmean
			sq += t.Vrms * t.Vrms
			count++
		}
		if t != nil && !r.HasTiming && t.HasTiming {
			period.add(t.Period)
			pos.add(t.PosWidthS)
			neg.add(t.NegWidthS)
		}
		if t == nil || !w.Shape || !(t.RiseS > 0 || t.FallS > 0) {
			continue
		}
		if c.RiseS == 0 && t.RiseS > 0 {
			c.RiseS = t.RiseS
		}
		if c.FallS == 0 && t.FallS > 0 {
			c.FallS = t.FallS
		}
		c.Overshoot, c.Preshoot = max(c.Overshoot, t.Overshoot), max(c.Preshoot, t.Preshoot)
		c.EdgeShape = true
	}
	if count > 0 {
		// Faster than a bucket, the windows hold several periods each.
		c.Vmean = sum / float64(count)
		c.Vrms = math.Sqrt(sq / float64(count))
		c.VrmsAC = math.Sqrt(math.Max(0, c.Vrms*c.Vrms-c.Vmean*c.Vmean))
		c.EnvAliased, c.WindowMean = false, true
	}
	if p := period.mean(); p > 0 {
		c.Period, c.Freq = p, 1/p
		c.PosWidthS, c.NegWidthS = pos.mean(), neg.mean()
		c.Duty = c.PosWidthS / p * 100
		c.HasTiming, c.EnvTiming, c.WindowTiming = true, false, true
	}
	return &c
}

// avg is a running mean of the positive values added.
type avg struct {
	sum float64
	n   int
}

// TRLC-LINKS: REQ-SDS-021
func (a *avg) add(v float64) {
	if v > 0 {
		a.sum += v
		a.n++
	}
}

// mean is the values' mean, 0 with none.
// TRLC-LINKS: REQ-SDS-021
func (a *avg) mean() float64 {
	if a.n == 0 {
		return 0
	}
	return a.sum / float64(a.n)
}

// Inverted is the result for the channel displayed inverted (SCPI Cn:INVS):
// levels negate (max and min, top and base swap), rising and falling edges
// and positive and negative widths swap, duty becomes its complement; Vpp,
// amplitude, RMS, frequency and period hold. A new value: callers cache r.
// Without it an inverted trace reads Vmax +3.3 V while it runs 0 to -3.3 V.
// TRLC-LINKS: REQ-SDS-021
func (r *Result) Inverted() *Result {
	if r == nil {
		return nil
	}
	c := *r
	c.Vmax, c.Vmin = -r.Vmin, -r.Vmax
	c.Vmean = -r.Vmean
	c.Vtop, c.Vbase = -r.Vbase, -r.Vtop
	c.Overshoot, c.Preshoot = r.Preshoot, r.Overshoot
	c.RiseS, c.FallS = r.FallS, r.RiseS
	c.PosWidthS, c.NegWidthS = r.NegWidthS, r.PosWidthS
	if r.HasTiming && r.Duty != 0 {
		c.Duty = 100 - r.Duty
	}
	return &c
}

// DropTiming removes the timing results: a peak-detect envelope holds each
// bucket's extremes, not samples in time order, so periods, widths and edges
// measured on it are artefacts. Amplitude extremes stay valid.
// TRLC-LINKS: REQ-SDS-021
func (r *Result) DropTiming() {
	if r == nil {
		return
	}
	r.Freq, r.Period, r.Duty, r.RiseS, r.FallS, r.PosWidthS, r.NegWidthS = 0, 0, 0, 0, 0, 0, 0
	r.Overshoot, r.Preshoot = 0, 0
	r.HasTiming, r.EnvTiming, r.EdgeShape, r.WindowTiming = false, false, false, false
}

// WithoutTiming returns a copy with the timing results removed (see
// DropTiming); cached results are left untouched.
// TRLC-LINKS: REQ-SDS-021
func (r *Result) WithoutTiming() *Result {
	if r == nil {
		return nil
	}
	c := *r
	c.DropTiming()
	return &c
}

// envMinBuckets is the fewest envelope buckets per period for which an
// envelope's timing is trusted: each edge is placed to within one bucket.
const envMinBuckets = 8

// WithEnvelopeTiming returns a copy whose timing is measured on a peak-detect
// envelope (min,max pairs per bucket, sampleS apart) instead of dropped. The
// pairs are not samples in time order, but each bucket's midpoint is: a level
// bucket sits at the level, an edge bucket between. Period, frequency, duty
// and pulse widths come from those midpoints, to within a bucket, when a
// period spans at least envMinBuckets buckets; rise/fall and over/preshoot,
// which happen inside a bucket, stay dropped. Without it every running frame
// at 10 us/div and slower has no frequency.
// TRLC-LINKS: REQ-SDS-021
func (r *Result) WithEnvelopeTiming(env []uint8, voltsPerCode, offV, sampleS float64) *Result {
	c := r.WithoutTiming()
	n := len(env) / 2
	if c == nil {
		return c
	}
	c.EnvAliased = envelopeAliased(env)
	if n < 2*envMinBuckets || !(sampleS > 0) || envelopeNarrowPulse(env) {
		return c
	}
	mids := make([]uint8, n)
	for i := range mids {
		mids[i] = uint8((int(env[2*i]) + int(env[2*i+1]) + 1) / 2)
	}
	bucket := 2 * sampleS
	t := Compute(mids, voltsPerCode, offV, bucket)
	if !t.HasTiming || t.Period < envMinBuckets*bucket {
		return c
	}
	// Midpoints of an unresolved signal alias into a slower false period.
	// A resolved one has about two edge buckets (spanning the swing) per
	// period; refuse when there are many more than the period implies.
	lo, hi := env[0], env[0]
	for _, v := range env {
		lo, hi = min(lo, v), max(hi, v)
	}
	q1, q3 := int(lo)+(int(hi)-int(lo))/4, int(hi)-(int(hi)-int(lo))/4
	edges := 0
	for i := 0; i < n; i++ {
		a, b := int(env[2*i]), int(env[2*i+1])
		if min(a, b) <= q1 && max(a, b) >= q3 {
			edges++
		}
	}
	if float64(edges) > 1.5*2*float64(n)*bucket/t.Period+2 {
		return c
	}
	c.Freq, c.Period, c.Duty, c.PosWidthS, c.NegWidthS = t.Freq, t.Period, t.Duty, t.PosWidthS, t.NegWidthS
	c.HasTiming, c.EnvTiming = true, true
	return c
}

// envelopeNarrowPulse reports a pulse within two buckets: one or two buckets
// that span the middle half of the swing between buckets wholly at the same
// level. Bucket midpoints miss such a pulse, so the timing would count too few
// edges (1, 2, 3 and 5 us pulses every 60 us at 200us/div read 51 kHz, not
// 66.8 kHz): it is unknown instead.
// TRLC-LINKS: REQ-SDS-021
func envelopeNarrowPulse(env []uint8) bool {
	n := len(env) / 2
	if n < 3 {
		return false
	}
	lo, hi := env[0], env[0]
	for _, v := range env {
		lo, hi = min(lo, v), max(hi, v)
	}
	if int(hi)-int(lo) < minAmplCodes {
		return false
	}
	q1, q3 := int(lo)+(int(hi)-int(lo))/4, int(hi)-(int(hi)-int(lo))/4
	level := func(i int) int { // -1 wholly low, 1 wholly high, 0 spanning or between
		a, b := int(env[2*i]), int(env[2*i+1])
		switch {
		case max(a, b) <= q1:
			return -1
		case min(a, b) >= q3:
			return 1
		}
		return 0
	}
	spans := func(i int) bool {
		a, b := int(env[2*i]), int(env[2*i+1])
		return min(a, b) <= q1 && max(a, b) >= q3
	}
	for i := 1; i+1 < n; i++ {
		if !spans(i) || spans(i-1) {
			continue
		}
		j := i // the run of spanning buckets from i
		for j+1 < n && spans(j+1) && j-i < 2 {
			j++
		}
		if j-i < 2 && j+1 < n {
			if l := level(i - 1); l != 0 && l == level(j+1) {
				return true
			}
		}
	}
	return false
}

// envelopeAliased reports whether most of an envelope's buckets span the
// middle half of its swing: a signal faster than a bucket.
// TRLC-LINKS: REQ-SDS-021
func envelopeAliased(env []uint8) bool {
	n := len(env) / 2
	if n == 0 {
		return false
	}
	lo, hi := env[0], env[0]
	for _, v := range env {
		lo, hi = min(lo, v), max(hi, v)
	}
	if int(hi)-int(lo) < minAmplCodes {
		return false
	}
	q1, q3 := int(lo)+(int(hi)-int(lo))/4, int(hi)-(int(hi)-int(lo))/4
	spans := 0
	for i := 0; i < n; i++ {
		a, b := int(env[2*i]), int(env[2*i+1])
		if min(a, b) <= q1 && max(a, b) >= q3 {
			spans++
		}
	}
	return 2*spans > n
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
// TRLC-LINKS: REQ-SDS-017
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
// TRLC-LINKS: REQ-SDS-017
func Compute(sig []uint8, voltsPerCode, offV, sampleS float64) *Result {
	return compute(sig, voltsPerCode, offV, sampleS, 1)
}

// ComputeQ8 preserves fractional acquisition codes in every measurement.
// TRLC-LINKS: REQ-SDS-017
func ComputeQ8(sig []uint16, voltsPerCode, offV, sampleS float64) *Result {
	return compute(sig, voltsPerCode/256, offV, sampleS, 256)
}

// ComputeAcquisition chooses the precision record when available. Coupling
// 1 removes its mean in the measurement domain, without quantizing samples;
// coupling 2 grounds the displayed input. The legacy path is already coupled.
// TRLC-LINKS: REQ-SDS-017
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
		r.Vrms = r.VrmsAC
	}
	return r
}

// TRLC-LINKS: REQ-SDS-017
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
	// The modes are found on whole ADC codes (below): a Q8.8 value's code is
	// its high byte. Counting codes directly keeps a 65536-bin histogram and
	// its per-bin division (no divide instruction on this ARM: a fifth of the
	// device's CPU at the live frame rate) out of every measurement.
	shift := 0
	if scale >= 256 {
		shift = 8
	}
	coarse := make([]int, 256)
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
		coarse[min(iv>>shift, 255)]++
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
	// The modes are found on whole ADC codes: a Q8.8 histogram has 256 bins per
	// code, far too sparse for a mode. The settled level is then the mean of the
	// samples in the winning code.
	settled := func(code int) int {
		if shift == 0 {
			return code
		}
		// int64: the device's int is 32 bits, and a 1 M-sample record's
		// count × Q8.8 code (~2.6e10) wraps it, so a single shot would measure
		// top -6.35 V and base -4.82 V on a 0-3.3 V square.
		var n, sum int64
		for _, v := range sig {
			if int(v)>>shift == code {
				n++
				sum += int64(v)
			}
		}
		if n == 0 {
			return code<<shift + 1<<shift/2
		}
		return int((sum + n/2) / n)
	}
	mid := (cmin + cmax) / 2
	topCode, baseCode := cmax, cmin
	if c := modeInRange(coarse, mid>>shift+1, cmax>>shift); c >= 0 {
		topCode = settled(c)
	}
	if c := modeInRange(coarse, cmin>>shift, mid>>shift); c >= 0 {
		baseCode = settled(c)
	}

	r := &Result{
		Vpp:    float64(cmax-cmin) * voltsPerCode,
		Vmax:   toV(float64(cmax)),
		Vmin:   toV(float64(cmin)),
		Vmean:  toV(mean),
		VrmsAC: math.Sqrt(variance) * voltsPerCode,
		Vtop:   toV(float64(topCode)),
		Vbase:  toV(float64(baseCode)),
		Vampl:  float64(topCode-baseCode) * voltsPerCode,
	}
	// True RMS of the displayed volts: the AC RMS and the mean in quadrature.
	// Reporting the standard deviation as Vrms reads 1.6 V for a 0-3.3 V square
	// (2.32 V true RMS).
	r.Vrms = math.Hypot(r.Vmean, r.VrmsAC)
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
		period := cyclePeriod(riseIdx) * sampleS
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
// TRLC-LINKS: REQ-SDS-017
func interp(a, b float64, i int, level float64) float64 {
	if b == a {
		return float64(i)
	}
	return float64(i-1) + (level-a)/(b-a)
}

// modeInRange returns the most-populated code in [lo,hi] (inclusive), or -1 if
// the range is empty.
// TRLC-LINKS: REQ-SDS-017
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

// cyclePeriod is the period of the rising crossings at idx (in samples): the
// mean of the edge-to-edge intervals within a quarter of their median. A
// steady signal keeps every interval (the mean is then the span over the
// cycles, to a fraction of a sample); a burst clock (an SPI flash read)
// leaves out the idle gaps between its bursts, which as a span over the
// record read a 104 MHz clock as 75 MHz.
// TRLC-LINKS: REQ-SDS-017
func cyclePeriod(idx []float64) float64 {
	gaps := make([]float64, 0, len(idx)-1)
	for k := 1; k < len(idx); k++ {
		gaps = append(gaps, idx[k]-idx[k-1])
	}
	sorted := append([]float64(nil), gaps...)
	slices.Sort(sorted)
	med := sorted[len(sorted)/2]
	sum, n := 0.0, 0
	for _, g := range gaps {
		if math.Abs(g-med) <= 0.25*med {
			sum += g
			n++
		}
	}
	if n == 0 {
		return med
	}
	return sum / float64(n)
}

// avgWidth returns the mean sample-count from each `from` crossing to the next
// `to` crossing after it (0 if none pair up). Used for ± pulse width. Both
// lists must be ascending (they are: Compute builds them scanning the record
// forward), which makes the matching `to` index non-decreasing across `from` —
// a single merge pass instead of a rescan per crossing (a deep 20480-sample
// record with many cycles made the rescan quadratic).
// TRLC-LINKS: REQ-SDS-017
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
// TRLC-LINKS: REQ-SDS-017
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
