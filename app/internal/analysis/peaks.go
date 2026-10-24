// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

// Spectral peaks for the SPECTRUM workspace's peak table, harmonics and THD.
// The peak search is the web's (peaks.js detectPeaks, held together by
// TestPeaksJSParity); harmonics and THD are new on both surfaces.

import (
	"math"
	"sort"
)

const (
	// PeakFloorDB is how far below the spectrum's strongest bin a local
	// maximum may lie and still be a peak (peaks.js floorDb).
	PeakFloorDB = -50.0

	// MaxHarmonic is the highest harmonic numbered and counted in the THD.
	MaxHarmonic = 10
)

// Peak is one spectral peak.
//
// DB is the tone's level, in the spectrum's own dB units: the peak bin's
// level corrected for the Hann window's scalloping at the refined frequency.
// A tone between two bins reads up to 1.42 dB low in its bin; corrected it
// reads within 0.06 dB (the parabola's own vertex would read up to 0.32 dB
// high). The spectrum must therefore be Hann-windowed, as both surfaces' FFTs
// are. peaks.js shows the bin level, which is db[Bin].
// TRLC-LINKS: REQ-SDS-216
type Peak struct {
	Freq  float64 // Hz, parabola-refined: (Bin + offset) * binHz
	DB    float64 // the tone's level
	RelDB float64 // DB less the largest peak's DB (0 for the largest)
	Bin   int     // the local maximum's bin
	Harm  int     // harmonic number set by Harmonics: 1 the fundamental, 0 none
}

// hannGain is a Hann window's response to a tone d bins (|d| <= 0.5) from a
// bin, relative to a tone on the bin: sinc(d) / (1 - d^2).
// TRLC-LINKS: REQ-SDS-216
func hannGain(d float64) float64 {
	if d == 0 {
		return 1
	}
	return math.Sin(math.Pi*d) / (math.Pi * d * (1 - d*d))
}

// Peaks finds up to maxPeaks peaks in a magnitude spectrum db (dB, bin k at
// k*binHz) as the web does (peaks.js detectPeaks): a bin above its two
// neighbours on each side and less than 50 dB below the strongest bin, its
// frequency refined by a parabola through the bin and its neighbours. The
// strongest maxPeaks are kept and returned in frequency order, which holds
// still frame to frame where a magnitude ranking would jitter.
// TRLC-LINKS: REQ-SDS-216, REQ-SDS-070
func Peaks(db []float64, binHz float64, maxPeaks int) []Peak {
	if maxPeaks <= 0 || len(db) < 5 {
		return nil
	}
	top := math.Inf(-1)
	for _, v := range db {
		if v > top {
			top = v
		}
	}
	if math.IsInf(top, 0) {
		return nil // nothing finite, or an infinite bin no level is relative to
	}
	floor := top + PeakFloorDB
	var ps []Peak
	for k := 2; k < len(db)-2; k++ {
		b := db[k]
		// The floor first: most bins of a spectrum lie under it (the tests
		// have no side effects, so their order changes nothing else).
		if !(b > floor && b > db[k-1] && b >= db[k+1] && b > db[k-2] && b >= db[k+2]) {
			continue
		}
		a, c := db[k-1], db[k+1]
		d := 0.5 * (a - c) / (a - 2*b + c) // within +-0.5 at a local maximum
		if !finite(d) || math.Abs(d) > 1 {
			d = 0
		}
		lvl := b - 20*math.Log10(hannGain(math.Max(-0.5, math.Min(0.5, d))))
		ps = append(ps, Peak{Freq: (float64(k) + d) * binHz, DB: lvl, Bin: k})
	}
	// the strongest first by bin level (the web's ranking), then by frequency
	sort.SliceStable(ps, func(i, j int) bool { return db[ps[i].Bin] > db[ps[j].Bin] })
	if len(ps) > maxPeaks {
		ps = ps[:maxPeaks]
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Freq < ps[j].Freq })
	best := math.Inf(-1)
	for _, p := range ps {
		best = math.Max(best, p.DB)
	}
	for i := range ps {
		ps[i].RelDB = ps[i].DB - best
	}
	return ps
}

