// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math"
	"math/bits"
	"slices"
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
	// sc lends the decodes of the line their working arrays: a stream
	// decoder's window (StreamDecoder), decoded again at every push.
	sc *scratch
}

// scratch holds the working arrays one decode takes, kept for the next: a
// stream decodes a window of tens of thousands of edges at every push, and
// arrays made afresh each time cost the device more in allocation, clearing
// and collection than the decode's own reading of them. Each request takes
// the next slot, so the arrays one decode holds at once are all distinct; a
// decode's arrays are its own until the next decode begins (reset), and a
// Result that holds them (a wireOut's spans) is read before then: the
// stream decoder copies the spans it keeps.
type scratch struct {
	slots []any
	next  int
}

// reset begins a decode: its requests take the slots from the first again.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (sc *scratch) reset() {
	if sc != nil {
		sc.next = 0
	}
}

// scratchAppend is append(s, x) for a slice from slot k of sc: an array it
// outgrows is replaced in the slot by the larger one.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func scratchAppend[T any](sc *scratch, k int, s []T, x T) []T {
	if len(s) == cap(s) {
		s = scratchGrow(sc, k, s)
	}
	return append(s, x)
}

// scratchGrow is s, from slot k of sc, in an array of twice the room,
// which replaces it in the slot (no scratch: append's growth).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func scratchGrow[T any](sc *scratch, k int, s []T) []T {
	if sc == nil {
		return slices.Grow(s, 1)
	}
	ns := make([]T, len(s), 2*cap(s)+64)
	copy(ns, s)
	sc.slots[k] = ns
	return ns
}

// scratchSlice is an empty slice of capacity n or more: the next slot's
// array when it holds as many of T, a new one (kept in the slot) when not,
// and a new one each call without a scratch.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func scratchSlice[T any](sc *scratch, n int) []T {
	if sc == nil {
		return make([]T, 0, n)
	}
	k := sc.next
	sc.next++
	if k == len(sc.slots) {
		sc.slots = append(sc.slots, nil)
	} else if s, ok := sc.slots[k].([]T); ok && cap(s) >= n {
		return s[:0]
	}
	s := make([]T, 0, n+n/4+16)
	sc.slots[k] = s
	return s
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
	m := len(edges)
	for i := 0; i < n; i++ {
		b := raw[4*i+o : 4*i+o+2 : 4*i+o+2]
		w := uint32(b[0]) | uint32(b[1])<<8
		if t := (w ^ (w<<1 | prev)) & 0xffff; t != 0 {
			// room for the word's sixteen edges, written in place
			if cap(edges)-m < 16 {
				edges = slices.Grow(edges[:m], 16)
			}
			e := edges[: m+16 : m+16]
			for t != 0 {
				e[m] = at + bits.TrailingZeros32(t)
				m++
				t &= t - 1
			}
		}
		prev = w >> 15
		at += 16
	}
	l.N, l.At = at, edges[:m]
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
	end := k + sort.SearchInts(src.At[k:], to) // the first edge at or past to
	m := len(l.At)
	l.At = slices.Grow(l.At, end-k)[:m+end-k]
	for j, at := range src.At[k:end] {
		l.At[m+j] = l.N + at - from
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
	lvl, from := l.First&1, 0
	for _, at := range l.At {
		if d := at - from; d >= 3 {
			held[lvl] += d
		}
		lvl, from = lvl^1, at
	}
	if d := l.N - from; d >= 3 {
		held[lvl] += d
	}
	return l.ones(), held
}

// ones counts the line's high samples: the runs between its edges, every
// other one from the first high.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (l *EdgeLine) ones() int {
	at, ones := l.At, 0
	k := 0
	if l.First != 0 {
		// high from sample 0 to the first edge
		if len(at) == 0 {
			return l.N
		}
		ones, k = at[0], 1
	}
	for ; k+1 < len(at); k += 2 {
		ones += at[k+1] - at[k]
	}
	if k < len(at) {
		ones += l.N - at[k] // high from the last edge to the end
	}
	return ones
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
	ones := l.ones()
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
		_, held := l.runs()
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
	edges := scratchSlice[edge](l.sc, len(l.At))[:len(l.At)]
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
		thHi: thHi, thLo: thLo, first: int8(l.First), edges: edges, sc: l.sc}
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

// scratch is the scratch the line's decodes take their arrays from (nil:
// none).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (in lineIn) scratch() *scratch {
	if in.e != nil {
		return in.e.sc
	}
	return nil
}

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

// roundUps is, for a line sliced from its edges (slice), what math.Round
// adds to each crossing's sample before it: a crossing lies at
// x = (i-1) + frac, frac the share of a sample the threshold lies across
// (fall's for a fall, rise's for a rise), and Round(x) is i-1 or i as frac
// lies below or above a half whatever i is, within the rounding of x
// (2^-29 under 2^24 samples); ok false where frac lies nearer a half than
// that can be told, and for a line sliced from samples.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func (s *sliced) roundUps() (fall, rise int, ok bool) {
	if !s.ok || s.codes != nil || s.n >= 1<<24 {
		return 0, 0, false
	}
	lo, hi := float64(uint8(0)), float64(uint8(255))
	up := func(frac float64) (int, bool) {
		switch {
		case frac == 0.5:
			return 1, true // the crossing lies on the half: Round goes up
		case frac > 0.5+0x1p-20:
			return 1, true
		case frac < 0.5-0x1p-20:
			return 0, true
		}
		return 0, false
	}
	r, okR := up((s.threshold - lo) / (hi - lo))
	f, okF := up((s.threshold - hi) / (lo - hi))
	return f, r, okR && okF
}

// levelCursor is logicAt for positions that mostly move forward (a
// clock's edges read on the data line): on a digital line's edges it
// walks them instead of searching for each position.
type levelCursor struct {
	s *sliced
	k int // the edges at or before the sample last asked for
}

// at is logicAt(c.s, x).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func (c *levelCursor) at(x float64) int {
	s := c.s
	if s.codes != nil {
		return logicAt(s, x)
	}
	return c.atSample(int(math.Round(x)))
}

// atSample is at of a position's nearest sample i.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
func (c *levelCursor) atSample(i int) int {
	s := c.s
	if s.codes != nil {
		return logicAt(s, float64(i))
	}
	if i < 0 || i >= s.n {
		return -1
	}
	// back over the edges past i, then on over those up to it
	for c.k > 0 && s.edges[c.k-1].i > i {
		c.k--
	}
	for c.k < len(s.edges) && s.edges[c.k].i <= i {
		c.k++
	}
	return int(s.first) ^ c.k&1
}
