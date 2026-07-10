package engine

// Software edge discrimination and centring (spec 03 §7, spec 05 §3).
// There are no hardware slope/type registers: the comparator only places the
// threshold; slope and position are judged over the drained record. Slope is
// judged in code space with exactly these predicates.

// ptp returns min, max and peak-to-peak of sig.
func ptp(sig []uint8) (lo, hi, p int) {
	if len(sig) == 0 {
		return 0, 0, 0
	}
	lo, hi = int(sig[0]), int(sig[0])
	for _, v := range sig {
		if int(v) < lo {
			lo = int(v)
		}
		if int(v) > hi {
			hi = int(v)
		}
	}
	return lo, hi, hi - lo
}

// validDepth estimates how many leading samples of a drained record carry
// real signal before it decays into a flat "dead tail" (the ports read a
// repeated last sample / zeros beyond what the FPGA actually captured). It
// scans fixed windows and returns the end of the last window whose local
// peak-to-peak still shows activity. Used to size the decimated drain safely:
// draining past validDepth would centre the display on dead samples.
func validDepth(sig []uint8) int {
	_, _, p := ptp(sig)
	return validDepthP(sig, p)
}

// validDepthP is validDepth with the record's peak-to-peak span precomputed, so
// a caller that already scanned the record (oneFrame does exactly one ptp pass
// per frame) doesn't pay a second O(n) pass here. Same arithmetic as validDepth.
func validDepthP(sig []uint8, p int) int {
	n := len(sig)
	if n == 0 {
		return 0
	}
	if p < 8 {
		return n // essentially flat everywhere — nothing to trim
	}
	const w = 128
	thr := p / 8
	last := 0
	for from := 0; from < n; from += w {
		to := from + w
		if to > n {
			to = n
		}
		if _, _, lp := ptp(sig[from:to]); lp >= thr {
			last = to
		}
	}
	return last
}

// realDepth is the count of leading samples the FPGA actually captured, before
// the native-fast DEAD TAIL. When the HW freezes a half record, DrainInto reads
// past the captured samples by cycling its 5 stream ports (0x30-0x34), so once
// the FIFO is dry each port returns a frozen value and the drain emits an EXACT
// period-5 repeat (e.g. 185,171,159,153,155,...). validDepth cannot see this —
// the repeat toggles, so its window peak-to-peak reads as live activity, which is
// exactly why a half record (realDepth ≈ cols/2) sailed through the old
// valid_depth re-capture gate and got published broken. Here we detect it head
// on: scan from the end while sig[i]==sig[i-5]; that contiguous run IS the dead
// tail. A flat record (no signal) has no tail to trim — return full.
func realDepth(sig []uint8) int {
	_, _, p := ptp(sig)
	return realDepthP(sig, p)
}

// realDepthP is realDepth with the peak-to-peak span precomputed (see
// validDepthP) — one shared ptp pass per frame instead of one per caller.
func realDepthP(sig []uint8, p int) int {
	n := len(sig)
	if n < 6 {
		return n
	}
	if p < 8 {
		return n // flat / quiet screen: a legitimate display, not a half record
	}
	run := 0
	for i := n - 1; i >= 5; i-- {
		if sig[i] == sig[i-5] {
			run++
		} else {
			break
		}
	}
	return n - run
}

// midLevel is the crossing threshold: (min+max)/2 over the drained samples
// (128 for an empty slice). It floats with amplitude so it works at any V/div.
func midLevel(sig []uint8) int {
	if len(sig) == 0 {
		return 128
	}
	lo, hi, _ := ptp(sig)
	return (lo + hi) / 2
}

// centerCross finds the qualifying mid-level crossing nearest the frame
// centre (phase-stable) and returns its sub-sample position, or -1 if none.
// rising: sig[c-1] < lvl && sig[c] >= lvl; falling mirrored.
func centerCross(sig []uint8, lvl int, rising bool) float64 {
	n := len(sig)
	best, bestDist := -1, n+1
	for c := 1; c < n; c++ {
		a, b := int(sig[c-1]), int(sig[c])
		var q bool
		if rising {
			q = a < lvl && b >= lvl
		} else {
			q = a > lvl && b <= lvl
		}
		if !q {
			continue
		}
		d := c - n/2
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			best, bestDist = c, d
		}
	}
	if best < 0 {
		return -1
	}
	a, b := int(sig[best-1]), int(sig[best])
	frac := 0.0
	if b != a {
		frac = float64(lvl-a) / float64(b-a)
		if frac < 0 || frac >= 1 {
			frac = 0
		}
	}
	return float64(best-1) + frac
}

// windowSlopeMatches validates that the anchored crossing really is the
// requested slope by comparing the plateaus immediately adjacent to the
// crossing — never the outer window edges (outer-eighth comparison
// false-rejects every correctly-centred edge in a multi-period window).
// Returns true (never veto) when the window is too small to judge.
func windowSlopeMatches(sig []uint8, xc float64, winCols int, rising bool) bool {
	n := len(sig)
	if n < 8 || winCols < 8 {
		return true
	}
	c := int(xc)
	skip := winCols / 16
	if skip < 1 {
		skip = 1
	}
	span := winCols / 4
	mean := func(from, to int) (float64, bool) {
		if from < 0 {
			from = 0
		}
		if to > n {
			to = n
		}
		if from >= to {
			return 0, false
		}
		s := 0
		for _, v := range sig[from:to] {
			s += int(v)
		}
		return float64(s) / float64(to-from), true
	}
	left, lok := mean(c-span, c-skip)
	right, rok := mean(c+skip, c+span)
	if !lok || !rok {
		return true
	}
	lo, hi, _ := ptp(sig)
	margin := float64(hi-lo) / 8
	if rising {
		return right-left >= -margin
	}
	return left-right >= -margin
}