// THD is the harmonic distortion of a peak list.
// TRLC-LINKS: REQ-SDS-216
type THD struct {
	F0   float64 // the fundamental (Hz) the harmonics were counted against
	Fund int     // the fundamental's index in the peak list, -1 when no peak sits on F0
	N    int     // harmonics found among H2..H10
	Pct  float64 // THD in percent of the fundamental, NaN when no harmonic was found
	DB   float64 // the same as a level ratio, 20*log10(Pct/100)
}

// Harmonics numbers the peaks that sit on multiples of the fundamental f0 and
// returns the THD from H2..H10: the root sum of their powers relative to the
// fundamental. f0 <= 0 takes the largest peak as the fundamental; a measured
// frequency may be passed instead. A peak is harmonic n when it lies within
// 1.5 bins (or 1 % of n*f0, if wider) of n*f0; of several, the strongest. It
// sets ps[i].Harm in place (0 for the others). The bin width is recovered
// from the peaks themselves (Freq/Bin of the highest bin).
// TRLC-LINKS: REQ-SDS-216
func Harmonics(ps []Peak, f0 float64) THD {
	t := THD{F0: f0, Fund: -1, Pct: math.NaN(), DB: math.NaN()}
	for i := range ps {
		ps[i].Harm = 0
	}
	if !(f0 > 0) || math.IsInf(f0, 0) {
		strongest := -1
		for i, p := range ps {
			if finite(p.DB) && p.Freq > 0 && finite(p.Freq) && (strongest < 0 || p.DB > ps[strongest].DB) {
				strongest = i
			}
		}
		if strongest < 0 {
			return t
		}
		f0 = ps[strongest].Freq
		t.F0 = f0
	}
	binHz, topBin := 0.0, 0
	for _, p := range ps {
		if p.Bin > topBin && p.Freq > 0 && finite(p.Freq) {
			binHz, topBin = p.Freq/float64(p.Bin), p.Bin
		}
	}
	var best [MaxHarmonic + 1]int
	for n := range best {
		best[n] = -1
	}
	for i, p := range ps {
		if !finite(p.Freq) || !finite(p.DB) {
			continue
		}
		n := jsRound(p.Freq / f0)
		if !(n >= 1 && n <= MaxHarmonic) {
			continue
		}
		if math.Abs(p.Freq-n*f0) > math.Max(1.5*binHz, 0.01*n*f0) {
			continue
		}
		if k := int(n); best[k] < 0 || p.DB > ps[best[k]].DB {
			best[k] = i
		}
	}
	for n, i := range best {
		if i >= 0 {
			ps[i].Harm = n
		}
	}
	t.Fund = best[1]
	if t.Fund < 0 {
		return t
	}
	sum := 0.0
	for n := 2; n <= MaxHarmonic; n++ {
		if i := best[n]; i >= 0 {
			sum += math.Pow(10, (ps[i].DB-ps[t.Fund].DB)/10)
			t.N++
		}
	}
	if t.N > 0 {
		t.Pct = 100 * math.Sqrt(sum)
		t.DB = 10 * math.Log10(sum)
	}
	return t
}

// SpectrumDB is the web's spectrum (peaks.js spectrum and dbAt): the largest
// power-of-two prefix of samples (at least 16) with its mean removed, Hann
// windowed and transformed, as dB relative to its strongest bin. Bin k lies at
// k/(N*sampleS) Hz, N being 2*len(result). It returns nil for fewer than 16
// samples.
// TRLC-LINKS: REQ-SDS-216, REQ-SDS-070
func SpectrumDB(samples []float64) []float64 {
	n := 1
	for n*2 <= len(samples) {
		n <<= 1
	}
	if n < 16 {
		return nil
	}
	re, im := make([]float64, n), make([]float64, n)
	mean := 0.0
	for i := 0; i < n; i++ {
		mean += samples[i]
	}
	mean /= float64(n)
	for i := 0; i < n; i++ {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		re[i] = (samples[i] - mean) * w
	}
	fftInPlace(re, im)
	half := n / 2
	db := make([]float64, half)
	peak := 1e-9
	for k := 0; k < half; k++ {
		db[k] = math.Hypot(re[k], im[k])
		if db[k] > peak {
			peak = db[k]
		}
	}
	for k := range db {
		db[k] = 20 * math.Log10(db[k]/peak+1e-12)
	}
	return db
}
