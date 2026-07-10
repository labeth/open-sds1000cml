// comp.go — Go port of the analog-falloff compensation (DSP bandwidth
// enhancement) from internal/web/superres_comp.js, algorithm-faithful so the
// device LCD review and the web show the SAME de-embedded stack (see
// comp_jsparity_test.go and app/docs/falloff-comp-plan.md).
//
// It de-embeds the MEASURED channel magnitude response (front end +
// interconnect) from a crunched super-res stack by reshaping its spectrum
// toward a flat target: G(f) = Htarget·Hcal/(Hcal²+ε²), Wiener-regularised,
// zero-phase, capped at gmax. The stack's extra ENOB is the SNR headroom the
// high-frequency boost spends — a single frame has no headroom to give.
//
// Numeric parity notes (same discipline as superres.go): products that feed
// sums are materialized into named float64 locals so Go's FMA fusion on
// arm64/ppc64 can't diverge from V8/amd64; loop bounds and evaluation order
// mirror the JS statement-for-statement.
package superres

import "math"

// Measured chain magnitude response |H_chain(f)|, normalised to 1 at DC,
// sampled every compDF Hz (JS SRCOMP_HCAL — the bench harmonic-comb cal).
const compDF = 4e6

var compHCal = [...]float64{
	1.0, 0.9292, 0.8725, 0.795, 0.7151, 0.6441, 0.5836, 0.5327,
	0.4892, 0.4525, 0.4212, 0.3959, 0.3778, 0.3644, 0.3493, 0.3265,
	0.2966, 0.2641, 0.2329, 0.2056, 0.1819, 0.1612, 0.1445, 0.1323,
}

const (
	// CompMeasF3Hz is the measured chain −3 dB (reported, not tuned).
	CompMeasF3Hz = 16.4e6
	compFCA      = 20.8e6 // 2-pole fit: dominant pole (interconnect + input C)
	compFCB      = 92e6   //             second pole (≈ scope 100 MHz front end)
)

// CompOpts mirrors the JS options object. Zero fields take the JS
// SRCOMP_DEFAULT values ({fbw:70e6, order:3, eps:0.06, gmax:6}), exactly like
// Object.assign({}, SRCOMP_DEFAULT, opts). BudgetDb/BitsGained/Auto are
// informational (filled by CompAuto).
type CompOpts struct {
	Fbw        float64 // target −3 dB (Hz)
	Order      int     // super-Gaussian order of the flat-top target
	Eps        float64 // Wiener regularisation floor
	Gmax       float64 // hard boost cap (linear)
	BudgetDb   float64 // auto: the spent noise budget (dB)
	BitsGained float64 // auto: the stack bits the budget came from
	Auto       bool    // set by CompAuto
}

func (o CompOpts) withDefaults() CompOpts {
	if o.Fbw == 0 {
		o.Fbw = 70e6
	}
	if o.Order == 0 {
		o.Order = 3
	}
	if o.Eps == 0 {
		o.Eps = 0.06
	}
	if o.Gmax == 0 {
		o.Gmax = 6
	}
	return o
}

// CompCalH is the measured chain response at |f|, linearly interpolated over
// the cal table; beyond it, the fitted 2-pole tail matched at the boundary
// (JS srCompCalH).
func CompCalH(f float64) float64 {
	f = math.Abs(f)
	last := float64(len(compHCal)-1) * compDF
	if f <= 0 {
		return 1
	}
	if f >= last {
		tp := func(ff float64) float64 {
			ra := ff / compFCA
			rb := ff / compFCB
			pa := ra * ra
			pb := rb * rb
			return 1 / math.Sqrt((1+pa)*(1+pb))
		}
		k := compHCal[len(compHCal)-1] / tp(last)
		v := k * tp(f)
		if v < 1e-4 {
			v = 1e-4
		}
		return v
	}
	i := int(math.Floor(f / compDF))
	w := f/compDF - float64(i)
	p1 := compHCal[i] * (1 - w)
	p2 := compHCal[i+1] * w
	return p1 + p2
}

// CompTargetH is the flat-top target response: −3 dB at fbw, order-`order`
// super-Gaussian (JS srCompTargetH).
func CompTargetH(f, fbw float64, order int) float64 {
	r := math.Abs(f) / fbw
	return math.Exp(-0.6931471805599453 * math.Pow(r, float64(2*order)))
}

