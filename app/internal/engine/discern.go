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
//
// The tail match is TOLERANT, not exact: the frozen stream-port values that a
// dead tail repeats can carry ±1-2 LSB of read noise, and an exact
// sig[i]==sig[i-5] comparison breaks on the first such sample — which let a
// noisy half-record sail through as "full" (the stuck-FPGA state published
// coherent:true for hours on the bench). A period-5 sample matches within
// ±realDepthTol codes, and up to realDepthMiss consecutive misses are forgiven
// (sparse glitches inside the dead tail); a longer miss streak is live signal.
func realDepthP(sig []uint8, p int) int {
	n := len(sig)
	if n < 6 {
		return n
	}
	if p < 8 {
		return n // flat / quiet screen: a legitimate display, not a half record
	}
	const (
		realDepthTol  = 2 // |Δ| per period-5 pair still "frozen"
		realDepthMiss = 3 // forgiven consecutive misses inside the tail
	)
	run, miss := 0, 0
	for i := n - 1; i >= 5; i-- {
		d := int(sig[i]) - int(sig[i-5])
		if d < 0 {
			d = -d
		}
		if d <= realDepthTol {
			run++
			miss = 0
			continue
		}
		if miss++; miss > realDepthMiss {
			break
		}
		run++ // forgiven glitch: still inside the dead tail
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

// centerCross finds the qualifying level crossing nearest the frame centre
// (phase-stable) and returns its sub-sample position, or -1 if none. rising:
// sig[c-1] < lvl && sig[c] >= lvl; falling mirrored.
//
// It only accepts a CONFIRMED crossing — one where the trace clearly sits on
// the low side just before and the high side just after (adaptive hysteresis,
// scaled to the signal). This rejects noise wiggles and near-tangent crossings
// where the level grazes a peak/trough: those produce clustered rising+falling
// crossings that make the anchor flip frame-to-frame (the display "jitter").
// A crossing must be CONFIRMED to anchor. If none is (the level grazes a peak,
// or the only "crossings" near the centre are single-sample noise blips in an
// otherwise falling region), it returns -1 = no lock, so NORM holds the last
// good frame and AUTO free-runs — far better than anchoring a noise blip, which
// centres a falling stretch under a rising trigger and makes the display flip.
//
// hint (a previous frame's edge index, or <0 for none) provides PHASE
// CONTINUITY: among confirmed crossings it prefers the one nearest the hint
// rather than nearest the record centre, so the anchor sticks to the same
// physical trigger event across frames instead of hopping to an adjacent
// same-slope crossing when the capture phase drifts — the residual display
// jitter on a multi-period record.
func centerCross(sig []uint8, lvl int, rising bool) float64 {
	return centerCrossHint(sig, lvl, rising, -1)
}

func centerCrossHint(sig []uint8, lvl int, rising bool, hint float64) float64 {
	n := len(sig)
	if n < 2 {
		return -1
	}
	ref := n / 2
	if hint >= 0 && hint < float64(n) {
		ref = int(hint)
	}
	// A CONFIRMED crossing is one the trace genuinely transits and stays across:
	// a majority of the window before sits on the low side and a majority after
	// on the high side (for rising). This accepts both steep edges and gentle
	// ramps, but rejects single-sample noise blips (which immediately cross
	// back, so the "after" side is NOT mostly high). The window scales with the
	// record but is bounded so it stays a small fraction of a period.
	const w = 8 // local confirmation window (samples each side)
	confirmed := func(c int) bool {
		beforeOK, nb := 0, 0
		for i := c - 1; i >= 0 && i >= c-w; i-- {
			nb++
			s := int(sig[i])
			if (rising && s < lvl) || (!rising && s > lvl) {
				beforeOK++ // was on the near side
			}
		}
		afterOK, na := 0, 0
		for j := c; j < n && j < c+w; j++ {
			na++
			s := int(sig[j])
			if (rising && s >= lvl) || (!rising && s <= lvl) {
				afterOK++ // stays on the far side
			}
		}
		// Genuine crossing: mostly near side before, mostly far side after. A
		// single-sample noise blip crosses back immediately, so the "after"
		// majority fails. 60% tolerates ADC noise on a real edge.
		return nb > 0 && na > 0 && beforeOK*5 >= nb*3 && afterOK*5 >= na*3
	}
	best, bestDist := -1, n+1
	for c := 1; c < n; c++ {
		a, b := int(sig[c-1]), int(sig[c])
		var q bool
		if rising {
			q = a < lvl && b >= lvl
		} else {
			q = a > lvl && b <= lvl
		}
		if !q || !confirmed(c) {
			continue
		}
		d := c - ref
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			best, bestDist = c, d
		}
	}
	if best < 0 {
		return -1 // no confirmed crossing → no lock (hold/free-run), never anchor noise
	}
	a, b := int(sig[best-1]), int(sig[best])
	frac := 0.0
	if b != a {
		frac = float64(lvl-a) / float64(b-a)
		if frac < 0 {
			frac = 0
		} else if frac > 1 {
			frac = 1 // clamp (old code snapped >=1 to 0, a ~1-sample jump)
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
