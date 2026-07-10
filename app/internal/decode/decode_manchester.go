package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// ManchesterCfg configures the Manchester decode of one channel.
//
//	Bitrate   0 => auto-infer the bit period from edge statistics; else bits/s.
//	IEEE      true  => IEEE 802.3: a rising mid-cell transition = 1, falling = 0.
//	          false => Thomas / G.E. convention: rising = 0, falling = 1.
//	MSB       true  => most-significant bit first when packing cells into bytes.
//	Bits      bits per word (default 8).
//	Format    byte render format for FmtByte (hex/dec/bin/ascii/both); "" = hex.
//	Threshold /HaveThr override the auto slice threshold (see sliceChannel).
type ManchesterCfg struct {
	Bitrate   int
	IEEE      bool
	MSB       bool
	Bits      int
	Format    string
	Threshold float64
	HaveThr   bool
}

// mCell is one recovered Manchester bit cell spanning sample indices [I0,I1].
// Bit is 0/1, or -1 for a coding violation (the cell had no clean mid-cell
// transition — the two half-cells sampled to the same level).
type mCell struct {
	I0, I1 int
	Bit    int
}

// recoverManchester samples the mid-cell transition DIRECTION at each bit cell,
// laying cells down at s0, s0+T, s0+2T, ... (T = samples per bit). It is the
// reusable bit-recovery core shared with MIL-STD-1553B: it makes no assumption
// about framing, only that every valid cell carries a mid-cell transition whose
// direction encodes the bit. `ieee` selects the direction->bit mapping (true =
// rising@mid is 1). Cells are emitted until the sampler runs past the last edge
// (trailing idle), so the stream ends where the signal does. Returns the cells
// plus good/violation counts so a caller can score competing phase hypotheses.
func recoverManchester(S sliced, s0, T float64, ieee bool, lastEdgeX float64) (cells []mCell, good, viol int) {
	if !(T >= 2) || S.n == 0 {
		return
	}
	limit := lastEdgeX + 0.25*T         // stop once cells fall into trailing idle
	maxCells := int(float64(S.n)/T) + 4 // safety cap; the limit/gap breaks normally
	consecViol := 0                     // a valid cell always has a mid transition, so a
	//                                     run of missing ones is an inter-frame IDLE gap:
	//                                     stop there (real captures hold several frames whose
	//                                     gaps are non-integer T apart, so one phase cannot
	//                                     align them all — decode the first frame cleanly).
	for k := 0; k < maxCells; k++ {
		centre := s0 + (float64(k)+0.5)*T
		if centre > limit {
			break
		}
		// Sample the first and third quarter of the cell: they straddle the
		// mid-cell transition, so their levels give its direction.
		l1 := logicAt(S, s0+(float64(k)+0.25)*T)
		l2 := logicAt(S, s0+(float64(k)+0.75)*T)
		if l1 < 0 || l2 < 0 { // ran off the captured region
			break
		}
		i0 := int(math.Round(s0 + float64(k)*T))
		i1 := int(math.Round(s0+float64(k+1)*T)) - 1
		if i0 < 0 {
			i0 = 0
		}
		if i1 >= S.n {
			i1 = S.n - 1
		}
		if i1 < i0 {
			i1 = i0
		}
		if l1 == l2 { // no mid transition
			if consecViol++; consecViol >= 2 { // idle run => frame boundary: drop the
				if n := len(cells); n > 0 && cells[n-1].Bit < 0 { // lone idle cell we just added
					cells = cells[:n-1]
					viol--
				}
				break
			}
			cells = append(cells, mCell{i0, i1, -1}) // isolated violation: keep as an error mark
			viol++
			continue
		}
		consecViol = 0
		rising := l2 > l1
		bit := 0
		if rising == ieee { // IEEE: rising@mid = 1; Thomas: rising@mid = 0
			bit = 1
		}
		cells = append(cells, mCell{i0, i1, bit})
		good++
	}
	return
}

