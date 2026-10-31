// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math"
	"math/bits"
	"sort"
)

// EdgeLine is a digital line - every sample 0 or 255, as the digital
// stream's words carry it (ADR-STREAM-BITS) - held as its edges: N samples,
// the level of the first (0 or 1) and the samples where the level changes.
// A decoder that reads a line only through its edges decodes it in time
// that follows the bus's edges, not its samples: on the device a decode
// sample by sample reads a few million samples a second beside the display,
// and the digital stream brings tens of millions on a fast bus.
type EdgeLine struct {
	N     int
	First uint8 // the level of sample 0: 0 or 1
	At    []int // the samples whose level differs from the one before, increasing, each in 1..N-1
}

// Reset empties the line.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (l *EdgeLine) Reset() {
	l.N, l.First, l.At = 0, 0, l.At[:0]
}

// Last is the level of the line's last sample (0 for an empty line).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (l *EdgeLine) Last() uint8 {
	if l.N == 0 {
		return 0
	}
	return l.First ^ uint8(len(l.At)&1)
}

// start opens the next samples at level lvl: the line's first, or an edge
// where it differs from the last.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (l *EdgeLine) start(lvl uint8) {
	if l.N == 0 {
		l.First, l.At = lvl, l.At[:0]
	} else if l.Last() != lvl {
		l.At = append(l.At, l.N)
	}
}

// AppendLevel appends n samples of level lvl (0 or 1).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (l *EdgeLine) AppendLevel(lvl uint8, n int) {
	if n <= 0 {
		return
	}
	l.start(lvl & 1)
	l.N += n
}

// AppendWords appends the digital stream's words (stream_bits.v: {CH2 bits
// 15..0, CH1 bits 15..0}, bit 0 the earliest period, little-endian), sixteen
// samples a word of line ch (0 CH1, 1 CH2). A word's edges are the bits
// that differ from the bit before (the word against itself shifted up a
// period, the last word's level below it), found a set bit at a time: a
// quiet word costs a compare.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (l *EdgeLine) AppendWords(raw []byte, ch int) {
	n := len(raw) / 4
	if n == 0 {
		return
	}
	o := 2 * (ch & 1)
	if l.N == 0 {
		l.First, l.At = raw[o]&1, l.At[:0]
	}
	prev := uint32(l.First ^ uint8(len(l.At)&1))
	at, edges := l.N, l.At
	for i := 0; i < n; i++ {
		w := uint32(raw[4*i+o]) | uint32(raw[4*i+o+1])<<8
		t := (w ^ (w<<1 | prev)) & 0xffff
		for t != 0 {
			edges = append(edges, at+bits.TrailingZeros32(t))
			t &= t - 1
		}
		prev = w >> 15
		at += 16
	}
	l.N, l.At = at, edges
}

// AppendRange appends samples from..to-1 of src.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (l *EdgeLine) AppendRange(src *EdgeLine, from, to int) {
	from, to = max(from, 0), min(to, src.N)
	if to <= from {
		return
	}
	k := sort.SearchInts(src.At, from+1) // the first edge after sample from
	l.start(src.First ^ uint8(k&1))
	for ; k < len(src.At) && src.At[k] < to; k++ {
		l.At = append(l.At, l.N+src.At[k]-from)
	}
	l.N += to - from
}

// Drop removes the first n samples.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (l *EdgeLine) Drop(n int) {
	if n <= 0 {
		return
	}
	if n >= l.N {
		l.Reset()
		return
	}
	k := sort.SearchInts(l.At, n+1)
	l.First ^= uint8(k & 1)
	m := copy(l.At, l.At[k:])
	l.At = l.At[:m]
	for i := range l.At {
		l.At[i] -= n
	}
	l.N -= n
}

// Samples is the line as samples, 0 or 255.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (l *EdgeLine) Samples() []uint8 {
	out := make([]uint8, l.N)
	lvl, from := l.First, 0
	for _, at := range append(l.At, l.N) {
		if lvl != 0 {
			for i := from; i < at; i++ {
				out[i] = 255
			}
		}
		lvl, from = lvl^1, at
	}
	return out
}

// runs counts the line's high samples, and each level's samples in runs
// of three or more (heldBeyond's runs).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (l *EdgeLine) runs() (ones int, held [2]int) {
	lvl, from := l.First, 0
	run := func(to int) {
		if lvl != 0 {
			ones += to - from
		}
		if to-from >= 3 {
			held[lvl] += to - from
		}
	}
	for _, at := range l.At {
		run(at)
		lvl, from = lvl^1, at
	}
	run(l.N)
	return ones, held
}

