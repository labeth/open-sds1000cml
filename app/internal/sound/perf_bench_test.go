// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import (
	"math"
	"math/rand"
	"testing"
)

// benchTone is a 220 Hz tone with harmonics and noise at fs.
// TRLC-LINKS: REQ-SDS-266
func benchTone(n int, fs float64) []float64 {
	r := rand.New(rand.NewSource(4))
	x := make([]float64, n)
	for i := range x {
		t := float64(i) / fs
		x[i] = 0.5*math.Sin(2*math.Pi*220*t) + 0.1*math.Sin(2*math.Pi*440*t+.3) + 0.05*math.Sin(2*math.Pi*660*t) + 0.01*r.NormFloat64()
	}
	return x
}

// TRLC-LINKS: REQ-SDS-275
func BenchmarkMinMaxColumns(b *testing.B) {
	x := benchTone(65536, 48828)
	for b.Loop() {
		MinMaxColumns(x, 800)
	}
}

// TRLC-LINKS: REQ-SDS-267
func BenchmarkTunerAdd(b *testing.B) {
	t := &Tuner{Slow: true}
	hz := 440.0
	for b.Loop() {
		hz += 0.01
		t.Add(Pitch{Hz: hz, OK: true})
	}
}

// TRLC-LINKS: REQ-SDS-272
func BenchmarkExtractCycles(b *testing.B) {
	x := benchTone(8192, 48828)
	for b.Loop() {
		ExtractCycles(x, 48828, 100, 48828/220.0, 2, 512)
	}
}

// TRLC-LINKS: REQ-SDS-270
func BenchmarkFFT8192(b *testing.B) {
	x := benchTone(8192, 48828)
	re, im := make([]float64, 8192), make([]float64, 8192)
	for b.Loop() {
		copy(re, x)
		clear(im)
		fft(re, im, false)
	}
}

// TRLC-LINKS: REQ-SDS-267
func BenchmarkDetectPitch(b *testing.B) {
	x := benchTone(32768, 48828)
	for b.Loop() {
		DetectPitch(x, 48828, DefaultPitchOpts)
	}
}

// TRLC-LINKS: REQ-SDS-268
func BenchmarkEnvelope(b *testing.B) {
	x := benchTone(65536, 48828)
	for b.Loop() {
		Envelope(x, 48828, 0.01, 0.002)
	}
}

// TRLC-LINKS: REQ-SDS-271
func BenchmarkDelay(b *testing.B) {
	a := benchTone(16384, 48828)
	c := append(make([]float64, 7), a[:len(a)-7]...)
	for b.Loop() {
		Delay(a, c, 48828, 0.01)
	}
}

// TRLC-LINKS: REQ-SDS-270
func BenchmarkHarmonics(b *testing.B) {
	sp := AudioSpectrum(benchTone(16384, 48828), 48828, 16384)
	for b.Loop() {
		Harmonics(sp, 220)
	}
}

// TRLC-LINKS: REQ-SDS-269
func BenchmarkModRate(b *testing.B) {
	x := make([]float64, 4000)
	for i := range x {
		x[i] = math.Sin(2 * math.Pi * float64(i) / 200)
	}
	for b.Loop() {
		ModRate(x, 1000)
	}
}

// fftRef is fft as first written: the oracle and the baseline.
// TRLC-LINKS: REQ-SDS-270
func fftRef(re, im []float64, inverse bool) {
	n := len(re)
	if n < 2 {
		return
	}
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
	cs, sn := twiddle(n)
	for l := 2; l <= n; l <<= 1 {
		h, step := l/2, n/l
		for i := 0; i < n; i += l {
			for k := 0; k < h; k++ {
				wr, wi := cs[k*step], sn[k*step]
				if inverse {
					wi = -wi
				}
				a, b := i+k, i+k+h
				vr := re[b]*wr - im[b]*wi
				vi := re[b]*wi + im[b]*wr
				re[b], im[b] = re[a]-vr, im[a]-vi
				re[a], im[a] = re[a]+vr, im[a]+vi
			}
		}
	}
}

// fft gives exactly the first version's results, both ways.
// TRLC-LINKS: REQ-SDS-270
func TestFFTIsTheReference(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for _, n := range []int{2, 8, 1024, 8192} {
		for _, inv := range []bool{false, true} {
			re, im := make([]float64, n), make([]float64, n)
			for i := range re {
				re[i], im[i] = r.NormFloat64(), r.NormFloat64()
			}
			re2, im2 := append([]float64(nil), re...), append([]float64(nil), im...)
			fft(re, im, inv)
			fftRef(re2, im2, inv)
			for i := range re {
				if re[i] != re2[i] || im[i] != im2[i] {
					t.Fatalf("n %d inverse %v bin %d differs", n, inv, i)
				}
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-270
func BenchmarkFFT8192Ref(b *testing.B) {
	x := benchTone(8192, 48828)
	re, im := make([]float64, 8192), make([]float64, 8192)
	for b.Loop() {
		copy(re, x)
		clear(im)
		fftRef(re, im, false)
	}
}

// TRLC-LINKS: REQ-SDS-267
func BenchmarkToneAt(b *testing.B) {
	x := benchTone(16384, 48828)
	for b.Loop() {
		toneAt(x, 220, 48828)
		toneAt(x[:12000], 440, 48828) // a length without a window table
	}
}
