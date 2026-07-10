package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// FlexRayCfg configures the FlexRay decode of one single-ended channel.
//
//	Bitrate   0 => auto-infer the bit period from edge statistics; else bits/s
//	          (FlexRay is 10 Mbit/s, but colTimeS varies so infer when unset).
//	Threshold /HaveThr override the auto slice threshold (see sliceChannel).
type FlexRayCfg struct {
	Bitrate   int
	Threshold float64
	HaveThr   bool
}

// DecodeFlexRay decodes a FlexRay byte stream on one channel's codes. The bus is
// shown here as a single logic line (the BP/BM pair collapsed to two levels):
// idle sits HIGH. A frame is framed like a stretched async word:
//
//	TSS  Transmission Start Sequence — a long LOW run (>= ~5 bit-times).
//	FSS  Frame Start Sequence        — one HIGH bit right after the TSS.
//	then repeatedly, one per byte:
//	  BSS Byte Start Sequence        — one HIGH bit then one LOW bit (1,0).
//	  8 data bits, MSB-first.
//	FES  Frame End Sequence          — a LOW then HIGH; or the line returns idle.
//
// The BSS in front of every byte is the key: it re-establishes a HIGH->LOW
// falling edge each byte (used here to re-lock phase against clock drift), and
// its shape distinguishes "another byte follows" (BSS = high,low) from "frame is
// over" (FES = low,high, or idle = high,high). Mirrors decode_flexray.js step
// for step so the web overlay and the on-device LCD agree byte-for-byte.
func DecodeFlexRay(codes []uint8, colTimeS float64, cfg FlexRayCfg) Result {
	const minSPB = 4.0
	const tssMinLowBits = 4.0 // "~5 bit-times of LOW" — accept a little short for jitter
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "flexray", Error: S.reason}
	}
	if len(S.edges) < 2 {
		return Result{Proto: "flexray", Error: "too few edges"}
	}

	// Bit period T in samples. cfg.Bitrate pins it; otherwise infer it from the
	// edge gaps: the shortest FlexRay pulses are exactly one bit wide (the BSS
	// high/low bits and isolated data bits), so the smallest gaps cluster at T.
	// Take a low percentile (robust to a stray short gap) then refine on it.
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
			return Result{Proto: "flexray", Error: "too few edges / cannot infer bitrate"}
		}
		sort.Float64s(gaps)
		bp := gaps[int(float64(len(gaps))*0.1)]
		sum, cnt := 0.0, 0
		for _, g := range gaps {
			if math.Abs(g-bp) <= 0.35*bp {
				sum += g
				cnt++
			}
		}
		if cnt > 0 {
			bp = sum / float64(cnt)
		}
		T = bp
	}
	if math.IsInf(T, 0) || math.IsNaN(T) || !(T >= minSPB) {
		return Result{Proto: "flexray", Error: fmt.Sprintf("%.1f samples/bit; need >= %g", T, minSPB)}
	}
	tol := 0.4 * T
	maxBytes := int(float64(S.n)/(10*T)) + 4 // safety cap; BSS/FES/EOF break normally

	// resync snaps a byte's BSS anchor onto the guaranteed HIGH->LOW falling edge
	// in the middle of its BSS (at anchor+T), correcting accumulated clock drift.
	// ei is a monotonic pointer: anchors only ever increase across the record.
	ei := 0
	resync := func(anchor float64) float64 {
		target := anchor + T
		for ei < len(S.edges) && S.edges[ei].x < target-tol {
			ei++
		}
		best := math.Inf(1)
		var bestX float64
		found := false
		for j := ei; j < len(S.edges) && S.edges[j].x <= target+tol; j++ {
			if S.edges[j].dir < 0 { // BSS mid transition is HIGH->LOW
				if d := math.Abs(S.edges[j].x - target); d < best {
					best, bestX, found = d, S.edges[j].x, true
				}
			}
		}
		if found {
			return bestX - T
		}
		return anchor
	}

	var spans []Span
	var bytesOut []int
	var toks []string
	frames := 0
	consumedUntil := -1

	// Scan rising edges for a TSS->FSS boundary: a rising edge preceded by a LOW
	// run of >= tssMinLowBits bit-times. Requiring a *preceding* falling edge
	// means the record must have captured the idle->TSS transition, so a frame
	// truncated at the record start (capture began mid-TSS) is dropped. Each
	// accepted frame is decoded whole and skipped past (consumedUntil), so the
	// long LOW runs an all-zero data byte can produce are never mistaken for a
	// second TSS.
	for k := 1; k < len(S.edges); k++ {
		e := S.edges[k]
		if e.dir <= 0 || e.i < consumedUntil {
			continue
		}
		prev := S.edges[k-1]
		if prev.dir >= 0 { // the run before a rising edge must be LOW (a falling edge opened it)
			continue
		}
		if e.x-prev.x < tssMinLowBits*T {
			continue
		}

		// TSS found. The FSS (one HIGH bit) begins at this rising edge; the first
		// BSS begins one bit later.
		anchorFSS := e.x
		frameSpans := []Span{{prev.i, e.i, "TSS", "start", 0}}
		var frameBytes []int
		var frameToks []string
		anchor := anchorFSS + T
		lastByteEnd := e.i
		for b := 0; b < maxBytes; b++ {
			anchor = resync(anchor)
			// A valid BSS is HIGH then LOW. Anything else — FES (low,high), idle
			// (high,high), or running off the end — ends the frame.
			if logicAt(S, anchor+0.5*T) != 1 || logicAt(S, anchor+1.5*T) != 0 {
				break
			}
			val, eof := 0, false
			for d := 0; d < 8; d++ { // 8 data bits, MSB-first
				bit := logicAt(S, anchor+(2.5+float64(d))*T)
				if bit < 0 {
					eof = true
					break
				}
				val = (val << 1) | bit
			}
			if eof { // trailing byte truncated by the record end — drop it
				break
			}
			i0 := int(math.Round(anchor))
			i1 := int(math.Round(anchor+10*T)) - 1
			if i0 < 0 {
				i0 = 0
			}
			if i1 >= S.n {
				i1 = S.n - 1
			}
			if i1 < i0 {
				i1 = i0
			}
			frameSpans = append(frameSpans, Span{i0, i1, hex2(val), "data", val})
			frameToks = append(frameToks, hex2(val))
			frameBytes = append(frameBytes, val)
			lastByteEnd = i1
			anchor += 10 * T
		}

		if len(frameBytes) == 0 { // a lone TSS-like low run, no real bytes behind it
			consumedUntil = e.i + 1
			continue
		}

		// Header bonus: the first 5 bytes are the FlexRay header —
		// flags(5) frameID(11) payloadLen(7) headerCRC(11) cycle(6) = 40 bits,
		// MSB-first. Emit a note span (kind "addr") right after the TSS.
		if len(frameBytes) >= 5 {
			var hdr uint64
			for hb := 0; hb < 5; hb++ {
				hdr = (hdr << 8) | uint64(frameBytes[hb]&0xff)
			}
			frameID := int((hdr >> 24) & 0x7FF)
			payloadLen := int((hdr >> 17) & 0x7F)
			cycle := int(hdr & 0x3F)
			sync := (hdr >> 36) & 1
			startup := (hdr >> 35) & 1
			note := fmt.Sprintf("ID=%d LEN=%d CYC=%d", frameID, payloadLen, cycle)
			if sync == 1 {
				note += " SYNC"
			}
			if startup == 1 {
				note += " STARTUP"
			}
			hdrSpan := Span{frameSpans[1].I0, frameSpans[5].I1, note, "addr", frameID}
			// insert the note right after the TSS span (index 0), before the data.
			out := make([]Span, 0, len(frameSpans)+1)
			out = append(out, frameSpans[0], hdrSpan)
			out = append(out, frameSpans[1:]...)
			frameSpans = out
			frameToks = append([]string{"[" + note + "]"}, frameToks...)
		}

		if frames > 0 { // separate frames in the transcript
			spans = append(spans, Span{frameSpans[0].I0, frameSpans[0].I0, "", "gap", 0})
			toks = append(toks, "|")
		}
		frames++
		spans = append(spans, frameSpans...)
		toks = append(toks, frameToks...)
		bytesOut = append(bytesOut, frameBytes...)
		consumedUntil = lastByteEnd + 1
	}

	if frames == 0 {
		return Result{Proto: "flexray", Error: "no FlexRay frame (TSS) found"}
	}
	baud := cfg.Bitrate
	if colTimeS > 0 {
		baud = int(math.Round(1.0 / (T * colTimeS)))
	}
	return Result{OK: true, Proto: "flexray", Spans: spans, Text: strings.Join(toks, " "),
		Bytes: bytesOut, Baud: baud, SPB: T, Thr: S.threshold}
}