// CompGain is the real, zero-phase de-embed gain G(f): Wiener inverse of the
// cal reshaped to the target, capped at Gmax (JS srCompGain).
func CompGain(f float64, o CompOpts) float64 {
	o = o.withDefaults()
	hc := CompCalH(f)
	ht := CompTargetH(f, o.Fbw, o.Order)
	num := ht * hc
	d1 := hc * hc
	d2 := o.Eps * o.Eps
	g := num / (d1 + d2)
	if g > o.Gmax {
		return o.Gmax
	}
	return g
}

// CompInfo carries the data-independent filter figures (JS srCompInfo).
type CompInfo struct {
	PeakBoostDb float64 // peak of G(f) in dB
	RecoveredF3 float64 // −3 dB of the compensated response Hcal·G (Hz)
}

// CompFigures scans G and Hcal·G on the JS 0..260 MHz / 0.5 MHz grid.
func CompFigures(o CompOpts) CompInfo {
	o = o.withDefaults()
	var peak, f3, prevDb, prevF float64
	for i := 0; i <= 520; i++ { // f = 0, 0.5e6, …, 260e6 — the exact JS grid
		f := float64(i) * 0.5e6
		g := CompGain(f, o)
		if g > peak {
			peak = g
		}
		hg := CompCalH(f) * g
		if hg < 1e-9 {
			hg = 1e-9
		}
		respDb := 20 * math.Log10(hg)
		if f3 == 0 && f > 0 && respDb <= -3 && prevDb > -3 {
			num := (f - prevF) * (-3 - prevDb)
			f3 = prevF + num/(respDb-prevDb) // linear interp, as in JS
		}
		prevDb, prevF = respDb, f
	}
	return CompInfo{PeakBoostDb: 20 * math.Log10(peak), RecoveredF3: f3}
}

// CompAuto sizes the compensation to spend the stack's MEASURED noise
// reduction as high-frequency boost (JS srCompAuto): budget = bits·6.02·spend
// dB (min 4), then the HIGHEST recovered bandwidth whose peak boost fits.
// Ceilings: 0.8×raw Nyquist and a 200 MHz cal-trust cap; floor 40 MHz.
func CompAuto(bitsGained, rawNyqHz, spend float64) CompOpts {
	s := spend
	if !(s > 0) {
		s = 0.8
	}
	bg := bitsGained
	if math.IsNaN(bg) {
		bg = 0
	}
	t1 := bg * 6.0206
	budgetDb := t1 * s
	if !(budgetDb > 4) {
		budgetDb = 4
	}
	budgetLin := math.Pow(10, budgetDb/20)
	eps := 0.5 / budgetLin // Wiener floor admits up to budgetLin boost
	if eps > 0.12 {
		eps = 0.12
	}
	gmax := budgetLin * 1.25 // hard cap just above the budget
	nyq := rawNyqHz
	if !(nyq > 0) {
		nyq = 250e6
	}
	ceil := 0.8 * nyq
	if ceil > 200e6 {
		ceil = 200e6
	}
	const floor = 40e6
	peak := func(fbw float64) float64 {
		return CompFigures(CompOpts{Fbw: fbw, Eps: eps, Gmax: gmax, Order: 3}).PeakBoostDb
	}
	mk := func(fbw float64) CompOpts {
		return CompOpts{Fbw: fbw, Eps: eps, Gmax: gmax, Order: 3,
			BudgetDb: budgetDb, BitsGained: bg, Auto: true}
	}
	if peak(floor) >= budgetDb {
		return mk(floor)
	}
	if peak(ceil) <= budgetDb {
		return mk(ceil)
	}
	lo, hi := floor, ceil
	for i := 0; i < 26; i++ {
		mid := (lo + hi) / 2
		if peak(mid) <= budgetDb {
			lo = mid
		} else {
			hi = mid
		}
	}
	return mk(lo)
}

// compFFT is the radix-2 iterative FFT from superres_comp.js (in place; n must
// be a power of two; inverse scales by 1/n).
func compFFT(re, im []float64, inverse bool) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		sign := -2.0
		if inverse {
			sign = 2.0
		}
		ang := sign * math.Pi / float64(length)
		wr, wi := math.Cos(ang), math.Sin(ang)
		half := length / 2
		for i := 0; i < n; i += length {
			cr, ci := 1.0, 0.0
			for k := 0; k < half; k++ {
				a, b := i+k, i+k+half
				t1 := re[b] * cr
				t2 := im[b] * ci
				xr := t1 - t2
				t3 := re[b] * ci
				t4 := im[b] * cr
				xi := t3 + t4
				re[b] = re[a] - xr
				im[b] = im[a] - xi
				re[a] += xr
				im[a] += xi
				u1 := cr * wr
				u2 := ci * wi
				ncr := u1 - u2
				u3 := cr * wi
				u4 := ci * wr
				ci = u3 + u4
				cr = ncr
			}
		}
	}
	if inverse {
		for i := 0; i < n; i++ {
			re[i] /= float64(n)
			im[i] /= float64(n)
		}
	}
}

