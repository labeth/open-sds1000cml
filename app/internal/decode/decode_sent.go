package decode

import (
	"fmt"
	"math"
	"strings"
)

// SENTCfg configures the SENT (SAE J2716) single-wire decode. SENT is a
// pulse-width / tick-encoded protocol measured falling-edge to falling-edge:
// each pulse's period, divided by a nominal "tick", gives a value. A frame opens
// with a 56-tick SYNC/calibration pulse (used to derive the tick), followed by a
// run of nibble pulses (12..27 ticks => value 0..15), optionally closed by a
// variable-length pause pulse.
type SENTCfg struct {
	TickNs     float64 // tick period override in ns (0 => derive tick from the 56-tick SYNC)
	Nibbles    int     // nibble pulses per frame after SYNC (status+data+CRC); 0 => 8
	PausePulse bool    // a variable pause pulse follows the CRC nibble
	Threshold  float64
	HaveThr    bool
}

const (
	sentSyncTicks = 56.0 // SYNC/calibration pulse width in ticks
	sentNibMin    = 12.0 // shortest nibble pulse (value 0)
	sentNibMax    = 27.0 // longest nibble pulse (value 15)
	sentTol       = 0.20 // ±20% jitter tolerance on the tick
)

func sentHex1(v int) string { return fmt.Sprintf("%X", v&0xf) }

func sentClampNib(v int) int {
	if v < 0 {
		return 0
	}
	if v > 15 {
		return 15
	}
	return v
}

// DecodeSENT decodes a SENT (SAE J2716) signal from one channel's sampled codes.
// It slices to logic, treats every falling edge as a pulse-period boundary, finds
// the 56-tick SYNC (or uses cfg.TickNs), then reads each following pulse as a
// nibble value = round(width/tick) - 12. Emits a "sync" span, one "data" span per
// nibble, a "crc" span for the last nibble of the frame, and (when cfg.PausePulse)
// a "pause" span for the trailing pause pulse. Robust to jitter and hostile input.
func DecodeSENT(codes []uint8, colTimeS float64, cfg SENTCfg) Result {
	nib := cfg.Nibbles
	if nib <= 0 {
		nib = 8 // status + 6 data + CRC (the typical fast-channel frame)
	}
	if nib > 64 { // bound the per-frame inner loop against a hostile config
		nib = 64
	}
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "sent", Error: S.reason}
	}
	n := S.n

	// Falling edges (high->low) mark the start of each pulse period. Use the
	// interpolated crossing x for sub-sample period precision; the integer index
	// i anchors the span.
	var fallX []float64
	var fallI []int
	for _, e := range S.edges {
		if e.dir < 0 && e.i < n {
			fallX = append(fallX, e.x)
			fallI = append(fallI, e.i)
		}
	}
	if len(fallX) < 2 {
		return Result{Proto: "sent", Error: "no SENT pulses (need >= 2 falling edges)"}
	}

	// One pulse period per consecutive pair of falling edges.
	np := len(fallX) - 1
	period := make([]float64, np)
	for k := 0; k < np; k++ {
		period[k] = fallX[k+1] - fallX[k]
	}

	haveTick := cfg.TickNs > 0 && colTimeS > 0
	var seedTick float64 // samples per tick
	if haveTick {
		seedTick = (cfg.TickNs * 1e-9) / colTimeS
	}

	// Locate the first SYNC (~56 ticks). With an explicit tick, a SYNC is any
	// pulse within ±20% of 56*tick. Otherwise hypothesize each pulse as the SYNC
	// (tick = width/56) and accept the first whose following pulses validate as
	// nibbles (12..27 ticks) — a real SYNC is followed by valid nibbles; a nibble
	// mis-taken as SYNC yields a tiny tick that blows every follower out of range.
	firstSync := -1
	if haveTick {
		for k := 0; k < np; k++ {
			if math.Abs(period[k]/seedTick-sentSyncTicks) <= sentTol*sentSyncTicks {
				firstSync = k
				break
			}
		}
	} else {
		for k := 0; k < np; k++ {
			if period[k] <= 0 {
				continue
			}
			tickHyp := period[k] / sentSyncTicks
			if tickHyp <= 0 {
				continue
			}
			valid, checked := 0, 0
			for j := 1; j <= nib && k+j < np; j++ {
				checked++
				r := period[k+j] / tickHyp
				if r >= sentNibMin*(1-sentTol) && r <= sentNibMax*(1+sentTol) {
					valid++
				}
			}
			if checked > 0 && float64(valid) >= math.Ceil(0.6*float64(checked)) {
				firstSync = k
				seedTick = tickHyp
				break
			}
		}
		// Fallback for a short capture that never validated a run: take the
		// longest pulse as the SYNC. Keeps a single-frame capture decodable and
		// never panics on garbage (bounded, best-effort).
		if firstSync < 0 {
			maxK, maxV := -1, 0.0
			for k := 0; k < np; k++ {
				if period[k] > maxV {
					maxV, maxK = period[k], k
				}
			}
			if maxK >= 0 && maxV > 0 {
				firstSync = maxK
				seedTick = maxV / sentSyncTicks
			}
		}
	}
	if firstSync < 0 || seedTick <= 0 {
		return Result{Proto: "sent", Error: "no SENT SYNC (~56-tick) pulse found"}
	}

	looksSync := func(P, tick float64) bool {
		return tick > 0 && math.Abs(P/tick-sentSyncTicks) <= sentTol*sentSyncTicks
	}

	var spans []Span
	var bytes []int
	var toks []string
	curTick := seedTick
	k := firstSync
	guard, maxIter := 0, np+4 // paranoia: k strictly advances, so this never trips
	for k < np {
		if guard++; guard > maxIter {
			break
		}
		// Expect a SYNC here; if this pulse isn't one, hunt forward for the next.
		if !looksSync(period[k], curTick) {
			k++
			continue
		}
		spans = append(spans, Span{fallI[k], fallI[k+1], "SYNC", "sync", 0})
		toks = append(toks, "SYNC")
		if !haveTick {
			curTick = period[k] / sentSyncTicks // recalibrate the tick every frame
		}
		k++

		// Decode up to `nib` nibble pulses.
		for nbIdx := 0; nbIdx < nib && k < np; nbIdx++ {
			P := period[k]
			if looksSync(P, curTick) { // a SYNC mid-frame => frame truncated; re-handle it
				break
			}
			val := int(math.Round(P/curTick)) - 12
			kind := "data"
			if nbIdx == nib-1 {
				kind = "crc"
			}
			if val < 0 || val > 15 {
				cv := sentClampNib(val)
				spans = append(spans, Span{fallI[k], fallI[k+1], "!" + sentHex1(cv), "frame-error", cv})
				toks = append(toks, "!"+sentHex1(cv))
			} else {
				spans = append(spans, Span{fallI[k], fallI[k+1], sentHex1(val), kind, val})
				toks = append(toks, sentHex1(val))
				bytes = append(bytes, val)
			}
			k++
		}

		// Optional trailing pause pulse (variable length, not a nibble).
		if cfg.PausePulse && k < np && !looksSync(period[k], curTick) {
			spans = append(spans, Span{fallI[k], fallI[k+1], "PAUSE", "pause", 0})
			toks = append(toks, "PAUSE")
			k++
		}
	}
	if len(spans) == 0 {
		return Result{Proto: "sent", Error: "no SENT SYNC (~56-tick) pulse found"}
	}
	return Result{OK: true, Proto: "sent", Spans: spans, Text: strings.Join(toks, " "),
		Bytes: bytes, SPB: curTick, Thr: S.threshold}
}
