package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// ARINC429Cfg configures the ARINC 429 decode of one channel carrying the
// BIPOLAR (three-level) return-to-zero line.
//
//	Bitrate   0 => auto-infer the bit period from the pulse spacing; else bits/s
//	          (typically 100000 high-speed, or 12500 low-speed).
//	Threshold /HaveThr override the auto NULL (mid) level of the tri-level slicer.
type ARINC429Cfg struct {
	Bitrate   int
	Threshold float64
	HaveThr   bool
}

// DecodeARINC429 decodes ARINC 429 on one channel that shows the BIPOLAR
// return-to-zero signal. The line has three levels: HI(+), NULL(0), LO(-). A
// logic 1 is a HI pulse for the first half of the bit cell then a return to
// NULL; a logic 0 is a LO pulse then NULL. So every bit cell carries exactly one
// RZ pulse whose polarity is the bit value — this is what makes ARINC self-
// clocking without a Manchester mid-cell transition.
//
// Slicing uses TWO thresholds straddling the NULL band, derived from the code
// histogram (NULL ~= the mode near mid-scale, HI ~= the high rail, LO ~= the low
// rail): thr_hi = mid + 0.4*(max-mid), thr_lo = mid - 0.4*(mid-min). A sample
// above thr_hi is a HI pulse (bit 1), below thr_lo a LO pulse (bit 0), in
// between is NULL. Pulses are detected as NULL->HI / NULL->LO transitions (with
// hysteresis on the return to NULL) and each one marks one bit cell — reading
// the bit at the centre of its pulse.
//
// A WORD is 32 bits, transmitted bit 1 first: label(8, shown 3-digit octal and
// bit-reversed), SDI(2), DATA(19), SSM(2), parity(1, odd over all 32 bits).
// Words are separated by >= 4 bit-times of NULL, so the pulse stream is
// segmented on that inter-word gap and each COMPLETE 32-bit word is decoded;
// a word truncated by the capture start/end (a partial with < 32 pulses) is
// dropped, since a free-running scope starts at a random phase.
func DecodeARINC429(codes []uint8, colTimeS float64, cfg ARINC429Cfg) Result {
	const proto = "arinc429"
	const minSPB = 4.0
	n := len(codes)
	if n < 8 {
		return Result{Proto: proto, Error: "no/too-few samples"}
	}

	// --- tri-level slice: histogram -> NULL(mid), HI rail(gmax), LO rail(gmin).
	var h [256]int
	for _, v := range codes {
		h[v]++
	}
	noiseFloor := math.Max(1, 0.001*float64(n))
	gmin := 0
	for gmin < 255 && float64(h[gmin]) < noiseFloor {
		gmin++
	}
	gmax := 255
	for gmax > 0 && float64(h[gmax]) < noiseFloor {
		gmax--
	}
	if gmax <= gmin {
		return Result{Proto: proto, Error: "flat/no transitions"}
	}
	// NULL level = the dominant (mode) code in the active range: an RZ line rests
	// at NULL for the second half of every bit plus the inter-word gaps.
	mid := gmin
	best := -1.0
	for c := gmin; c <= gmax; c++ {
		if float64(h[c]) > best {
			best = float64(h[c])
			mid = c
		}
	}
	midf := float64(mid)
	if cfg.HaveThr {
		midf = cfg.Threshold
	}
	rangeUp := float64(gmax) - midf
	rangeDn := midf - float64(gmin)
	span := math.Max(rangeUp, rangeDn)
	if span < 16 {
		return Result{Proto: proto, Error: "amplitude too small / not a bipolar RZ signal"}
	}
	// If one polarity is absent (an all-1s or all-0s word) mirror the present
	// rail so its threshold stays meaningful instead of collapsing onto NULL.
	if rangeUp < 0.25*span {
		rangeUp = span
	}
	if rangeDn < 0.25*span {
		rangeDn = span
	}
	thrHi := midf + 0.4*rangeUp
	thrLo := midf - 0.4*rangeDn
	exitHi := midf + 0.15*rangeUp // hysteretic return-to-NULL from a HI pulse
	exitLo := midf - 0.15*rangeDn // ... and from a LO pulse

	// --- detect pulses: NULL->HI / NULL->LO transitions. Each RZ pulse is one
	// bit cell; its polarity is the bit (HI=1, LO=0) and its start locates it.
	type pulse struct {
		i    int
		sign int
	}
	var pulses []pulse
	state := 0 // 0 NULL, +1 HI, -1 LO
	for i := 0; i < n; i++ {
		v := float64(codes[i])
		switch state {
		case 0:
			if v >= thrHi {
				state = 1
				pulses = append(pulses, pulse{i, 1})
			} else if v <= thrLo {
				state = -1
				pulses = append(pulses, pulse{i, -1})
			}
		case 1:
			if v <= thrLo { // straight to the opposite rail (rare)
				state = -1
				pulses = append(pulses, pulse{i, -1})
			} else if v < exitHi {
				state = 0
			}
		default: // -1
			if v >= thrHi {
				state = 1
				pulses = append(pulses, pulse{i, 1})
			} else if v > exitLo {
				state = 0
			}
		}
	}
	if len(pulses) < 2 {
		return Result{Proto: proto, Error: "no ARINC pulses"}
	}

	// --- bit period T (samples). cfg.Bitrate pins it; else infer it from the
	// pulse-start spacing: consecutive pulses within a word are T apart while the
	// inter-word gap is >= ~5T, so the low-percentile cluster of gaps is T.
	var T float64
	if cfg.Bitrate > 0 {
		T = (1.0 / float64(cfg.Bitrate)) / colTimeS
	} else {
		var gaps []float64
		for k := 1; k < len(pulses); k++ {
			if g := float64(pulses[k].i - pulses[k-1].i); g >= 1 {
				gaps = append(gaps, g)
			}
		}
		if len(gaps) < 3 {
			return Result{Proto: proto, Error: "too few pulses / cannot infer bitrate"}
		}
		sort.Float64s(gaps)
		p := gaps[int(float64(len(gaps))*0.1)]
		sum, cnt := 0.0, 0
		for _, g := range gaps {
			if math.Abs(g-p) <= 0.35*p {
				sum += g
				cnt++
			}
		}
		if cnt > 0 {
			p = sum / float64(cnt)
		}
		T = p
	}
	if math.IsInf(T, 0) || math.IsNaN(T) || !(T >= minSPB) {
		return Result{Proto: proto, Error: fmt.Sprintf("%.1f samples/bit; need >= %g", T, minSPB)}
	}

	// --- segment pulses into 32-bit WORDS on the inter-word NULL gap (> 2.5*T:
	// intra-word pulse gaps are ~1*T, the inter-word one is >= ~5*T).
	var segs [][2]int
	segStart := 0
	for k := 1; k < len(pulses); k++ {
		if float64(pulses[k].i-pulses[k-1].i) > 2.5*T {
			segs = append(segs, [2]int{segStart, k - 1})
			segStart = k
		}
	}
	segs = append(segs, [2]int{segStart, len(pulses) - 1})

	var spans []Span
	var bytesOut []int
	var toks []string
	words := 0
	for _, sg := range segs {
		s0 := float64(pulses[sg[0]].i)
		var bits [32]int
		for i := range bits {
			bits[i] = -1
		}
		filled := 0
		for j := sg[0]; j <= sg[1]; j++ {
			k := int(math.Round((float64(pulses[j].i) - s0) / T))
			if k < 0 || k >= 32 {
				continue // stray pulse outside the 32-bit window
			}
			if bits[k] == -1 {
				filled++
			}
			if pulses[j].sign > 0 {
				bits[k] = 1
			} else {
				bits[k] = 0
			}
		}
		if filled != 32 {
			continue // partial / malformed word (record-edge truncation) — drop it
		}

		// Fields, transmission order (bits[0] = first bit on the wire = ARINC #1).
		labelRev := 0
		for i := 0; i < 8; i++ {
			labelRev = (labelRev << 1) | bits[i] // bit 1 -> octal MSB (bit-reversed)
		}
		dataVal := 0
		for i := 0; i < 19; i++ {
			dataVal |= bits[10+i] << i
		}
		ssm := bits[29] | bits[30]<<1
		word32 := 0
		for i := 0; i < 32; i++ {
			word32 |= bits[i] << i // ARINC bit 1 = LSB
		}
		parityOdd := popcount(word32)&1 == 1 // odd parity over all 32 bits

		cellStart := func(k int) int {
			p := int(math.Round(s0 + float64(k)*T))
			if p < 0 {
				p = 0
			}
			if p >= n {
				p = n - 1
			}
			return p
		}
		cellEnd := func(k int) int {
			p := int(math.Round(s0+float64(k+1)*T)) - 1
			if p < 0 {
				p = 0
			}
			if p >= n {
				p = n - 1
			}
			return p
		}

		if words > 0 { // separate words in the transcript
			spans = append(spans, Span{cellStart(0), cellStart(0), "", "gap", 0})
			toks = append(toks, "|")
		}
		words++
		lblTxt := fmt.Sprintf("%03o", labelRev&0xff)
		spans = append(spans, Span{cellStart(0), cellEnd(7), lblTxt, "addr", labelRev})
		dataTxt := fmt.Sprintf("%05X", dataVal&0x7ffff)
		spans = append(spans, Span{cellStart(10), cellEnd(28), dataTxt, "data", dataVal})
		ssmTxt := fmt.Sprintf("SSM%d", ssm)
		spans = append(spans, Span{cellStart(29), cellEnd(30), ssmTxt, "rw", ssm})
		toks = append(toks, lblTxt, dataTxt, ssmTxt)
		if !parityOdd {
			spans = append(spans, Span{cellStart(31), cellEnd(31), "!P", "frame-error", 0})
			toks = append(toks, "!P")
		}
		// Result.Bytes = the 4 raw bytes of the 32-bit word (little-endian: byte 0
		// is the label field, ... byte 3 holds SSM+parity).
		bytesOut = append(bytesOut,
			word32&0xff, (word32>>8)&0xff, (word32>>16)&0xff, (word32>>24)&0xff)
	}

	if words == 0 {
		return Result{Proto: proto, Error: "no complete ARINC 429 word found"}
	}
	baud := cfg.Bitrate
	if colTimeS > 0 {
		baud = int(math.Round(1.0 / (T * colTimeS)))
	}
	return Result{OK: true, Proto: proto, Spans: spans, Text: strings.Join(toks, " "),
		Bytes: bytesOut, Baud: baud, SPB: T, Thr: midf}
}
