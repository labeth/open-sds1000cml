// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

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
// TRLC-LINKS: REQ-SDS-009, REQ-SDS-128
func realDepth(sig []uint8) int {
	_, _, p := ptp(sig)
	return realDepthP(sig, p)
}

// windowSlopeMatches validates that the anchored crossing really is the
// requested slope by comparing the plateaus immediately adjacent to the
// crossing — never the outer window edges (outer-eighth comparison
// false-rejects every correctly-centred edge in a multi-period window).
// Returns true (never veto) when the window is too small to judge.
// TRLC-LINKS: REQ-SDS-011
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