// compResample circular-linearly resamples src[0..m-1] → length nDst over the
// SAME time span (JS srCompResample), so bin k always maps to k/T.
func compResample(src []float64, m, nDst int) []float64 {
	dst := make([]float64, nDst)
	ratio := float64(m) / float64(nDst)
	for j := 0; j < nDst; j++ {
		t := float64(j) * ratio
		i0 := int(math.Floor(t))
		w := t - float64(i0)
		a := src[i0%m]
		b := src[(i0+1)%m]
		p1 := a * (1 - w)
		p2 := b * w
		dst[j] = p1 + p2
	}
	return dst
}

// Compensate de-embeds a code-space fine-grid stack (JS srCompensate):
// mean is the crunched trace (−1 = unfilled-gap sentinel), dtFine the seconds
// per fine bin (SampleS/K). Gaps are interpolation-filled for the transform
// and RESTORED after (never fabricate a stacked sample); an endpoint detrend
// keeps the circular FFT from ringing a gated step across the record; DC is
// held at unity so the vertical offset is preserved; filled samples floor at
// 0. Returns the input slice untouched when inapplicable (dtFine ≤ 0, fewer
// than 8 bins, or an all-gap grid) — the same gating as the JS.
func Compensate(mean []float32, dtFine float64, o CompOpts) []float32 {
	o = o.withDefaults()
	m := len(mean)
	if !(dtFine > 0) || m < 8 {
		return mean
	}

	// 1. Fill −1 gaps by linear interpolation into a work buffer.
	x := make([]float64, m)
	anyFilled := false
	for i, v := range mean {
		if v >= 0 {
			x[i] = float64(v)
			anyFilled = true
		}
	}
	if !anyFilled {
		return mean
	}
	last := -1
	for i := 0; i < m; i++ {
		if mean[i] >= 0 {
			if last >= 0 && i-last > 1 {
				a, b := x[last], x[i]
				d := b - a
				for j := last + 1; j < i; j++ {
					num := d * float64(j-last)
					x[j] = a + num/float64(i-last)
				}
			} else if last < 0 {
				for j := 0; j < i; j++ { // leading gap: hold first value
					x[j] = x[i]
				}
			}
			last = i
		}
	}
	if last >= 0 && last < m-1 { // trailing gap
		for j := last + 1; j < m; j++ {
			x[j] = x[last]
		}
	}

	// 1b. Endpoint detrend (boundary-match the circular record).
	den := m - 1
	if den < 1 {
		den = 1
	}
	trend0 := x[0]
	trendSlope := (x[m-1] - x[0]) / float64(den)
	for i := 0; i < m; i++ {
		p := trendSlope * float64(i)
		x[i] -= trend0 + p
	}

	// 2. Resample to the NEAREST power of two (exact frequency map bin k ↔ k/T).
	n := 1
	for n < m {
		n <<= 1
	}
	if n-m > m-(n>>1) && (n>>1) >= 8 {
		n >>= 1
	}
	re := compResample(x, m, n)
	im := make([]float64, n)

	// 3. FFT → real, even, zero-phase gain per bin; DC held at unity.
	compFFT(re, im, false)
	T := float64(m) * dtFine
	for k := 0; k <= n>>1; k++ {
		g := 1.0
		if k != 0 {
			g = CompGain(float64(k)/T, o)
		}
		re[k] *= g
		im[k] *= g
		kk := (n - k) % n
		if kk != k {
			re[kk] *= g
			im[kk] *= g
		}
	}
	compFFT(re, im, true)

	// 4. Resample back, floor filled samples at 0, restore the gap pattern.
	back := compResample(re, n, m)
	comp := make([]float32, m)
	for i := 0; i < m; i++ {
		if mean[i] < 0 {
			comp[i] = -1
			continue
		}
		p := trendSlope * float64(i)
		v := back[i] + trend0 + p
		if v < 0 {
			v = 0
		}
		comp[i] = float32(v)
	}
	return comp
}
