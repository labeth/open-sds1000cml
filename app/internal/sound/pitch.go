// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import (
	"fmt"
	"math"
)

// The tuner: YIN (de Cheveigne and Kawahara, 2002) for the period, then a
// phase refinement on the strongest of the first four harmonics: the phase a
// tone advances between the two halves of the window measures its frequency
// to a small fraction of a cent on a stable tone, where YIN's parabola alone
// is off by several cents at a dozen samples a period.

// Pitch is one pitch reading.
type Pitch struct {
	Hz      float64 // 0 when unvoiced
	Clarity float64 // 1 - the YIN dip: 1 a pure periodic signal, 0 noise
	OK      bool
}

// PitchOpts bounds the search.
type PitchOpts struct {
	Fmin, Fmax float64 // Hz
	Threshold  float64 // YIN's absolute threshold (0.15 typical)
}

// DefaultPitchOpts covers bass to the top of a piano's fundamentals.
var DefaultPitchOpts = PitchOpts{Fmin: 25, Fmax: 5000, Threshold: 0.15}

// voicedDip is the deepest a normalised difference dip may be and the
// stretch still read as a pitch: noise sits near 1.
const voicedDip = 0.45

// DetectPitch reads the pitch of x (any units) sampled at fs.
// TRLC-LINKS: REQ-SDS-267
func DetectPitch(x []float64, fs float64, o PitchOpts) Pitch {
	n := len(x)
	if n < 64 || !(fs > 0) {
		return Pitch{}
	}
	if !(o.Fmin > 0) {
		o.Fmin = DefaultPitchOpts.Fmin
	}
	if !(o.Fmax > o.Fmin) {
		o.Fmax = DefaultPitchOpts.Fmax
	}
	if !(o.Threshold > 0) {
		o.Threshold = DefaultPitchOpts.Threshold
	}
	tauMax := min(n/2, int(math.Ceil(fs/o.Fmin))+2)
	tauMin := max(2, int(math.Floor(fs/o.Fmax)))
	if tauMax <= tauMin+2 {
		return Pitch{}
	}
	w := n - tauMax
	d := yinDifference(x, w, tauMax)
	// Cumulative mean normalised difference.
	cm := make([]float64, tauMax)
	cm[0] = 1
	run := 0.0
	for t := 1; t < tauMax; t++ {
		run += d[t]
		if run > 0 {
			cm[t] = d[t] * float64(t) / run
		} else {
			cm[t] = 1
		}
	}
	tau := -1
	for t := tauMin; t < tauMax-1; t++ {
		if cm[t] < o.Threshold {
			for t+1 < tauMax-1 && cm[t+1] < cm[t] {
				t++
			}
			tau = t
			break
		}
	}
	if tau < 0 {
		best := tauMin
		for t := tauMin; t < tauMax-1; t++ {
			if cm[t] < cm[best] {
				best = t
			}
		}
		tau = best
	}
	dip := cm[tau]
	if dip > voicedDip || tau <= tauMin && cm[tau+1] < dip {
		return Pitch{Clarity: math.Max(0, 1-dip)}
	}
	ft := float64(tau)
	if tau > 0 && tau < tauMax-1 {
		a, b, c := cm[tau-1], cm[tau], cm[tau+1]
		if den := a - 2*b + c; den > 0 {
			ft += 0.5 * (a - c) / den
		}
	}
	f := fs / ft
	return Pitch{Hz: refinePitch(x, fs, f), Clarity: math.Max(0, 1-dip), OK: true}
}

// yinDifference is YIN's squared difference d(tau) for tau < tauMax over a
// window of w samples, by FFT: d = E(0) + E(tau) - 2 r(tau).
// TRLC-LINKS: REQ-SDS-267
func yinDifference(x []float64, w, tauMax int) []float64 {
	n := len(x)
	m := mean(x)
	size := pow2(n + w)
	ar, ai := make([]float64, size), make([]float64, size)
	br, bi := make([]float64, size), make([]float64, size)
	for i := 0; i < n; i++ {
		br[i] = x[i] - m
		if i < w {
			ar[i] = br[i]
		}
	}
	fft(ar, ai, false)
	fft(br, bi, false)
	for k := range ar {
		// conj(A) * B
		re := ar[k]*br[k] + ai[k]*bi[k]
		im := ar[k]*bi[k] - ai[k]*br[k]
		ar[k], ai[k] = re, im
	}
	fft(ar, ai, true)
	// Prefix sums of the squares for the energies.
	cum := make([]float64, n+1)
	for i := 0; i < n; i++ {
		v := x[i] - m
		cum[i+1] = cum[i] + v*v
	}
	d := make([]float64, tauMax)
	e0 := cum[w]
	for t := 0; t < tauMax; t++ {
		r := ar[t] / float64(size)
		et := cum[t+w] - cum[t]
		d[t] = math.Max(0, e0+et-2*r)
	}
	return d
}

