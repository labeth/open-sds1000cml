// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math"
	"strconv"
	"strings"
)

// The infrared remote protocols beyond NEC, SIRC, RC-5 and RC-6 that IRMP
// (sigrok's ir_irmp) reads most often: Samsung32 and Samsung, JVC, NEC16,
// NEC42, Kaseikyo (Panasonic), Matsushita and Denon (Sharp). Each is a
// pulse-distance code: a pulse and a pause per bit, the pause's length the
// bit. A frame's address and command are units, as IRMP reports them.
// Mirrored step for step by decode_irmp.js.

// IR multi protocols by IRMP's numbers (the decode's Variant).
const (
	IRMatsushita = 4
	IRKaseikyo   = 5
	IRSamsung    = 3
	IRDenon      = 8
	IRSamsung32  = 10
	IRJVC        = 20
	IRNEC16      = 27
	IRNEC42      = 28
)

// IRMPNames name the IR multi protocols.
var IRMPNames = map[int]string{IRMatsushita: "MATSUSH", IRKaseikyo: "KASEIKYO", IRSamsung: "SAMSUNG", IRDenon: "DENON",
	IRSamsung32: "SAMSG32", IRJVC: "JVC", IRNEC16: "NEC16", IRNEC42: "NEC42"}

// irmpTiming is a protocol's pulse and pauses (us) and its bits.
type irmpTiming struct{ pulse, zero, one float64 }

// irmpTimings are the IR multi protocols' bit timings.
var irmpTimings = map[int]irmpTiming{IRSamsung32: {550, 550, 1650}, IRSamsung: {550, 550, 1650}, IRJVC: {560, 560, 1690},
	IRNEC16: {560, 560, 1690}, IRNEC42: {560, 560, 1690}, IRKaseikyo: {423, 423, 1269}, IRMatsushita: {872, 872, 2616},
	IRDenon: {310, 745, 1780}}

// irDenonRepeatUS is the longest a Denon frame's inverted repeat waits after
// it (us), from the frame's last mark to the repeat's first.
const irDenonRepeatUS = 70000

// MessageMaxS is the longest a message of the decoded bus can take, in
// seconds, where its protocol bounds it and the bits it carries move the
// length a lot; 0 otherwise. An IR Denon pair: two frames of 15 bits, each
// up to a pulse and a 1's pause, and the repeat's wait before the second
// (about 133 ms: AUTO framed on the pair it saw, and a longer one lost its
// repeat to the screen's end).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func MessageMaxS(r Result) float64 {
	if r.Proto == "irmp" && r.FrameBits == IRDenon {
		tm := irmpTimings[IRDenon]
		return (2*15*(tm.pulse+tm.one) + irDenonRepeatUS) * 1e-6
	}
	return 0
}

// irWithin reports d within frac of want.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func irWithin(d, want, frac float64) bool { return d >= want*(1-frac) && d <= want*(1+frac) }

// irmpBits reads pulse-distance bits from mark i on: each mark a pulse of
// the timing's (within 40 %), the pause after it a 0 or a 1 (within 30 %);
// it stops at the first mark or pause that is neither, at most max bits. It
// returns the bits and the index of the mark after the last bit's pulse.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func irmpBits(m []irMark, i int, tm irmpTiming, max int, us func(float64) float64) ([]int, int) {
	bits := make([]int, 0, max)
	for ; i+1 < len(m) && len(bits) < max; i++ {
		if !irWithin(us(m[i].x1-m[i].x0), tm.pulse, 0.4) {
			break
		}
		sp := us(m[i+1].x0 - m[i].x1)
		switch {
		case irWithin(sp, tm.zero, 0.3):
			bits = append(bits, 0)
		case irWithin(sp, tm.one, 0.3):
			bits = append(bits, 1)
		default:
			return bits, i
		}
	}
	return bits, i
}

// lsbOf is n bits from b[at], least significant first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func lsbOf(b []int, at, n int) int {
	v := 0
	for k := 0; k < n; k++ {
		v |= b[at+k] << k
	}
	return v
}

