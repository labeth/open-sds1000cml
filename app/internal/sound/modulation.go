// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import (
	"math"
	"sort"
)

// Modulation: tremolo (AM) and vibrato (FM) of a tone, wow and flutter, the
// index of audio-rate FM from its sidebands, and an LFO read directly.
//
// The tone is demodulated against its own carrier (the tuner's pitch): mixed
// to zero frequency, low-passed by two boxcars a carrier period long (their
// nulls sit on every multiple of the carrier, so the mixing image and the
// harmonics of a rich waveform drop out) and decimated to about a kilohertz.
// The baseband's magnitude is the amplitude, its phase step the frequency.

// Demod is a tone's demodulated amplitude and frequency.
type Demod struct {
	Fs   float64   // the rate of Amp and Freq
	Fc   float64   // the carrier mixed against
	Amp  []float64 // the carrier's amplitude (volts peak)
	Freq []float64 // instantaneous frequency (Hz)
}

// Demodulate demodulates x (fs) against the carrier fc to about outRate.
// TRLC-LINKS: REQ-SDS-269
func Demodulate(x []float64, fs, fc, outRate float64) Demod {
	d := Demod{Fc: fc}
	n := len(x)
	if n < 16 || !(fs > 0) || !(fc > 0) || fc >= fs/2 {
		return d
	}
	// A carrier period past the stretch demodulates nothing; bounded so, the
	// boxcars' lengths stay ints on the scope (a float past MaxInt32
	// converts to it there, and their sum wrapped negative).
	per := math.Min(fs/fc, float64(n)+1)
	p1 := max(1, int(math.Floor(per)))
	p2 := max(1, int(math.Ceil(per)))
	dec := max(1, int(math.Round(fs/math.Max(outRate, 1))))
	d.Fs = fs / float64(dec)
	m := mean(x)
	w := 2 * math.Pi * fc / fs
	c, s := math.Cos(w), math.Sin(w)
	ck, sk := 1.0, 0.0
	// Prefix sums of the mixed signal, then of the first boxcar's output.
	cr, ci := getScratch(n+1), getScratch(n+1)
	b1r, b1i := getScratch(n+1), getScratch(n+1)
	defer putScratch(cr)
	defer putScratch(ci)
	defer putScratch(b1r)
	defer putScratch(b1i)
	cr[0], ci[0] = 0, 0
	clear(b1r[:min(p1, n+1)])
	clear(b1i[:min(p1, n+1)])
	for i, v := range x {
		v -= m
		cr[i+1] = cr[i] + v*ck
		ci[i+1] = ci[i] - v*sk
		ck, sk = ck*c-sk*s, sk*c+ck*s
		if i&1023 == 1023 {
			a := w * float64(i+1)
			ck, sk = math.Cos(a), math.Sin(a)
		}
	}
	// First boxcar (p1) at i: (cum[i+1]-cum[i+1-p1])/p1, defined from i = p1-1.
	for i := p1 - 1; i < n; i++ {
		b1r[i+1] = b1r[i] + (cr[i+1]-cr[i+1-p1])/float64(p1)
		b1i[i+1] = b1i[i] + (ci[i+1]-ci[i+1-p1])/float64(p1)
	}
	first := p1 - 1 + p2 - 1
	prevPh, have := 0.0, false
	for i := first; i < n; i += dec {
		zr := (b1r[i+1] - b1r[i+1-p2]) / float64(p2)
		zi := (b1i[i+1] - b1i[i+1-p2]) / float64(p2)
		d.Amp = append(d.Amp, 2*math.Hypot(zr, zi))
		ph := math.Atan2(zi, zr)
		if have {
			d.Freq = append(d.Freq, fc+wrapPi(ph-prevPh)*d.Fs/(2*math.Pi))
		} else {
			d.Freq = append(d.Freq, fc)
		}
		prevPh, have = ph, true
	}
	if len(d.Freq) > 1 {
		d.Freq[0] = d.Freq[1]
	}
	return d
}

