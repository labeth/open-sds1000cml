// Package decode ports the web protocol decoders (internal/web/decode.js) to Go
// for the on-device LCD: UART / I2C / SPI over sampled 8-bit codes. Kept
// algorithm-faithful to decode.js so the two surfaces agree byte-for-byte.
// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math"
)

// Span is one decoded token spanning sample indices [I0,I1].
// TRLC-LINKS: REQ-SDS-018
type Span struct {
	I0, I1 int
	Text   string
	Kind   string // data|start|stop|addr|rw|ack|nak|frame-error|parity-error|gap
	Val    int
}

// Result is a decode outcome.
// TRLC-LINKS: REQ-SDS-018
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
	Src    string  // Autodetect: the roles and settings it chose, e.g. "C2" or "CLK C2 DATA C1 mode 0 MSB"
	Roles  Roles   // Autodetect: the same choice, for configuring a hardware decoder
	// Parity is the character framing a UART-framed protocol read
	// (Modbus RTU: "even", "odd" or "none").
	Parity string
	// Inverted reports a line read with its marks at the high level (the
	// IR decoders find the idle level themselves).
	Inverted bool
	// FrameBits is the bits of the first whole frame of a protocol whose
	// frames come in several lengths (SIRC 12, 15 or 20; Wiegand 26, 34...).
	FrameBits int
	// Cover is the share of the clock's cycles the decode's frames, idle and
	// resets explain (PS/2, SWD, MDIO): data an SPI bus clocks reads as a
	// few frames among many cycles none explains.
	Cover float64
	// Feature is the shortest run the decode read, in samples, where its
	// bit time does not tell it (a PWM line's narrowest high or low, a
	// stepper's STEP pulse); 0 otherwise.
	Feature float64
}

// FeatureSamples is the shortest feature a decode's bus holds, in samples: a
// bit of an NRZ line (UART, CAN, USB, FlexRay, the UART-framed protocols,
// J1850's short symbol), half a clock (I2C, SPI, PS/2, SWD, MDIO), half a bit
// of a line code (Manchester, MIL-1553, ARINC 429's return to zero, DALI,
// USB PD, S/PDIF, the IR codes), SENT's shortest pulse of four ticks, a
// WS281x bit's short high (a third of the bit), CEC's short low (a quarter of
// its bit), 1-Wire's 15 us within which a slot's low ends for a 1 (SPB is a
// microsecond; a 0 holds it 60 us), DHT's short high of 26 us, a Wiegand
// pulse (a twentieth of its bit interval), a quadrature encoder's quarter
// cycle, a stepper's STEP pulse and a PWM line's narrowest run (Feature),
// half a bit of OOK, Miller, EM4100, DSI and Qi, half a clock of the
// clocked buses and half a field clock of a reader's line, DCF77's 100 ms
// pulse, a SWIM 1's low (a bit's 2 of 22 clocks), an SDQ 1's quarter bit.
// 0 when the result names none (decode.js decodeFeatureSamples).
// TRLC-LINKS: REQ-SDS-018
func FeatureSamples(r Result) float64 {
	if !r.OK || !(r.SPB > 0) {
		return 0
	}
	switch r.Proto {
	case "i2c", "spi", "manchester", "mil1553", "arinc429", "dali", "usbpd", "spdif", "irnec", "irrc5", "irrc6",
		"ps2", "swd", "mdio":
		return r.SPB / 2
	case "sent":
		return 4 * r.SPB
	case "ws281x":
		return r.SPB / 3
	case "cec":
		return r.SPB / 4
	case "onewire":
		return 15 * r.SPB
	case "dht":
		return 26 * r.SPB
	case "wiegand":
		return r.SPB / 20
	case "quad":
		return r.SPB / 4 // a quarter cycle between A's and B's edges
	case "stepper", "pwm":
		return r.Feature
	case "ook", "miller", "em4100", "em4305", "t55xx", "dsi", "qi", "sdcard", "pdi", "cjtag", "maple", "caliper":
		return r.SPB / 2
	case "dcf77":
		return r.SPB / 10 // a 0's 100 ms pulse
	case "swim":
		return r.SPB / 11 // a 1's low: 2 of a bit's 22 SWIM clocks
	case "sdq":
		return r.SPB / 4
	}
	return r.SPB
}

