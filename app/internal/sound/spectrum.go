// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import (
	"math"
	"sort"
	"strconv"
)

// The audio spectrum: a Hann-windowed transform in absolute dBV (a sine of
// V volts RMS reads 20 log10 V in its bin), the harmonics H1..H10 against
// the fundamental with THD and the even/odd balance, the mains hum, and the
// scope's own floor at the range the channel is on.

// Spectrum is an audio spectrum.
type Spectrum struct {
	N     int       // transform length
	BinHz float64   // bin spacing
	DBV   []float64 // N/2 bins
}

// AudioSpectrum transforms the last power-of-two stretch of x (volts, fs),
// at most n samples, mean removed and Hann windowed.
// TRLC-LINKS: REQ-SDS-270
func AudioSpectrum(x []float64, fs float64, n int) Spectrum {
	n = floorPow2(min(n, len(x)))
	if n < 64 || !(fs > 0) {
		return Spectrum{}
	}
	x = x[len(x)-n:]
	re, im := getScratch(n), getScratch(n)
	defer putScratch(re)
	defer putScratch(im)
	m := mean(x)
	win := hannTable(n)
	for i, v := range x {
		if win != nil {
			re[i] = (v - m) * win[i]
		} else {
			re[i] = (v - m) * hann(i, n)
		}
		im[i] = 0
	}
	fft(re, im, false)
	sp := Spectrum{N: n, BinHz: fs / float64(n), DBV: make([]float64, n/2)}
	// A sine of amplitude A reads A*N/4 through the Hann window; its RMS is
	// A/sqrt2.
	k := 4 / float64(n) / math.Sqrt2
	for i := range sp.DBV {
		sp.DBV[i] = 20 * math.Log10(math.Hypot(re[i], im[i])*k+1e-12)
	}
	return sp
}

// Harmonic is one harmonic's reading.
type Harmonic struct {
	N     int
	Hz    float64
	DBV   float64
	RelDB float64 // against H1
	Found bool    // stood above the local floor
}

// HarmonicReport is the harmonics of a fundamental and what they add up to.
type HarmonicReport struct {
	OK        bool
	F0        float64
	H         [10]Harmonic
	THDPct    float64 // NaN with no harmonic found
	THDDB     float64
	EvenDB    float64 // power of H2, H4.. against H1 (dB; -Inf none)
	OddDB     float64 // H3, H5..
	EvenOdd   float64 // EvenDB - OddDB
	Hum       Hum
	FloorDBV  float64 // the median bin level (the spectrum's own floor)
	Character string  // "even (asymmetric)", "odd (symmetric)", "mixed", ""
}

// Hum is the mains hum found: the 50 or 60 Hz family's strongest.
type Hum struct {
	Found  bool
	MainHz float64 // 50 or 60
	DBV    float64 // its line's level
	SumDBV float64 // the family's first three lines together
}

// lineAt finds the strongest bin within tol bins of bin f/binHz and returns
// its refined frequency and Hann-corrected level.
// TRLC-LINKS: REQ-SDS-270
func lineAt(sp Spectrum, f, tol float64) (hz, dbv float64, k int) {
	c := f / sp.BinHz
	lo := max(1, int(math.Floor(c-tol)))
	hi := min(len(sp.DBV)-2, int(math.Ceil(c+tol)))
	if hi < lo {
		return 0, math.Inf(-1), -1
	}
	k = lo
	for i := lo; i <= hi; i++ {
		if sp.DBV[i] > sp.DBV[k] {
			k = i
		}
	}
	off := 0.0
	if k > 0 && k < len(sp.DBV)-1 {
		a, b, cc := sp.DBV[k-1], sp.DBV[k], sp.DBV[k+1]
		if den := a - 2*b + cc; den < 0 {
			off = math.Max(-0.5, math.Min(0.5, 0.5*(a-cc)/den))
		}
	}
	return (float64(k) + off) * sp.BinHz, sp.DBV[k] - 20*math.Log10(hannGain(off)), k
}

// localFloor is the median level of the bins from 0.8 to 1.25 times bin k,
// those within 3 bins of k left out.
// TRLC-LINKS: REQ-SDS-270
func localFloor(sp Spectrum, k int) float64 {
	lo, hi := max(1, int(float64(k)*0.8)), min(len(sp.DBV)-1, int(float64(k)*1.25)+1)
	v := make([]float64, 0, max(0, hi-lo+1))
	for i := lo; i <= hi; i++ {
		if absInt(i-k) > 3 {
			v = append(v, sp.DBV[i])
		}
	}
	if len(v) == 0 {
		return math.Inf(-1)
	}
	// v is this call's own: sorted in place, the median as percentile reads it.
	sort.Float64s(v)
	mid := 0.5 * float64(len(v)-1)
	a := int(mid)
	b := min(a+1, len(v)-1)
	f := mid - float64(a)
	return v[a]*(1-f) + v[b]*f
}