// smooth is a centred moving average of 2h+1 samples (shorter at the ends).
// TRLC-LINKS: REQ-SDS-269
func smooth(x []float64, h int) []float64 {
	out := make([]float64, len(x))
	cum := make([]float64, len(x)+1)
	for i, v := range x {
		cum[i+1] = cum[i] + v
	}
	for i := range x {
		a, b := max(0, i-h), min(len(x), i+h+1)
		out[i] = (cum[b] - cum[a]) / float64(b-a)
	}
	return out
}

// ModRate is the rate of a slow periodic signal by its mean crossings
// (hysteresis a tenth of its 5-95 % swing): rate in Hz and whole cycles
// seen (0 when fewer than one).
// TRLC-LINKS: REQ-SDS-269
func ModRate(x []float64, fs float64) (float64, int) {
	if len(x) < 4 || !(fs > 0) {
		return 0, 0
	}
	m := mean(x)
	// Both percentiles from one sorted copy (as percentile reads them).
	s := append([]float64(nil), x...)
	sort.Float64s(s)
	q := func(p float64) float64 {
		i := p * float64(len(s)-1)
		lo := int(math.Floor(i))
		hi := min(lo+1, len(s)-1)
		f := i - float64(lo)
		return s[lo]*(1-f) + s[hi]*f
	}
	h := 0.1 * (q(0.95) - q(0.05)) / 2
	if !(h > 0) {
		return 0, 0
	}
	armed := false
	first, last := -1.0, -1.0
	n := 0
	for i := 1; i < len(x); i++ {
		if x[i] < m-h {
			armed = true
		}
		if armed && x[i-1] < m && x[i] >= m {
			t := float64(i-1) + (m-x[i-1])/(x[i]-x[i-1])
			if first < 0 {
				first = t
			}
			last = t
			n++
			armed = false
		}
	}
	if n < 2 || !(last > first) {
		return 0, 0
	}
	return float64(n-1) * fs / (last - first), n - 1
}

// ShapeOf names a periodic signal's shape from its second and third
// harmonics against the fundamental at rate: "sine", "triangle", "square"
// or "saw".
// TRLC-LINKS: REQ-SDS-269
func ShapeOf(x []float64, fs, rate float64) string {
	if !(rate > 0) || len(x) < 8 {
		return ""
	}
	h := [4]float64{}
	for k := 1; k <= 3; k++ {
		if float64(k)*rate >= fs/2 {
			break
		}
		re, im := toneAt(x, float64(k)*rate, fs)
		h[k] = math.Hypot(re, im)
	}
	if !(h[1] > 0) {
		return ""
	}
	r2, r3 := h[2]/h[1], h[3]/h[1]
	switch {
	case r2 > 0.3:
		return "saw"
	case r3 > 0.22:
		return "square"
	case r3 > 0.06:
		return "triangle"
	}
	return "sine"
}

// Modulation is a tone's modulation reading.
type Modulation struct {
	OK  bool
	Why string
	Fc  float64 // the carrier (mean instantaneous frequency)

	HasAM            bool
	AMPct, AMDB      float64 // depth: (max-min)/(max+min) and max/min
	AMRate           float64
	AMShape          string
	AMMin, AMMax     float64 // the amplitude's 1 and 99 % levels (volts peak)
	HasFM            bool
	FMHz, FMCents    float64 // deviation: half the 1-99 % span
	FMRate           float64
	FMShape          string
	WowPct           float64 // RMS frequency deviation 0.5-6 Hz, % of the carrier
	FlutterPct       float64 // RMS 6-100 Hz
	WFPct            float64 // weighted (peak at 4 Hz), two-sigma, %
	Seconds          float64
	AMCycles         int
	FMCycles         int
	FreqMin, FreqMax float64
}

// AM and FM below these read as none.
const (
	amMinPct    = 2.0
	fmMinCents  = 2.0
	modTrimFrac = 0.03 // the demodulator's settling at each end
)

