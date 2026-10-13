// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"

	"open-sds/app/internal/sramcapture"
)

// eresQ8 keeps fractional codes through a centred moving average. The
// shrinking end windows never wrap or introduce fabricated samples.
// TRLC-LINKS: REQ-SDS-012
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
// TRLC-LINKS: REQ-SDS-012
type sramAverage struct {
	sum1, sum2 []uint32
	count      int
	anchor     float64
	lo, hi     int
}

// TRLC-LINKS: REQ-SDS-012
func (a *sramAverage) reset() { a.count = 0 }

// averageCfg is the part of a capture's configuration an average's block
// keeps to: the same settings' captures, not the record's split around the
// trigger. A serial trigger moves the split by each match's measured shift;
// keyed on it, the block would restart on almost every capture and Average
// never get past a few - a master on a serial trigger would never finish. The
// block aligns each capture on its edge anyway.
// TRLC-LINKS: REQ-SDS-012
func averageCfg(c sramcapture.Config) sramcapture.Config {
	c.PreWords, c.PostWords = 0, 0
	return c
}

// averageLengthSlack is how far a record's length may stray from its block's
// and still join it: a decimated record's length moves a sample or two with
// the trigger's phase (14515 to 14517 samples at 20us/div), and a block
// restarting on each change would never reach its count - a master would
// never finish. Another plan's record is far longer or shorter.
const averageLengthSlack = 8

// push adds a record to the average and writes the average into f: the mean
// of the records so far until target are in, then a running average in which
// each record weighs 1/target. Restarting after target showed every target-th
// frame as one raw capture, as noisy as Normal. It returns the records the
// average holds (at most target).
// TRLC-LINKS: REQ-SDS-012
func (a *sramAverage) push(f *Frame, target int) int {
	n := f.Valid
	target = max(target, 1)
	if d := n - len(a.sum1); a.count == 0 && d != 0 || d < -averageLengthSlack || d > averageLengthSlack {
		a.sum1 = make([]uint32, n)
		a.sum2 = make([]uint32, n)
		a.count = 0
	}
	m := min(n, len(a.sum1)) // the samples this record and the block both hold
	f.Q1 = qBuffer(f.Q1, n)
	f.Q2 = qBuffer(f.Q2, n)
	if a.count == 0 {
		a.anchor = f.EdgeX
		a.lo = 0
		a.hi = n
	}
	// Align to the block's first edge to a fraction of a sample: a whole-sample
	// shift left each record up to half a sample off, blurring the averaged
	// edge and moving it between blocks (0.87 ns peak to peak at 10 ns/div).
	delta := f.EdgeX - a.anchor
	shift := int(math.Floor(delta))
	frac := delta - float64(shift)
	if frac < 1e-6 {
		frac = 0
	}
	lo, hi := 0, m
	if shift < 0 {
		lo = -shift
	}
	if top := n - shift; frac > 0 && top-1 < hi {
		hi = top - 1
	} else if top < hi {
		hi = top
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
	// Once full, a sum holds target times the average: it loses its own
	// 1/target share for the new record's.
	running, t := a.count >= target, uint32(target)
	for i := 0; i < m; i++ {
		if a.count == 0 {
			a.sum1[i] = 0
			a.sum2[i] = 0
		}
		j := i + shift
		if j < 0 || j >= n {
			continue
		}
		x1, x2 := uint32(f.Q1[j]), uint32(f.Q2[j])
		if frac != 0 && j+1 < n {
			x1 = uint32(math.Round(float64(f.Q1[j])*(1-frac) + float64(f.Q1[j+1])*frac))
			x2 = uint32(math.Round(float64(f.Q2[j])*(1-frac) + float64(f.Q2[j+1])*frac))
		}
		if running {
			a.sum1[i] -= a.sum1[i] / t
			a.sum2[i] -= a.sum2[i] / t
		}
		a.sum1[i] += x1
		a.sum2[i] += x2
	}
	count := uint32(min(a.count+1, target))
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

	a.count = int(count)
	return a.count
}