// slice is sliceChannel of the line's samples (0 or 255), built from its
// edges: the same rails, threshold, first level and edges, each edge at the
// crossing sliceChannel computes between a 0 and a 255. A threshold whose
// band does not lie within the codes (sliceChannel would then read a 0 or a
// 255 as the other level) slices the samples themselves.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (l *EdgeLine) slice(thr float64, haveThr bool) sliced {
	const hystFrac = 0.20
	n := l.N
	ones, held := l.runs()
	// rails: two codes, each a level where it holds the noise floor's share.
	reason := ""
	floor := math.Max(1, 0.001*float64(n))
	switch {
	case n < 8:
		reason = "no/too-few valid samples"
	case float64(n-ones) < floor || float64(ones) < floor:
		reason = "flat/no transitions"
	}
	if reason != "" && haveThr {
		// railsAround: each level holds a run of three samples or more.
		if on := codeCeil(thr); on >= 1 && on <= 255 && min(held[0], held[1]) >= 3 {
			reason = ""
		}
	}
	if reason != "" {
		return sliced{reason: reason}
	}
	lowRail, highRail := 0.0, 255.0
	amp := highRail - lowRail
	threshold := (lowRail + highRail) / 2
	if haveThr {
		threshold = thr
	}
	band := hystFrac * amp / 2
	thHi, thLo := threshold+band, threshold-band
	on, hiAt, loAt := codeCeil(threshold), codeCeil(thHi), codeFloor(thLo)
	if on < 1 || on > 255 || hiAt > 255 || loAt < 0 {
		return sliceChannel(l.Samples(), thr, haveThr)
	}
	// sliceChannel's crossing between samples p and j = p+1 of codes 0 and
	// 255, in its own operations.
	lo, hi := float64(uint8(0)), float64(uint8(255))
	rise, fall := (threshold-lo)/(hi-lo), (threshold-hi)/(lo-hi)
	edges := make([]edge, len(l.At))
	d := 1
	if l.First != 0 {
		d = -1
	}
	for k, j := range l.At {
		frac := rise
		if d < 0 {
			frac = fall
		}
		edges[k] = edge{j, d, float64(j-1) + frac}
		d = -d
	}
	return sliced{ok: true, n: n, threshold: threshold, lowRail: lowRail, highRail: highRail, amp: amp,
		thHi: thHi, thLo: thLo, first: int8(l.First), edges: edges}
}

// lineIn is a decoder's line: 8-bit samples, or a digital line's edges.
// Neither is a line not given (a nil sample slice).
type lineIn struct {
	codes []uint8
	e     *EdgeLine
}

// samplesIn is a line of samples.
// TRLC-LINKS: REQ-SDS-018
func samplesIn(codes []uint8) lineIn { return lineIn{codes: codes} }

// edgesIn is a digital line's edges (nil: no line).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func edgesIn(l *EdgeLine) lineIn { return lineIn{e: l} }

// none reports a line not given.
// TRLC-LINKS: REQ-SDS-018
func (in lineIn) none() bool { return in.codes == nil && in.e == nil }

// slice is sliceChannel of the line.
// TRLC-LINKS: REQ-SDS-018
func (in lineIn) slice(thr float64, haveThr bool) sliced {
	if in.e != nil {
		return in.e.slice(thr, haveThr)
	}
	return sliceChannel(in.codes, thr, haveThr)
}

// samples is the line's sample count.
// TRLC-LINKS: REQ-SDS-018
func (in lineIn) samples() int {
	if in.e != nil {
		return in.e.N
	}
	return len(in.codes)
}

// sum is the sum of the line's samples.
// TRLC-LINKS: REQ-SDS-018
func (in lineIn) sum() int {
	if in.e != nil {
		ones, _ := in.e.runs()
		return 255 * ones
	}
	s := 0
	for _, v := range in.codes {
		s += int(v)
	}
	return s
}

// levelCursor is logicAt for positions that mostly move forward (a
// clock's edges read on the data line): on a digital line's edges it
// walks them instead of searching for each position.
type levelCursor struct {
	s    *sliced
	k, i int // edges at or before sample i
}

// at is logicAt(c.s, x).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func (c *levelCursor) at(x float64) int {
	s := c.s
	if s.codes != nil {
		return logicAt(s, x)
	}
	i := int(math.Round(x))
	if i < 0 || i >= s.n {
		return -1
	}
	if i < c.i {
		c.k = 0
	}
	c.i = i
	for c.k < len(s.edges) && s.edges[c.k].i <= i {
		c.k++
	}
	return int(s.first) ^ c.k&1
}
