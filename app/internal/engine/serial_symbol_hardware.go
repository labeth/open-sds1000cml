// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
)

// symbolProtocol reports a protocol the qualify image's symbol trigger
// takes (ADR-SYMBOL-TRIGGER): the buses of timed pulses on one or two lines.
// MIDI is plain UART characters and runs on the general image's receiver.
// TRLC-LINKS: REQ-SDS-013
func symbolProtocol(proto int) bool {
	return proto >= serLIN && proto <= serWiegand && proto != serMIDI
}

// symbolTwoLines reports the clocked two-wire protocols (line B in use).
// TRLC-LINKS: REQ-SDS-013
func symbolTwoLines(proto int) bool {
	return proto == serPS2 || proto == serSWD || proto == serMDIO || proto == serWiegand
}

// symBits is a frame's line bits in arrival order, each either required to
// equal its value (care) or free.
type symBits struct{ val, care []bool }

// TRLC-LINKS: REQ-SDS-013
func (b *symBits) put(v, care bool) {
	b.val = append(b.val, v && care)
	b.care = append(b.care, care)
}

// fixed appends required bits (0 or 1 each).
// TRLC-LINKS: REQ-SDS-013
func (b *symBits) fixed(bits ...int) {
	for _, v := range bits {
		b.put(v != 0, true)
	}
}

// skip appends n free bits.
// TRLC-LINKS: REQ-SDS-013
func (b *symBits) skip(n int) {
	for i := 0; i < n; i++ {
		b.put(false, false)
	}
}

// field appends n bits of value under mask, MSB or LSB first.
// TRLC-LINKS: REQ-SDS-013
func (b *symBits) field(value, mask, n int, msb bool) {
	for i := 0; i < n; i++ {
		k := i
		if msb {
			k = n - 1 - i
		}
		b.put(value>>k&1 != 0, mask>>k&1 != 0)
	}
}

// manchester appends n bits as two half-bit levels each: one is the levels
// of a 1 (a 0 is their complement).
// TRLC-LINKS: REQ-SDS-013
func (b *symBits) manchester(value, mask, n int, msb bool, one [2]int) {
	for i := 0; i < n; i++ {
		k := i
		if msb {
			k = n - 1 - i
		}
		care, v := mask>>k&1 != 0, value>>k&1 != 0
		first := one[0] != 0
		if !v {
			first = !first
		}
		b.put(first, care)
		b.put(!first, care)
	}
}

// bmc appends n bits in the edge domain of biphase mark: every bit cell
// opens with an edge, and a 1 has another mid-cell.
// TRLC-LINKS: REQ-SDS-013
func (b *symBits) bmc(value, mask, n int, msb bool) {
	for i := 0; i < n; i++ {
		k := i
		if msb {
			k = n - 1 - i
		}
		b.put(true, true)
		b.put(value>>k&1 != 0, mask>>k&1 != 0)
	}
}

// symTab builds the run table: durations in seconds become prescaled tick
// counts; a bucket of the table takes a window's code when its middle count
// lies inside the window.
type symTab struct {
	t        [sramcapture.SymbolTableSize]uint8
	prescale int
}

// newSymTab is a table whose longest finite window bound is longest
// seconds, every entry the given leader and data codes.
// TRLC-LINKS: REQ-SDS-013
func newSymTab(longest float64, lead, data uint8) *symTab {
	t := &symTab{}
	clocks := longest / (sramcapture.SymbolClockNs * 1e-9)
	for t.prescale < 7 && clocks/float64(int(1)<<t.prescale) >= decode.SymbolMaxCount-1 {
		t.prescale++
	}
	for i := range t.t {
		t.t[i] = lead<<4 | data
	}
	return t
}

// count is a duration's prescaled tick count.
// TRLC-LINKS: REQ-SDS-013
func (t *symTab) count(s float64) float64 {
	if math.IsInf(s, 1) {
		return math.Inf(1)
	}
	w := math.Round(s / (sramcapture.SymbolClockNs * 1e-9))
	return math.Max(0, math.Floor((w-1)/float64(int(1)<<t.prescale)))
}

