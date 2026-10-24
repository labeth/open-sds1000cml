// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import (
	"math"
	"math/bits"
	"sync"
	"sync/atomic"
)

// The transforms: radix-2, in place, with the twiddles from a table per size
// (the rotation recurrence drifts by a few parts in 1e12 a step, which a
// 2^17-point phase-sensitive transform would carry into the sub-cent pitch
// refinement). sound.js has the same transform, so the two surfaces agree to
// rounding.

var twiddles sync.Map // n -> *[2][]float64 (cos, sin of -2*pi*k/n, k < n/2)

// twiddle returns the cos and sin tables of an n-point transform.
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-270
func twiddle(n int) ([]float64, []float64) {
	if t, ok := twiddles.Load(n); ok {
		p := t.(*[2][]float64)
		return p[0], p[1]
	}
	c, s := make([]float64, n/2), make([]float64, n/2)
	for k := range c {
		a := -2 * math.Pi * float64(k) / float64(n)
		c[k], s[k] = math.Cos(a), math.Sin(a)
	}
	twiddles.Store(n, &[2][]float64{c, s})
	return c, s
}

var hannTables sync.Map // n -> []float64, hann(i, n) for i < n

// lastHann is the window of the last length that is not a power of two.
var lastHann atomic.Pointer[[]float64]

// hannTable is hann(i, n) for every i, kept per power-of-two n and for the
// last other n: the tuner and the spectra window the same few sizes several
// times a second, and a cosine a sample cost the instrument as much as
// their transforms.
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-270
func hannTable(n int) []float64 {
	if n < 2 || n > 1<<20 {
		return nil
	}
	if n&(n-1) != 0 {
		// Another length: the last one is kept (a window setting keeps its
		// length while it stays), a cosine a sample spared.
		if t := lastHann.Load(); t != nil && len(*t) == n {
			return *t
		}
		w := make([]float64, n)
		for i := range w {
			w[i] = hann(i, n)
		}
		lastHann.Store(&w)
		return w
	}
	if t, ok := hannTables.Load(n); ok {
		return t.([]float64)
	}
	w := make([]float64, n)
	for i := range w {
		w[i] = hann(i, n)
	}
	hannTables.Store(n, w)
	return w
}

// scratchPools lend float64 buffers to one analysis call, a pool per
// power-of-two capacity: windows of up to a quarter million samples several
// times a second, freshly allocated, kept the collector running (5 MB/s)
// under the app's memory limit.
var scratchPools [32]sync.Pool

// getScratch is a buffer of n (contents undefined) from the pool.
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-269, REQ-SDS-270
func getScratch(n int) []float64 {
	k := bits.Len(uint(max(n, 1) - 1))
	if p, ok := scratchPools[k].Get().(*[]float64); ok {
		return (*p)[:n]
	}
	return make([]float64, n, 1<<k)
}

// putScratch gives b (from getScratch) back to its pool; the caller keeps
// no reference.
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-269, REQ-SDS-270
func putScratch(b []float64) {
	if c := cap(b); c > 0 && c&(c-1) == 0 {
		scratchPools[bits.Len(uint(c-1))].Put(&b)
	}
}

// fft transforms re, im in place (len a power of two); inverse runs the
// inverse transform without the 1/n scale.
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-270
func fft(re, im []float64, inverse bool) {
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

// pow2 is the smallest power of two at least n.
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-270
func pow2(n int) int {
	p := 1
	for p < n && p < 1<<30 { // doubling on would wrap a 32-bit int and never end
		p <<= 1
	}
	return p
}

// floorPow2 is the largest power of two at most n (0 for n < 1).
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-270
func floorPow2(n int) int {
	if n < 1 {
		return 0
	}
	p := 1
	for p*2 <= n && p < 1<<30 {
		p <<= 1
	}
	return p
}

// hann is the periodic-free (symmetric) Hann window's value at i of n, as the
// web's spectrum uses.
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-270
func hann(i, n int) float64 {
	return 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
}

// hannGain is a Hann window's response to a tone d bins (|d| <= 0.5) from a
// bin, relative to a tone on the bin.
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-270
func hannGain(d float64) float64 {
	if d == 0 {
		return 1
	}
	return math.Sin(math.Pi*d) / (math.Pi * d * (1 - d*d))
}

// mean is the average of x (0 for none).
// TRLC-LINKS: REQ-SDS-266
func mean(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

// toneAt is the Hann-windowed DFT of x at f (Hz) for sample rate fs: the
// complex amplitude a tone of amplitude A on f reads as A/2 times the window's
// sum (the Bode measurement's single-bin DFT, windowed).
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-271
func toneAt(x []float64, f, fs float64) (re, im float64) {
	n := len(x)
	if n < 2 {
		return 0, 0
	}
	m := mean(x)
	w := 2 * math.Pi * f / fs
	c, s := math.Cos(w), math.Sin(w)
	ck, sk := 1.0, 0.0
	win := hannTable(n)
	for i, v := range x {
		if win != nil {
			v = (v - m) * win[i]
		} else {
			v = (v - m) * hann(i, n)
		}
		re += v * ck
		im -= v * sk
		ck, sk = ck*c-sk*s, sk*c+ck*s
		if i&1023 == 1023 { // keep the rotation on the unit circle
			a := w * float64(i+1)
			ck, sk = math.Cos(a), math.Sin(a)
		}
	}
	return re, im
}

// wrapPi wraps an angle into (-pi, pi].
// TRLC-LINKS: REQ-SDS-267
func wrapPi(a float64) float64 {
	a = math.Mod(a+math.Pi, 2*math.Pi)
	if a <= 0 {
		a += 2 * math.Pi
	}
	return a - math.Pi
}

// finite reports a finite float.
// TRLC-LINKS: REQ-SDS-266
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