// ShortestRun is the fewest samples between two edges of a two-level
// signal (sliced as the decoders slice it), 0 when it holds under three.
// TRLC-LINKS: REQ-SDS-018
func ShortestRun(codes []uint8) int {
	s := sliceChannel(codes, 0, false)
	if !s.ok || len(s.edges) < 3 {
		return 0
	}
	run := 0
	for k := 1; k < len(s.edges); k++ {
		if g := s.edges[k].i - s.edges[k-1].i; run == 0 || g < run {
			run = g
		}
	}
	return run
}

// EnvelopeRun bounds, from an ordered peak-detect envelope (each bucket's
// extremes as a pair in time order, sampleS apart), how far apart a line's
// edges are where it is busy: when most buckets that swing across the line
// come in stretches of envStretch or more in a row, each holding an edge,
// the line changes about once a bucket there, and EnvelopeRun is the mean
// spacing that allows, 4/3 of a bucket (8/3*sampleS), in seconds. A
// decimated record's samples average a line faster than that into a level
// (a 104 MHz SPI clock in 256 ns samples reads as a slow pulse). 0 when the
// envelope shows no such stretch: a line quiet, slow or seldom busy (an
// ARINC 429 word's 5 us half bits swing in single 1 us buckets). busy is
// the share of the buckets that swing across the line.
// TRLC-LINKS: REQ-SDS-214
func EnvelopeRun(codes []uint8, sampleS float64) (run, busy float64) {
	const (
		swingFrac  = 0.2  // a full swing reaches within this share of the swing of both extremes
		minFull    = 16   // full-swing buckets the envelope must hold
		envStretch = 4    // buckets in a row that make a stretch
		minShare   = 0.75 // of the full-swing buckets, the share in stretches
	)
	// The line's extremes, not its rails' means: an ARINC 429 leg's null level
	// between them, full of fast noise, is no swing across the line.
	if !(sampleS > 0) || len(codes) < 64 {
		return 0, 0
	}
	var hist [256]int
	for _, v := range codes {
		hist[v]++
	}
	pct := func(p float64) float64 {
		need, acc := int(p*float64(len(codes))), 0
		for v, k := range hist {
			if acc += k; acc > need {
				return float64(v)
			}
		}
		return 255
	}
	lo, hi := pct(0.02), pct(0.98)
	amp := hi - lo
	if amp < 20 {
		return 0, 0
	}
	n := len(codes) / 2
	nFull, inStretch, row := 0, 0, 0
	for k := 0; k <= n; k++ {
		full := false
		if k < n {
			a, b := float64(codes[2*k]), float64(codes[2*k+1])
			full = min(a, b) <= lo+swingFrac*amp && max(a, b) >= hi-swingFrac*amp
		}
		if full {
			nFull++
			row++
			continue
		}
		if row >= envStretch {
			inStretch += row
		}
		row = 0
	}
	busy = float64(nFull) / float64(n)
	if nFull < minFull || float64(inStretch) < minShare*float64(nFull) {
		return 0, busy
	}
	return 8.0 / 3 * sampleS, busy
}