// AnalyzeModulation reads tremolo and vibrato from a demodulated tone.
// TRLC-LINKS: REQ-SDS-269
func AnalyzeModulation(d Demod) Modulation {
	m := Modulation{}
	n := len(d.Amp)
	if n < 32 || !(d.Fs > 0) {
		m.Why = "too short"
		return m
	}
	trim := int(modTrimFrac * float64(n))
	a := smooth(d.Amp[trim:n-trim], 2)
	f := smooth(d.Freq[trim:n-trim], 2)
	m.Seconds = float64(len(a)) / d.Fs
	lo, hi := percentile(a, 0.01), percentile(a, 0.99)
	if !(hi > 0) {
		m.Why = "no tone"
		return m
	}
	m.AMMin, m.AMMax = lo, hi
	m.AMPct = 100 * (hi - lo) / (hi + lo)
	m.AMDB = 20 * math.Log10(hi/math.Max(lo, 1e-12))
	m.Fc = mean(f)
	flo, fhi := percentile(f, 0.01), percentile(f, 0.99)
	m.FreqMin, m.FreqMax = flo, fhi
	m.FMHz = (fhi - flo) / 2
	if flo > 0 {
		m.FMCents = 600 * math.Log2(fhi/flo)
	}
	m.HasAM = m.AMPct >= amMinPct
	m.HasFM = m.FMCents >= fmMinCents
	if m.HasAM {
		m.AMRate, m.AMCycles = ModRate(a, d.Fs)
		m.AMShape = ShapeOf(a, d.Fs, m.AMRate)
	}
	if m.HasFM {
		m.FMRate, m.FMCycles = ModRate(f, d.Fs)
		m.FMShape = ShapeOf(f, d.Fs, m.FMRate)
	}
	if m.Fc > 0 {
		rel := make([]float64, len(f))
		for i, v := range f {
			rel[i] = (v - m.Fc) / m.Fc
		}
		m.WowPct = 100 * rms(bandPass(rel, d.Fs, 0.5, 6))
		m.FlutterPct = 100 * rms(bandPass(rel, d.Fs, 6, math.Min(100, 0.45*d.Fs)))
		m.WFPct = 200 * rms(weightWF(rel, d.Fs))
	}
	m.OK = true
	return m
}

// rms is the root mean square of x about zero.
// TRLC-LINKS: REQ-SDS-269
func rms(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range x {
		s += v * v
	}
	return math.Sqrt(s / float64(len(x)))
}

// biquad is a direct-form-I second-order section (RBJ cookbook).
type biquad struct{ b0, b1, b2, a1, a2 float64 }

// rbj builds a high-pass ("hp"), low-pass ("lp") or band-pass ("bp",
// 0 dB peak) section at f0 Hz, quality q, rate fs.
// TRLC-LINKS: REQ-SDS-269
func rbj(kind string, f0, q, fs float64) biquad {
	w := 2 * math.Pi * f0 / fs
	cw, sw := math.Cos(w), math.Sin(w)
	al := sw / (2 * q)
	a0 := 1 + al
	var b biquad
	switch kind {
	case "hp":
		b = biquad{(1 + cw) / 2, -(1 + cw), (1 + cw) / 2, -2 * cw, 1 - al}
	case "lp":
		b = biquad{(1 - cw) / 2, 1 - cw, (1 - cw) / 2, -2 * cw, 1 - al}
	default:
		b = biquad{al, 0, -al, -2 * cw, 1 - al}
	}
	return biquad{b.b0 / a0, b.b1 / a0, b.b2 / a0, b.a1 / a0, b.a2 / a0}
}

// run filters x through the section.
// TRLC-LINKS: REQ-SDS-269
func (b biquad) run(x []float64) []float64 {
	out := make([]float64, len(x))
	var x1, x2, y1, y2 float64
	for i, v := range x {
		y := b.b0*v + b.b1*x1 + b.b2*x2 - b.a1*y1 - b.a2*y2
		x2, x1, y2, y1 = x1, v, y1, y
		out[i] = y
	}
	return out
}

