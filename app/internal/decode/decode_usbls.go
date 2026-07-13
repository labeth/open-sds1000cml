package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// USBLSCfg configures the USB low/full-speed decode of the single D+ line.
//
//	Bitrate   0 => infer the bit period from the shortest level-run (= one bit);
//	          else bits/s (LS = 1_500_000, FS = 12_000_000).
//	Threshold /HaveThr override the auto slice threshold (see sliceChannel).
type USBLSCfg struct {
	Bitrate   int
	Threshold float64
	HaveThr   bool
}

// usbPIDName maps a 4-bit PID value (PID3..PID0, the low nibble transmitted
// LSB-first) to its packet name. The full 8-bit PID byte on the wire is this
// nibble followed by its ones-complement, so e.g. DATA0 (nibble 0x3) => 0xC3.
var usbPIDName = map[int]string{
	0x1: "OUT", 0x9: "IN", 0x5: "SOF", 0xD: "SETUP",
	0x3: "DATA0", 0xB: "DATA1", 0x7: "DATA2", 0xF: "MDATA",
	0x2: "ACK", 0xA: "NAK", 0xE: "STALL", 0x6: "NYET",
	0xC: "PRE", 0x8: "SPLIT", 0x4: "PING",
}

// DecodeUSBLS decodes USB low/full-speed packets on one channel's codes — the
// D+ single-ended line, where the J/K bus states appear as the two logic
// levels. Mirrors decode_usbls.js step for step so LCD and web agree byte-for-
// byte.
//
// Line coding is NRZI: a 0 bit is a level TRANSITION, a 1 bit is NO transition.
// Bit stuffing inserts a 0 after six consecutive 1s (dropped on decode). A
// packet is SYNC (00000001 = KJKJKJKK) + PID (4 bits + 4-bit complement) +
// optional data/CRC + EOP (~2 bit-times of SE0, which on the single D+ line
// reads as an extended idle that, with the inter-packet idle, bounds the packet).
func DecodeUSBLS(dp []uint8, colTimeS float64, cfg USBLSCfg) Result {
	const minSPB = 4.0  // samples per bit floor
	const eopCells = 2  // EOP ~ 2 bit-times of SE0 trailing every packet
	const splitK = 10.0 // inter-packet idle: a gap wider than this many bit periods
	S := sliceChannel(dp, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "usbls", Error: S.reason}
	}
	if len(S.edges) < 8 { // SYNC alone carries 7 transitions
		return Result{Proto: "usbls", Error: "too few edges"}
	}

	// Bit period T (samples/bit). cfg.Bitrate pins it; otherwise infer it: NRZI
	// edge gaps are integer multiples of one bit period (1..7, capped by bit
	// stuffing), so the shortest cluster is T. A blind low percentile broke on
	// realistic idle-bus captures — a keep-alive train (one 2-bit SE0 every
	// ~1 ms) floods the gap list with 2-bit and huge inter-KA gaps, drags the
	// percentile past the packet's few 1-bit gaps, and the collapsed estimate
	// made auto decode fail on a capture sigrok reads fine (found by the
	// sigrok oracle). Use inferUARTspb's deterministic cluster walk instead:
	// each ascending gap cluster is tried as the 1-bit hypothesis, refined by
	// re-centered mean, validated by the fraction of gaps it explains as
	// integer bit multiples; ties go to the larger period. Gaps beyond ~16
	// candidate bits (inter-packet / keep-alive spacing) carry no bit-timing
	// evidence and are excluded from refine and validation.
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
			return Result{Proto: "usbls", Error: "too few edges / cannot infer bitrate"}
		}
		sort.Float64s(gaps)
		var cands []float64
		for i := 0; i < len(gaps); {
			seed := gaps[i]
			sum, j := 0.0, i
			for j < len(gaps) && gaps[j] <= 1.5*seed {
				sum += gaps[j]
				j++
			}
			cands = append(cands, sum/float64(j-i))
			i = j
		}
		best, bestFrac := 0.0, -1.0
		for _, cand := range cands {
			if cand < 2.5 { // below the samples/bit floor: a spur cluster
				continue
			}
			var kg []float64
			for _, g := range gaps {
				if g <= 16*cand {
					kg = append(kg, g)
				}
			}
			if len(kg) < 3 {
				continue
			}
			ref := cand
			for pass := 0; pass < 2; pass++ {
				sum, cnt := 0.0, 0
				for _, g := range kg {
					if math.Abs(g-ref) <= 0.35*ref {
						sum += g
						cnt++
					}
				}
				if cnt > 0 {
					ref = sum / float64(cnt)
				}
			}
			good := 0
			for _, g := range kg {
				if m := math.Round(g / ref); m >= 1 && math.Abs(g-m*ref) <= 0.35*ref {
					good++
				}
			}
			// >= keeps the LARGER candidate on an exact tie (candidates ascend).
			if frac := float64(good) / float64(len(kg)); frac >= 0.7 && frac >= bestFrac {
				best, bestFrac = ref, frac
			}
		}
		if best <= 0 {
			return Result{Proto: "usbls", Error: "bitrate ambiguous — set it explicitly"}
		}
		T = best
	}
	if math.IsInf(T, 0) || math.IsNaN(T) || !(T >= minSPB) {
		return Result{Proto: "usbls", Error: fmt.Sprintf("%.1f samples/bit; need >= %g", T, minSPB)}
	}

	// Segment edges into PACKETS on the inter-packet idle. Within a packet bit
	// stuffing guarantees a transition at least every ~7 bit periods; the EOP
	// then the idle line open a much wider gap, so split where consecutive edges
	// are more than splitK bit periods apart.
	var segs [][2]int
	segStart := 0
	for k := 1; k < len(S.edges); k++ {
		if float64(S.edges[k].i-S.edges[k-1].i) > splitK*T {
			segs = append(segs, [2]int{segStart, k - 1})
			segStart = k
		}
	}
	segs = append(segs, [2]int{segStart, len(S.edges) - 1})

	n := S.n
	clampI := func(i int) int {
		if i < 0 {
			return 0
		}
		if i >= n {
			return n - 1
		}
		return i
	}
	cellStart := func(x0 float64, k int) int { return clampI(int(math.Round(x0 + float64(k)*T))) }
	cellEnd := func(x0 float64, k int) int { return clampI(int(math.Round(x0+float64(k+1)*T)) - 1) }

	var spans []Span
	var bytesOut []int
	var toks []string
	packets := 0
	for sgIdx, sg := range segs {
		if sg[1] <= sg[0] { // a lone edge carries no packet
			continue
		}
		x0, x1 := S.edges[sg[0]].x, S.edges[sg[1]].x
		nCells := int(math.Round((x1 - x0) / T))
		if nCells < 8+8+eopCells { // SYNC + PID + EOP minimum
			continue
		}
		if nCells > n { // safety cap
			nCells = n
		}
		// A record that ends mid-transmission leaves the trailing packet with no
		// EOP/idle to close it — require trailing idle on the last segment and drop
		// the partial otherwise (real captures start/end at a random phase).
		if sgIdx == len(segs)-1 && float64(n-1-S.edges[sg[1]].i) < 2*T {
			continue
		}

		// NRZI-decode the cells: idle level seeds the first comparison, then a level
		// change vs the previous cell = 0, no change = 1.
		idleX := x0 - 0.5*T
		if idleX < 0 {
			idleX = 0
		}
		if idleX > float64(n-1) {
			idleX = float64(n - 1)
		}
		prev := logicAt(S, idleX)
		if prev < 0 {
			prev = 0
		}
		var rawBits, rawCell []int
		for k := 0; k < nCells; k++ {
			lv := logicAt(S, x0+(float64(k)+0.5)*T)
			if lv < 0 { // ran off the captured region
				break
			}
			b := 1
			if lv != prev {
				b = 0
			}
			prev = lv
			rawBits = append(rawBits, b)
			rawCell = append(rawCell, k)
		}
		// Strip the trailing EOP cells (SE0 reads as an extended idle bound here).
		if len(rawBits) < eopCells+16 {
			continue
		}
		rawBits = rawBits[:len(rawBits)-eopCells]
		rawCell = rawCell[:len(rawCell)-eopCells]

		// De-stuff: the 0 inserted after six consecutive 1s is dropped. Track each
		// kept bit's raw cell so spans map back to sample indices.
		var bitsArr, cellOf []int
		ones := 0
		for i := range rawBits {
			if ones == 6 {
				// A valid stream inserts a 0 after six 1s. A SEVENTH 1 is a stuff
				// violation — i.e. the held idle level after the EOP (a captured
				// segment often merges the packet with the following idle when the
				// gap is near the split threshold). Terminate the packet here so the
				// idle never decodes as trailing garbage bytes.
				if rawBits[i] != 0 {
					break
				}
				ones = 0
				continue // drop the stuffed 0
			}
			bitsArr = append(bitsArr, rawBits[i])
			cellOf = append(cellOf, rawCell[i])
			if rawBits[i] == 1 {
				ones++
			} else {
				ones = 0
			}
		}
		if len(bitsArr) < 16 {
			continue
		}
		// SYNC must be 00000001; anything else is a partial/garbage frame — drop it.
		syncOK := bitsArr[7] == 1
		for i := 0; i < 7; i++ {
			if bitsArr[i] != 0 {
				syncOK = false
			}
		}
		if !syncOK {
			continue
		}
		// PID = 8 bits LSB-first: low nibble = PID, high nibble = its complement.
		pidByte := 0
		for i := 0; i < 8; i++ {
			pidByte |= bitsArr[8+i] << i
		}
		pid4 := pidByte & 0xF
		check := (pidByte >> 4) & 0xF
		name := usbPIDName[pid4]
		if name == "" {
			name = fmt.Sprintf("PID%X", pid4)
		}
		pidBad := check != (^pid4 & 0xF)

		if packets > 0 { // separate packets in the transcript
			spans = append(spans, Span{cellStart(x0, cellOf[0]), cellStart(x0, cellOf[0]), "", "gap", 0})
			toks = append(toks, "|")
		}
		packets++
		spans = append(spans, Span{cellStart(x0, cellOf[0]), cellEnd(x0, cellOf[7]), "SYNC", "start", 0})
		pidText, pidKind := name, "addr"
		if pidBad {
			pidText, pidKind = "!"+name, "frame-error"
		}
		spans = append(spans, Span{cellStart(x0, cellOf[8]), cellEnd(x0, cellOf[15]), pidText, pidKind, pid4})
		toks = append(toks, pidText)

		// Payload bytes (data + CRC) after the PID, LSB-first.
		nBytes := (len(bitsArr) - 16) / 8
		for b := 0; b < nBytes; b++ {
			base := 16 + b*8
			val := 0
			for i := 0; i < 8; i++ {
				val |= bitsArr[base+i] << i
			}
			spans = append(spans, Span{cellStart(x0, cellOf[base]), cellEnd(x0, cellOf[base+7]), hex2(val), "data", val})
			toks = append(toks, hex2(val))
			bytesOut = append(bytesOut, val)
		}
	}
	if packets == 0 {
		return Result{Proto: "usbls", Error: "no USB packet (SYNC+PID) found"}
	}

	baud := cfg.Bitrate
	if colTimeS > 0 {
		baud = int(math.Round(1.0 / (T * colTimeS)))
	}
	return Result{OK: true, Proto: "usbls", Spans: spans, Text: strings.Join(toks, " "),
		Bytes: bytesOut, Baud: baud, SPB: T, Thr: S.threshold}
}
