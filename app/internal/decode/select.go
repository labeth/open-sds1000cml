// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"container/heap"
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

// runningMedian is medianOf of a list that grows a value at a time, each in
// O(log n): a decoder that reads its period from all the frames so far, at
// every frame, sorted them all again each time, and a busy window of
// thousands of frames cost it more than everything else. The smaller half
// is a max-heap, the larger a min-heap whose least is the median (the
// upper middle of an even count, as medianOf).
type runningMedian struct{ lo, hi floatHeap }

// add puts x in the list.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (m *runningMedian) add(x float64) {
	if len(m.lo.v) > 0 && x < -m.lo.v[0] {
		heap.Push(&m.lo, -x)
	} else {
		heap.Push(&m.hi, x)
	}
	n := len(m.lo.v) + len(m.hi.v)
	for len(m.lo.v) < n/2 {
		heap.Push(&m.lo, -heap.Pop(&m.hi).(float64))
	}
	for len(m.lo.v) > n/2 {
		heap.Push(&m.hi, -heap.Pop(&m.lo).(float64))
	}
}

// value is medianOf of the values added, 0 for none.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (m *runningMedian) value() float64 {
	if len(m.hi.v) == 0 {
		return 0
	}
	return m.hi.v[0]
}

// floatHeap is a min-heap of float64 (container/heap); the max-heap holds
// the values negated.
type floatHeap struct{ v []float64 }

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (h *floatHeap) Len() int { return len(h.v) }

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (h *floatHeap) Less(i, j int) bool { return h.v[i] < h.v[j] }

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (h *floatHeap) Swap(i, j int) { h.v[i], h.v[j] = h.v[j], h.v[i] }

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (h *floatHeap) Push(x any) { h.v = append(h.v, x.(float64)) }

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (h *floatHeap) Pop() any {
	x := h.v[len(h.v)-1]
	h.v = h.v[:len(h.v)-1]
	return x
}
