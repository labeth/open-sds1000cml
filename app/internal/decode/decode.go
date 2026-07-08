// Package decode ports the web protocol decoders (internal/web/decode.js) to Go
// for the on-device LCD: UART / I2C / SPI over sampled 8-bit codes. Kept
// algorithm-faithful to decode.js so the two surfaces agree byte-for-byte.
package decode

import (
	"fmt"
	"math"
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

// ---- autodetect (ports decode.js scoreResult/clockScore/idleLevel/autodetect) --
