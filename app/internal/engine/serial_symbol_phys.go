// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"math/bits"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
)

// physSymbolSpec compiles the line timing of the sigrok protocols beyond
// the first thirty into the symbol trigger (symbolSpec's other protocols):
// false when the request's rate is missing or out of the block's reach.
// Each frame start and unit is laid out as the decoder of the protocol
// reads it, bit for bit in the block's bit source.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-257
func physSymbolSpec(p SerialParams, s *symSpec) bool {
	if p.Baud <= 0 && p.Proto != serSDCard && p.Proto != serPDI && p.Proto != serCJTAG {
		return false
	}
	T := 1 / float64(max(1, p.Baud))
	inf := math.Inf(1)
	us, ms := 1e-6, 1e-3
	q8 := func(d float64) uint32 { return uint32(math.Round(d / (sramcapture.SymbolClockNs * 1e-9) * 256)) }
	set := func(mode, lead int, longest float64, data uint8) {
		s.cfg.Mode, s.cfg.LeadLen = uint8(mode), uint8(lead)
		s.tab = newSymTab(longest, decode.SymLeadNone, data)
	}
	w8 := func(int) int { return 8 }
	s.width = w8
	byteLSB := func(b *symBits, _ int, u symUnit) bool { b.field(u.value, u.mask, 8, false); return true }
	freeOnly := func(b *symBits, _ int, u symUnit) bool { return u.mask == 0 }
	// a bit as Manchester half levels: first half v (high is a 1)
	manch := func(b *symBits, v, care bool) { b.put(v, care); b.put(!v, care) }
	switch p.Proto {
	case serQuad, serStepper:
		// the bit is line B at line A's rising edges (forward: an encoder's
		// B low, a stepper's DIR high); a motion opens after A rested two
		// step periods
		set(sramcapture.SymbolClocked, 1, 2*T, decode.SymDataSep)
		s.cfg.TwoLines, s.cfg.InvA, s.cfg.InvB = true, false, p.Proto == serQuad
		s.tab.both(true, 2*T, inf, leadStep0, decode.SymDataRestart)
		s.tab.both(false, 2*T, inf, leadStep0, decode.SymDataRestart)
		s.width = func(int) int { return 1 }
		s.period = 1
		s.unit = func(b *symBits, _ int, u symUnit) bool { b.field(u.value, u.mask, 1, false); return true }
	case serPWM:
		// a period's high time read as a bit: 1 inside the window of the
		// asked duty cycles (a prefix of the duty byte's bits), else 0
		set(sramcapture.SymbolClass, 0, T, decode.SymDataBit0)
		s.tab.data(false, 0, inf, decode.SymDataSep)
		s.noFrame, s.period = true, 1
		window := [2]float64{-1, -1}
		s.unit = func(b *symBits, _ int, u symUnit) bool {
			if u.mask == 0 {
				b.skip(1)
				return true
			}
			free := ^u.mask & 0xff
			if free&(free+1) != 0 {
				return false // not a prefix: no one window of high time
			}
			w := [2]float64{float64(u.value) / 256 * T, float64(u.value+free+1) / 256 * T}
			if window[0] >= 0 && window != w {
				return false // the table holds one window
			}
			window = w
			s.tab.data(true, w[0], w[1]-1e-12, decode.SymDataBit1)
			b.fixed(1)
			return true
		}
	case serRCEncode:
		// high pulses short (0) or long (1); a frame opens after the sync's
		// low of eight shorts or more
		set(sramcapture.SymbolClass, 1, 8*T, decode.SymDataSep)
		s.tab.data(true, 0, 2*T, decode.SymDataBit0)
		s.tab.data(true, 2*T, 6*T, decode.SymDataBit1)
		s.tab.both(false, 8*T, inf, leadStep0, decode.SymDataRestart)
		if p.Bits == 24 { // EV1527: a byte of pulses, first bit first
			s.unit, s.period = byteLSB, 8
			break
		}
		// PT2262: a trit of two pulses, 0 short short, 1 long long, F short long
		s.width = func(int) int { return 2 }
		s.period = 2
		s.unit = func(b *symBits, _ int, u symUnit) bool {
			switch {
			case u.mask == 0:
				b.skip(2)
			case u.mask != 3 || u.value > 2:
				return false
			case u.value == 0:
				b.fixed(0, 0)
			case u.value == 1:
				b.fixed(1, 1)
			default:
				b.fixed(0, 1)
			}
			return true
		}
	case serOOK:
		// Oregon's Manchester bursts at half bits (Baud: the line's bit
		// rate); protocol 2.1 sends each bit inverted then as is
		half := T / 2
		set(sramcapture.SymbolLevel, 0, 4*half, decode.SymDataSep)
		s.cfg.UnitQ8, s.sliding = q8(half), true
		v21 := p.Bits == 21
		bit := func(b *symBits, v, care bool) {
			if v21 {
				manch(b, !v, care)
			}
			manch(b, v, care)
		}
		s.width = func(int) int { return 4 }
		s.start = func(b *symBits, _ []symUnit) bool {
			ones := 8
			if v21 {
				ones = 4
			}
			for i := 0; i < ones; i++ {
				bit(b, true, true)
			}
			for _, v := range []bool{false, true, false, true} {
				bit(b, v, true)
			}
			return true
		}
		s.unit = func(b *symBits, _ int, u symUnit) bool {
			for k := 0; k < 4; k++ {
				bit(b, u.value>>k&1 != 0, u.mask>>k&1 != 0)
			}
			return true
		}
	case serMiller:
		// edges in half bits: a message opens at its first edge after an
		// idle of two and a half bits; its first unit's first bit is the
		// start 0 (an edge then none), as the decoder's bytes hold it
		half := T / 2
		set(sramcapture.SymbolEdge, 1, 2.5*T, decode.SymDataSep)
		s.cfg.UnitQ8 = q8(half)
		s.tab.both(true, 2.5*T, inf, leadStep0, decode.SymDataRestart)
		s.tab.both(false, 2.5*T, inf, leadStep0, decode.SymDataRestart)
		prev, known := 0, true
		s.period = 16
		s.start = func(b *symBits, _ []symUnit) bool {
			prev, known = 0, true // after the idle: a 0's edge at its start
			return true
		}
		s.unit = func(b *symBits, i int, u symUnit) bool {
			if i == 0 && len(b.val) == 0 {
				known = false // at any unit boundary: the bit before is not known
			}
			for k := 0; k < 8; k++ {
				v, care := u.value>>k&1, u.mask>>k&1 != 0
				switch {
				case care && v == 1:
					b.fixed(0, 1)
				case care:
					b.put(prev == 0, known) // a 0 after a 0 has its edge at its start
					b.fixed(0)
				default:
					b.skip(2)
				}
				prev, known = v, care
			}
			return true
		}
	case serMorse:
		// dots and dashes, the gap after each a 0 inside a letter and a 1
		// after its last element; a word gap opens a frame
		u := T
		set(sramcapture.SymbolClass, 0, 5*u, decode.SymDataBit0)
		s.tab.data(true, 2*u, inf, decode.SymDataBit1)
		s.tab.data(false, 2*u, 5*u, decode.SymDataBit1)
		s.tab.data(false, 5*u, inf, decode.SymDataEnd)
		s.custom = func(units []symUnit, anchored bool) (symBits, int, bool) {
			var b symBits
			if !anchored {
				b.fixed(1) // the letter before ended here
			}
			for i, x := range units {
				if x.mask == 0 && i == len(units)-1 {
					b.skip(1) // any letter: its first element
					break
				}
				code := decode.MorseOf(byte(x.value))
				if x.mask != 0xff || code == "" {
					return b, 0, false
				}
				for k := 0; k < len(code); k++ {
					b.put(code[k] == '-', true)
					b.put(k == len(code)-1, true)
				}
			}
			if anchored {
				return b, 0, true
			}
			return b, 2, true // at a gap's place: an even bit of the frame
		}
	case serDCF77:
		// a second's pulse of 100 ms (0) or 200 ms (1) (Baud scales the
		// second); the minute's gap opens a frame at second 0
		sec := T
		set(sramcapture.SymbolClass, 1, 2*sec, decode.SymDataSep)
		s.tab.data(true, 0.04*sec, 0.16*sec, decode.SymDataBit0)
		s.tab.data(true, 0.16*sec, 0.26*sec, decode.SymDataBit1)
		s.tab.both(false, 1.5*sec, inf, leadStep0, decode.SymDataRestart)
		fields := [][2]int{{21, 7}, {29, 6}, {36, 6}, {42, 3}, {45, 5}, {50, 8}}
		s.width = func(i int) int {
			if i < len(fields) {
				return fields[i][1]
			}
			return 0
		}
		s.oneFrame, s.dropFreeTail = true, true
		s.custom = func(units []symUnit, _ bool) (symBits, int, bool) {
			var bits [59]int
			var care [59]bool
			last := -1
			full := func(i int) bool { return i < len(units) && units[i].mask == 1<<fields[i][1]-1 }
			for i, x := range units {
				if i >= len(fields) {
					return symBits{}, 0, false
				}
				f := fields[i]
				for k := 0; k < f[1]; k++ {
					bits[f[0]+k], care[f[0]+k] = x.value>>k&1, x.mask>>k&1 != 0
				}
				last = f[0] + f[1] - 1
			}
			// a field's parity is asked with the whole field
			par := func(at, from, to int) {
				p := 0
				for q := from; q <= to; q++ {
					p ^= bits[q]
				}
				bits[at], care[at] = p, true
				last = max(last, at)
			}
			if full(0) {
				par(28, 21, 27)
			}
			if full(1) {
				par(35, 29, 34)
			}
			if full(2) && full(3) && full(4) && full(5) {
				par(58, 36, 57)
			}
			var b symBits
			for i := 0; i <= last; i++ {
				b.put(bits[i] != 0, care[i])
			}
			return b, 0, true
		}
	case serEM4100:
		// Manchester at half bits, a 1 high then low: the last frame's stop
		// 0 and the nine 1s of the header, then rows of four bits and their
		// even parity, two to a byte (a tag sends its frames back to back)
		half := T / 2
		set(sramcapture.SymbolLevel, 0, 4*half, decode.SymDataSep)
		s.cfg.UnitQ8, s.sliding = q8(half), true
		s.start = func(b *symBits, _ []symUnit) bool {
			manch(b, false, true)
			for i := 0; i < 9; i++ {
				manch(b, true, true)
			}
			return true
		}
		s.unit = func(b *symBits, _ int, u symUnit) bool {
			for _, sh := range []int{4, 0} {
				nib, m := u.value>>sh&15, u.mask>>sh&15
				p := 0
				for k := 3; k >= 0; k-- {
					manch(b, nib>>k&1 != 0, m>>k&1 != 0)
					p ^= nib >> k & 1
				}
				manch(b, p != 0, m == 15)
			}
			return true
		}
	case serEM4305, serT55xx:
		fc := T // Baud: the field's frequency
		if p.Inverted {
			// read off the carrier: the first field stop opens a command
			stop := 40 * fc
			if p.Proto == serT55xx {
				stop = 20 * fc
			}
			set(sramcapture.SymbolClass, 1, stop, decode.SymDataSep)
			s.cfg.InvA = false
			s.tab.both(true, stop, inf, leadStep0, decode.SymDataRestart)
			s.tab.both(false, stop, inf, leadStep0, decode.SymDataRestart)
			s.unit = freeOnly
			break
		}
		s.cfg.InvA = false
		if p.Proto == serEM4305 {
			// the field's level each 16 field clocks: a 1 field twice, a 0
			// field then its gap; the first field stop opens a command
			set(sramcapture.SymbolLevel, 1, 40*fc, decode.SymDataSep)
			s.cfg.UnitQ8 = q8(16 * fc)
			s.tab.both(false, 40*fc, inf, leadStep0, decode.SymDataRestart)
			bit := func(b *symBits, v, care bool) { b.put(true, true); b.put(v, care) }
			write := false
			s.width = func(i int) int {
				if i == 0 {
					return 3
				}
				return 8 // a write's address: 4 (s.unit refuses more)
			}
			s.start = func(b *symBits, units []symUnit) bool {
				write = len(units) > 0 && units[0].mask == 7 && units[0].value == 2
				return true
			}
			s.unit = func(b *symBits, i int, u symUnit) bool {
				switch {
				case i == 0: // the logic 0, the command and its parity
					bit(b, false, true)
					for k := 2; k >= 0; k-- {
						bit(b, u.value>>k&1 != 0, u.mask>>k&1 != 0)
					}
					bit(b, onesOf(u.value&7)&1 != 0, u.mask&7 == 7)
				case i == 1 && write: // the address, two zeros, parity
					if u.value > 15 || u.mask > 15 {
						return false
					}
					for k := 0; k < 4; k++ {
						bit(b, u.value>>k&1 != 0, u.mask>>k&1 != 0)
					}
					bit(b, false, true)
					bit(b, false, true)
					bit(b, onesOf(u.value&15)&1 != 0, u.mask&15 == 15)
				default: // a byte and its parity
					for k := 7; k >= 0; k-- {
						bit(b, u.value>>k&1 != 0, u.mask>>k&1 != 0)
					}
					bit(b, onesOf(u.value&255)&1 != 0, u.mask&255 == 255)
				}
				return true
			}
			break
		}
		// T55xx: the field's on-times between gaps, 16 to 31 field clocks a
		// 0, 48 to 63 a 1; 64 of field then a gap open a command
		set(sramcapture.SymbolClass, 2, 64*fc, decode.SymDataSep)
		s.tab.data(true, 16*fc, 31*fc, decode.SymDataBit0)
		s.tab.data(true, 48*fc, 63*fc, decode.SymDataBit1)
		s.tab.both(true, 64*fc, inf, leadStep0, decode.SymDataRestart)
		s.tab.lead(false, 8*fc, 64*fc, leadStep1)
		widths := []int{2, 1, 8, 8, 8, 8, 3}
		if p.Bits == 70 {
			widths = []int{2, 8, 8, 8, 8, 1, 8, 8, 8, 8, 3}
		}
		s.width = func(i int) int {
			if i < len(widths) {
				return widths[i]
			}
			return 0
		}
		s.unit = func(b *symBits, i int, u symUnit) bool {
			w := s.width(i)
			if w == 0 {
				return false
			}
			b.field(u.value, u.mask, w, true)
			return true
		}
	case serSWIM:
		// lows: under half a bit a 1, else a 0; an idle of a bit and a half
		// opens a frame: the header, a 3-bit command or a byte, parity, ack.
		// A command is its frame's first six bits, so a byte frame whose top
		// bits read as the command, its parity and an ack fires it too
		set(sramcapture.SymbolClass, 1, 1.5*T, decode.SymDataSep)
		s.cfg.InvA = false
		s.tab.data(false, 0, 0.5*T, decode.SymDataBit1)
		s.tab.data(false, 0.5*T, 1.5*T, decode.SymDataBit0)
		s.tab.both(true, 1.5*T, inf, leadStep0, decode.SymDataRestart)
		s.tab.both(false, 1.5*T, inf, leadStep0, decode.SymDataRestart)
		s.oneFrame = true
		s.unit = func(b *symBits, i int, u symUnit) bool {
			if i != 0 {
				return false // one frame a request
			}
			if u.mask == 0 {
				b.skip(1)
				return true
			}
			n := 8
			if u.mask <= 7 && u.value <= 7 {
				n = 3 // a command
			}
			b.skip(1) // the header: who sends
			b.field(u.value, u.mask, n, true)
			b.put(onesOf(u.value)&1 != 0, u.mask == 1<<n-1)
			b.fixed(1) // acknowledged
			return true
		}
	case serSDQ:
		set(sramcapture.SymbolClass, 1, 1.2*T, decode.SymDataSep)
		s.cfg.InvA = false
		s.tab.data(false, 0, 0.5*T, decode.SymDataBit1)
		s.tab.data(false, 0.5*T, 1.2*T, decode.SymDataBit0)
		s.tab.both(false, 1.2*T, inf, leadStep0, decode.SymDataRestart)
		s.unit, s.period = byteLSB, 8
	case serPJDL:
		// the level each data time; an idle, a pad and its zero open a
		// frame at the next pad: the frame's sync pads, then each byte's pad,
		// zero and bits
		mode := p.Bits
		if mode < 1 || mode > 4 {
			mode = 1
		}
		times := [5][2]float64{{}, {44, 116}, {40, 92}, {28, 88}, {26, 60}}[mode]
		data, pad := times[0]*us, times[1]*us
		tol := func(w float64) (float64, float64) {
			return math.Min(w*0.9, w-1.5*us), math.Max(w*1.1, w+1.5*us)
		}
		idle := pad + 9*data + 4*us
		set(sramcapture.SymbolLevel, 3, idle, decode.SymDataSep)
		s.cfg.UnitQ8, s.cfg.InvA = q8(data), false
		s.tab.both(false, idle, inf, leadStep0, decode.SymDataRestart)
		lo, hi := tol(pad)
		s.tab.lead(true, lo, hi, leadStep1)
		lo, hi = tol(data)
		s.tab.lead(false, lo, hi, leadStep2)
		padBits := 0 // the samples a pad holds
		for float64(padBits)+0.5 < pad/data {
			padBits++
		}
		sync := func(b *symBits) {
			for i := 0; i < padBits; i++ {
				b.fixed(1)
			}
			b.fixed(0)
		}
		s.start = func(b *symBits, _ []symUnit) bool {
			for i := 0; i < 3; i++ {
				sync(b)
			}
			return true
		}
		s.unit = func(b *symBits, i int, u symUnit) bool {
			if i > 0 {
				sync(b)
			}
			b.field(u.value, u.mask, 8, false)
			return true
		}
	case serDSI:
		// half bits of 1/1200 s, a 1 at the marks' level first; the idle
		// before a frame opens it at its start bit's middle
		half := 1 / 1200.0
		set(sramcapture.SymbolLevel, 1, 4*half, decode.SymDataSep)
		s.cfg.UnitQ8, s.cfg.InvA = q8(half), !p.Inverted
		s.tab.both(false, 3.5*half, inf, leadStep0, decode.SymDataRestart)
		s.start = func(b *symBits, _ []symUnit) bool { b.fixed(1); return true }
		s.unit = func(b *symBits, _ int, u symUnit) bool {
			for k := 7; k >= 0; k-- {
				manch(b, u.value>>k&1 != 0, u.mask>>k&1 != 0)
			}
			return true
		}
	case serQi:
		// biphase mark at half bits of 2 kbit/s: a preamble of 1s, then
		// bytes of a start 0, eight bits, odd parity and a stop 1. The
		// preamble's 11 1s (its least) are more than a packet's bytes hold
		// (an FF byte, its parity and stop: ten), so the frame start fires
		// once a packet; with two bytes asked, as many as the window leaves.
		set(sramcapture.SymbolEdge, 0, 500*us, decode.SymDataSep)
		s.cfg.UnitQ8, s.sliding = q8(250*us), true
		bmc := func(b *symBits, v, care bool) { b.put(true, true); b.put(v, care) }
		s.start = func(b *symBits, units []symUnit) bool {
			room := 64 - 22*len(units)
			if len(units) == 0 {
				room -= 2 // the first byte's start 0
			}
			ones := min(11, room/2)
			if ones < 4 {
				return false
			}
			for i := 0; i < ones; i++ {
				bmc(b, true, true)
			}
			if len(units) == 0 {
				bmc(b, false, true)
			}
			return true
		}
		s.unit = func(b *symBits, _ int, u symUnit) bool {
			bmc(b, false, true)
			for k := 0; k < 8; k++ {
				bmc(b, u.value>>k&1 != 0, u.mask>>k&1 != 0)
			}
			bmc(b, onesOf(u.value&255)&1 == 0, u.mask&255 == 255)
			bmc(b, true, true)
			return true
		}
	case serSDCard:
		// CMD at CLK's rises: an idle of 1s, then the token's bytes, most
		// significant first, its first bit the start 0
		set(sramcapture.SymbolClocked, 0, T, decode.SymDataSep)
		s.cfg.TwoLines, s.sliding = true, true
		s.start = func(b *symBits, units []symUnit) bool {
			for i := 0; i < 8; i++ {
				b.fixed(1)
			}
			if len(units) == 0 {
				b.fixed(0) // the token's start bit
			}
			return true
		}
		s.unit = func(b *symBits, i int, u symUnit) bool {
			if i == 0 {
				b.field(u.value&^0x80, u.mask|0x80, 8, true)
				return true
			}
			b.field(u.value, u.mask, 8, true)
			return true
		}
	case serPDI:
		// DATA at PDI_CLK's rises: two idle 1s, then characters of a start
		// 0, eight bits, even parity and two stops
		set(sramcapture.SymbolClocked, 0, T, decode.SymDataSep)
		s.cfg.TwoLines, s.sliding = true, true
		s.start = func(b *symBits, units []symUnit) bool {
			b.fixed(1, 1)
			if len(units) == 0 { // a whole character's framing: any character
				b.fixed(0)
				b.skip(9)
				b.fixed(1, 1)
			}
			return true
		}
		s.unit = func(b *symBits, _ int, u symUnit) bool {
			b.fixed(0)
			b.field(u.value, u.mask, 8, false)
			b.put(onesOf(u.value&255)&1 != 0, u.mask&255 == 255)
			b.fixed(1, 1)
			return true
		}
	case serCJTAG:
		// TCKC at TMSC's rises: an escape's rises come with TCKC high, three
		// of the six edges that select the online activation code
		set(sramcapture.SymbolClocked, 0, T, decode.SymDataSep)
		s.cfg.Channel, s.cfg.TwoLines, s.sliding = uint8(p.ChB), true, true
		s.start = func(b *symBits, _ []symUnit) bool { b.fixed(0, 1, 1, 1, 0); return true }
		s.unit = freeOnly
	case serMaple:
		// SDCKA at SDCKB's falls: after SDCKB idled high, the start
		// pattern's four falls with SDCKA low
		set(sramcapture.SymbolClocked, 1, 4*T, decode.SymDataSep)
		s.cfg.Channel, s.cfg.TwoLines, s.cfg.ClockFall = uint8(p.ChB), true, true
		s.tab.both(true, 4*T, inf, leadStep0, decode.SymDataRestart)
		s.start = func(b *symBits, _ []symUnit) bool { b.fixed(0, 0, 0, 0); return true }
		s.unit = freeOnly
	case serCaliper:
		// DATA at CLK's rises after 5 ms of quiet: the count's two bytes and
		// the flags, least significant first
		quiet := math.Max(5*ms, 20*T)
		set(sramcapture.SymbolClocked, 1, quiet, decode.SymDataSep)
		s.cfg.TwoLines = true
		s.tab.both(true, quiet, inf, leadStep0, decode.SymDataRestart)
		s.tab.both(false, quiet, inf, leadStep0, decode.SymDataRestart)
		s.unit, s.period = byteLSB, 8
	case serIRMP:
		return irmpSymbolSpec(p, s, set)
	default:
		return false
	}
	return true
}