// each calls f with every index of level whose bucket's middle count lies
// in [lo, hi] seconds (hi +Inf: open-ended).
// TRLC-LINKS: REQ-SDS-013
func (t *symTab) each(level bool, lo, hi float64, f func(int)) {
	clo, chi := t.count(lo), t.count(hi)
	for i := 0; i < 1024; i++ {
		e, m := i>>6, i&63
		var mid float64
		if e == 0 {
			mid = float64(m)
		} else {
			first := float64(int(64+m) << (e - 1))
			mid = first + (float64(int(1)<<(e-1))-1)/2
		}
		if e == 15 && m == 63 {
			mid = decode.SymbolMaxCount // saturated runs
		}
		if mid >= clo && mid <= chi {
			idx := i
			if level {
				idx |= 1024
			}
			f(idx)
		}
	}
}

// lead sets the leader code of a window; data its data code.
// TRLC-LINKS: REQ-SDS-013
func (t *symTab) lead(level bool, lo, hi float64, code uint8) {
	t.each(level, lo, hi, func(i int) { t.t[i] = t.t[i]&15 | code<<4 })
}

// TRLC-LINKS: REQ-SDS-013
func (t *symTab) data(level bool, lo, hi float64, code uint8) {
	t.each(level, lo, hi, func(i int) { t.t[i] = t.t[i]&0xf0 | code })
}

// both sets a window's leader and data codes.
// TRLC-LINKS: REQ-SDS-013
func (t *symTab) both(level bool, lo, hi float64, lead, data uint8) {
	t.lead(level, lo, hi, lead)
	t.data(level, lo, hi, data)
}

// symUnit is a pattern unit: value under mask.
type symUnit struct{ value, mask int }

// symSpec is one protocol's compilation: the block's settings and table,
// the bits a frame start requires (start, given the frame's units: RC-5's
// second start bit carries a command bit), each unit's bits, the width of
// the unit (for its value) and the bits a unit takes at any unit boundary
// (period; 0 where units are not all alike). sliding: the window carries the
// frame start itself (no leader).
type symSpec struct {
	cfg     sramcapture.SymbolTriggerConfig
	tab     *symTab
	start   func(b *symBits, units []symUnit) bool
	unit    func(b *symBits, i int, u symUnit) bool
	width   func(i int) int
	period  int
	sliding bool
}

const (
	leadStep0 = decode.SymLeadStep
	leadStep1 = decode.SymLeadStep + 1
	leadStep2 = decode.SymLeadStep + 2
)

// uartChar appends a character: start bit, data LSB first, parity (even,
// odd or none; free unless all data bits are given), stop bit.
// TRLC-LINKS: REQ-SDS-013
func uartChar(b *symBits, u symUnit, bits int, parity string) {
	b.fixed(0)
	b.field(u.value, u.mask, bits, false)
	if parity == "even" || parity == "odd" {
		full := u.mask&(1<<bits-1) == 1<<bits-1
		ones := 0
		for v := u.value & (1<<bits - 1); v != 0; v &= v - 1 {
			ones++
		}
		pb := ones & 1
		if parity == "odd" {
			pb ^= 1
		}
		b.put(pb != 0, full)
	}
	b.fixed(1)
}