// bandPass keeps lo..hi Hz of x: a Butterworth high-pass then low-pass,
// the first second's settling dropped when the record allows.
// TRLC-LINKS: REQ-SDS-269
func bandPass(x []float64, fs, lo, hi float64) []float64 {
	if !(hi > lo) || hi >= fs/2 {
		return nil
	}
	y := rbj("lp", hi, math.Sqrt2/2, fs).run(rbj("hp", lo, math.Sqrt2/2, fs).run(x))
	return y[min(len(y)/2, int(fs/lo)):]
}

// weightWF is the wow-and-flutter weighting: a broad band-pass peaking at
// 4 Hz (0 dB there), as the IEC 386 curve is, its settling dropped.
// TRLC-LINKS: REQ-SDS-269
func weightWF(x []float64, fs float64) []float64 {
	if fs < 20 {
		return nil
	}
	y := rbj("bp", 4, 0.6, fs).run(x)
	return y[min(len(y)/2, int(fs/2)):]
}

// Sidebands is audio-rate FM read from its spectrum: carrier, modulator,
// the C:M ratio and the index from the lines' Bessel pattern.
type Sidebands struct {
	OK    bool
	Why   string
	Fc    float64
	Fm    float64
	Ratio string  // "2:1", or the decimal
	Beta  float64 // modulation index
	Fit   float64 // 0..1, how well the lines follow J_k(Beta)
	Lines int     // lines used
}

// specPeak is a spectral line: refined bin and linear amplitude.
type specPeak struct {
	bin, amp float64
}

// findLines are the local maxima of db (two bins either side) within 50 dB
// of the top, parabola-refined, in bin order.
// TRLC-LINKS: REQ-SDS-269, REQ-SDS-270
func findLines(db []float64, below float64) []specPeak {
	top := math.Inf(-1)
	for k := 1; k < len(db); k++ {
		top = math.Max(top, db[k])
	}
	var out []specPeak
	for k := 2; k < len(db)-2; k++ {
		v := db[k]
		if v < top-below || v < db[k-1] || v < db[k+1] || v < db[k-2] || v < db[k+2] || v == db[k-1] {
			continue
		}
		off := 0.0
		if den := db[k-1] - 2*v + db[k+1]; den < 0 {
			off = 0.5 * (db[k-1] - db[k+1]) / den
		}
		out = append(out, specPeak{bin: float64(k) + off, amp: math.Pow(10, v/20) / hannGain(off)})
	}
	return out
}

// sbMaxLines bounds the lines the sideband search weighs: it compares every
// line with every other, and a noisy or wideband spectrum has thousands
// within 40 dB of its top (seconds a call on the instrument). An FM tone's
// lines worth reading are far fewer.
const sbMaxLines = 64

// strongestLines keeps the n strongest lines, in bin order.
// TRLC-LINKS: REQ-SDS-269
func strongestLines(l []specPeak, n int) []specPeak {
	if len(l) <= n {
		return l
	}
	c := append([]specPeak(nil), l...)
	sort.SliceStable(c, func(i, j int) bool { return c[i].amp > c[j].amp })
	c = c[:n]
	sort.Slice(c, func(i, j int) bool { return c[i].bin < c[j].bin })
	return c
}