// onesOf is the 1 bits of v (v at least 0).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-257
func onesOf(v int) int { return bits.OnesCount(uint(v)) }

// irmpTiming is an IR multi protocol's frame as the symbol trigger reads
// it: the leader's mark and space (no mark: a frame opens after an idle of
// space), the pauses of a 0 and a 1 (us), the frame's bits and each unit's
// bits' places, least significant first.
type irmpFrame struct {
	mark, space, pulse, zero, one float64
	bits                          int
	units                         [][]int
	inverse                       [][2]int // a unit's inverse: unit, first place
}

// irmpPlaces is n places from at on (inc +1) or descending (inc -1).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-257
func irmpPlaces(at, n, inc int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = at + inc*i
	}
	return out
}

// irmpFrames are the IR multi protocols by IRMP's numbers.
var irmpFrames = map[int]irmpFrame{
	decode.IRSamsung32: {4500, 4500, 550, 550, 1650, 32, [][]int{irmpPlaces(0, 16, 1), irmpPlaces(16, 16, 1)}, nil},
	decode.IRJVC:       {9000, 4500, 560, 560, 1690, 16, [][]int{irmpPlaces(0, 4, 1), irmpPlaces(4, 12, 1)}, nil},
	decode.IRNEC16:     {9000, 4500, 560, 560, 1690, 16, [][]int{irmpPlaces(0, 8, 1), irmpPlaces(8, 8, 1)}, nil},
	decode.IRNEC42: {9000, 4500, 560, 560, 1690, 42, [][]int{irmpPlaces(0, 13, 1), irmpPlaces(26, 8, 1)},
		[][2]int{{0, 13}, {1, 34}}},
	decode.IRKaseikyo: {3380, 1690, 423, 423, 1269, 48, [][]int{irmpPlaces(0, 16, 1),
		append(irmpPlaces(28, 12, 1), irmpPlaces(20, 4, 1)...)}, nil},
	decode.IRMatsushita: {3488, 3488, 872, 872, 2616, 24, [][]int{irmpPlaces(12, 12, 1), irmpPlaces(0, 12, 1)}, nil},
	decode.IRSamsung: {4500, 4500, 550, 550, 1650, 36, [][]int{irmpPlaces(0, 16, 1),
		append(irmpPlaces(20, 8, 1), irmpPlaces(16, 4, 1)...)}, nil},
	decode.IRDenon: {0, 10000, 310, 745, 1780, 15, [][]int{irmpPlaces(4, 5, -1), irmpPlaces(14, 10, -1)}, nil},
}

