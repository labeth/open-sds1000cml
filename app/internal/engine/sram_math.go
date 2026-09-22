package engine

import "math"

// eresQ8 keeps fractional codes through a centred moving average. The
// shrinking end windows never wrap or introduce fabricated samples.
func eresQ8(src, scratch []uint16, length int) {
	length = clampEresLen(length)
	if length <= 1 || len(src) == 0 {
		return
	}
	half := length / 2
	lo, hi, sum := 0, 0, uint32(0)
	for i := range src {
		end := i + half + 1
		if end > len(src) {
			end = len(src)
		}
		for hi < end {
			sum += uint32(src[hi])
			hi++
		}
		start := i - half
		if start < 0 {
			start = 0
		}
		for lo < start {
			sum -= uint32(src[lo])
			lo++
		}
		n := uint32(hi - lo)
		scratch[i] = uint16((sum + n/2) / n)
	}
	copy(src, scratch[:len(src)])
}

// sramAverage accumulates disjoint blocks, retaining Q8 fractions with memory
// independent of the requested count. Callers admit only matching captures.
type sramAverage struct {
	sum1, sum2 []uint32
	count      int
	anchor     float64
	lo, hi     int
}

func (a *sramAverage) reset() { a.count = 0 }
func (a *sramAverage) push(f *Frame, target int) int {
	n := f.Valid
	if len(a.sum1) != n {
		a.sum1 = make([]uint32, n)
		a.sum2 = make([]uint32, n)
		a.count = 0
	}
	if a.count >= target {
		a.count = 0
	}
	f.Q1 = qBuffer(f.Q1, n)
	f.Q2 = qBuffer(f.Q2, n)
	if a.count == 0 {
		a.anchor = f.EdgeX
		a.lo = 0
		a.hi = n
	}
	shift := int(math.Round(f.EdgeX - a.anchor))
	lo, hi := 0, n
	if shift < 0 {
		lo = -shift
	} else {
		hi = n - shift
	}
	if lo > a.lo {
		a.lo = lo
	}
	if hi < a.hi {
		a.hi = hi
	}
	if a.lo >= a.hi {
		a.reset()
		return 0
	}
	// Accumulate before writing output, so shifting cannot overwrite input.

	for i := 0; i < n; i++ {
		if a.count == 0 {
			a.sum1[i] = 0
			a.sum2[i] = 0
		}
		j := i + shift
		if j >= 0 && j < n {
			a.sum1[i] += uint32(f.Q1[j])
			a.sum2[i] += uint32(f.Q2[j])
		}
	}
	count := uint32(a.count + 1)
	for i := a.lo; i < a.hi; i++ {
		out := i - a.lo
		f.Q1[out] = uint16((a.sum1[i] + count/2) / count)
		f.Q2[out] = uint16((a.sum2[i] + count/2) / count)
		f.C1[out] = roundQ8(f.Q1[out])
		f.C2[out] = roundQ8(f.Q2[out])
	}
	f.Valid = a.hi - a.lo
	f.Q1 = f.Q1[:f.Valid]
	f.Q2 = f.Q2[:f.Valid]
	f.EdgeX = a.anchor - float64(a.lo)
	if f.WinCols > f.Valid {
		f.WinCols = f.Valid
	}

	a.count++
	return a.count
}