// AnalyzeSidebands reads audio-rate FM from a Hann spectrum db (dB, any
// reference) with bins binHz apart. The carrier is the line the others sit
// most symmetrically about, the modulator their common spacing; the index
// is the one whose Bessel amplitudes |J_k| best match the lines.
// TRLC-LINKS: REQ-SDS-269
func AnalyzeSidebands(db []float64, binHz float64) Sidebands {
	sb := Sidebands{}
	lines := strongestLines(findLines(db, 40), sbMaxLines)
	if len(lines) < 3 {
		sb.Why = "fewer than three lines"
		return sb
	}
	var gaps []float64
	for i := 1; i < len(lines); i++ {
		gaps = append(gaps, lines[i].bin-lines[i-1].bin)
	}
	sort.Float64s(gaps)
	fmBins := gaps[0]
	// The common spacing: the smallest gap that most gaps are multiples of.
	best := -1
	for _, g := range gaps {
		if g < 1.5 {
			continue
		}
		cnt := 0
		for _, h := range gaps {
			k := math.Round(h / g)
			if k >= 1 && math.Abs(h-k*g) <= math.Max(1.5, 0.03*h) {
				cnt++
			}
		}
		if cnt > best {
			best, fmBins = cnt, g
		}
	}
	if best < 2 {
		sb.Why = "no common line spacing"
		return sb
	}
	// Refine the spacing over the lines that fit it.
	ampAt := func(bin float64) float64 {
		a := 0.0
		for _, l := range lines {
			if math.Abs(l.bin-bin) <= math.Max(1.5, 0.01*bin) && l.amp > a {
				a = l.amp
			}
		}
		return a
	}
	carrier, bestScore := -1, math.Inf(1)
	for i, l := range lines {
		num, den, pairs := 0.0, 0.0, 0
		for k := 1; k <= 10; k++ {
			lo, hi := l.bin-float64(k)*fmBins, l.bin+float64(k)*fmBins
			if lo < fmBins/2 || hi >= float64(len(db)-2) {
				break
			}
			a, b := ampAt(lo), ampAt(hi)
			if a == 0 && b == 0 {
				continue
			}
			num += math.Abs(a - b)
			den += a + b
			pairs++
		}
		if pairs == 0 || den == 0 {
			continue
		}
		score := num / den
		if score < bestScore-1e-9 || (math.Abs(score-bestScore) <= 1e-9 && l.amp > lines[carrier].amp) {
			carrier, bestScore = i, score
		}
	}
	if carrier < 0 {
		sb.Why = "no line with sidebands either side"
		return sb
	}
	fc := lines[carrier].bin
	var amps []float64
	var ks []int
	norm := 0.0
	for k := -10; k <= 10; k++ {
		b := fc + float64(k)*fmBins
		if b < fmBins/2 || b >= float64(len(db)-2) {
			continue
		}
		a := ampAt(b)
		amps = append(amps, a)
		ks = append(ks, k)
		norm += a * a
	}
	norm = math.Sqrt(norm)
	if !(norm > 0) {
		sb.Why = "no lines"
		return sb
	}
	err := func(beta float64) float64 {
		j := BesselJ(10, beta)
		jn := 0.0
		for _, k := range ks {
			v := j[absInt(k)]
			jn += v * v
		}
		jn = math.Sqrt(jn)
		e := 0.0
		for i, k := range ks {
			d := amps[i]/norm - math.Abs(j[absInt(k)])/math.Max(jn, 1e-12)
			e += d * d
		}
		return e
	}
	bb, be := 0.0, math.Inf(1)
	for b := 0.0; b <= 12; b += 0.05 {
		if e := err(b); e < be {
			bb, be = b, e
		}
	}
	lo, hi := math.Max(0, bb-0.05), bb+0.05
	for it := 0; it < 40; it++ { // golden-section refinement
		m1, m2 := hi-(hi-lo)*0.618, lo+(hi-lo)*0.618
		if err(m1) < err(m2) {
			hi = m2
		} else {
			lo = m1
		}
	}
	sb.Beta = (lo + hi) / 2
	sb.Fit = math.Max(0, 1-math.Sqrt(err(sb.Beta)))
	sb.Fc, sb.Fm = fc*binHz, fmBins*binHz
	sb.Ratio = RatioText(sb.Fc / sb.Fm)
	sb.Lines = 0
	for _, a := range amps {
		if a > 0 {
			sb.Lines++
		}
	}
	sb.OK = true
	return sb
}