// DecodeManchester decodes Manchester-encoded data on one channel's codes.
// Mirrors decode_manchester.js step for step so LCD and web agree byte-for-byte.
func DecodeManchester(codes []uint8, colTimeS float64, cfg ManchesterCfg) Result {
	bits := cfg.Bits
	if bits == 0 {
		bits = 8
	}
	if bits < 1 || bits > 16 { // bound the shift/mask math
		return Result{Proto: "manchester", Error: "data bits out of range (1..16)"}
	}
	const minSPB = 4.0
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "manchester", Error: S.reason}
	}
	if len(S.edges) < 2 {
		return Result{Proto: "manchester", Error: "too few edges"}
	}

	// Bit period T in samples. cfg.Bitrate pins it; otherwise infer it from the edge
	// gaps: consecutive edges are T/2 apart when adjacent bits are EQUAL (a cell
	// boundary then a mid-cell transition) or T apart when they DIFFER (mid to mid).
	// Any real Manchester frame — a preamble followed by varied data — therefore has
	// its SHORTEST gaps at T/2, so T = 2·(shortest-gap cluster). We deliberately do
	// NOT try the half period T/2 as an alternative: for a fragmented/degraded
	// signal that the true period can't frame, a T/2 hypothesis cherry-picks the
	// alternating runs into confident all-0x55/0xAA GARBAGE — a false positive. If
	// the true period can't frame, we report no-decode. (Pinning cfg.Bitrate on a
	// pathological pure-alternating-only capture overrides this if ever needed.)
	var T float64
	if cfg.Bitrate > 0 {
		T = (1.0 / float64(cfg.Bitrate)) / colTimeS
	} else {
		var gaps []float64
		for k := 1; k < len(S.edges); k++ {
			// sub-sample crossings: at small T the integer index quantizes a T/2 gap
			// of 2.5 down to 2, which would mis-scale the inferred period low.
			if g := S.edges[k].x - S.edges[k-1].x; g >= 1 {
				gaps = append(gaps, g)
			}
		}
		if len(gaps) < 3 {
			return Result{Proto: "manchester", Error: "too few edges / cannot infer bitrate"}
		}
		sort.Float64s(gaps)
		hp := gaps[int(float64(len(gaps))*0.1)]
		// Average the shortest-gap cluster with a ±0.5 window (not ±0.35): at small
		// periods the half-gap quantizes across two integers (T/2=2.5 -> {2,3}), and
		// a tighter window would keep only the 2s and bias it low. The next cluster
		// (T) sits a full factor of 2 away, so ±0.5 never bleeds in.
		sum, cnt := 0.0, 0
		for _, g := range gaps {
			if math.Abs(g-hp) <= 0.5*hp {
				sum += g
				cnt++
			}
		}
		if cnt > 0 {
			hp = sum / float64(cnt)
		}
		T = 2 * hp
	}
	if math.IsInf(T, 0) || math.IsNaN(T) || !(T >= minSPB) {
		return Result{Proto: "manchester", Error: fmt.Sprintf("%.1f samples/bit; need >= %g", T, minSPB)}
	}
	res, _, _ := decodeManchesterAt(S, T, cfg, bits, colTimeS)
	return res
}

