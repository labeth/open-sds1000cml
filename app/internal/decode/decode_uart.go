// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// UARTCfg configures the UART decode. Baud=0 auto-infers; Bits default 8.
// TRLC-LINKS: REQ-SDS-018
type UARTCfg struct {
	Inverted  bool // electrical low represents logical one (idle low)
	Baud      int
	Bits      int
	Parity    string // none|even|odd
	Format    string
	Threshold float64
	HaveThr   bool
}

// inferUARTspb estimates the samples-per-bit from the edge-gap statistics,
// robust to RINGY edges (1-3-sample spurious toggles around real transitions —
// the HW-documented auto-baud killer). It borrows the Manchester inference
// rigor: sub-sample crossing positions (edge.x, not the integer index), then a
// deterministic ascending CLUSTER WALK over the sorted gaps instead of a blind
// low percentile — ring-spur gaps form their own tiny cluster that a percentile
// would mistake for the bit width. Each cluster is tried as the 1-bit
// hypothesis: edges within the ring scale of the last kept edge are collapsed
// (a ring bounce is part of the SAME transition), the candidate is refined on the
// de-glitched 1-bit cluster, and it must explain >=70% of the de-glitched gaps
// as ~integer bit multiples. Among validating candidates the BEST-fitting one
// wins, ties to the larger period: ring debris at an exact sub-multiple of the
// true bit (spur gaps of 4 under a 16-sample bit) can validate perfectly, but
// so does the true bit — and the true bit is the wide one. If nothing
// validates the input is genuinely ambiguous and the caller must set the baud
// — that honesty is preserved. Mirrors decode.js inferUARTspb step for step.
// TRLC-LINKS: REQ-SDS-018
func inferUARTspb(S sliced) (float64, string) {
	var gaps []float64
	for k := 1; k < len(S.edges); k++ {
		if g := S.edges[k].x - S.edges[k-1].x; g >= 1 {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) < 3 {
		return 0, "too few edges / cannot infer baud"
	}
	sort.Float64s(gaps)
	// Deterministic greedy clusters: each starts at the smallest unassigned gap
	// and absorbs everything within 1.5x its seed (bit multiples sit a full
	// factor of 2 up, so clusters never bleed into the next multiple).
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
		if cand < 2.5 { // below the 3-samples/bit floor: a ring-spur cluster
			continue
		}
		// De-glitch: collapse edges within the RING SCALE of the last KEPT edge —
		// ring bounces cluster tightly (a few samples) around the true transition
		// instant. The window is capped at 5 samples, NOT half the candidate:
		// ringing is a fixed-time artifact, so a window that scaled with the
		// candidate would let a 2x-bit hypothesis swallow real 1-bit edges and
		// then validate on the merged gaps it manufactured itself.
		win := 0.5 * cand
		if win > 5 {
			win = 5
		}
		var kg []float64
		prevX := S.edges[0].x
		for k := 1; k < len(S.edges); k++ {
			if g := S.edges[k].x - prevX; g >= win {
				kg = append(kg, g)
				prevX = S.edges[k].x
			}
		}
		if len(kg) < 3 {
			continue
		}
		// Refine on the de-glitched 1-bit cluster by MEAN-SHIFT (two passes,
		// re-centering the ±0.35 window on the running estimate): the raw cluster
		// mean is ring-shortened, and a single window around it clips the upper
		// quantization branch of the true bit ({12,13} for spb 12.4 seen from a
		// cand of 9.5) — biasing spb low enough to mis-sample late bits. A WIDER
		// window instead would merge genuinely incommensurate pulse widths and
		// destroy the ambiguity honesty; re-centering does not.
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
			// Final estimate over every fitting gap, multi-bit runs included:
			// total time over total bits. The 1-bit mean alone carries the edge
			// quantization of each short gap; peak-detect frames (half a bucket
			// per sample) read 115200 Bd 1.2% fast that way.
			sumG, sumM := 0.0, 0.0
			for _, g := range kg {
				if m := math.Round(g / ref); m >= 1 && math.Abs(g-m*ref) <= 0.35*ref {
					sumG += g
					sumM += m
				}
			}
			if sumM > 0 {
				ref = sumG / sumM
			}
			best, bestFrac = ref, frac
		}
	}
	if best > 0 {
		return best, ""
	}
	return 0, "baud ambiguous — set it explicitly"
}

