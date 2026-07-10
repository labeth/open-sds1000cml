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

	// Bit period T in samples. cfg.Bitrate pins it; otherwise infer it from the
	// edge gaps: consecutive edges are T/2 apart (a cell boundary followed by a
	// mid-cell transition) or T apart (mid to mid), so the shortest gaps cluster
	// at the half-period. Take a low percentile (robust to one stray short gap)
	// then refine on that cluster; T is twice it.
	var T float64
	if cfg.Bitrate > 0 {
		T = (1.0 / float64(cfg.Bitrate)) / colTimeS
	} else {
		var gaps []float64
		for k := 1; k < len(S.edges); k++ {
			if g := float64(S.edges[k].i - S.edges[k-1].i); g >= 1 {
				gaps = append(gaps, g)
			}
		}
		if len(gaps) < 3 {
			return Result{Proto: "manchester", Error: "too few edges / cannot infer bitrate"}
		}
		sort.Float64s(gaps)
		hp := gaps[int(float64(len(gaps))*0.1)]
		sum, cnt := 0.0, 0
		for _, g := range gaps {
			if math.Abs(g-hp) <= 0.35*hp {
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

	// Segment the edges into FRAMES: a captured record holds several frames
	// separated by idle gaps, and a free-running scope starts at a random phase,
	// so the leading frame is usually partial. Split where consecutive edges are
	// more than 1.5·T apart (an inter-frame idle), then decode each frame on its
	// OWN phase lock — one global phase cannot align frames whose gaps are a
	// non-integer number of bit periods.
	var segs [][2]int // inclusive edge-index ranges
	segStart := 0
	for k := 1; k < len(S.edges); k++ {
		if float64(S.edges[k].i-S.edges[k-1].i) > 1.5*T {
			segs = append(segs, [2]int{segStart, k - 1})
			segStart = k
		}
	}
	segs = append(segs, [2]int{segStart, len(S.edges) - 1})

	var spans []Span
	var bytesOut []int
	var toks []string
	frames := 0
	for sgIdx, sg := range segs {
		if sg[1] <= sg[0] { // a lone edge carries no cell
			continue
		}
		// Phase lock this frame: the segment's first edge is a cell boundary or a
		// mid-cell transition (cell began half a period earlier). Try both, keep
		// the phase with more clean mid transitions and fewer coding violations.
		s0e, lastE := S.edges[sg[0]].x, S.edges[sg[1]].x
		var cells []mCell
		bestScore, bestGood := -1<<30, 0
		for _, s0 := range []float64{s0e, s0e - 0.5*T} {
			c, good, viol := recoverManchester(S, s0, T, cfg.IEEE, lastE)
			if sc := good - 4*viol; sc > bestScore {
				bestScore, bestGood, cells = sc, good, c
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
		spans = append(spans, Span{cells[0].I0, cells[run-1].I1, "SYNC", "start", 0})
		// Pack this frame's cells into words; a coding violation flushes the word.
		curVal, curBits, byteStart := 0, 0, 0
		for _, c := range cells {
			if c.Bit < 0 {
				spans = append(spans, Span{c.I0, c.I1, "!", "frame-error", 0})
				curVal, curBits = 0, 0
				continue
			}
			if curBits == 0 {
				byteStart, curVal = c.I0, 0
			}
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
	}
	if frames == 0 {
		return Result{Proto: "manchester", Error: "no Manchester frame (preamble) found"}
	}

	baud := cfg.Bitrate
	if colTimeS > 0 {
		baud = int(math.Round(1.0 / (T * colTimeS)))
	}
	return Result{OK: true, Proto: "manchester", Spans: spans, Text: strings.Join(toks, " "),
		Bytes: bytesOut, Baud: baud, SPB: T, Thr: S.threshold}
}