// Harmonics reads H1..H10 of f0 (the tuner's pitch; 0 takes the strongest
// line above 15 Hz). A harmonic counts when it stands 6 dB above its
// neighbourhood's median and within 90 dB of H1 (deeper is past any 8-bit
// converter's linearity: window leakage, not the signal).
// TRLC-LINKS: REQ-SDS-270
func Harmonics(sp Spectrum, f0 float64) HarmonicReport {
	r := HarmonicReport{THDPct: math.NaN(), THDDB: math.NaN(), EvenDB: math.Inf(-1), OddDB: math.Inf(-1), EvenOdd: math.NaN()}
	if len(sp.DBV) < 16 {
		return r
	}
	r.FloorDBV = median(sp.DBV[1:])
	if !(f0 > 0) {
		k0 := max(1, int(math.Ceil(15/sp.BinHz)))
		best := k0
		for k := k0; k < len(sp.DBV)-1; k++ {
			if sp.DBV[k] > sp.DBV[best] {
				best = k
			}
		}
		f0, _, _ = lineAt(sp, float64(best)*sp.BinHz, 1)
	}
	nyq := sp.BinHz * float64(len(sp.DBV))
	if !(f0 > 0) || f0 >= nyq {
		return r
	}
	r.F0 = f0
	p1 := 0.0
	var even, odd, all float64
	for n := 1; n <= 10; n++ {
		h := Harmonic{N: n, Hz: float64(n) * f0, DBV: math.Inf(-1), RelDB: math.Inf(-1)}
		if h.Hz < nyq*0.98 {
			tol := math.Max(2, 0.005*h.Hz/sp.BinHz)
			hz, db, k := lineAt(sp, h.Hz, tol)
			if k >= 0 {
				h.Hz, h.DBV = hz, db
				h.Found = db > localFloor(sp, k)+6
			}
		}
		if n > 1 && h.DBV < r.H[0].DBV-90 {
			h.Found = false
		}
		r.H[n-1] = h
		if n == 1 {
			if !h.Found {
				return r
			}
			p1 = math.Pow(10, h.DBV/10)
			continue
		}
		r.H[n-1].RelDB = h.DBV - r.H[0].DBV
		if !h.Found {
			continue
		}
		p := math.Pow(10, h.DBV/10) / p1
		all += p
		if n%2 == 0 {
			even += p
		} else {
			odd += p
		}
	}
	r.H[0].RelDB = 0
	r.F0 = r.H[0].Hz
	r.OK = true
	if all > 0 {
		r.THDPct = 100 * math.Sqrt(all)
		r.THDDB = 10 * math.Log10(all)
	}
	if even > 0 {
		r.EvenDB = 10 * math.Log10(even)
	}
	if odd > 0 {
		r.OddDB = 10 * math.Log10(odd)
	}
	if even > 0 || odd > 0 {
		r.EvenOdd = r.EvenDB - r.OddDB
		switch {
		case even > 0 && (odd == 0 || r.EvenOdd > 6):
			r.Character = "even (asymmetric)"
		case odd > 0 && (even == 0 || r.EvenOdd < -6):
			r.Character = "odd (symmetric)"
		default:
			r.Character = "mixed"
		}
	}
	r.Hum = FindHum(sp)
	return r
}

// FindHum reads the 50 and 60 Hz families (the line and its second and third
// harmonics, each standing 6 dB above its neighbourhood) and keeps the
// stronger.
// TRLC-LINKS: REQ-SDS-270
func FindHum(sp Spectrum) Hum {
	best := Hum{SumDBV: math.Inf(-1), DBV: math.Inf(-1)}
	if len(sp.DBV) < 16 || sp.BinHz > 10 {
		return best
	}
	for _, mains := range []float64{50, 60} {
		h := Hum{MainHz: mains, DBV: math.Inf(-1), SumDBV: math.Inf(-1)}
		sum := 0.0
		for n := 1; n <= 3; n++ {
			_, db, k := lineAt(sp, float64(n)*mains, math.Max(1, 1.5/sp.BinHz))
			if k < 0 || db <= localFloor(sp, k)+6 {
				continue
			}
			h.Found = true
			if n == 1 {
				h.DBV = db
			}
			sum += math.Pow(10, db/10)
		}
		if sum > 0 {
			h.SumDBV = 10 * math.Log10(sum)
		}
		if h.Found && h.SumDBV > best.SumDBV {
			best = h
		}
	}
	return best
}

// FloorCodesPerRtHz is the stream's own noise density in converter codes
// per root hertz on a quiet input, through the decimator.
const FloorCodesPerRtHz = 6e-5

// FloorDBV is the scope's own floor in a bin of binHz at vpc volts a code:
// noise of density FloorCodesPerRtHz through a Hann bin (1.5 bins wide),
// read on the spectrum's sine-RMS scale.
// TRLC-LINKS: REQ-SDS-270
func FloorDBV(binHz, vpc float64) float64 {
	if !(binHz > 0) || !(vpc > 0) {
		return math.Inf(-1)
	}
	return 20 * math.Log10(FloorCodesPerRtHz*vpc*math.Sqrt(1.5*binHz))
}

// LogX places f on a log axis from f0 to f1 as 0..1.
// TRLC-LINKS: REQ-SDS-270
func LogX(f, f0, f1 float64) float64 {
	if !(f > 0) {
		return math.Inf(-1)
	}
	return math.Log(f/f0) / math.Log(f1/f0)
}

// NoteGrid is the C of every octave between f0 and f1 Hz (A4 = a4), for a
// log axis's note grid: frequencies and names ("C4").
// TRLC-LINKS: REQ-SDS-270
func NoteGrid(f0, f1, a4 float64) ([]float64, []string) {
	if !(a4 > 0) {
		a4 = 440
	}
	var fs []float64
	var ns []string
	for oct := -1; oct <= 10; oct++ {
		midi := 12 * (oct + 1)
		f := a4 * math.Pow(2, float64(midi-69)/12)
		if f >= f0 && f <= f1 {
			fs = append(fs, f)
			ns = append(ns, "C"+strconv.Itoa(oct))
		}
	}
	return fs, ns
}

// itoa is strconv.Itoa.
// TRLC-LINKS: REQ-SDS-269
func itoa(i int) string { return strconv.Itoa(i) }

// ftoa3 writes v with three significant digits.
// TRLC-LINKS: REQ-SDS-269
func ftoa3(v float64) string { return strconv.FormatFloat(v, 'g', 3, 64) }