// DecodeUART decodes 8N1-style UART on one channel's codes (decode.js decodeUART).
// TRLC-LINKS: REQ-SDS-018
func DecodeUART(codes []uint8, colTimeS float64, cfg UARTCfg) Result {
	if cfg.Inverted {
		logical := make([]uint8, len(codes))
		for i, code := range codes {
			logical[i] = 255 - code
		}
		codes = logical
		if cfg.HaveThr {
			cfg.Threshold = 255 - cfg.Threshold
		}
	}
	bits := cfg.Bits
	if bits == 0 {
		bits = 8
	}
	if bits < 1 || bits > 16 { // physical UART is 5..9; bound the shift/mask math
		return Result{Proto: "uart", Error: "data bits out of range (1..16)"}
	}
	parity := cfg.Parity
	if parity == "" {
		parity = "none"
	}
	const idle = 1
	const guard = 4
	const minSPB = 3.0
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "uart", Error: S.reason}
	}
	n := S.n
	var spb, baud float64
	if cfg.Baud > 0 {
		spb = (1.0 / float64(cfg.Baud)) / colTimeS
		baud = float64(cfg.Baud)
	} else {
		var reason string
		spb, reason = inferUARTspb(S)
		if spb > 0 {
			spb = snapStandardBaud(spb, colTimeS)
		}
		if reason != "" {
			return Result{Proto: "uart", Error: reason}
		}
		baud = 1.0 / (spb * colTimeS)
	}
	if !(spb >= minSPB) {
		return Result{Proto: "uart", Error: fmt.Sprintf("%.1f samples/bit; need >= 3", spb)}
	}
	parityOf := func(v int) int {
		p := popcount(v&((1<<bits)-1)) & 1
		if parity == "even" {
			return p
		}
		return 1 - p
	}
	var spans []Span
	var bytes []int
	var toks []string
	pcNeed := 0 // the parity bit lengthens the frame — count it so a frame near the
	if parity != "none" {
		pcNeed = 1 // record end isn't started with its stop bit off the captured range
	}
	need := int(math.Ceil(float64(bits+2+pcNeed) * spb))
	i := guard
	for i < n-need-guard {
		if logicAt(S, float64(i-1)) == idle && logicAt(S, float64(i)) == 1-idle { // start edge
			start := i
			if logicAt(S, float64(start)+0.5*spb) == 1-idle { // confirm start bit
				val, gap := 0, false
				for c := 0; c < bits; c++ {
					b := logicAt(S, float64(start)+(1.5+float64(c))*spb)
					if b < 0 {
						gap = true
						break
					}
					val |= b << c // LSB-first
				}
				i1 := int(math.Min(float64(n-1), math.Round(float64(start)+float64(bits+1)*spb)))
				if gap {
					spans = append(spans, Span{start, i1, "gap", "gap", 0})
					i = start + 1
					continue
				}
				kind, pfx, pc := "data", "", 0
				if parity != "none" {
					pc = 1
					pb := logicAt(S, float64(start)+(1.5+float64(bits))*spb)
					if pb >= 0 && pb != parityOf(val) {
						kind, pfx = "parity-error", "!"
					}
				}
				sb := logicAt(S, float64(start)+(1.5+float64(bits)+float64(pc))*spb)
				if sb < 0 { // stop bit ran off the record: an incomplete frame, not clean
					spans = append(spans, Span{start, i1, "gap", "gap", 0})
					i = start + 1
					continue
				}
				if sb != idle { // wrong stop level = framing error
					if kind == "data" {
						kind = "frame-error"
					}
					pfx = "!"
				}
				spans = append(spans, Span{start, i1, pfx + FmtWord(val, bits, cfg.Format), kind, val})
				toks = append(toks, pfx+FmtWord(val, bits, cfg.Format))
				bytes = append(bytes, val)
				i = int(math.Round(float64(start)+float64(bits+1+pc)*spb)) + 1
				continue
			}
		}
		i++
	}
	if syncIn(spans, S, spb, bits+2+pcNeed, float64(bits)-0.5) {
		toks, bytes = toks[:0], bytes[:0]
		for _, sp := range spans {
			if sp.Kind != "gap" {
				toks, bytes = append(toks, sp.Text), append(bytes, sp.Val)
			}
		}
	}
	return Result{OK: true, Proto: "uart", Spans: spans, Text: strings.Join(toks, " "),
		Bytes: bytes, Baud: int(math.Round(baud)), SPB: spb, Thr: S.threshold}
}