// Names are the protocols as the screens name them.
var Names = map[string]string{"uart": "UART", "i2c": "I2C", "spi": "SPI", "manchester": "Manchester", "sent": "SENT",
	"canfd": "CAN", "mil1553": "MIL-1553", "arinc429": "ARINC 429", "usbls": "USB", "flexray": "FlexRay",
	"lin": "LIN", "dmx512": "DMX512", "midi": "MIDI", "modbus": "Modbus RTU", "onewire": "1-Wire", "ws281x": "WS281x",
	"dht": "DHT", "cec": "HDMI-CEC", "j1850": "J1850 VPW", "irnec": "IR NEC", "irsirc": "IR Sony", "irrc5": "IR RC-5",
	"irrc6": "IR RC-6", "dali": "DALI", "usbpd": "USB PD", "spdif": "S/PDIF", "ps2": "PS/2", "swd": "SWD", "mdio": "MDIO",
	"wiegand": "Wiegand", "quad": "Quadrature", "stepper": "Stepper", "pwm": "PWM", "rcencode": "RC encode", "ook": "OOK",
	"miller": "Miller", "morse": "Morse", "dcf77": "DCF77", "em4100": "EM4100", "em4305": "EM4305", "t55xx": "T55xx",
	"swim": "SWIM", "sdq": "SDQ", "pjdl": "PJON", "dsi": "DSI", "qi": "Qi", "sdcard": "SD card", "pdi": "PDI", "cjtag": "cJTAG",
	"maple": "Maple", "caliper": "Caliper", "irmp": "IR multi"}

// Roles is the channel assignment and SPI framing Autodetect chose: ChA is the
// line (or I2C SCL / SPI CLK), ChB the I2C SDA / SPI DATA, 0 = C1.
// TRLC-LINKS: REQ-SDS-018
type Roles struct {
	ChA, ChB        int
	CPOL, CPHA, MSB bool
}

// TRLC-LINKS: REQ-SDS-018
func invertLogic(codes []uint8) []uint8 {
	out := make([]uint8, len(codes))
	for i, v := range codes {
		out[i] = 255 - v
	}
	return out
}

// TRLC-LINKS: REQ-SDS-018
func hex2(b int) string { return fmt.Sprintf("%02X", b&0xff) }

// TRLC-LINKS: REQ-SDS-018
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
// TRLC-LINKS: REQ-SDS-018
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

// TRLC-LINKS: REQ-SDS-018
type edge struct {
	i, dir int
	x      float64
}

// TRLC-LINKS: REQ-SDS-018
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

// AutoThreshold is the slicing level the decoders use when none is set: the
// midpoint of the channel's low and high rails. The serial trigger gives it to
// the hardware matcher, so the FPGA slices the line where the decode does.
// ok is false for a channel without two clear levels.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-013
func AutoThreshold(codes []uint8) (float64, bool) {
	lo, hi, reason := rails(codes)
	return (lo + hi) / 2, reason == ""
}

// Extent is a channel's code range without its sparse noise tails (codes
// under one in a thousand samples at either end): the levels trigger
// qualifiers place their thresholds between. ok is false when the channel
// is flat or holds too few samples.
// TRLC-LINKS: REQ-SDS-011
func Extent(codes []uint8) (lo, hi int, ok bool) {
	var h [256]int
	for _, v := range codes {
		h[v]++
	}
	if len(codes) < 8 {
		return 0, 0, false
	}
	noiseFloor := max(1, len(codes)/1000)
	lo, hi = 0, 255
	for lo < 255 && h[lo] < noiseFloor {
		lo++
	}
	for hi > 0 && h[hi] < noiseFloor {
		hi--
	}
	return lo, hi, hi > lo
}