// refinePitch measures f by the phase the strongest of harmonics 1..4 of a
// first estimate advances between the window's two halves; it keeps the
// estimate when the refinement disagrees by more than 2 %.
// TRLC-LINKS: REQ-SDS-267
func refinePitch(x []float64, fs, f float64) float64 {
	n := len(x)
	half := n / 2
	if half < 32 || !(f > 0) {
		return f
	}
	bestH, bestM := 1, -1.0
	for h := 1; h <= 4; h++ {
		fh := float64(h) * f
		if fh >= fs*0.45 {
			break
		}
		re, im := toneAt(x, fh, fs)
		if m := math.Hypot(re, im); m > bestM {
			bestH, bestM = h, m
		}
	}
	// Each half must hold several cycles of the harmonic for its phase to
	// stand clear of the window's negative-frequency image.
	fh := float64(bestH) * f
	if fh*float64(half)/fs < 4 {
		return f
	}
	for it := 0; it < 2; it++ {
		r1, i1 := toneAt(x[:half], fh, fs)
		r2, i2 := toneAt(x[half:2*half], fh, fs)
		if math.Hypot(r1, i1) == 0 || math.Hypot(r2, i2) == 0 {
			return f
		}
		dphi := math.Atan2(i2, r2) - math.Atan2(i1, r1)
		expect := 2 * math.Pi * fh * float64(half) / fs
		fh += wrapPi(dphi-expect) * fs / (2 * math.Pi * float64(half))
	}
	g := fh / float64(bestH)
	if math.Abs(g-f) > 0.02*f || !finite(g) {
		return f
	}
	return g
}

// Note is a frequency as the nearest equal-tempered note.
type Note struct {
	Name   string  // "A", "C#"
	Octave int     // scientific: A4 = 440 Hz
	Midi   int     // 69 = A4
	Cents  float64 // from the note, -50..50
	RefHz  float64 // the note's frequency
}

// noteNames are the twelve note names from C.
var noteNames = [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// NoteOf names f against A4 = a4 Hz (440 when 0).
// TRLC-LINKS: REQ-SDS-267
func NoteOf(f, a4 float64) Note {
	if !(a4 > 0) {
		a4 = 440
	}
	if !(f > 0) || !finite(f) {
		return Note{}
	}
	semis := 12 * math.Log2(f/a4)
	m := int(math.Round(semis)) + 69
	ref := a4 * math.Pow(2, float64(m-69)/12)
	pc := ((m % 12) + 12) % 12
	oct := int(math.Floor(float64(m)/12)) - 1
	return Note{Name: noteNames[pc], Octave: oct, Midi: m, Cents: 1200 * math.Log2(f/ref), RefHz: ref}
}

// String is "A4" or "C#3".
// TRLC-LINKS: REQ-SDS-267
func (n Note) String() string {
	if n.Name == "" {
		return "--"
	}
	return fmt.Sprintf("%s%d", n.Name, n.Octave)
}

// Cents is the interval from b to a in cents.
// TRLC-LINKS: REQ-SDS-267
func Cents(a, b float64) float64 {
	if !(a > 0) || !(b > 0) {
		return math.NaN()
	}
	return 1200 * math.Log2(a/b)
}

// TunerWindow is the samples a tuner reading takes for a pitch near f (0:
// unknown): about sixteen periods (eight a half for the refinement), a power
// of two between 4096 and 32768 at the stream's rate.
// TRLC-LINKS: REQ-SDS-267
func TunerWindow(f, fs float64) int {
	n := 8192
	if f > 0 {
		n = pow2(int(16 * fs / f))
	}
	return min(32768, max(4096, n))
}

// PitchRange is a tuner range's search bounds.
// TRLC-LINKS: REQ-SDS-267
func PitchRange(id string) PitchOpts {
	switch id {
	case "bass":
		return PitchOpts{Fmin: 20, Fmax: 600, Threshold: 0.15}
	case "high":
		return PitchOpts{Fmin: 200, Fmax: 12000, Threshold: 0.15}
	}
	return DefaultPitchOpts
}