// uartSyncFrames bounds the sync-in: a record that opens inside traffic
// mis-frames until a stop bit lands on idle, a few frames at most.
const uartSyncFrames = 8

// syncIn marks the frames a record opened inside traffic with as gaps: read
// from a data bit's edge, they are no bytes on the line. A record opens idle
// when the line is high, within the record, for confirm bit times before the
// first start edge - the data bits less half a bit: no data bit's edge
// follows a run that long (a parity bit's can, after a word of ones; a parity
// error after a short idle stays an error) - then every frame stands, and a
// break after a clean first byte stays an error. Otherwise the frames back to back with the first one (each starting
// before such a run could pass) are suspect: when none of the first
// uartSyncFrames is an error and there are two or more, they are in sync (one
// mis-framed byte can end on an idle-looking stop bit, two in a row hardly);
// else sync comes two clean frames after the last error, or with the first
// frame after an idle. A line that is not in sync within uartSyncFrames frames
// keeps everything. Without it every capture of a back-to-back UART line
// begins "!0B !AA !A9"; counting the first edge 1.5 bits in as opening idle,
// a quarter of the bench line's captures still begin so, or with a byte made
// of a cut one's last bits ("F8" for its 0A). Reports whether it changed
// anything.
// TRLC-LINKS: REQ-SDS-018
func syncIn(spans []Span, S sliced, spb float64, frameBits int, confirm float64) bool {
	if len(spans) == 0 {
		return false
	}
	if run := int(math.Ceil(confirm * spb)); spans[0].I0 >= run {
		idle := true
		for j := spans[0].I0 - run; j < spans[0].I0 && idle; j++ {
			idle = logicAt(S, float64(j)) == 1
		}
		if idle {
			return false // opened idle
		}
	}
	clean := func(k int) bool { return spans[k].Kind == "data" }
	end := 1 // the frames back to back with the first: [0, end)
	for end < len(spans) && float64(spans[end].I0)-(float64(spans[end-1].I0)+float64(frameBits)*spb) < confirm*spb {
		end++
	}
	lastErr := -1
	for k := 0; k < end && k < uartSyncFrames; k++ {
		if !clean(k) {
			lastErr = k
		}
	}
	sync := end // the first frame after an idle
	switch {
	case lastErr < 0 && end >= 2:
		return false // in sync from the first frame
	case lastErr >= 0 && lastErr+2 < end && clean(lastErr+1) && clean(lastErr+2):
		sync = lastErr + 1
	}
	if sync > uartSyncFrames || sync >= len(spans) {
		return false
	}
	for k := 0; k < sync; k++ {
		spans[k].Kind, spans[k].Text = "gap", "gap"
	}
	return true
}

// standardBauds are the common UART rates auto-baud snaps to.
var standardBauds = []float64{300, 600, 1200, 2400, 4800, 9600, 14400, 19200, 28800, 38400,
	57600, 76800, 115200, 230400, 250000, 460800, 500000, 921600, 1000000, 1500000, 2000000, 3000000}

// snapStandardBaud moves an inferred samples-per-bit onto the nearest standard
// rate when one lies within 1.5%: an estimate from a short record is a
// fraction of a sample off per bit, and the rate on the wire is almost always
// a standard one. Otherwise the estimate stands.
// TRLC-LINKS: REQ-SDS-018
func snapStandardBaud(spb, colTimeS float64) float64 {
	if !(spb > 0) || !(colTimeS > 0) {
		return spb
	}
	baud := 1 / (spb * colTimeS)
	for _, std := range standardBauds {
		if math.Abs(baud-std) <= 0.015*std {
			return 1 / (std * colTimeS)
		}
	}
	return spb
}
