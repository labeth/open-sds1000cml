// Package decode ports the web protocol decoders (internal/web/decode.js) to Go
// for the on-device LCD: UART / I2C / SPI over sampled 8-bit codes. Kept
// algorithm-faithful to decode.js so the two surfaces agree byte-for-byte.
package decode

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Span is one decoded token spanning sample indices [I0,I1].
type Span struct {
	I0, I1 int
	Text   string
	Kind   string // data|start|stop|addr|rw|ack|nak|frame-error|parity-error|gap
	Val    int
}

// Result is a decode outcome.
type Result struct {
	OK     bool
	Error  string
	Proto  string
	Spans  []Span
	Text   string
	Bytes  []int
	Baud   int
	SPB    float64 // samples per bit / cols per clock
	Thr    float64 // threshold code
	Margin float64 // SPI: mean |sample−threshold|/halfAmp (autodetect CPHA tiebreak)
}

func hex2(b int) string { return fmt.Sprintf("%02X", b&0xff) }

func popcount(v int) int {
	// UNSIGNED shift: an arithmetic >> on a negative int shifts in sign bits
	// and never reaches 0 — an infinite loop. A hostile UART bit count can set
	// the sign bit of the assembled word (found by the decoder fuzz DoS).
	u := uint64(v)
	c := 0
	for u != 0 {
		c += int(u & 1)
		u >>= 1
	}
	return c
}

// FmtByte renders a byte in the requested format (hex/dec/bin/ascii).
func FmtByte(v int, format string) string {
	switch format {
	case "dec":
		return fmt.Sprint(v & 0xff)
	case "bin":
		return fmt.Sprintf("%08b", v&0xff)
	case "ascii":
		if v >= 32 && v < 127 {
			return string(rune(v))
		}
		return "."
	case "both": // hex + printable char (LCD font is ASCII, so use ' not the web's ·)
		if v >= 32 && v < 127 {
			return hex2(v) + "'" + string(rune(v))
		}
		return hex2(v)
	default:
		return hex2(v)
	}
}

type edge struct {
	i, dir int
	x      float64
}

type sliced struct {
	ok         bool
	reason     string
	n          int
	codes      []uint8
	threshold  float64
	lowRail    float64
	highRail   float64
	amp        float64
	thHi, thLo float64
	level      []int8
	edges      []edge
}

// sliceChannel builds the threshold/hysteresis + level/edge model for one
// channel (decode.js sliceChannel). thr overrides the auto midpoint when set.
func sliceChannel(codes []uint8, thr float64, haveThr bool) sliced {
	const hystFrac = 0.20
	const minAmp = 20.0
	n := len(codes)
	var h [256]float64
	valid := 0
	for _, v := range codes {
		h[v]++
		valid++
	}
	if valid < 8 {
		return sliced{reason: "no/too-few valid samples"}
	}
	noiseFloor := math.Max(1, 0.001*float64(valid))
	gmin := 0
	for gmin < 255 && h[gmin] < noiseFloor {
		gmin++
	}
	gmax := 255
	for gmax > 0 && h[gmax] < noiseFloor {
		gmax--
	}
	if gmax <= gmin {
		return sliced{reason: "flat/no transitions"}
	}
	mid0 := float64(gmin+gmax) / 2
	var lw, ls, hw, hs float64
	for c := gmin; c <= gmax; c++ {
		if float64(c) <= mid0 {
			lw += h[c] * float64(c)
			ls += h[c]
		} else {
			hw += h[c] * float64(c)
			hs += h[c]
		}
	}
	lowRail := float64(gmin)
	if ls != 0 {
		lowRail = lw / ls
	}
	highRail := float64(gmax)
	if hs != 0 {
		highRail = hw / hs
	}
	amp := highRail - lowRail
	if amp < minAmp {
		return sliced{reason: fmt.Sprintf("amplitude %.0f < %g", amp, minAmp)}
	}
	threshold := (lowRail + highRail) / 2
	if haveThr {
		threshold = thr
	}
	band := hystFrac * amp / 2
	thHi, thLo := threshold+band, threshold-band
	level := make([]int8, n)
	var edges []edge
	cur := -1
	for i := 0; i < n; i++ {
		v := float64(codes[i])
		nl := cur
		switch {
		case cur < 0:
			if v >= threshold {
				nl = 1
			} else {
				nl = 0
			}
		case cur == 0 && v >= thHi:
			nl = 1
		case cur == 1 && v <= thLo:
			nl = 0
		}
		if cur >= 0 && nl != cur {
			frac := float64(i)
			if p := i - 1; p >= 0 && codes[i] != codes[p] {
				frac = float64(p) + (threshold-float64(codes[p]))/(float64(codes[i])-float64(codes[p]))
			}
			d := -1
			if nl > cur {
				d = 1
			}
			edges = append(edges, edge{i, d, frac})
		}
		cur = nl
		level[i] = int8(nl)
	}
	return sliced{ok: true, n: n, codes: codes, threshold: threshold, lowRail: lowRail,
		highRail: highRail, amp: amp, thHi: thHi, thLo: thLo, level: level, edges: edges}
}

