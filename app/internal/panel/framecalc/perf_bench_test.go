// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package framecalc

import (
	"math"
	"math/rand"
	"testing"
)

// fftRef is FFT as first written: the oracle the faster one matches bit for bit.
// TRLC-LINKS: REQ-SDS-070
func fftRef(re, im []float64) {
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
	for l := 2; l <= n; l <<= 1 {
		ang := -2 * math.Pi / float64(l)
		wr, wi := math.Cos(ang), math.Sin(ang)
		for i := 0; i < n; i += l {
			cr, ci := 1.0, 0.0
			for k := range l / 2 {
				tr := re[i+k+l/2]*cr - im[i+k+l/2]*ci
				ti := re[i+k+l/2]*ci + im[i+k+l/2]*cr
				re[i+k+l/2] = re[i+k] - tr
				im[i+k+l/2] = im[i+k] - ti
				re[i+k] += tr
				im[i+k] += ti
				cr, ci = cr*wr-ci*wi, cr*wi+ci*wr
			}
		}
	}
}

// FFT gives exactly the first version's results.
// TRLC-LINKS: REQ-SDS-070
func TestFFTIsTheReference(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	for _, n := range []int{2, 4, 16, 256, 4096} {
		re, im := make([]float64, n), make([]float64, n)
		for i := range re {
			re[i], im[i] = r.NormFloat64(), r.NormFloat64()
		}
		re2, im2 := append([]float64(nil), re...), append([]float64(nil), im...)
		FFT(re, im)
		fftRef(re2, im2)
		for i := range re {
			if re[i] != re2[i] || im[i] != im2[i] {
				t.Fatalf("n %d bin %d: %v%+vi, want %v%+vi", n, i, re[i], im[i], re2[i], im2[i])
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-070
func BenchmarkMags8192(b *testing.B) {
	r := rand.New(rand.NewSource(1))
	s := make([]float64, 8192)
	for i := range s {
		s[i] = 128 + 60*math.Sin(float64(i)*0.07) + r.Float64()
	}
	for b.Loop() {
		Mags(s)
	}
}

// TRLC-LINKS: REQ-SDS-070
func benchFFT(b *testing.B, fft func(re, im []float64)) {
	b.Helper()
	r := rand.New(rand.NewSource(1))
	re0, im0 := make([]float64, 4096), make([]float64, 4096)
	for i := range re0 {
		re0[i] = r.NormFloat64()
	}
	re, im := make([]float64, 4096), make([]float64, 4096)
	for b.Loop() {
		copy(re, re0)
		copy(im, im0)
		fft(re, im)
	}
}

// TRLC-LINKS: REQ-SDS-070
func BenchmarkFFT4096(b *testing.B) { benchFFT(b, FFT) }

// TRLC-LINKS: REQ-SDS-070
func BenchmarkFFT4096Ref(b *testing.B) { benchFFT(b, fftRef) }

// Log10 is math.Log10 to a few units in the last place over the spectrum's
// range of levels, and math.Log10 itself where it is not a positive normal.
// TRLC-LINKS: REQ-SDS-070
func TestLog10(t *testing.T) {
	r := rand.New(rand.NewSource(10))
	worst := 0.0
	for range 200000 {
		x := math.Exp(r.Float64()*1400 - 700)
		got, want := Log10(x), math.Log10(x)
		err := math.Abs(got - want)
		if a := math.Abs(want); a > 1 {
			err /= a
		}
		worst = math.Max(worst, err)
	}
	if worst > 4e-16*8 {
		t.Fatalf("worst error %g", worst)
	}
	for _, x := range []float64{0, -1, math.Inf(1), math.Inf(-1), math.NaN(), 5e-324, 1e-310, 1, 10, 1e-15} {
		got, want := Log10(x), math.Log10(x)
		if !(got == want || math.IsNaN(got) && math.IsNaN(want)) && math.Abs(got-want) > 1e-15*math.Max(1, math.Abs(want)) {
			t.Fatalf("Log10(%g) = %g, want %g", x, got, want)
		}
	}
}

// TRLC-LINKS: REQ-SDS-070
func BenchmarkLog10(b *testing.B) {
	x := make([]float64, 4096)
	for i := range x {
		x[i] = math.Exp(float64(i%997)*-0.03) + 1e-15
	}
	s := 0.0
	for b.Loop() {
		for _, v := range x {
			s += Log10(v)
		}
	}
	sinkLog = s
}

// TRLC-LINKS: REQ-SDS-070
func BenchmarkMathLog10(b *testing.B) {
	x := make([]float64, 4096)
	for i := range x {
		x[i] = math.Exp(float64(i%997)*-0.03) + 1e-15
	}
	s := 0.0
	for b.Loop() {
		for _, v := range x {
			s += math.Log10(v)
		}
	}
	sinkLog = s
}

var sinkLog float64
