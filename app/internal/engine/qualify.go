package engine

// Trigger qualifiers (spec 05, spec 03 §7): PULSE (GLIT), SLOPE (SLEW) and
// VIDEO (TV) are pure software discrimination over the drained record — they
// REPLACE the EDGE pipeline (centerCross + windowSlopeMatches), never
// augment it. No hardware trigger state changes; the qualifier's anchor is
// the published EdgeX. All level thresholds are fractions of the frame's own
// min/max span (band- and V/div-independent).

// TrigType selects the discrimination pipeline.
type TrigType int

const (
	TrigEdge  TrigType = 0
	TrigPulse TrigType = 1 // GLIT
	TrigSlope TrigType = 2 // SLEW
	TrigVideo TrigType = 3 // TV
)

// Condition semantics for width/time windows: any=0, less=1, greater=2,
// inside=3. Literal: with min=max=0, greater passes everything > 0 and less
// passes nothing.
const (
	CondAny     = 0
	CondLess    = 1
	CondGreater = 2
	CondInside  = 3
)

// trigParams is the staged qualifier parameter set (command mutex).
type trigParams struct {
	typ TrigType

	pulseLvlFrac float64
	pulseWMinNs  float64
	pulseWMaxNs  float64
	pulseCond    int

	slopeLoFrac float64
	slopeHiFrac float64
	slopeTMinNs float64
	slopeTMaxNs float64
	slopeCond   int

	videoStd  int // 0=PAL (≤625 lines), 1=NTSC (≤525)
	videoLine int // 0 = any line, else 1-based line N
	videoNeg  bool
}

func defaultTrigParams() trigParams {
	return trigParams{
		typ:          TrigEdge,
		pulseLvlFrac: 0.5, pulseCond: CondAny,
		slopeLoFrac: 0.2, slopeHiFrac: 0.8, slopeCond: CondAny,
		videoStd: 0, videoLine: 0, videoNeg: true,
	}
}

func condOK(m, min, max float64, cond int) bool {
	switch cond {
	case CondLess:
		return m < min
	case CondGreater:
		return m > max
	case CondInside:
		return min <= m && m <= max
	default:
		return true
	}
}

// flatReject is the qualifier preamble's no-event gate: a span under 40
// codes is a rail — never fabricate an event from noise crossings.
const flatRejectSpan = 40

// crossFrac interpolates the sub-sample position of a crossing at index c.
func crossFrac(disc []uint8, c, lvl int) float64 {
	a, b := int(disc[c-1]), int(disc[c])
	frac := 0.0
	if b != a {
		frac = float64(lvl-a) / float64(b-a)
		if frac < 0 || frac >= 1 {
			frac = 0
		}
	}
	return float64(c-1) + frac
}

// qualifyPulse (spec 05: GLIT). rising=true = high pulse (region above the
// level between a rising entry and the next falling exit); false mirrored.
// The anchor is the COMPLETING edge of the qualifying pulse nearest the
// frame centre — only when the pulse completes is its width known.
func qualifyPulse(disc []uint8, intervalNs float64, p trigParams, rising bool) float64 {
	n := len(disc)
	mn, mx, span := ptp(disc)
	if span < flatRejectSpan {
		return -1
	}
	lvl := mn + int(p.pulseLvlFrac*float64(span))
	_ = mx

	bestX, bestDist := -1.0, n+1
	enter := -1
	for c := 1; c < n; c++ {
		a, b := int(disc[c-1]), int(disc[c])
		var entering, exiting bool
		if rising { // high pulse: enter rising, exit falling
			entering = a < lvl && b >= lvl
			exiting = a > lvl && b <= lvl
		} else { // low pulse: enter falling, exit rising
			entering = a > lvl && b <= lvl
			exiting = a < lvl && b >= lvl
		}
		if entering {
			enter = c
			continue
		}
		if exiting && enter >= 0 {
			widthNs := float64(c-enter) * intervalNs
			if condOK(widthNs, p.pulseWMinNs, p.pulseWMaxNs, p.pulseCond) {
				d := c - n/2
				if d < 0 {
					d = -d
				}
				if d < bestDist {
					bestDist = d
					bestX = crossFrac(disc, c, lvl)
				}
			}
			enter = -1
		}
	}
	return bestX
}