// absInt is |k|.
// TRLC-LINKS: REQ-SDS-269
func absInt(k int) int {
	if k < 0 {
		return -k
	}
	return k
}

// BesselJ returns J_0..J_nmax at x (x >= 0) by Miller's backward recurrence,
// normalised by J_0 + 2(J_2 + J_4 + ...) = 1.
// TRLC-LINKS: REQ-SDS-269
func BesselJ(nmax int, x float64) []float64 {
	out := make([]float64, nmax+1)
	if !(x > 0) {
		out[0] = 1
		return out
	}
	top := max(nmax, int(x))
	m := 2 * ((top + 16 + int(math.Sqrt(40*float64(top)))) / 2)
	jp1, j := 0.0, 1e-30
	sum := 0.0
	for k := m; k >= 1; k-- {
		jm1 := 2*float64(k)/x*j - jp1
		jp1, j = j, jm1
		// j is now J_{k-1}, jp1 J_k.
		if k-1 <= nmax {
			out[k-1] = j
		}
		if (k-1)%2 == 0 && k-1 > 0 {
			sum += 2 * j
		}
		if math.Abs(j) > 1e10 { // rescale to keep the recurrence in range
			j *= 1e-10
			jp1 *= 1e-10
			sum *= 1e-10
			for i := k - 1; i <= nmax; i++ {
				out[i] *= 1e-10
			}
		}
	}
	sum += j
	for i := range out {
		out[i] /= sum
	}
	return out
}

// RatioText writes r as a small fraction p:q when one within 0.1 % has q <= 12,
// else to three digits.
// TRLC-LINKS: REQ-SDS-269
func RatioText(r float64) string {
	if !(r > 0) || !finite(r) {
		return ""
	}
	for q := 1; q <= 12; q++ {
		p := math.Round(r * float64(q))
		if p >= 1 && math.Abs(p/float64(q)-r) <= 0.001*r {
			return itoa(int(p)) + ":" + itoa(q)
		}
	}
	return ftoa3(r)
}

// LFO is a slow modulation source read directly (a CV).
type LFO struct {
	OK      bool
	Why     string
	RateHz  float64
	Cycles  int
	Min     float64
	Max     float64
	Vpp     float64
	Mean    float64
	RisePct float64 // share of a cycle spent rising: 50 sine/triangle, ~100 ramp up, ~0 ramp down
	Shape   string
}

// AnalyzeLFO reads an LFO (volts, fs): rate, levels, rise share and shape.
// TRLC-LINKS: REQ-SDS-269
func AnalyzeLFO(x []float64, fs float64) LFO {
	l := LFO{}
	if len(x) < 16 || !(fs > 0) {
		l.Why = "too short"
		return l
	}
	if fs > 2000 { // a kilohertz or two is plenty for an LFO
		x, fs = BlockMeans(x, fs, 1/1000.0)
	}
	l.Min, l.Max = percentile(x, 0.005), percentile(x, 0.995)
	l.Vpp, l.Mean = l.Max-l.Min, mean(x)
	if !(l.Vpp > 0) {
		l.Why = "flat"
		return l
	}
	l.RateHz, l.Cycles = ModRate(x, fs)
	if l.Cycles < 1 {
		l.Why = "less than a cycle"
		return l
	}
	l.Shape = ShapeOf(x, fs, l.RateHz)
	// Rise share: per cycle, from its minimum to its maximum.
	per := fs / l.RateHz
	var shares []float64
	for s := 0.0; s+per <= float64(len(x)); s += per {
		a, b := int(s), min(len(x), int(s+per))
		lo, hi := a, a
		for i := a; i < b; i++ {
			if x[i] < x[lo] {
				lo = i
			}
			if x[i] > x[hi] {
				hi = i
			}
		}
		d := float64(hi - lo)
		if d < 0 {
			d += per
		}
		shares = append(shares, 100*d/per)
	}
	if len(shares) > 0 {
		l.RisePct = median(shares)
	}
	l.OK = true
	return l
}