// DecodeIRMP decodes the IR multi protocols on a receiver's output or an
// emitter's drive: a frame's leader names its protocol (NEC's 9 ms and 4.5
// ms: 16 bits JVC, 8 bits, a sync and 8 NEC16, 42 bits NEC42, 32 bits NEC
// itself, left to the NEC decoder; 4.5 ms and 4.5 ms: 32 bits Samsung32, 16
// bits, a sync and 20 Samsung; 3.4 ms and 1.7 ms Kaseikyo's 48 bits; 3.5 ms
// twice Matsushita's 24); Denon has none, its 15 bits come twice, the
// second with its command inverted, and a JVC frame's repeats come without
// one. Each frame opens with its protocol's name ("sof"), then the address
// ("A") and command ("C") units as IRMP reports them (Samsung's id above
// its command, Kaseikyo's first genre above its 12 command bits). A frame
// whose check fails (Samsung's command inverse, NEC42's inverses,
// Kaseikyo's parities, Denon's inverted repeat) is flagged. Variant
// (FrameBits) is the first frame's protocol, IRMP's number. OK needs a
// frame whose checks hold.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func DecodeIRMP(codes []uint8, colTimeS float64, cfg LineCfg) Result {
	S := sliceChannel(codes, cfg.Threshold, cfg.HaveThr)
	if !S.ok {
		return Result{Proto: "irmp", Error: S.reason}
	}
	if !(colTimeS > 0) {
		return Result{Proto: "irmp", Error: "no sample time"}
	}
	m, high := irMarks(S, colTimeS)
	us := func(d float64) float64 { return d * colTimeS * 1e6 }
	o := newWireOut(S.n, S.sc)
	good, first, unit := false, 0, 0.0
	lastJVC := -1.0 // where the last JVC frame ended, for its repeats
	frame := func(proto int, x0, x1 float64, addr, cmd int, ok bool, name string) {
		o.add(x0, x0, name, "sof", proto, false)
		kind, ka, kc := "data", "A", "C"
		if !ok {
			kind, ka, kc = "frame-error", "!A", "!C"
		} else {
			good = true
		}
		mid := (x0 + x1) / 2
		o.add(x0, mid, labelHex(ka, addr), kind, addr, true)
		o.add(mid, x1, labelHex(kc, cmd), kind, cmd, true)
		if first == 0 {
			first = proto
			unit = irmpTimings[proto].pulse
		}
	}
	for i := 0; i+1 < len(m); i++ {
		lead, sp := us(m[i].x1-m[i].x0), us(m[i+1].x0-m[i].x1)
		x0 := m[i].x0
		switch {
		case irWithin(lead, 9000, 0.12) && irWithin(sp, 4500, 0.2):
			tm := irmpTimings[IRJVC]
			b, j := irmpBits(m, i+1, tm, 8, us)
			if len(b) == 8 && j+1 < len(m) && irWithin(us(m[j].x1-m[j].x0), tm.pulse, 0.4) &&
				irWithin(us(m[j+1].x0-m[j].x1), 4500, 0.2) {
				c, k := irmpBits(m, j+1, tm, 8, us)
				if len(c) == 8 {
					frame(IRNEC16, x0, m[k].x1, lsbOf(b, 0, 8), lsbOf(c, 0, 8), true, IRMPNames[IRNEC16])
					i = k
				}
				continue
			}
			b, j = irmpBits(m, i+1, tm, 42, us)
			switch len(b) {
			case 16:
				frame(IRJVC, x0, m[j].x1, lsbOf(b, 0, 4), lsbOf(b, 4, 12), true, IRMPNames[IRJVC])
				lastJVC = m[j].x1
			case 42:
				a, ai, c, ci := lsbOf(b, 0, 13), lsbOf(b, 13, 13), lsbOf(b, 26, 8), lsbOf(b, 34, 8)
				frame(IRNEC42, x0, m[j].x1, a, c, a^ai == 0x1fff && c^ci == 0xff, IRMPNames[IRNEC42])
			default:
				continue // NEC itself (32 bits), or none
			}
			i = j
		case irWithin(lead, 4500, 0.15) && irWithin(sp, 4500, 0.2):
			tm := irmpTimings[IRSamsung32]
			b, j := irmpBits(m, i+1, tm, 16, us)
			if len(b) == 16 && j+1 < len(m) && irWithin(us(m[j].x1-m[j].x0), tm.pulse, 0.4) &&
				irWithin(us(m[j+1].x0-m[j].x1), 4500, 0.2) {
				c, k := irmpBits(m, j+1, tm, 20, us)
				if len(c) == 20 {
					id, cmd, inv := lsbOf(c, 0, 4), lsbOf(c, 4, 8), lsbOf(c, 12, 8)
					frame(IRSamsung, x0, m[k].x1, lsbOf(b, 0, 16), cmd|id<<8, cmd^inv == 0xff, IRMPNames[IRSamsung])
					i = k
				}
				continue
			}
			b, j = irmpBits(m, i+1, tm, 32, us)
			if len(b) == 32 {
				frame(IRSamsung32, x0, m[j].x1, lsbOf(b, 0, 16), lsbOf(b, 16, 16), true, IRMPNames[IRSamsung32])
				i = j
			}
		case irWithin(lead, 3380, 0.15) && irWithin(sp, 1690, 0.2):
			b, j := irmpBits(m, i+1, irmpTimings[IRKaseikyo], 48, us)
			if len(b) != 48 {
				continue
			}
			var by [6]int
			for k := range by {
				by[k] = lsbOf(b, 8*k, 8)
			}
			maker := by[0] | by[1]<<8
			par := (maker ^ maker>>4 ^ maker>>8 ^ maker>>12) & 15
			ok := by[2]&15 == par && by[5] == by[2]^by[3]^by[4]
			frame(IRKaseikyo, x0, m[j].x1, maker, lsbOf(b, 28, 12)|lsbOf(b, 20, 4)<<12, ok, IRMPNames[IRKaseikyo])
			i = j
		case irWithin(lead, 3488, 0.15) && irWithin(sp, 3488, 0.2):
			b, j := irmpBits(m, i+1, irmpTimings[IRMatsushita], 24, us)
			if len(b) != 24 {
				continue
			}
			frame(IRMatsushita, x0, m[j].x1, lsbOf(b, 12, 12), lsbOf(b, 0, 12), true, IRMPNames[IRMatsushita])
			i = j
		case lastJVC >= 0 && irWithin(lead, 560, 0.4) && us(x0-lastJVC) < 30000:
			// a JVC repeat: its 16 bits without a leader
			b, j := irmpBits(m, i, irmpTimings[IRJVC], 16, us)
			if len(b) != 16 {
				continue
			}
			frame(IRJVC, x0, m[j].x1, lsbOf(b, 0, 4), lsbOf(b, 4, 12), true, "REP")
			lastJVC = m[j].x1
			i = j
		case irWithin(lead, 310, 0.4) && (i == 0 || us(x0-m[i-1].x1) > 10000):
			// Denon: 15 bits, most significant first, then again inverted
			tm := irmpTimings[IRDenon]
			b, j := irmpBits(m, i, tm, 15, us)
			if len(b) != 15 || j+1 >= len(m) || us(m[j+1].x0-m[j].x1) > irDenonRepeatUS {
				continue
			}
			b2, k := irmpBits(m, j+1, tm, 15, us)
			a, c := bitsInt(b[:5]), bitsInt(b[5:])
			ok := len(b2) == 15 && bitsInt(b2[:5]) == a && bitsInt(b2[5:])^c == 0x3ff && c&1 == 0
			if len(b2) == 15 {
				j = k
			}
			frame(IRDenon, x0, m[j].x1, a, c, ok, IRMPNames[IRDenon])
			i = j
		}
	}
	if len(o.spans) == 0 {
		return Result{Proto: "irmp", Error: "no IR multi frame"}
	}
	res := Result{OK: good, Proto: "irmp", Spans: o.spans, Text: strings.Join(o.toks, " "), Bytes: o.units,
		Thr: S.threshold, Inverted: high, FrameBits: first}
	if unit > 0 {
		res.SPB = unit * 1e-6 / colTimeS
		res.Baud = int(math.Round(1e6 / unit))
	}
	if !good {
		res.Error = "no IR multi frame whose checks hold"
	}
	return res
}

// labelHex is a label and a value in upper-case hex (fmt's "%s%X").
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func labelHex(label string, v int) string {
	b := make([]byte, 0, len(label)+8)
	b = append(b, label...)
	at := len(b)
	b = strconv.AppendInt(b, int64(v), 16)
	for i := at; i < len(b); i++ {
		if c := b[i]; c >= 'a' && c <= 'f' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}
