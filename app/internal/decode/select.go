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

// nearHist is the most integers nthNear counts in a histogram above the
// least; the rest are counted together.
const nearHist = 4096

// nearBuf is nthNear's working arrays, kept from call to call.
type nearBuf struct {
	hist         []int32
	rest         []int
	below, above []float64
}

// nthNear is nthValue(v, k) for values that each lie within half of an
// integer, iv[j] the one beside v[j] (a gap between two edges of a digital
// line, iv the gap in samples): two values whose integers differ order as
// their integers do, so the k-th value's integer is found by counting the
// integers, and only the values at that integer are compared as floats -
// and of them only the ones that differ from it. On the device a float
// compare costs ten integer ones, and a clock's gaps nearly all equal a
// period's samples exactly. b holds the working arrays (nil: made anew).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func nthNear(v []float64, iv []int, k int, b *nearBuf) float64 {
	if b == nil {
		b = &nearBuf{}
	}
	lo, hi := iv[0], iv[0]
	for _, x := range iv {
		lo, hi = min(lo, x), max(hi, x)
	}
	// the histogram counts lo up to lo+size-1, and the rest in hist[size]
	size := min(hi-lo+1, nearHist, len(iv)+16)
	if cap(b.hist) < size+1 {
		b.hist = make([]int32, size+1, max(size+1, 2*cap(b.hist)))
	}
	hist := b.hist[:size+1]
	clear(hist)
	for _, x := range iv {
		hist[min(x-lo, size)]++
	}
	// the k-th integer, d, and the values below it, less
	d, less := 0, 0
	for c := 0; ; c++ {
		if c == size {
			// past the histogram: the integers there, selected whole
			rest := b.rest[:0]
			for _, x := range iv {
				if x-lo >= size {
					rest = append(rest, x)
				}
			}
			b.rest = rest
			d = nthInt(rest, k-less)
			for _, x := range rest {
				if x < d {
					less++
				}
			}
			break
		}
		if less+int(hist[c]) > k {
			d = lo + c
			break
		}
		less += int(hist[c])
	}
	// the values at d: below it, at it exactly, above it
	df := float64(d)
	db := math.Float64bits(df)
	below, above := b.below[:0], b.above[:0]
	at := 0
	for j, x := range iv {
		if x != d {
			continue
		}
		y := v[j]
		if math.Float64bits(y) == db { // equal, told by the bits
			at++
			continue
		}
		switch {
		case y < df:
			below = append(below, y)
		case y > df:
			above = append(above, y)
		default:
			at++
		}
	}
	b.below, b.above = below, above
	r := k - less
	if r < len(below) {
		return nthValue(below, r)
	}
	if r -= len(below); r < at {
		return df
	}
	return nthValue(above, r-at)
}

// nearBufOf is a nearBuf kept in sc's next slot (nil sc: a new one).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func nearBufOf(sc *scratch) *nearBuf {
	s := scratchSlice[nearBuf](sc, 1)[:1]
	return &s[0]
}

// nthInt is the k-th smallest of c (0-based), c reordered: nthValue's
// partitioning on integers.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func nthInt(c []int, k int) int {
	lo, hi := 0, len(c)-1
	for lo < hi {
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
// upper middle of an even count, as medianOf). The heaps hold each value's
// orderKey, compared as integers: on the device a float compare costs ten
// integer ones (-0 and +0 then differ, which the lengths it is given never
// hold both of).
type runningMedian struct{ lo, hi keyHeap } // lo holds the keys inverted

// orderKey is an integer that orders as x does among values that are not
// NaN (-0 below +0): the sign bit set for a positive x, all bits inverted
// for a negative one.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func orderKey(x float64) uint64 {
	b := math.Float64bits(x)
	if b>>63 != 0 {
		return ^b
	}
	return b | 1<<63
}

// keyValue is the value of an orderKey.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func keyValue(k uint64) float64 {
	if k>>63 != 0 {
		return math.Float64frombits(k &^ (1 << 63))
	}
	return math.Float64frombits(^k)
}

// add puts x in the list.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (m *runningMedian) add(x float64) {
	k := orderKey(x)
	if len(m.lo.v) > 0 && k < ^m.lo.v[0] {
		m.lo.push(^k)
	} else {
		m.hi.push(k)
	}
	n := len(m.lo.v) + len(m.hi.v)
	for len(m.lo.v) < n/2 {
		m.lo.push(^m.hi.pop())
	}
	for len(m.lo.v) > n/2 {
		m.hi.push(^m.lo.pop())
	}
}

// value is medianOf of the values added, 0 for none.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (m *runningMedian) value() float64 {
	if len(m.hi.v) == 0 {
		return 0
	}
	return keyValue(m.hi.v[0])
}

// keyHeap is a min-heap of keys, container/heap's steps on a typed slice
// (through heap.Interface every value moved in or out is boxed, an
// allocation each).
type keyHeap struct{ v []uint64 }

// push adds x (heap.Push).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (h *keyHeap) push(x uint64) {
	h.v = append(h.v, x)
	v, j := h.v, len(h.v)-1
	for {
		i := (j - 1) / 2 // parent
		if i == j || !(v[j] < v[i]) {
			break
		}
		v[i], v[j] = v[j], v[i]
		j = i
	}
}

// pop removes and returns the least (heap.Pop).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (h *keyHeap) pop() uint64 {
	v := h.v
	n := len(v) - 1
	v[0], v[n] = v[n], v[0]
	i := 0
	for {
		j1 := 2*i + 1
		if j1 >= n || j1 < 0 {
			break
		}
		j := j1 // left child
		if j2 := j1 + 1; j2 < n && v[j2] < v[j1] {
			j = j2 // right child
		}
		if !(v[j] < v[i]) {
			break
		}
		v[i], v[j] = v[j], v[i]
		i = j
	}
	x := v[n]
	h.v = v[:n]
	return x
}
