// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import "math"

// A/B: a circuit's input on one channel and its output on the other. Gain
// and phase at the fundamental are the Bode measurement's (a single-bin DFT
// of each channel at one shared frequency: the ratio cancels a small
// frequency error), the delay is the cross-correlation's peak, and the added
// distortion is the output's THD less the input's.

// AB is input against output.
type AB struct {
	OK        bool
	Why       string
	F0        float64
	GainDB    float64 // at the fundamental
	PhaseDeg  float64 // output against input, -180..180
	RMSGainDB float64 // broadband (AC RMS)
	DelayS    float64
	DelayOK   bool
	THDA      float64 // percent (NaN: none found)
	THDB      float64
	AddedPct  float64 // THDB - THDA
	AddedDB   float64 // the output's THD against the input's, dB
	Match     float64 // the output over the input at the fundamental (the gain-matched difference's scale)
}

// maxDelayS bounds the delay search.
const maxDelayS = 0.02

// CompareAB compares output b with input a (volts, fs) at f0 (the tuner's
// pitch; 0 finds it in a).
// TRLC-LINKS: REQ-SDS-271
func CompareAB(a, b []float64, fs, f0 float64) AB {
	r := AB{THDA: math.NaN(), THDB: math.NaN(), AddedPct: math.NaN(), AddedDB: math.NaN()}
	n := min(len(a), len(b))
	if n < 256 || !(fs > 0) {
		r.Why = "too short"
		return r
	}
	a, b = a[len(a)-n:], b[len(b)-n:]
	if !(f0 > 0) {
		p := DetectPitch(a[n-min(n, 8192):], fs, DefaultPitchOpts)
		if !p.OK {
			r.Why = "no pitch on the input"
			return r
		}
		f0 = p.Hz
	}
	r.F0 = f0
	ar, ai := toneAt(a, f0, fs)
	br, bi := toneAt(b, f0, fs)
	ma, mb := math.Hypot(ar, ai), math.Hypot(br, bi)
	if !(ma > 0) {
		r.Why = "no input at the fundamental"
		return r
	}
	r.Match = mb / ma
	r.GainDB = 20 * math.Log10(math.Max(mb, 1e-15)/ma)
	// arg(B * conj(A))
	r.PhaseDeg = math.Atan2(bi*ar-br*ai, br*ar+bi*ai) * 180 / math.Pi
	la, lb := Measure(a), Measure(b)
	if la.RMS > 0 && lb.RMS > 0 {
		r.RMSGainDB = 20 * math.Log10(lb.RMS/la.RMS)
	}
	r.DelayS, r.DelayOK = Delay(a, b, fs, maxDelayS)
	nn := min(n, 16384)
	sa, sb := AudioSpectrum(a, fs, nn), AudioSpectrum(b, fs, nn)
	ha, hb := Harmonics(sa, f0), Harmonics(sb, f0)
	r.THDA, r.THDB = ha.THDPct, hb.THDPct
	ta, tb := ha.THDPct, hb.THDPct
	if math.IsNaN(ta) && hb.OK {
		ta = 0
	}
	if math.IsNaN(tb) && hb.OK {
		tb = 0
	}
	r.AddedPct = tb - ta
	if ta > 0 && tb > 0 {
		r.AddedDB = 20 * math.Log10(tb/ta)
	}
	r.OK = true
	return r
}

// Delay is how much later b carries a's signal: the cross-correlation's
// peak within maxS, parabola-refined; ok false without a clear peak.
// TRLC-LINKS: REQ-SDS-271
func Delay(a, b []float64, fs, maxS float64) (float64, bool) {
	n := min(len(a), len(b), 65536)
	if n < 64 {
		return 0, false
	}
	a, b = a[len(a)-n:], b[len(b)-n:]
	size := pow2(2 * n)
	ar, ai := make([]float64, size), make([]float64, size)
	br, bi := make([]float64, size), make([]float64, size)
	ma, mb := mean(a), mean(b)
	for i := 0; i < n; i++ {
		w := hann(i, n)
		ar[i], br[i] = (a[i]-ma)*w, (b[i]-mb)*w
	}
	fft(ar, ai, false)
	fft(br, bi, false)
	for k := range ar {
		// conj(A) * B: r[l] = sum a[i] b[i+l]
		re := ar[k]*br[k] + ai[k]*bi[k]
		im := ar[k]*bi[k] - ai[k]*br[k]
		ar[k], ai[k] = re, im
	}
	fft(ar, ai, true)
	lim := min(n-1, int(maxS*fs))
	at := func(l int) float64 { // lag l (negative wraps)
		if l < 0 {
			l += size
		}
		return ar[l]
	}
	best, bv := 0, math.Inf(-1)
	for l := -lim; l <= lim; l++ {
		if v := at(l); v > bv {
			best, bv = l, v
		}
	}
	if !(bv > 0) {
		return 0, false
	}
	off := 0.0
	if best > -lim && best < lim {
		p, c, q := at(best-1), bv, at(best+1)
		if den := p - 2*c + q; den < 0 {
			off = 0.5 * (p - q) / den
		}
	}
	return (float64(best) + off) / fs, true
}

// Transfer bins (a, b) pairs into a w x h histogram over the given volt
// ranges (row 0 the top): the XY transfer curve's density.
// TRLC-LINKS: REQ-SDS-271
func Transfer(a, b []float64, w, h int, aLo, aHi, bLo, bHi float64) []uint16 {
	out := make([]uint16, w*h)
	if w <= 0 || h <= 0 || !(aHi > aLo) || !(bHi > bLo) {
		return out
	}
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		x := int((a[i] - aLo) / (aHi - aLo) * float64(w))
		y := h - 1 - int((b[i]-bLo)/(bHi-bLo)*float64(h))
		if x < 0 || x >= w || y < 0 || y >= h {
			continue
		}
		if c := &out[y*w+x]; *c < 65535 {
			*c++
		}
	}
	return out
}
