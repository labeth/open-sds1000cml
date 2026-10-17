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
		// the whole record.
		if res.OK && res.Proto != "" && res.Proto != "off" {
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

// autodetectStrided is AutodetectFast on the record as given.
// TRLC-LINKS: REQ-SDS-018
func autodetectStrided(c1, c2 []uint8, colTimeS float64, format string) (Result, float64) {
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