func logicAt(s sliced, x float64) int {
	i := int(math.Round(x))
	if i < 0 || i >= s.n {
		return -1
	}
	if float64(s.codes[i]) >= s.threshold {
		return 1
	}
	return 0
}

func minEdgeGap(edges []edge, dirOk func(int) bool) float64 {
	prev := -1
	min := math.Inf(1)
	for _, e := range edges {
		if !dirOk(e.dir) {
			continue
		}
		if prev >= 0 && float64(e.i-prev) < min {
			min = float64(e.i - prev)
		}
		prev = e.i
	}
	return min
}

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
	need := int(math.Ceil(float64(bits+2) * spb))
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
				if sb >= 0 && sb != idle {
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

// I2CCfg / SPICfg configure those decoders.
type I2CCfg struct {
	Format    string
	Threshold float64
	HaveThr   bool
}
type SPICfg struct {
	CPOL, CPHA bool
	MSB        bool // bit order; true = MSB-first (default)
	Format     string
	Threshold  float64
	HaveThr    bool
}

// DecodeI2C decodes I2C given SCL + SDA codes (decode.js decodeI2C).
func DecodeI2C(scl, sda []uint8, colTimeS float64, cfg I2CCfg) Result {
	CL := sliceChannel(scl, cfg.Threshold, cfg.HaveThr)
	DA := sliceChannel(sda, cfg.Threshold, cfg.HaveThr)
	if !CL.ok {
		return Result{Proto: "i2c", Error: "SCL " + CL.reason}
	}
	if !DA.ok {
		return Result{Proto: "i2c", Error: "SDA " + DA.reason}
	}
	n := CL.n
	if DA.n < n {
		n = DA.n
	}
	risings := 0
	for _, e := range CL.edges {
		if e.dir > 0 && e.i < n {
			risings++
		}
	}
	if risings < 2 {
		return Result{Proto: "i2c", Error: "no SCL clock edges"}
	}
	var clEdges []edge
	for _, e := range CL.edges {
		if e.i < n {
			clEdges = append(clEdges, e)
		}
	}
	colsPerClock := minEdgeGap(clEdges, func(d int) bool { return d > 0 })
	if colsPerClock < 3 {
		return Result{Proto: "i2c", Error: fmt.Sprintf("%.1f cols/clock; too few samples/bit", colsPerClock)}
	}
	var spans []Span
	var bytes []int
	var toks []string
	cl, da := -1, -1
	inTxn, expectAddr := false, false
	bitCount, val, bitStart := 0, 0, 0
	for i := 0; i < n; i++ {
		l, d := int(CL.level[i]), int(DA.level[i])
		pcl, pda := cl, da
		cl, da = l, d
		if pcl < 0 || pda < 0 {
			continue
		}
		if cl == 1 && pda == 1 && da == 0 { // START
			spans = append(spans, Span{i, i, "S", "start", 0})
			toks = append(toks, "START")
			inTxn, expectAddr, bitCount, val = true, true, 0, 0
			continue
		}
		if cl == 1 && pda == 0 && da == 1 { // STOP
			spans = append(spans, Span{i, i, "P", "stop", 0})
			toks = append(toks, "STOP")
			inTxn, bitCount = false, 0
			continue
		}
		if pcl == 0 && cl == 1 && inTxn { // sample on SCL rising
			bit := logicAt(DA, float64(i))
			if bit < 0 {
				continue
			}
			if bitCount < 8 {
				if bitCount == 0 {
					bitStart, val = i, 0
				}
				val = (val << 1) | bit
				bitCount++
				if bitCount == 8 {
					if expectAddr {
						addr := val >> 1
						spans = append(spans, Span{bitStart, i, hex2(addr), "addr", addr})
						rw := "W"
						if val&1 != 0 {
							rw = "R"
						}
						spans = append(spans, Span{i, i, rw, "rw", 0})
						toks = append(toks, hex2(addr), rw)
						expectAddr = false
					} else {
						spans = append(spans, Span{bitStart, i, FmtByte(val, cfg.Format), "data", val})
						toks = append(toks, FmtByte(val, cfg.Format))
						bytes = append(bytes, val)
					}
				}
			} else { // 9th clock = ACK/NAK
				k, t := "ack", "A"
				if bit != 0 {
					k, t = "nak", "N"
				}
				spans = append(spans, Span{i, i, t, k, 0})
				if bit == 0 {
					toks = append(toks, "ACK")
				} else {
					toks = append(toks, "NAK")
				}
				bitCount, val = 0, 0
			}
		}
	}
	if inTxn {
		spans = append(spans, Span{n - 1, n - 1, "(no STOP)", "frame-error", 0})
	}
	return Result{OK: true, Proto: "i2c", Spans: spans, Text: strings.Join(toks, " "),
		Bytes: bytes, SPB: colsPerClock, Thr: CL.threshold}
}

// DecodeSPI decodes SPI given CLK + DATA codes, no chip-select (decode.js decodeSPI).
func DecodeSPI(clk, data []uint8, colTimeS float64, cfg SPICfg) Result {
	CK := sliceChannel(clk, cfg.Threshold, cfg.HaveThr)
	DA := sliceChannel(data, cfg.Threshold, cfg.HaveThr)
	if !CK.ok {
		return Result{Proto: "spi", Error: "CLK " + CK.reason}
	}
	if !DA.ok {
		return Result{Proto: "spi", Error: "DATA " + DA.reason}
	}
	n := CK.n
	if DA.n < n {
		n = DA.n
	}
	var eIn []edge
	for _, e := range CK.edges {
		if e.i < n {
			eIn = append(eIn, e)
		}
	}
	if len(eIn) < 2 {
		return Result{Proto: "spi", Error: "no CLK edges"}
	}
	halfGap := minEdgeGap(eIn, func(int) bool { return true })
	if halfGap < 3 {
		return Result{Proto: "spi", Error: fmt.Sprintf("%.1f cols/edge; too few samples/bit", halfGap)}
	}
	cpol, cpha := 0, 0
	if cfg.CPOL {
		cpol = 1
	}
	if cfg.CPHA {
		cpha = 1
	}
	sampleRising := cpol == cpha
	gapReset := halfGap * 3
	var spans []Span
	var bytes []int
	var toks []string
	ck := -1
	bitCount, val, bitStart, lastSample := 0, 0, 0, -1
	halfAmp := DA.amp / 2
	if halfAmp <= 0 {
		halfAmp = 1
	}
	var mSum float64
	mN := 0
	for i := 0; i < n; i++ {
		l := int(CK.level[i])
		pck := ck
		ck = l
		if pck < 0 {
			continue
		}
		rising := pck == 0 && ck == 1
		falling := pck == 1 && ck == 0
		if (sampleRising && rising) || (!sampleRising && falling) {
			bit := logicAt(DA, float64(i))
			if bit < 0 {
				continue
			}
			mSum += math.Min(1, math.Abs(float64(DA.codes[i])-DA.threshold)/halfAmp)
			mN++
			if lastSample >= 0 && float64(i-lastSample) > gapReset && bitCount > 0 {
				bitCount, val = 0, 0 // idle gap → new frame
			}
			lastSample = i
			if bitCount == 0 {
				bitStart, val = i, 0
			}
			if cfg.MSB {
				val = (val << 1) | bit
			} else {
				val |= bit << bitCount
			}
			bitCount++
			if bitCount == 8 {
				spans = append(spans, Span{bitStart, i, FmtByte(val, cfg.Format), "data", val & 0xff})
				toks = append(toks, FmtByte(val, cfg.Format))
				bytes = append(bytes, val&0xff)
				bitCount, val = 0, 0
			}
		}
	}
	margin := 0.0
	if mN > 0 {
		margin = mSum / float64(mN)
	}
	return Result{OK: true, Proto: "spi", Spans: spans, Text: strings.Join(toks, " "),
		Bytes: bytes, SPB: halfGap * 2, Thr: CK.threshold, Margin: margin}
}

// ---- autodetect (ports decode.js scoreResult/clockScore/idleLevel/autodetect) --

// scoreResult ranks competing protocol/role hypotheses. The discriminators are
// structural: I2C's START/STOP framing is hard to forge, UART's auto-baud only
// locks on clean bit gaps + stop bits, SPI has no framing (weakest, the fallback).
func scoreResult(r Result) float64 {
	if !r.OK {
		return -1e9
	}
	switch r.Proto {
	case "i2c":
		addrs, acks, datas, starts := 0, 0, 0, 0
		for _, s := range r.Spans {
			switch s.Kind {
			case "addr":
				addrs++
			case "ack", "nak":
				acks++
			case "data":
				datas++
			case "start":
				starts++
			}
		}
		if addrs == 0 { // no addressed device -> a channel-swap mis-read, not real I2C
			return -1e9
		}
		return float64(addrs*100 + acks*30 + datas*20 + starts*5)
	case "uart":
		if len(r.Bytes) == 0 {
			return -1e9
		}
		ferr, perr := 0, 0
		for _, s := range r.Spans {
			switch s.Kind {
			case "frame-error":
				ferr++
			case "parity-error":
				perr++
			}
		}
		return float64(len(r.Bytes)*10 - ferr*35 - perr*18 + 15)
	case "spi":
		if len(r.Bytes) == 0 {
			return -1e9
		}
		printable := 0
		for _, b := range r.Bytes {
			if b >= 0x20 && b < 0x7f {
				printable++
			}
		}
		pf := float64(printable) / float64(len(r.Bytes))
		// no framing -> weakest; Margin breaks CPHA, printable breaks bit-order.
		return float64(len(r.Bytes)*2) + r.Margin*10 + pf*6
	}
	return -1e9
}

type clockInfo struct {
	ok      bool
	uniFrac float64
	edges   int
	s       sliced
}

// clockScore measures how clock-like a channel is, robust to idle gaps: the
// dominant half-period is a low percentile of the edge gaps (ignoring big idle
// gaps); uniFrac is the fraction of gaps that ARE that half-period (~1 for a
// clock, low for a data line whose edges land on data-dependent bit boundaries).
func clockScore(codes []uint8) clockInfo {
	s := sliceChannel(codes, 0, false)
	if !s.ok || len(s.edges) < 6 {
		return clockInfo{s: s}
	}
	gaps := make([]int, 0, len(s.edges)-1)
	for k := 1; k < len(s.edges); k++ {
		gaps = append(gaps, s.edges[k].i-s.edges[k-1].i)
	}
	sorted := append([]int(nil), gaps...)
	sort.Ints(sorted)
	hp := float64(sorted[len(sorted)*20/100])
	if hp <= 0 {
		return clockInfo{s: s}
	}
	tol := math.Max(0.4*hp, 2.5) // floor: ±1-sample quantization eats a fast clock's band
	uni := 0
	for _, g := range gaps {
		if math.Abs(float64(g)-hp) <= tol {
			uni++
		}
	}
	return clockInfo{ok: true, uniFrac: float64(uni) / float64(len(gaps)), edges: len(s.edges), s: s}
}

// idleLevel is the rail a channel rests on most (a clock idles at its CPOL rail).
func idleLevel(s sliced) int {
	if !s.ok {
		return 0
	}
	hi, lo := 0, 0
	for _, l := range s.level {
		if l == 1 {
			hi++
		} else if l == 0 {
			lo++
		}
	}
	if hi > lo {
		return 1
	}
	return 0
}

// Autodetect tries every plausible protocol / channel-role / sub-setting against
// the two channels (c1=index 0, c2=index 1) and returns the best-scoring decoded
// Result, formatted per `format`. A Result with Proto=="off" means nothing matched.
func Autodetect(c1, c2 []uint8, colTimeS float64, format string) Result {
	chans := [2][]uint8{c1, c2}
	var active []int
	for k := 0; k < 2; k++ {
		if chans[k] == nil {
			continue
		}
		if s := sliceChannel(chans[k], 0, false); s.ok && len(s.edges) >= 2 {
			active = append(active, k)
		}
	}
	best := Result{Proto: "off", Error: "no protocol matched"}
	if len(active) == 0 {
		best.Error = "no active signal"
		return best
	}
	bestScore := -1e8
	consider := func(r Result) {
		if sc := scoreResult(r); sc > bestScore {
			bestScore, best = sc, r
		}
	}

	clk := [2]clockInfo{clockScore(c1), clockScore(c2)}
	u0, u1 := clk[0].uniFrac, clk[1].uniFrac
	hi, lo := math.Max(u0, u1), math.Min(u0, u1)
	clockedPair := len(active) >= 2 && hi > 0.72 && hi > lo+0.12
	isClocky := func(k int) bool { return clk[k].uniFrac > 0.78 && clk[k].edges >= 40 }

	// UART — async single-wire. Skip on a clocked pair (that's SPI) or a lone clock
	// (else auto-baud invents bogus 0x55s from the square wave).
	if !clockedPair {
		for _, k := range active {
			if !isClocky(k) {
				consider(DecodeUART(chans[k], colTimeS, UARTCfg{Bits: 8, Parity: "none", Format: format}))
			}
		}
	}
	if len(active) >= 2 {
		for _, ord := range [][2]int{{0, 1}, {1, 0}} { // I2C: scoring resolves SCL/SDA order
			consider(DecodeI2C(chans[ord[0]], chans[ord[1]], colTimeS, I2CCfg{Format: format}))
		}
		if clockedPair {
			ci := 0
			if u1 > u0 {
				ci = 1
			}
			di := 1 - ci
			cpol := idleLevel(clk[ci].s)
			for _, cpha := range []bool{false, true} {
				for _, msb := range []bool{true, false} { // msb first on a binary tie (SPI default)
					consider(DecodeSPI(chans[ci], chans[di], colTimeS,
						SPICfg{CPOL: cpol == 1, CPHA: cpha, MSB: msb, Format: format}))
				}
			}
		}
	}
	return best
}
