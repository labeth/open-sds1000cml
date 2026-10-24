// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math"
	"sync"
)

// nthScratch holds nthValue's working copies.
var nthScratch = sync.Pool{New: func() any { return new([]float64) }}

// nthValue is the k-th smallest of v (0-based) in sort.Float64s order (NaNs
// first) without sorting: a copy is partitioned until k is in place, linear
// on average. A busy line holds hundreds of thousands of runs, and sorting
// them all for one quantile cost the bus search seconds on the scope.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214
func nthValue(v []float64, k int) float64 {
	// The copy is scratch: a pooled buffer, not an allocation the
	// collector has to clear and sweep each call.
	bp := nthScratch.Get().(*[]float64)
	c := append((*bp)[:0], v...)
	defer func() { *bp = c; nthScratch.Put(bp) }()
	nan := 0
	for i, x := range c {
		if math.IsNaN(x) {
			c[i], c[nan] = c[nan], c[i]
			nan++
		}
	}
	if k < nan {
		return math.NaN()
	}
	c, k = c[nan:], k-nan
	lo, hi := 0, len(c)-1
	for lo < hi {
		// median of three as the pivot
		mid := lo + (hi-lo)/2
		if c[mid] < c[lo] {
			c[mid], c[lo] = c[lo], c[mid]
		}
		if c[hi] < c[lo] {
			c[hi], c[lo] = c[lo], c[hi]
		}
		if c[hi] < c[mid] {
			c[hi], c[mid] = c[mid], c[hi]
		}
		p := c[mid]
		i, j := lo, hi
		for i <= j {
			for c[i] < p {
				i++
			}
			for c[j] > p {
				j--
			}
			if i <= j {
				c[i], c[j] = c[j], c[i]
				i++
				j--
			}
		}
		switch {
		case k <= j:
			hi = j
		case k >= i:
			lo = i
		default:
			return c[k]
		}
	}
	return c[k]
}
