package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// UARTCfg configures the UART decode. Baud=0 auto-infers; Bits default 8.
type UARTCfg struct {
	Baud      int
	Bits      int
	Parity    string // none|even|odd
	Format    string
	Threshold float64
	HaveThr   bool
}

// DecodeUART decodes 8N1-style UART on one channel's codes (decode.js decodeUART).
func DecodeUART(codes []uint8, colTimeS float64, cfg UARTCfg) Result {
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
		var gaps []float64
		for k := 1; k < len(S.edges); k++ {
			if g := float64(S.edges[k].i - S.edges[k-1].i); g >= 2 {
				gaps = append(gaps, g)
			}
		}
		if len(gaps) < 3 {
			return Result{Proto: "uart", Error: "too few edges / cannot infer baud"}
		}
		sort.Float64s(gaps)
		spb = gaps[int(float64(len(gaps))*0.1)]
		sum, cnt := 0.0, 0
		for _, g := range gaps {
			if math.Abs(g-spb) <= 0.35*spb {
				sum += g
				cnt++
			}
		}
		if cnt > 0 {
			spb = sum / float64(cnt)
		}
		good := 0
		for _, g := range gaps {
			if m := math.Round(g / spb); m >= 1 && math.Abs(g-m*spb) <= 0.35*spb {
				good++
			}
		}
		if float64(good) < 0.7*float64(len(gaps)) {
			return Result{Proto: "uart", Error: "baud ambiguous — set it explicitly"}
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
				spans = append(spans, Span{start, i1, pfx + FmtByte(val, cfg.Format), kind, val})
				toks = append(toks, pfx+FmtByte(val, cfg.Format))
				bytes = append(bytes, val)
				i = int(math.Round(float64(start)+float64(bits+1+pc)*spb)) + 1
				continue
			}
		}
		i++
	}
	return Result{OK: true, Proto: "uart", Spans: spans, Text: strings.Join(toks, " "),
		Bytes: bytes, Baud: int(math.Round(baud)), SPB: spb, Thr: S.threshold}
}