// symbolSpec compiles a protocol's line timing: false when its rate is out
// of the block's reach.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func symbolSpec(p SerialParams) (symSpec, bool) {
	T := 1 / float64(p.Baud)
	inf := math.Inf(1)
	us, ms := 1e-6, 1e-3
	q8 := func(s float64) uint32 { return uint32(math.Round(s / (sramcapture.SymbolClockNs * 1e-9) * 256)) }
	w8 := func(int) int { return 8 }
	s := symSpec{cfg: sramcapture.SymbolTriggerConfig{Enabled: true, Channel: uint8(p.ChA), InvA: p.Inverted, InvB: p.Inverted, UnitQ8: 2048}, width: w8}
	none := func(*symBits, []symUnit) bool { return true }
	s.start = none
	set := func(mode int, lead int, longest float64, data uint8) {
		s.cfg.Mode, s.cfg.LeadLen = uint8(mode), uint8(lead)
		s.tab = newSymTab(longest, decode.SymLeadNone, data)
	}
	switch p.Proto {
	case serLIN, serDMX, serModbus:
		bits, parity, frame := 8, "none", 10
		if p.Proto == serModbus {
			parity = p.Parity
			if parity == "" {
				parity = "even"
			}
			if parity != "none" {
				frame = 11
			}
		}
		s.cfg.UnitQ8, s.cfg.FrameUnits, s.period = q8(T), uint8(frame), frame
		s.unit = func(b *symBits, _ int, u symUnit) bool { uartChar(b, u, bits, parity); return true }
		switch p.Proto {
		case serLIN:
			// break: low 11 bit times or more; delimiter; the sync character 0x55
			set(sramcapture.SymbolLevel, 2, 20*T, decode.SymDataSep)
			s.tab.both(false, 11*T, inf, leadStep0, decode.SymDataRestart)
			s.tab.lead(true, 0.5*T, 20*T, leadStep1)
			s.start = func(b *symBits, _ []symUnit) bool { uartChar(b, symUnit{0x55, 0xff}, 8, "none"); return true }
		case serDMX:
			// break of 22 bit times (88 us) or more, mark after break of 1.5 or more
			set(sramcapture.SymbolLevel, 2, 22*T, decode.SymDataSep)
			s.tab.both(false, 0.95*22*T, inf, leadStep0, decode.SymDataRestart)
			s.tab.lead(true, 1.5*T, inf, leadStep1)
		default:
			// idle of 3.5 characters (11 bits each), 1.75 ms above 19200 Bd
			gap := 3.4 * 11 * T
			if p.Baud > 19200 {
				gap = 1.7 * ms
			}
			set(sramcapture.SymbolLevel, 1, gap, decode.SymDataSep)
			s.tab.both(true, gap, inf, leadStep0, decode.SymDataRestart)
		}
		if s.cfg.UnitQ8 < 16*256 {
			return s, false // framed characters need 16 receiver clocks a bit
		}
	case serOneWire:
		set(sramcapture.SymbolClass, 2, 410*us, decode.SymDataEnd)
		reset, presLo, presHi, one, zero := 410*us, 60*us, 300*us, 15*us, 150*us
		if p.Baud >= 50000 { // overdrive
			reset, presLo, presHi, one, zero = 40*us, 6*us, 30*us, 2*us, 20*us
		}
		s.tab.both(true, 0, inf, decode.SymLeadSep, decode.SymDataSep)
		s.tab.data(false, 0, one, decode.SymDataBit1)
		s.tab.data(false, one, zero, decode.SymDataBit0)
		s.tab.lead(false, presLo, presHi, leadStep1)
		s.tab.both(false, reset, inf, leadStep0, decode.SymDataRestart)
		s.unit = func(b *symBits, _ int, u symUnit) bool { b.field(u.value, u.mask, 8, false); return true }
		s.period = 8
	case serWS281x:
		set(sramcapture.SymbolClass, 1, 9*T, decode.SymDataEnd)
		s.tab.both(false, 9*T, inf, leadStep0, decode.SymDataRestart)
		s.tab.data(false, 0, 9*T, decode.SymDataSep)
		s.tab.data(true, 0, 0.5*T, decode.SymDataBit0)
		s.tab.data(true, 0.5*T, 2*T, decode.SymDataBit1)
		s.unit = func(b *symBits, _ int, u symUnit) bool { b.field(u.value, u.mask, 8, true); return true }
		s.period = 8
	case serDHT:
		set(sramcapture.SymbolClass, 3, 500*us, decode.SymDataEnd)
		s.tab.both(false, 500*us, inf, leadStep0, decode.SymDataRestart)
		s.tab.lead(true, 5*us, 60*us, decode.SymLeadSep)
		s.tab.lead(false, 60*us, 100*us, leadStep1)
		s.tab.lead(true, 60*us, 100*us, leadStep2)
		s.tab.data(false, 30*us, 70*us, decode.SymDataSep)
		s.tab.data(true, 10*us, 48*us, decode.SymDataBit0)
		s.tab.data(true, 48*us, 95*us, decode.SymDataBit1)
		s.unit = func(b *symBits, _ int, u symUnit) bool { b.field(u.value, u.mask, 8, true); return true }
		s.period = 8
	case serCEC:
		set(sramcapture.SymbolClass, 2, 3.9*ms, decode.SymDataEnd)
		s.tab.both(false, 3.5*ms, 3.9*ms, leadStep0, decode.SymDataRestart)
		s.tab.lead(true, 0.4*ms, 1.2*ms, leadStep1)
		s.tab.data(false, 0.4*ms, 0.8*ms, decode.SymDataBit1)
		s.tab.data(false, 1.3*ms, 1.7*ms, decode.SymDataBit0)
		s.tab.data(true, 0.3*ms, 2.4*ms, decode.SymDataSep)
		// a block: eight data bits MSB first, end of message, acknowledge
		s.unit = func(b *symBits, _ int, u symUnit) bool { b.field(u.value, u.mask, 8, true); b.skip(2); return true }
		s.period = 10
	case serJ1850:
		set(sramcapture.SymbolClass, 1, 239*us, decode.SymDataEnd)
		s.tab.both(true, 163*us, 239*us, leadStep0, decode.SymDataRestart)
		s.tab.data(true, 34*us, 96*us, decode.SymDataBit1)
		s.tab.data(true, 96*us, 163*us, decode.SymDataBit0)
		s.tab.data(false, 34*us, 96*us, decode.SymDataBit0)
		s.tab.data(false, 96*us, 163*us, decode.SymDataBit1)
		s.unit = func(b *symBits, _ int, u symUnit) bool { b.field(u.value, u.mask, 8, true); return true }
		s.period = 8
	case serIRNEC:
		set(sramcapture.SymbolClass, 2, 10*ms, decode.SymDataEnd)
		s.tab.both(false, 8*ms, 10*ms, leadStep0, decode.SymDataRestart)
		s.tab.lead(true, 4*ms, 5*ms, leadStep1)
		s.tab.data(false, 0.4*ms, 0.75*ms, decode.SymDataSep)
		s.tab.data(true, 0.4*ms, 0.75*ms, decode.SymDataBit0)
		s.tab.data(true, 1.3*ms, 2.0*ms, decode.SymDataBit1)
		s.unit = func(b *symBits, _ int, u symUnit) bool { b.field(u.value, u.mask, 8, false); return true }
		s.period = 8
	case serIRSIRC:
		set(sramcapture.SymbolClass, 2, 2.7*ms, decode.SymDataEnd)
		s.tab.both(false, 2.2*ms, 2.7*ms, leadStep0, decode.SymDataRestart)
		s.tab.lead(true, 0.4*ms, 0.8*ms, leadStep1)
		s.tab.data(false, 0.4*ms, 0.9*ms, decode.SymDataBit0)
		s.tab.data(false, 0.9*ms, 1.5*ms, decode.SymDataBit1)
		s.tab.data(true, 0.4*ms, 0.9*ms, decode.SymDataSep)
		addr := 5
		if p.Bits == 15 {
			addr = 8
		}
		s.width = func(i int) int { return []int{7, addr, 8, 0}[min(i, 3)] }
		s.unit = func(b *symBits, i int, u symUnit) bool {
			w := s.width(i)
			if w == 0 || (i == 2 && p.Bits != 20) {
				return false
			}
			b.field(u.value, u.mask, w, false)
			return true
		}
	case serIRRC5:
		// half bits of 889 us; a 1 is space then mark (high, low on the
		// receiver's active-low output); a frame opens at S1's mid-bit mark
		half := T / 2
		set(sramcapture.SymbolLevel, 1, 5*ms, decode.SymDataSep)
		s.cfg.UnitQ8 = q8(half)
		s.tab.both(true, 5*ms, inf, leadStep0, decode.SymDataRestart)
		s.width = func(i int) int { return []int{5, 7, 0}[min(i, 2)] }
		s.start = func(b *symBits, units []symUnit) bool {
			b.fixed(0) // S1's second half
			s2 := symUnit{}
			if len(units) > 1 && units[1].mask&0x40 != 0 {
				s2 = symUnit{^units[1].value >> 6 & 1, 1} // RC-5X: S2 is the inverted command bit 6
			}
			b.manchester(s2.value, s2.mask, 1, true, [2]int{1, 0})
			b.skip(2) // toggle
			return true
		}
		s.unit = func(b *symBits, i int, u symUnit) bool {
			switch i {
			case 0:
				b.manchester(u.value, u.mask, 5, true, [2]int{1, 0})
			case 1:
				b.manchester(u.value, u.mask, 6, true, [2]int{1, 0})
			default:
				return false
			}
			return true
		}
	case serIRRC6:
		// t = 444 us; leader mark 6t, space 2t; a 1 is mark then space
		t := T / 2
		set(sramcapture.SymbolLevel, 2, 6*t*1.25, decode.SymDataSep)
		s.cfg.UnitQ8 = q8(t)
		s.tab.both(false, 6*t*0.8, 6*t*1.25, leadStep0, decode.SymDataRestart)
		s.tab.lead(true, 2*t*0.7, 2*t*1.3, leadStep1)
		s.start = func(b *symBits, _ []symUnit) bool {
			b.fixed(0, 1) // start bit 1
			b.skip(6)     // mode bits
			b.skip(4)     // trailer (the toggle), two units a half
			return true
		}
		s.unit = func(b *symBits, _ int, u symUnit) bool {
			b.manchester(u.value, u.mask, 8, true, [2]int{0, 1})
			return true
		}
	case serDALI:
		// half bits of 416.7 us; a 1 rises mid-bit; idle high
		half := T / 2
		set(sramcapture.SymbolLevel, 1, 1.8*T, decode.SymDataSep)
		s.cfg.UnitQ8 = q8(half)
		s.tab.both(true, 1.8*T, inf, leadStep0, decode.SymDataRestart)
		s.start = func(b *symBits, _ []symUnit) bool { b.fixed(0, 1); return true }
		s.unit = func(b *symBits, _ int, u symUnit) bool {
			b.manchester(u.value, u.mask, 8, true, [2]int{0, 1})
			return true
		}
	case serUSBPD:
		// half UI units, edges; SOP is Sync-1 x3, Sync-2, a byte two 4b5b
		// symbols, low nibble first, each LSB first
		set(sramcapture.SymbolEdge, 0, T, decode.SymDataSep)
		s.cfg.UnitQ8, s.sliding = q8(T/2), true
		sym := func(b *symBits, code int) { b.bmc(code, 31, 5, false) }
		s.start = func(b *symBits, _ []symUnit) bool {
			for _, k := range []int{0b11000, 0b11000, 0b11000, 0b10001} {
				sym(b, k)
			}
			return true
		}
		enc := [16]int{0b11110, 0b01001, 0b10100, 0b10101, 0b01010, 0b01011, 0b01110, 0b01111,
			0b10010, 0b10011, 0b10110, 0b10111, 0b11010, 0b11011, 0b11100, 0b11101}
		s.unit = func(b *symBits, _ int, u symUnit) bool {
			for _, sh := range []int{0, 4} {
				switch u.mask >> sh & 15 {
				case 15:
					sym(b, enc[u.value>>sh&15])
				case 0:
					b.bmc(0, 0, 5, false)
				default:
					return false // a nibble is all or nothing in 4b5b
				}
			}
			return true
		}
	case serSPDIF:
		// half-cell units, edges; preamble B (3,1,1,3 half cells), then the
		// 24-bit sample LSB first
		set(sramcapture.SymbolEdge, 0, T, decode.SymDataSep)
		s.cfg.UnitQ8, s.sliding = q8(T/2), true
		s.width = func(int) int { return 24 }
		s.start = func(b *symBits, _ []symUnit) bool { b.fixed(1, 0, 0, 1, 1, 1, 0, 0); return true }
		s.unit = func(b *symBits, i int, u symUnit) bool {
			if i != 0 {
				return false
			}
			b.bmc(u.value, u.mask, 24, false)
			return true
		}
	case serPS2:
		// data at the clock's falling edges; a frame after the clock idles
		set(sramcapture.SymbolClocked, 1, 0.75*T, decode.SymDataSep)
		s.cfg.ClockFall, s.cfg.TwoLines = true, true
		s.tab.both(true, 0.75*T, inf, leadStep0, decode.SymDataRestart)
		s.unit = func(b *symBits, _ int, u symUnit) bool { uartChar(b, u, 8, "odd"); return true }
		s.period = 11
	case serSWD:
		// data at SWCLK's rising edges: request {start 1, APnDP, RnW, A2,
		// A3, parity, stop 0, park 1}, turnaround, ACK (OK 1,0,0 LSB first)
		set(sramcapture.SymbolClocked, 0, T, decode.SymDataSep)
		s.cfg.TwoLines, s.sliding = true, true
		s.width = func(i int) int { return []int{8, 3, 32, 0}[min(i, 3)] }
		var read, readKnown bool
		count := 0
		request := func(b *symBits, u symUnit) {
			b.field((u.value|0x81)&^0x40, u.mask|0xc1, 8, false)
			b.skip(1) // turnaround
			if count < 2 {
				b.fixed(1, 0, 0) // ACK OK unless asked
			}
		}
		s.start = func(b *symBits, units []symUnit) bool {
			count = len(units)
			if len(units) > 0 && units[0].mask&4 != 0 {
				read, readKnown = units[0].value&4 != 0, true
			}
			if len(units) == 0 {
				request(b, symUnit{})
			}
			return true
		}
		s.unit = func(b *symBits, i int, u symUnit) bool {
			switch i {
			case 0:
				request(b, u)
			case 1:
				b.field(u.value, u.mask, 3, false)
			case 2:
				if !readKnown {
					return false
				}
				if !read {
					b.skip(1)
				}
				b.field(u.value, u.mask, 32, false)
			default:
				return false
			}
			return true
		}
	case serMDIO:
		// data at MDC's rising edges: 32 preamble ones, ST (0 first), then
		// the header's 13 further bits; TA, 16 data bits, MSB first
		set(sramcapture.SymbolClocked, 0, T, decode.SymDataSep)
		s.cfg.TwoLines, s.sliding = true, true
		s.width = func(i int) int { return []int{14, 16, 0}[min(i, 2)] }
		s.start = func(b *symBits, units []symUnit) bool {
			for i := 0; i < 32; i++ {
				b.fixed(1)
			}
			b.fixed(0)
			return true
		}
		s.unit = func(b *symBits, i int, u symUnit) bool {
			switch i {
			case 0:
				if u.mask>>13&1 != 0 && u.value>>13&1 != 0 {
					return false
				}
				b.field(u.value, u.mask, 13, true)
			case 1:
				b.skip(2)
				b.field(u.value, u.mask, 16, true)
			default:
				return false
			}
			return true
		}
	case serWiegand:
		// line A is D1 and line B is D0: their AND falls at each pulse, and
		// D0 then reads 0 for a D0 pulse and 1 for a D1 pulse
		gap := math.Max(5*T, 2*ms)
		set(sramcapture.SymbolClocked, 1, gap, decode.SymDataSep)
		s.cfg.Channel, s.cfg.AndAB, s.cfg.ClockFall, s.cfg.TwoLines = uint8(p.ChB), true, true, true
		s.tab.both(true, gap, inf, leadStep0, decode.SymDataRestart)
		widths := []int{8, 8, 8, 8, 8, 8, 8}
		switch p.Bits {
		case 26:
			widths = []int{8, 16}
		case 34:
			widths = []int{16, 16}
		}
		s.width = func(i int) int {
			if i < len(widths) {
				return widths[i]
			}
			return 0
		}
		s.start = func(b *symBits, _ []symUnit) bool { b.skip(1); return true } // leading parity
		s.unit = func(b *symBits, i int, u symUnit) bool {
			if s.width(i) == 0 {
				return false
			}
			b.field(u.value, u.mask, s.width(i), true)
			return true
		}
	default:
		return s, false
	}
	if s.cfg.Mode == sramcapture.SymbolLevel || s.cfg.Mode == sramcapture.SymbolEdge {
		if s.cfg.UnitQ8 < 5*256 || s.cfg.UnitQ8 >= 1<<28 {
			return s, false // five receiver clocks a unit at least
		}
	}
	s.cfg.Prescale = uint8(s.tab.prescale)
	return s, true
}

