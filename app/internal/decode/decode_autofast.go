// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// AutodetectFast is Autodetect on a strided copy of long records: the
// decoders need only a few samples per bit, and on the scope's ARM a full
// Autodetect of a 500 k-sample screen takes 14 s. A record that idles
// most of the time is cut to its bursts first (activeBlocks). It keeps
// about ten samples on the shortest real pulse of either channel and returns
// the result with the sample time it was decoded at (colTimeS × stride).
// TRLC-LINKS: REQ-SDS-018
func AutodetectFast(c1, c2 []uint8, colTimeS float64, format string) (Result, float64) {
	c1, c2 = quietFlat(c1), quietFlat(c2)
	if keep := activeBlocks(c1, c2); keep != nil {
		res, st := autodetectStrided(compact(c1, keep), compact(c2, keep), colTimeS, format)
		// The cut keeps a pulse-width bus's edges but not its long levels (a
		// 1-Wire reset's 480 us low): what it does not name is looked for on
		// the whole record. Nor PWM: an output runs on, with no idle to cut,
		// so PWM read off the cut record is a burst's steady edges (a MIDI
		// message's bits), judged on the whole record (pwmFillsRecord).
		if res.OK && res.Proto != "" && res.Proto != "off" && res.Proto != "pwm" {
			k := int(st/colTimeS + 0.5)
			back := func(i int) int { return uncompact(i*k, keep) / k }
			for j := range res.Spans {
				res.Spans[j].I0, res.Spans[j].I1 = back(res.Spans[j].I0), back(res.Spans[j].I1)
			}
			return res, st
		}
	}
	return autodetectStrided(c1, c2, colTimeS, format)
}

// quietFlat is a line with no swing of its own (rails) as one level: its
// noise spikes, a sample or two a burst, picked by the stride or kept by the
// cut to bursts, read as a level of their own and made a quiet probe beside
// a 1-Wire line the data of an SPI bus.
// TRLC-LINKS: REQ-SDS-018
func quietFlat(c []uint8) []uint8 {
	if c == nil {
		return nil
	}
	lo, hi, why := rails(c)
	if why == "" {
		return c
	}
	out := make([]uint8, len(c))
	v := uint8(max(0, min(255, (lo+hi)/2)))
	for i := range out {
		out[i] = v
	}
	return out
}

// autoEdgeBudget bounds the crossings a channel brings to Autodetect: every
// decoder walks every edge, and a busy line (a sigma-delta source switching
// every few samples, 200 k crossings a 1M record) took the bus search 20 to
// 40 s a look on the scope's ARM, to name no bus (14 s with fifty thousand,
// 3 s with twenty). Twenty thousand crossings hold a thousand or more of any
// bus's units (1250 SPI bytes, 5000 UART characters).
const autoEdgeBudget = 20000

// budgetEnd is the length of the record from its start in which no channel
// has more than budget crossings (shortestPulse's), the whole record when
// none has.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214
func budgetEnd(c1, c2 []uint8, budget int) int {
	n := len(c1)
	for _, c := range [][]uint8{c1, c2} {
		if c != nil && len(c) == len(c1) {
			if e := crossingsEnd(c, budget); e < n {
				n = e
			}
		}
	}
	return n
}

// crossingsEnd is the index at which channel c makes its budget-th crossing
// of its mid level (hysteresis a tenth of its swing, the new level held
// three samples, as shortestPulse), or len(c).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214
func crossingsEnd(c []uint8, budget int) int {
	if len(c) < 64 {
		return len(c)
	}
	// Four tables, summed after (as rails): a run of one code would
	// otherwise make each increment wait for the previous one's store.
	var h4 [4][256]int32
	i := 0
	for ; i+4 <= len(c); i += 4 {
		q := c[i : i+4 : i+4]
		h4[0][q[0]]++
		h4[1][q[1]]++
		h4[2][q[2]]++
		h4[3][q[3]]++
	}
	for _, v := range c[i:] {
		h4[0][v]++
	}
	var hist [256]int
	for v := range hist {
		hist[v] = int(h4[0][v] + h4[1][v] + h4[2][v] + h4[3][v])
	}
	pct := func(p float64) int {
		need, acc := int(p*float64(len(c))), 0
		for v, k := range hist {
			if acc += k; acc > need {
				return v
			}
		}
		return 255
	}
	lo, hi := pct(0.05), pct(0.95)
	if hi-lo < 20 {
		return len(c)
	}
	mid, h := (lo+hi)/2, (hi-lo)/10
	high, crossings := int(c[0]) > mid, 0
	side := func(v uint8, wantHigh bool) bool {
		if wantHigh {
			return int(v) > mid+h
		}
		return int(v) < mid-h
	}
	up, down := mid+h, mid-h // beyond these the line has changed side
	for i, v := range c {
		if high && int(v) >= down || !high && int(v) <= up {
			continue // still on its side
		}
		next := !high
		held := true
		for k := 1; k <= 2 && i+k < len(c); k++ {
			held = held && side(c[i+k], next)
		}
		if !held {
			continue
		}
		high = next
		if crossings++; crossings >= budget {
			return i
		}
	}
	return len(c)
}

