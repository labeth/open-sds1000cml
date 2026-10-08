// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// AutodetectFast is Autodetect on a strided copy of long records: the
// decoders need only a few samples per bit, and on the scope's ARM a full
// Autodetect of a 500 k-sample screen took 14 s (bench 2026-10-06). It keeps
// about ten samples on the shortest real pulse of either channel and returns
// the result with the sample time it was decoded at (colTimeS × stride).
// TRLC-LINKS: REQ-SDS-018
func AutodetectFast(c1, c2 []uint8, colTimeS float64, format string) (Result, float64) {
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