// planHardwareSymbol compiles a request on a protocol of the symbol trigger
// (ADR-SYMBOL-TRIGGER) into the block's settings: the frame start is the
// leader (or, sliding, the start's own bits), and units follow it at their
// line positions; units without a start match at any unit boundary of a
// frame where every unit has the same bits. Error tokens and requests past
// the 64-bit window are unplannable.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func planHardwareSymbol(p SerialParams) sramcapture.SymbolTriggerConfig {
	off := sramcapture.SymbolTriggerConfig{}
	if !symbolProtocol(p.Proto) || !serialThresholdOK(p) {
		return off
	}
	if symbolTwoLines(p.Proto) && p.ChB != 1-p.ChA {
		return off
	}
	s, ok := symbolSpec(p)
	if !ok {
		return off
	}
	elems := patternOf(p)
	if len(elems) == 0 {
		elems = []PatternElem{{Kind: patternStart}}
	}
	anchored := elems[0].Kind == patternStart
	var units []symUnit
	for i, e := range elems {
		switch {
		case e.Kind == patternStart && i == 0:
		case e.Kind == patternData:
			w := s.width(len(units))
			if w <= 0 || w > 32 {
				return off
			}
			allowed := 1<<w - 1
			if e.Value < 0 || e.Mask < 0 || e.Value&^allowed != 0 || e.Mask&^allowed != 0 {
				return off
			}
			units = append(units, symUnit{e.Value & e.Mask, e.Mask})
		default:
			return off
		}
	}
	var b symBits
	if anchored && !s.start(&b, units) {
		return off
	}
	for i, u := range units {
		if !s.unit(&b, i, u) {
			return off
		}
	}
	n := len(b.val)
	c := s.cfg
	switch {
	case s.sliding:
		if !anchored || n > 64 || n == 0 {
			return off
		}
		c.K, c.Period = uint16(n), 1
	case anchored:
		if n > 1023 {
			return off
		}
		for i := 0; i < n-64; i++ {
			if b.care[i] {
				return off // the window holds the last 64 bits
			}
		}
		c.K = uint16(n)
	default:
		// units at any unit boundary after the frame start's own bits
		var lead symBits
		if s.period == 0 || n > 64 || !s.start(&lead, nil) {
			return off
		}
		k := len(lead.val) + n
		c.K, c.Period = uint16(k), uint8(s.period)
		c.Phase0 = uint8((s.period - k%s.period) % s.period)
	}
	for i := max(0, n-64); i < n; i++ {
		c.Pattern, c.Mask = c.Pattern<<1, c.Mask<<1
		if b.val[i] {
			c.Pattern |= 1
		}
		if b.care[i] {
			c.Mask |= 1
		}
	}
	table := s.tab.t
	c.Table = table
	return c
}

// symbolCfg is the Go model's form of a planned symbol trigger.
// TRLC-LINKS: REQ-SDS-013
func symbolCfg(c sramcapture.SymbolTriggerConfig) decode.SymbolCfg {
	d := decode.SymbolCfg{InvA: c.InvA, InvB: c.InvB, AndAB: c.AndAB, ClockFall: c.ClockFall, Mode: int(c.Mode), LeadLen: int(c.LeadLen),
		Prescale: int(c.Prescale), UnitQ8: c.UnitQ8, FrameUnits: int(c.FrameUnits), Period: int(c.Period), Phase0: int(c.Phase0),
		K: int(c.K), Pattern: c.Pattern, Mask: c.Mask, Table: c.Table}
	return d
}