// autodetectStrided is AutodetectFast on the record as given, from its start
// to where its busiest channel spends autoEdgeBudget crossings.
// TRLC-LINKS: REQ-SDS-018
func autodetectStrided(c1, c2 []uint8, colTimeS float64, format string) (Result, float64) {
	if n := budgetEnd(c1, c2, autoEdgeBudget); n < len(c1) {
		c1 = c1[:n]
		if c2 != nil {
			c2 = c2[:min(n, len(c2))]
		}
	}
	stride := autoStride(c1, c2)
	if stride <= 1 {
		return Autodetect(c1, c2, colTimeS, format), colTimeS
	}
	pick := func(c []uint8) []uint8 {
		if c == nil {
			return nil
		}
		out := make([]uint8, 0, len(c)/stride+1)
		for i := 0; i < len(c); i += stride {
			out = append(out, c[i])
		}
		return out
	}
	st := colTimeS * float64(stride)
	return Autodetect(pick(c1), pick(c2), st, format), st
}

// activeBlock is the samples a block of activeBlocks spans; activeShare
// the most of a record's blocks that may be busy for it to be cut down.
const (
	activeBlock = 1024
	activeShare = 0.1
)

// activeBlocks finds the bursts of a long record that idles most of the
// time: the blocks of activeBlock samples either line swings across in,
// with two blocks of idle each side. A 62.5 MBd UART sending 5 us a
// millisecond at 2 ns holds its low level on a tenth of a percent of the
// samples, under the decoders' histogram noise floor (no active signal).
// It returns the kept blocks' starts, nil to keep the whole record.
// TRLC-LINKS: REQ-SDS-018
func activeBlocks(c1, c2 []uint8) []int {
	n := len(c1)
	if n < 32*activeBlock || (c2 != nil && len(c2) != n) {
		return nil
	}
	nb := n / activeBlock
	busy := make([]bool, nb)
	for _, c := range [][]uint8{c1, c2} {
		if c == nil {
			continue
		}
		lo, hi := make([]uint8, nb), make([]uint8, nb)
		gLo, gHi := uint8(255), uint8(0)
		for b := range nb {
			l, h := uint8(255), uint8(0)
			for _, v := range c[b*activeBlock : (b+1)*activeBlock] {
				l, h = min(l, v), max(h, v)
			}
			lo[b], hi[b] = l, h
			gLo, gHi = min(gLo, l), max(gHi, h)
		}
		swing := int(gHi) - int(gLo)
		if swing < 20 {
			continue
		}
		for b := range nb {
			if 2*(int(hi[b])-int(lo[b])) >= swing {
				busy[b] = true
			}
		}
	}
	nBusy := 0
	for _, b := range busy {
		if b {
			nBusy++
		}
	}
	if nBusy == 0 || float64(nBusy) > activeShare*float64(nb) {
		return nil
	}
	var keep []int
	for b := range nb {
		near := false
		for d := -2; d <= 2; d++ {
			near = near || (b+d >= 0 && b+d < nb && busy[b+d])
		}
		if near {
			keep = append(keep, b*activeBlock)
		}
	}
	return keep
}

// compact is the record's kept blocks (activeBlocks) end to end.
// TRLC-LINKS: REQ-SDS-018
func compact(c []uint8, keep []int) []uint8 {
	if c == nil {
		return nil
	}
	out := make([]uint8, 0, len(keep)*activeBlock)
	for _, b := range keep {
		out = append(out, c[b:b+activeBlock]...)
	}
	return out
}

// uncompact maps a sample index of a compacted record back to the record's.
// TRLC-LINKS: REQ-SDS-018
func uncompact(i int, keep []int) int {
	b := min(max(i/activeBlock, 0), len(keep)-1)
	return keep[b] + i - b*activeBlock
}

// autoStride is how many samples AutodetectFast may skip: a tenth of the
// shortest pulse on either channel, ignoring glitches of two samples or less.
// TRLC-LINKS: REQ-SDS-018
func autoStride(c1, c2 []uint8) int {
	minRun := 0
	for _, c := range [][]uint8{c1, c2} {
		if r := shortestPulse(c); r > 0 && (minRun == 0 || r < minRun) {
			minRun = r
		}
	}
	return max(1, minRun/10)
}

// shortestPulse is the shortest run between threshold crossings of a
// two-level signal (hysteresis a tenth of its swing, crossings held three
// samples), or 0 when the channel has no swing or too few crossings.
// TRLC-LINKS: REQ-SDS-018
func shortestPulse(c []uint8) int {
	if len(c) < 64 {
		return 0
	}
	var hist [256]int
	for _, v := range c {
		hist[v]++
	}
	pct := func(p float64) int {
		need, acc := int(p*float64(len(c))), 0
		for v, n := range hist {
			if acc += n; acc > need {
				return v
			}
		}
		return 255
	}
	lo, hi := pct(0.05), pct(0.95)
	if hi-lo < 20 {
		return 0
	}
	mid, h := (lo+hi)/2, (hi-lo)/10
	high := int(c[0]) > mid
	last, shortest, crossings := -1, 0, 0
	side := func(v uint8, wantHigh bool) bool {
		if wantHigh {
			return int(v) > mid+h
		}
		return int(v) < mid-h
	}
	for i, v := range c {
		next := !high
		if !side(v, next) {
			continue
		}
		// A crossing counts only when the new level holds for three samples:
		// a one- or two-sample glitch must not split a pulse.
		held := true
		for k := 1; k <= 2 && i+k < len(c); k++ {
			held = held && side(c[i+k], next)
		}
		if !held {
			continue
		}
		high = next
		crossings++
		if last >= 0 {
			if run := i - last; shortest == 0 || run < shortest {
				shortest = run
			}
		}
		last = i
	}
	if crossings < 3 {
		return 0
	}
	return shortest
}