// irmpSymbolSpec compiles an IR multi protocol (Bits: IRMP's number): the
// spaces' lengths are the bits, the leader's mark and space open a frame,
// and the address and command lie at their places in it; Samsung's command
// inverse and NEC42's inverses are asked with a whole unit.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-257
func irmpSymbolSpec(p SerialParams, s *symSpec, set func(mode, lead int, longest float64, data uint8)) bool {
	f, ok := irmpFrames[p.Bits]
	if !ok {
		return false
	}
	us := 1e-6
	inf := math.Inf(1)
	lead := 2
	longest := f.mark * 1.15 * us
	if f.mark == 0 {
		lead, longest = 1, f.space*us
	}
	set(sramcapture.SymbolClass, lead, longest, decode.SymDataSep)
	// marks are the low level of a receiver's output (Inverted: the high)
	s.tab.data(true, f.zero*0.7*us, f.zero*1.3*us, decode.SymDataBit0)
	s.tab.data(true, f.one*0.7*us, f.one*1.3*us, decode.SymDataBit1)
	if f.mark > 0 {
		s.tab.both(false, f.mark*0.85*us, f.mark*1.15*us, leadStep0, decode.SymDataRestart)
		s.tab.lead(true, f.space*0.8*us, f.space*1.2*us, leadStep1)
	} else {
		s.tab.both(true, f.space*us, inf, leadStep0, decode.SymDataRestart)
	}
	s.width = func(i int) int {
		if i < len(f.units) {
			return len(f.units[i])
		}
		return 0
	}
	s.oneFrame, s.dropFreeTail = true, true
	s.custom = func(units []symUnit, _ bool) (symBits, int, bool) {
		val := make([]bool, f.bits)
		care := make([]bool, f.bits)
		last := -1
		for i, u := range units {
			if i >= len(f.units) {
				return symBits{}, 0, false
			}
			places := f.units[i]
			for k, at := range places {
				val[at], care[at] = u.value>>k&1 != 0, u.mask>>k&1 != 0
				if care[at] {
					last = max(last, at)
				}
			}
			for _, inv := range f.inverse {
				if inv[0] == i && u.mask == 1<<len(places)-1 {
					for k := range places {
						val[inv[1]+k], care[inv[1]+k] = u.value>>k&1 == 0, true
						last = max(last, inv[1]+k)
					}
				}
			}
		}
		if p.Bits == decode.IRSamsung && len(units) > 1 && units[1].mask&0xff == 0xff {
			for k := 0; k < 8; k++ { // the command's inverse
				val[28+k], care[28+k] = units[1].value>>k&1 == 0, true
				last = max(last, 28+k)
			}
		}
		var b symBits
		for i := 0; i <= last; i++ {
			b.put(val[i], care[i])
		}
		return b, 0, true
	}
	return true
}
