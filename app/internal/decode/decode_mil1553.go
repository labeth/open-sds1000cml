package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// MIL1553Cfg configures the MIL-STD-1553B decode of one channel.
//
//	Bitrate   0 => auto-infer the bit period from the edge statistics; else
//	          bits/s (real 1553 is 1_000_000).
//	Threshold/HaveThr override the auto slice threshold (see sliceChannel).
type MIL1553Cfg struct {
	Bitrate   int
	Threshold float64
	HaveThr   bool
}

// DecodeMIL1553 decodes MIL-STD-1553B on one channel's codes. 1553 is a
// 1 Mbit/s bi-phase (Manchester II) bus; the DATA bits reuse recoverManchester
// from decode_manchester.go. A 20-bit-time WORD = a 3-bit-time SYNC + 16 data
// bits (MSB first) + 1 odd-parity bit. The SYNC is a Manchester CODING
// VIOLATION spanning 3 bit-times: a command/status sync holds HIGH for 1.5
// bit-times then LOW for 1.5; a data sync is the inverse (LOW then HIGH). 1553
// uses the "1 = high-then-low" mapping, i.e. a falling mid-cell transition = 1
// and a rising = 0 — the Thomas convention (ieee=false) of recoverManchester.
//
// Kept algorithm-faithful to decode_mil1553.js so LCD and web agree byte-for-byte.
func DecodeMIL1553(codes []uint8, colTimeS float64, cfg MIL1553Cfg) Result {
	const minSPB = 4.0
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "mil1553", Error: S.reason}
	}
	if len(S.edges) < 2 {
		return Result{Proto: "mil1553", Error: "too few edges"}
	}

	// Bit period T in samples. cfg.Bitrate pins it; otherwise infer it from the
	// edge gaps exactly as Manchester does: consecutive edges are T/2 apart (a
	// cell boundary followed by a mid-cell transition) or T apart, so the
	// shortest gaps cluster at the half-period. Take a low percentile (robust to
	// a stray short gap) then refine on that cluster; T is twice it. The SYNC's
	// long ~1.5T/2T holds sit far above the T/2 cluster and never bias it.
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
			return Result{Proto: "mil1553", Error: "too few edges / cannot infer bitrate"}
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
		return Result{Proto: "mil1553", Error: fmt.Sprintf("%.1f samples/bit; need >= %g", T, minSPB)}
	}

	var spans []Span
	var bytesOut []int
	var toks []string
	words := 0

	// Find each word's SYNC by its coding violation. In clean Manchester data no
	// level is ever held longer than one bit-time (T): a level held ~1.5T (up to
	// 2T when an equal-level neighbouring half-cell merges in) is only possible
	// inside a SYNC. The SYNC's mid transition is therefore the UNIQUE edge with
	// a >1.25T hold on BOTH sides — its two ~1.5T half-holds. That two-sided test
	// rejects both ordinary data edges (gaps <=T) and the sync-start edge after
	// an inter-message idle (a huge hold on one side only). Words repeat, so we
	// simply scan every interior edge; each real SYNC-mid is matched exactly once.
	for k := 1; k < len(S.edges)-1; k++ {
		gapBefore := S.edges[k].x - S.edges[k-1].x
		gapAfter := S.edges[k+1].x - S.edges[k].x
		if gapBefore < 1.25*T || gapBefore > 2.5*T {
			continue
		}
		if gapAfter < 1.25*T || gapAfter > 2.5*T {
			continue
		}
		syncMid := S.edges[k].x
		syncStart := syncMid - 1.5*T
		syncEnd := syncMid + 1.5*T
		firstHalf := logicAt(S, syncMid-0.75*T) // level of the sync's first 1.5T hold
		if firstHalf < 0 {
			continue // sync clipped by the record start
		}
		isCmd := firstHalf == 1 // command/status sync is high first; data sync is low first

		// Recover the 17 Manchester cells that follow the sync (16 data bits +
		// parity). ieee=false is the 1553 mapping (rising mid = 0, falling = 1).
		// Cap the sampler just past cell 17 so it can never wander into the next
		// word's sync while still yielding the full parity cell.
		cells, _, _ := recoverManchester(S, syncEnd, T, false, syncEnd+17.2*T)
		if len(cells) < 17 {
			continue // word truncated by the record end — drop the partial
		}
		// 16 data bits, MSB first. A coding violation among them => not a real word.
		word, bad := 0, false
		for c := 0; c < 16; c++ {
			if cells[c].Bit < 0 {
				bad = true
				break
			}
			word = (word << 1) | cells[c].Bit
		}
		if bad {
			continue
		}
		// Odd parity: the count of 1s across the 16 data bits + the parity bit is odd.
		parityBit := cells[16].Bit
		expect := 1 - (popcount(word) & 1)
		parityOK := parityBit >= 0 && parityBit == expect

		label := "csync"
		if !isCmd {
			label = "dsync"
		}
		syncI0 := int(math.Round(syncStart))
		if syncI0 < 0 {
			syncI0 = 0
		}
		syncI1 := int(math.Round(syncEnd)) - 1
		if syncI1 >= S.n {
			syncI1 = S.n - 1
		}
		if syncI1 < syncI0 {
			syncI1 = syncI0
		}
		spans = append(spans, Span{syncI0, syncI1, label, "start", 0})
		hexw := fmt.Sprintf("%04X", word)
		spans = append(spans, Span{cells[0].I0, cells[15].I1, hexw, "data", word})
		pfx := ""
		if !parityOK {
			pfx = "!"
			spans = append(spans, Span{cells[16].I0, cells[16].I1, "!par", "frame-error", 0})
		}
		toks = append(toks, label, pfx+hexw)
		bytesOut = append(bytesOut, word)
		words++
	}

	if words == 0 {
		return Result{Proto: "mil1553", Error: "no MIL-STD-1553 sync found"}
	}

	baud := cfg.Bitrate
	if colTimeS > 0 {
		baud = int(math.Round(1.0 / (T * colTimeS)))
	}
	return Result{OK: true, Proto: "mil1553", Spans: spans, Text: strings.Join(toks, " "),
		Bytes: bytesOut, Baud: baud, SPB: T, Thr: S.threshold}
}