// qualifySlope (spec 05: SLEW): a monotone lo→hi (rising) or hi→lo
// (falling) traversal whose time qualifies. Anchor = the second-threshold
// crossing nearest the frame centre.
func qualifySlope(disc []uint8, intervalNs float64, p trigParams, rising bool) float64 {
	n := len(disc)
	mn, _, span := ptp(disc)
	if span < flatRejectSpan {
		return -1
	}
	lo := mn + int(p.slopeLoFrac*float64(span))
	hi := mn + int(p.slopeHiFrac*float64(span))
	bail := span / 10

	first, second := lo, hi // rising: up through lo, then up through hi
	if !rising {
		first, second = hi, lo
	}

	bestX, bestDist := -1.0, n+1
	for c := 1; c < n; c++ {
		a, b := int(disc[c-1]), int(disc[c])
		var firstCross bool
		if rising {
			firstCross = a < first && b >= first
		} else {
			firstCross = a > first && b <= first
		}
		if !firstCross {
			continue
		}
		// Walk toward the second threshold; bail if the level reverses back
		// past the first threshold by more than span/10 (not monotone). Start
		// at k=c so a single sample step that spans BOTH thresholds (a fast
		// edge at any decimated band) is detected as a zero-time traversal at
		// index c — otherwise disc[k-1] is already past `second` for the whole
		// plateau and the crossing is never found.
		for k := c; k < n; k++ {
			v := int(disc[k])
			if rising {
				if v < first-bail {
					break
				}
				if int(disc[k-1]) < second && v >= second {
					tNs := float64(k-c) * intervalNs
					if condOK(tNs, p.slopeTMinNs, p.slopeTMaxNs, p.slopeCond) {
						d := k - n/2
						if d < 0 {
							d = -d
						}
						if d < bestDist {
							bestDist = d
							bestX = crossFrac(disc, k, second)
						}
					}
					break
				}
			} else {
				if v > first+bail {
					break
				}
				if int(disc[k-1]) > second && v <= second {
					tNs := float64(k-c) * intervalNs
					if condOK(tNs, p.slopeTMinNs, p.slopeTMaxNs, p.slopeCond) {
						d := k - n/2
						if d < 0 {
							d = -d
						}
						if d < bestDist {
							bestDist = d
							bestX = crossFrac(disc, k, second)
						}
					}
					break
				}
			}
		}
	}
	return bestX
}

// qualifyVideo (spec 05: TV): sync-separate at 30% up from the sync tip,
// collect crossings INTO the sync region as line boundaries, and anchor on
// the selected line's sync edge. Only all-lines (line=0) and line-N exist;
// odd/even field discrimination is NOT implementable here (needs a full
// video frame in the record) and must never silently mis-trigger.
func qualifyVideo(disc []uint8, p trigParams) float64 {
	n := len(disc)
	mn, mx, span := ptp(disc)
	if span < flatRejectSpan {
		return -1
	}
	var syncLvl int
	if p.videoNeg {
		syncLvl = mn + int(0.30*float64(span))
	} else {
		syncLvl = mx - int(0.30*float64(span))
	}

	maxLine := 625 // PAL
	if p.videoStd == 1 {
		maxLine = 525 // NTSC
	}
	line := p.videoLine
	if line > maxLine {
		line = maxLine
	}

	count := 0
	bestX, bestDist := -1.0, n+1
	for c := 1; c < n; c++ {
		a, b := int(disc[c-1]), int(disc[c])
		var syncEdge bool
		if p.videoNeg {
			syncEdge = a > syncLvl && b <= syncLvl // falling into sync
		} else {
			syncEdge = a < syncLvl && b >= syncLvl // rising into sync
		}
		if !syncEdge {
			continue
		}
		count++
		if line == 0 {
			d := c - n/2
			if d < 0 {
				d = -d
			}
			if d < bestDist {
				bestDist = d
				bestX = crossFrac(disc, c, syncLvl)
			}
		} else if count == line {
			return crossFrac(disc, c, syncLvl)
		}
	}
	if line != 0 {
		return -1 // fewer than N sync edges in the record
	}
	return bestX
}