// decodeManchesterAt segments the edges into frames and decodes each at bit
// period T, returning the Result plus (frames, total good cells) so the caller
// can score competing T hypotheses. Split logic + per-frame phase lock as before.
func decodeManchesterAt(S sliced, T float64, cfg ManchesterCfg, bits int, colTimeS float64) (Result, int, int) {
	// Segment the edges into FRAMES: a captured record holds several frames
	// separated by idle gaps, and a free-running scope starts at a random phase,
	// so the leading frame is usually partial. Split where consecutive edges are
	// more than 1.5·T apart (an inter-frame idle), then decode each frame on its
	// OWN phase lock — one global phase cannot align frames whose gaps are a
	// non-integer number of bit periods.
	var segs [][2]int // inclusive edge-index ranges
	segStart := 0
	for k := 1; k < len(S.edges); k++ {
		// Split on an inter-frame idle. Use a 2.5·T threshold, not 1.5·T: a single
		// flattened cell (a coding violation) deletes one mid transition, opening a
		// ~2·T gap. At 1.5·T that gap split the frame, orphaning the corrupted tail
		// as a dropped partial — so the violation went unreported. Keeping it in the
		// frame lets recoverManchester see l1==l2 and emit a frame-error cell. A real
		// inter-frame idle is many bit-times, comfortably above 2.5·T.
		if float64(S.edges[k].i-S.edges[k-1].i) > 2.5*T {
			segs = append(segs, [2]int{segStart, k - 1})
			segStart = k
		}
	}
	segs = append(segs, [2]int{segStart, len(S.edges) - 1})

	var spans []Span
	var bytesOut []int
	var toks []string
	frames, totalGood := 0, 0
	for sgIdx, sg := range segs {
		if sg[1] <= sg[0] { // a lone edge carries no cell
			continue
		}
		// Phase lock this frame: the segment's first edge is a cell boundary or a
		// mid-cell transition (cell began half a period earlier). Try both, keep
		// the phase with more clean mid transitions and fewer coding violations.
		s0e, lastE := S.edges[sg[0]].x, S.edges[sg[1]].x
		// Phase-lock: the segment's first edge is a cell boundary (phase A, s0=s0e)
		// or a mid-cell transition (phase B, boundary half a period earlier). Score
		// each on clean cells minus violations and take the winner.
		cA, goodA, violA := recoverManchester(S, s0e, T, cfg.IEEE, lastE)
		cB, goodB, violB := recoverManchester(S, s0e-0.5*T, T, cfg.IEEE, lastE)
		scA, scB := goodA-4*violA, goodB-4*violB
		var cells []mCell
		var bestGood int
		bestScore := scA
		if scB > bestScore {
			bestScore = scB
		}
		switch {
		case scA > scB:
			cells, bestGood = cA, goodA
		case scB > scA:
			cells, bestGood = cB, goodB
		default:
			// A bare constant-bit frame is a pure square wave: both phases decode
			// cleanly but into complementary bits — an inherent ±half-cell ambiguity
			// that a preamble would resolve. Resolve it by idle SYMMETRY: the bit
			// whose leading half-cell equals the idle level leaves an extra ~half-
			// period of idle-coloured signal on the LEAD side (its first edge is a
			// mid, not a boundary), so the leading idle run outlasts the trailing one
			// — pick phase B there; otherwise the first edge is a true boundary (A).
			leadRun := s0e
			if sg[0] > 0 {
				leadRun = s0e - S.edges[sg[0]-1].x
			}
			trailRun := float64(S.n) - lastE
			if sg[1] < len(S.edges)-1 {
				trailRun = S.edges[sg[1]+1].x - lastE
			}
			if leadRun > trailRun {
				cells, bestGood = cB, goodB
			} else {
				cells, bestGood = cA, goodA
			}
		}
		if len(cells) == 0 || bestScore <= 0 || bestGood < bits {
			continue // need at least one whole byte of clean cells
		}
		// Leading alternating run = preamble. The FIRST/LAST segment of a free-
		// running capture may be a frame truncated by the record edge (it starts or
		// ends mid-data); require a preamble there to drop that partial. Interior
		// segments are whole frames bounded by idle gaps on both sides, so accept
		// them as-is (and a lone single frame — the synthetic test case — too).
		run := 0
		if cells[0].Bit >= 0 {
			run = 1
			for i := 1; i < len(cells) && cells[i].Bit >= 0 && cells[i].Bit != cells[i-1].Bit; i++ {
				run++
			}
		}
		atEdge := len(segs) > 1 && (sgIdx == 0 || sgIdx == len(segs)-1)
		if run < 3 && atEdge {
			continue
		}
		if frames > 0 { // separate frames in the transcript
			spans = append(spans, Span{cells[0].I0, cells[0].I0, "", "gap", 0})
			toks = append(toks, "|")
		}
		frames++
		totalGood += bestGood
		// run==0 (an interior frame whose FIRST cell is a coding violation) has no
		// preamble to span — anchor the SYNC marker on the first cell instead of
		// indexing cells[-1] (this exact shape panicked on a real SENT capture fed
		// through autodetect's Manchester hypothesis).
		runEnd := cells[0].I0
		if run > 0 {
			runEnd = cells[run-1].I1
		}
		spans = append(spans, Span{cells[0].I0, runEnd, "SYNC", "start", 0})
		// Pack this frame's cells into words; a coding violation flushes the word.
		curVal, curBits, byteStart, lastI1 := 0, 0, 0, 0
		for _, c := range cells {
			if c.Bit < 0 {
				spans = append(spans, Span{c.I0, c.I1, "!", "frame-error", 0})
				curVal, curBits = 0, 0
				continue
			}
			if curBits == 0 {
				byteStart, curVal = c.I0, 0
			}
			lastI1 = c.I1
			if cfg.MSB {
				curVal = (curVal << 1) | c.Bit
			} else {
				curVal |= c.Bit << curBits
			}
			if curBits++; curBits == bits {
				spans = append(spans, Span{byteStart, c.I1, FmtByte(curVal, cfg.Format), "data", curVal})
				toks = append(toks, FmtByte(curVal, cfg.Format))
				bytesOut = append(bytesOut, curVal)
				curVal, curBits = 0, 0
			}
		}
		// A dangling partial word means the good-cell count wasn't a whole number of
		// bytes: the tail was eaten by a coding violation at the very end of the frame
		// (the erased mid-cell transition also erased the last edge, so the violated
		// cell fell just outside recoverManchester's window and was never marked).
		// Flag the stub instead of silently dropping it — it is the corruption signal.
		if curBits > 0 {
			spans = append(spans, Span{byteStart, lastI1, "!", "frame-error", 0})
			toks = append(toks, "!")
		}
	}
	if frames == 0 {
		return Result{Proto: "manchester", Error: "no Manchester frame (preamble) found"}, 0, 0
	}

	baud := cfg.Bitrate
	if colTimeS > 0 {
		baud = int(math.Round(1.0 / (T * colTimeS)))
	}
	return Result{OK: true, Proto: "manchester", Spans: spans, Text: strings.Join(toks, " "),
		Bytes: bytesOut, Baud: baud, SPB: T, Thr: S.threshold}, frames, totalGood
}