// rails finds a channel's low and high levels: the code histogram's extent
// without its sparse noise tails, split at the middle, each half's mean. The
// reason is set when there are too few samples or the swing is under minAmp.
// A line resting at one level and pulsed seldom (a Wiegand line's 50 us
// pulses, a fraction of a percent of a record, spread over the codes of their
// edges) holds no code of its other level as often as the noise floor: when
// the per-code tails leave no swing, they are cut where their counts add up
// to the floor instead, if the line holds its other level as a level does
// (heldBeyond): a quiet line's noise spikes are single samples
// (decode.js sliceChannel).
// TRLC-LINKS: REQ-SDS-018
func rails(codes []uint8) (lowRail, highRail float64, reason string) {
	var h [256]float64
	valid := 0
	for _, v := range codes {
		h[v]++
		valid++
	}
	if valid < 8 {
		return 0, 0, "no/too-few valid samples"
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
	lowRail, highRail, reason = railsWithin(&h, gmin, gmax)
	if reason == "" {
		return lowRail, highRail, ""
	}
	gmin, gmax = 0, 255
	for acc := h[0]; gmin < 255 && acc < noiseFloor; acc += h[gmin] {
		gmin++
	}
	for acc := h[255]; gmax > 0 && acc < noiseFloor; acc += h[gmax] {
		gmax--
	}
	if l, hi, r := railsWithin(&h, gmin, gmax); r == "" && heldBeyond(codes, (l+hi)/2) >= noiseFloor {
		return l, hi, ""
	}
	return lowRail, highRail, reason
}

// heldBeyond counts the samples of a line's rarer side of thr that lie in
// runs of three or more: a pulse holds its level, a noise spike does not.
// TRLC-LINKS: REQ-SDS-018
func heldBeyond(codes []uint8, thr float64) float64 {
	var held [2]int
	run, side := 0, -1
	for _, v := range codes {
		s := 0
		if float64(v) >= thr {
			s = 1
		}
		if s == side {
			run++
		} else {
			run, side = 1, s
		}
		if run == 3 {
			held[s] += 3
		} else if run > 3 {
			held[s]++
		}
	}
	return float64(min(held[0], held[1]))
}

// railsWithin is rails over the histogram's codes gmin..gmax.
// TRLC-LINKS: REQ-SDS-018
func railsWithin(h *[256]float64, gmin, gmax int) (lowRail, highRail float64, reason string) {
	const minAmp = 20.0
	if gmax <= gmin {
		return 0, 0, "flat/no transitions"
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
	lowRail = float64(gmin)
	if ls != 0 {
		lowRail = lw / ls
	}
	highRail = float64(gmax)
	if hs != 0 {
		highRail = hw / hs
	}
	if amp := highRail - lowRail; amp < minAmp {
		return lowRail, highRail, fmt.Sprintf("amplitude %.0f < %g", amp, minAmp)
	}
	return lowRail, highRail, ""
}

// sliceChannel builds the threshold/hysteresis + level/edge model for one
// channel (decode.js sliceChannel). thr overrides the auto midpoint when set.
// TRLC-LINKS: REQ-SDS-018
func sliceChannel(codes []uint8, thr float64, haveThr bool) sliced {
	const hystFrac = 0.20
	n := len(codes)
	lowRail, highRail, reason := rails(codes)
	if reason != "" {
		return sliced{reason: reason}
	}
	amp := highRail - lowRail
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

// TRLC-LINKS: REQ-SDS-018
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

// TRLC-LINKS: REQ-SDS-018
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

// FmtWord preserves wide UART/Manchester words instead of truncating to a byte.
// TRLC-LINKS: REQ-SDS-018
func FmtWord(v, bits int, format string) string {
	if bits <= 8 {
		return FmtByte(v, format)
	}
	if bits > 16 {
		bits = 16
	}
	v &= (1 << bits) - 1
	switch format {
	case "dec":
		return fmt.Sprint(v)
	case "bin":
		return fmt.Sprintf("%0*b", bits, v)
	case "ascii":
		if v >= 32 && v < 127 {
			return string(rune(v))
		}
		return "."
	}
	h := fmt.Sprintf("%0*X", (bits+3)/4, v)
	if format == "both" && v >= 32 && v < 127 {
		return h + "'" + string(rune(v))
	}
	return h
}

// snapRate is a bit rate read from a record's edges moved onto the nearest of
// a bus's standard rates within 1.5 % (a 1 Mbit/s MIL-1553 bus read 999 kb/s,
// a 10 Mbit/s FlexRay 9.88 or 10.1 Mb/s); otherwise it stands.
// TRLC-LINKS: REQ-SDS-018
func snapRate(baud int, rates ...float64) int {
	for _, r := range rates {
		if math.Abs(float64(baud)-r) <= 0.015*r {
			return int(math.Round(r))
		}
	}
	return baud
}
