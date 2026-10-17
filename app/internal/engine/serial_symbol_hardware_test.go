// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"testing"

	"open-sds/app/internal/decode"
)

// symWave builds two lines at the symbol trigger's 125 MHz clock from timed
// holds, rounding accumulated time so long waves do not drift.
type symWave struct {
	t    float64
	a, b []bool
	// marks are the clocks where frames the test names start
	marks []int
}

// TRLC-LINKS: REQ-SDS-013
func (w *symWave) hold(a, b bool, s float64) {
	from := int(math.Round(w.t / 8e-9))
	w.t += s
	to := int(math.Round(w.t / 8e-9))
	for i := from; i < to; i++ {
		w.a = append(w.a, a)
		w.b = append(w.b, b)
	}
}

// TRLC-LINKS: REQ-SDS-013
func (w *symWave) mark() { w.marks = append(w.marks, len(w.a)) }

// runs holds line A through alternating levels (line B high).
// TRLC-LINKS: REQ-SDS-013
func (w *symWave) runs(first bool, d ...float64) {
	l := first
	for _, s := range d {
		w.hold(l, true, s)
		l = !l
	}
}

// uart holds one character: start, data LSB first, parity, stops.
// TRLC-LINKS: REQ-SDS-013
func (w *symWave) uart(v, bits int, parity string, stops int, T float64) {
	w.hold(false, true, T)
	ones := 0
	for i := 0; i < bits; i++ {
		bit := v>>i&1 != 0
		if bit {
			ones++
		}
		w.hold(bit, true, T)
	}
	switch parity {
	case "even":
		w.hold(ones&1 != 0, true, T)
	case "odd":
		w.hold(ones&1 == 0, true, T)
	}
	w.hold(true, true, float64(stops)*T)
}

// halves holds Manchester half bits: each bit's two levels from one (the
// levels of a 1), MSB first.
// TRLC-LINKS: REQ-SDS-013
func (w *symWave) halves(v, n int, one [2]bool, half float64) {
	for i := n - 1; i >= 0; i-- {
		f := one[0]
		if v>>i&1 == 0 {
			f = !f
		}
		w.hold(f, true, half)
		w.hold(!f, true, half)
	}
}

// symMatches runs the planned trigger's Go model over the wave.
// TRLC-LINKS: REQ-SDS-013
func symMatches(t *testing.T, p SerialParams, w *symWave) []int {
	t.Helper()
	c := planHardwareSymbol(p)
	if !c.Enabled {
		t.Fatalf("unplannable: %+v", p)
	}
	a, b := w.a, w.b
	if c.Channel == 1 { // line A is the second channel
		a, b = b, a
	}
	return decode.SymbolMatches(symbolCfg(c), a, b, 8)
}

// symCase is one protocol's synthetic bus: frames with known content, the
// requests to try and how many frames each must match (want), and the
// latest the trigger may come after the frame's mark (late, seconds).
type symCase struct {
	name string
	p    SerialParams
	wave func() *symWave
	reqs []symReq
}

type symReq struct {
	pattern []PatternElem
	want    int
}

var (
	symStart = PatternElem{Kind: patternStart}
	symAny   = PatternElem{Kind: patternData}
)

// TRLC-LINKS: REQ-SDS-013
func symData(v, m int) PatternElem { return PatternElem{Kind: patternData, Value: v, Mask: m} }

// TRLC-LINKS: REQ-SDS-013
func symParams(proto, baud int) SerialParams {
	return SerialParams{Proto: proto, ChA: 0, ChB: 1, Baud: baud, Threshold: 128, HaveThr: true, ThresholdB: 128, HaveThrB: true}
}

// symbolCases are synthetic buses of every symbol-trigger protocol with
// requests and the frames each must match.
// TRLC-LINKS: REQ-SDS-013
func symbolCases() []symCase {
	us, ms := 1e-6, 1e-3
	return []symCase{
		{name: "LIN", p: symParams(serLIN, 19200), wave: func() *symWave {
			T := 1 / 19200.0
			w := &symWave{}
			w.hold(true, true, 1*ms)
			for f, pid := range []int{0x80, 0xc1, 0x80, 0x42} {
				w.mark()
				w.hold(false, true, 13*T)
				w.hold(true, true, float64(1+f%2)*T)
				w.uart(0x55, 8, "none", 1, T)
				if f == 3 {
					w.hold(true, true, 2.5*T) // inter-byte space
				}
				w.uart(pid, 8, "none", 1, T)
				sum := pid // the enhanced checksum: the PID and the data, carries added back
				for _, d := range []int{0x12, 0x34, 0xab} {
					w.uart(d, 8, "none", 1, T)
					w.hold(true, true, 0.7*T)
					if sum += d; sum > 255 {
						sum -= 255
					}
				}
				w.uart(^sum&255, 8, "none", 1, T)
				w.hold(true, true, 3*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 4}, {[]PatternElem{symStart, symAny}, 4}, {[]PatternElem{symStart, symData(0x80, 0xff)}, 2},
			{[]PatternElem{symStart, symData(0x42, 0xff), symData(0x12, 0xff)}, 1}, {[]PatternElem{symData(0x34, 0xff)}, 4}, {[]PatternElem{symData(0x55, 0xff)}, 0}}},
		{name: "DMX", p: symParams(serDMX, 250000), wave: func() *symWave {
			T := 4 * us
			w := &symWave{}
			w.hold(true, true, 100*us)
			for f := 0; f < 3; f++ {
				w.mark()
				w.hold(false, true, 120*us)
				w.hold(true, true, 12*us)
				w.uart([]int{0, 0x17, 0}[f], 8, "none", 2, T)
				for s := 0; s < 20; s++ {
					w.uart(s*7+f, 8, "none", 2, T)
					if s%5 == 0 {
						w.hold(true, true, 10*us)
					}
				}
				w.hold(true, true, 300*us)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0, 0xff)}, 2}, {[]PatternElem{symStart, symData(0, 0xff), symData(2, 0xff)}, 1}}},
		{name: "Modbus", p: func() SerialParams { p := symParams(serModbus, 9600); p.Parity = "even"; return p }(), wave: func() *symWave {
			T := 1 / 9600.0
			w := &symWave{}
			w.hold(true, true, 5*ms)
			for f, frame := range [][]int{{0x11, 0x03, 0x00, 0x6b, 0x00, 0x03}, {0x01, 0x06, 0x00, 0x01}, {0x11, 0x10, 0x55}} {
				w.mark()
				crc := 0xffff // CRC-16/MODBUS, low byte first
				for _, v := range frame {
					crc ^= v
					for k := 0; k < 8; k++ {
						if crc&1 != 0 {
							crc = crc>>1 ^ 0xa001
						} else {
							crc >>= 1
						}
					}
				}
				frame = append(frame, crc&255, crc>>8)
				for i, v := range frame {
					w.uart(v, 8, "even", 1, T)
					if i == 1 && f == 1 {
						w.hold(true, true, 1.2*11*T)
					}
				}
				w.hold(true, true, 6*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symAny}, 3}, {[]PatternElem{symStart, symData(0x11, 0xff)}, 2},
			{[]PatternElem{symStart, symData(0x01, 0xff), symData(0x06, 0xff)}, 1}, {[]PatternElem{symData(0x6b, 0xff)}, 1}}},
		{name: "1-Wire", p: symParams(serOneWire, 16300), wave: func() *symWave {
			w := &symWave{}
			w.hold(true, true, 200*us)
			for _, cmd := range []int{0xcc, 0x33, 0xcc, 0x55} {
				w.mark()
				w.runs(false, 500*us, 30*us, 120*us, 400*us)
				for i := 0; i < 16; i++ {
					v := cmd
					if i >= 8 {
						v = 0x44
					}
					if v>>(i%8)&1 != 0 {
						w.runs(false, 6*us, 64*us)
					} else {
						w.runs(false, 62*us, 8*us)
					}
				}
				w.hold(true, true, 2*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 4}, {[]PatternElem{symStart, symData(0xcc, 0xff)}, 2}, {[]PatternElem{symStart, symData(0x55, 0xff), symData(0x44, 0xff)}, 1}}},
		{name: "WS281x", p: symParams(serWS281x, 800000), wave: func() *symWave {
			w := &symWave{}
			w.hold(false, true, 80*us)
			for f := 0; f < 3; f++ {
				w.mark()
				for _, v := range []int{0x10 + f, 0x80, 0xff, 0x00, 0x5a, 0x0f} {
					for i := 7; i >= 0; i-- {
						if v>>i&1 != 0 {
							w.runs(true, 0.8*us, 0.45*us)
						} else {
							w.runs(true, 0.4*us, 0.85*us)
						}
					}
				}
				w.hold(false, true, 80*us)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x11, 0xff)}, 1}, {[]PatternElem{symData(0x5a, 0xff)}, 3}}},
		{name: "DHT", p: symParams(serDHT, 20000), wave: func() *symWave {
			w := &symWave{}
			w.hold(true, true, 2*ms)
			for f := 0; f < 3; f++ {
				w.mark()
				w.runs(false, 1.1*ms, 30*us, 80*us, 80*us)
				for _, v := range []int{0x02, 0x8c + f, 0x01, 0x5f, 0xee} {
					for i := 7; i >= 0; i-- {
						hi := 26 * us
						if v>>i&1 != 0 {
							hi = 70 * us
						}
						w.runs(false, 50*us, hi)
					}
				}
				w.runs(false, 50*us)
				w.hold(true, true, 5*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x02, 0xff), symData(0x8d, 0xff)}, 1}}},
		{name: "CEC", p: symParams(serCEC, 417), wave: func() *symWave {
			w := &symWave{}
			w.hold(true, true, 10*ms)
			for _, hdr := range []int{0x40, 0x4f, 0x40} {
				w.mark()
				w.runs(false, 3.7*ms, 0.8*ms)
				for bi, v := range []int{hdr, 0x82} {
					bits := []int{}
					for i := 7; i >= 0; i-- {
						bits = append(bits, v>>i&1)
					}
					bits = append(bits, bi, 0) // EOM, ACK
					for _, b := range bits {
						if b != 0 {
							w.runs(false, 0.6*ms, 1.8*ms)
						} else {
							w.runs(false, 1.5*ms, 0.9*ms)
						}
					}
				}
				w.hold(true, true, 10*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x40, 0xff)}, 2}, {[]PatternElem{symData(0x82, 0xff)}, 3}}},
		{name: "J1850", p: symParams(serJ1850, 10400), wave: func() *symWave {
			w := &symWave{}
			w.hold(false, true, 1*ms)
			for _, hdr := range []int{0x68, 0x6c, 0x68} {
				w.mark()
				w.hold(true, true, 200*us)
				level := false
				for _, v := range []int{hdr, 0x6a, 0xf1, 0x01, 0x00} {
					for i := 7; i >= 0; i-- {
						one := v>>i&1 != 0
						d := 64 * us
						if one != level { // passive long = 1, active long = 0
							d = 128 * us
						}
						w.hold(level, true, d)
						level = !level
					}
				}
				if level {
					w.hold(true, true, 64*us)
				}
				w.hold(false, true, 2*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x68, 0xff), symData(0x6a, 0xff)}, 2}}},
		{name: "NEC", p: symParams(serIRNEC, 889), wave: func() *symWave {
			w := &symWave{}
			w.hold(true, true, 20*ms)
			for _, addr := range []int{0x00, 0x04, 0x00} {
				w.mark()
				w.runs(false, 9*ms, 4.5*ms)
				for _, v := range []int{addr, addr ^ 0xff, 0x16, 0xe9} {
					for i := 0; i < 8; i++ {
						sp := 0.5625 * ms
						if v>>i&1 != 0 {
							sp = 1.6875 * ms
						}
						w.runs(false, 0.5625*ms, sp)
					}
				}
				w.runs(false, 0.5625*ms)
				w.hold(true, true, 40*ms)
				w.runs(false, 9*ms, 2.25*ms, 0.5625*ms) // repeat code: no frame
				w.hold(true, true, 40*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 6}, {[]PatternElem{symStart, symData(0x04, 0xff)}, 1}, {[]PatternElem{symStart, symAny, symAny, symData(0x16, 0xff)}, 3}}},
		{name: "SIRC", p: func() SerialParams { p := symParams(serIRSIRC, 1667); p.Bits = 12; return p }(), wave: func() *symWave {
			w := &symWave{}
			w.hold(true, true, 10*ms)
			for _, cmd := range []int{0x15, 0x12, 0x15} {
				w.mark()
				w.runs(false, 2.4*ms, 0.6*ms)
				v := cmd | 1<<7 // address 1
				for i := 0; i < 12; i++ {
					m := 0.6 * ms
					if v>>i&1 != 0 {
						m = 1.2 * ms
					}
					w.runs(false, m, 0.6*ms)
				}
				w.hold(true, true, 25*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x15, 0x7f)}, 2}, {[]PatternElem{symStart, symData(0x12, 0x7f), symData(1, 0x1f)}, 1}}},
		{name: "RC-5", p: symParams(serIRRC5, 562), wave: func() *symWave {
			w := &symWave{}
			half := 1 / 562.0 / 2
			w.hold(true, true, 20*ms)
			for f, cmd := range []int{0x0c, 0x10, 0x0c} {
				w.mark()
				// S1 S2 T A4..A0 C5..C0; a 1 is space then mark (high, low)
				v := 1<<13 | 1<<12 | (f&1)<<11 | 5<<6 | cmd
				w.halves(v, 14, [2]bool{true, false}, half)
				w.hold(true, true, 90*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(5, 0x1f), symData(0x0c, 0x3f)}, 2}, {[]PatternElem{symStart, symAny, symData(0x10, 0x3f)}, 1}}},
		{name: "RC-6", p: symParams(serIRRC6, 1125), wave: func() *symWave {
			w := &symWave{}
			tt := 1 / 1125.0 / 2
			w.hold(true, true, 20*ms)
			for f, cmd := range []int{0x0c, 0x10, 0x0c} {
				w.mark()
				w.runs(false, 6*tt, 2*tt)
				w.halves(1, 1, [2]bool{false, true}, tt) // start bit
				w.halves(0, 3, [2]bool{false, true}, tt) // mode 0
				w.halves(f&1, 1, [2]bool{false, true}, 2*tt)
				w.halves(0x24, 8, [2]bool{false, true}, tt)
				w.halves(cmd, 8, [2]bool{false, true}, tt)
				w.hold(true, true, 90*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x24, 0xff), symData(0x0c, 0xff)}, 2}}},
		{name: "DALI", p: symParams(serDALI, 1200), wave: func() *symWave {
			w := &symWave{}
			half := 1 / 1200.0 / 2
			w.hold(true, true, 20*ms)
			for _, addr := range []int{0xfe, 0x01, 0xfe} {
				w.mark()
				w.halves(1, 1, [2]bool{false, true}, half)
				w.halves(addr<<8|0x80, 16, [2]bool{false, true}, half)
				w.hold(true, true, 20*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0xfe, 0xff), symData(0x80, 0xff)}, 2}}},
		{name: "USB-PD", p: symParams(serUSBPD, 300000), wave: func() *symWave {
			w := &symWave{}
			ui := 1 / 300000.0
			level := false
			bit := func(v int) {
				level = !level
				if v != 0 {
					w.hold(level, true, ui/2)
					level = !level
					w.hold(level, true, ui/2)
				} else {
					w.hold(level, true, ui)
				}
			}
			enc := []int{0b11110, 0b01001, 0b10100, 0b10101, 0b01010, 0b01011, 0b01110, 0b01111, 0b10010, 0b10011, 0b10110, 0b10111, 0b11010, 0b11011, 0b11100, 0b11101}
			sym := func(code int) {
				for i := 0; i < 5; i++ {
					bit(code >> i & 1)
				}
			}
			w.hold(false, true, 50*us)
			for _, hdr := range []int{0x41, 0xa1, 0x41} {
				w.mark()
				for i := 0; i < 64; i++ {
					bit(i & 1)
				}
				for _, k := range []int{0b11000, 0b11000, 0b11000, 0b10001} {
					sym(k)
				}
				for _, by := range []int{hdr, 0x10, 0x2c, 0x91} {
					sym(enc[by&15])
					sym(enc[by>>4])
				}
				sym(0b01101) // EOP
				level = false
				w.hold(false, true, 100*us)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x41, 0xff)}, 2}, {[]PatternElem{symStart, symData(0x01, 0x0f)}, 3}}},
		{name: "S/PDIF", p: symParams(serSPDIF, 3072000), wave: func() *symWave {
			w := &symWave{}
			h := 1 / 3072000.0 / 2
			level := false
			half := func(edge bool) {
				if edge {
					level = !level
				}
				w.hold(level, true, h)
			}
			for i := 0; i < 64; i++ { // the end of a subframe before the first B
				half(i%2 == 0 || i%4 == 1)
			}
			pre := map[string][]bool{"B": {true, false, false, true, true, true, false, false}, "M": {true, false, false, true, false, false, true, true}, "W": {true, false, false, true, false, true, true, false}}
			for f := 0; f < 6; f++ {
				for ch := 0; ch < 2; ch++ {
					p := "W"
					if ch == 0 {
						p = "M"
						if f%3 == 0 {
							p = "B"
							w.mark()
						}
					}
					for _, e := range pre[p] {
						half(e)
					}
					sample := 0x123456 + f
					if ch == 1 {
						sample = 0x0f0f0f
					}
					ones := 0
					for i := 0; i < 28; i++ {
						v := 0
						if i < 24 {
							v = sample >> i & 1
						}
						if i == 27 {
							v = ones & 1
						}
						ones += v
						half(true)
						half(v != 0)
					}
				}
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 2}, {[]PatternElem{symStart, symData(0x123456, 0xffffff)}, 1}}},
		{name: "PS/2", p: symParams(serPS2, 12000), wave: func() *symWave {
			w := &symWave{}
			T := 1 / 12000.0
			w.hold(true, true, 1*ms)
			for f, v := range []int{0x1c, 0xf0, 0x1c} {
				w.mark()
				ones := 0
				bits := []int{0}
				for i := 0; i < 8; i++ {
					bits = append(bits, v>>i&1)
					ones += v >> i & 1
				}
				bits = append(bits, 1-ones&1, 1)
				for _, b := range bits {
					// data changes while the clock is high, read at its fall
					w.hold(true, b != 0, T/2)
					w.hold(false, b != 0, T/2)
				}
				w.hold(true, true, float64(1+f)*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x1c, 0xff)}, 2}, {[]PatternElem{symData(0xf0, 0xff)}, 1}}},
		{name: "SWD", p: symParams(serSWD, 1000000), wave: func() *symWave {
			w := &symWave{}
			T := 1e-6
			// The host sets its bits as SWCLK falls; the target sets its own
			// (acknowledge, read data) 20 ns after a rise, for the next rise.
			type cyc struct{ b, target bool }
			var cs []cyc
			bits := func(v, n int, target bool) {
				for i := 0; i < n; i++ {
					cs = append(cs, cyc{v>>i&1 != 0, target})
				}
			}
			var marks []int
			bits(0, 8, false)
			for _, req := range []int{0xa5, 0x8d, 0xa5} {
				marks = append(marks, len(cs))
				bits(req, 8, false)
				bits(0, 1, false)          // turnaround
				bits(1, 3, true)           // ACK OK
				bits(0x0000ffff, 32, true) // read data
				bits(0, 1, true)           // parity
				bits(0, 1, false)          // turnaround
				bits(0, 8, false)          // idle
			}
			for i, c := range cs {
				if len(marks) > 0 && marks[0] == i {
					w.mark()
					marks = marks[1:]
				}
				w.hold(false, c.b, T/2)
				if i+1 < len(cs) && cs[i+1].target {
					w.hold(true, c.b, 20e-9)
					w.hold(true, cs[i+1].b, T/2-20e-9)
				} else {
					w.hold(true, c.b, T/2)
				}
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0xa5, 0xff)}, 2}, {[]PatternElem{symStart, symData(0xa5, 0xff), symData(1, 7), symData(0x0000ffff, 0xffffffff)}, 2}}},
		{name: "MDIO", p: symParams(serMDIO, 1000000), wave: func() *symWave {
			w := &symWave{}
			T := 1e-6
			bits := func(v, n int) {
				for i := n - 1; i >= 0; i-- {
					b := v>>i&1 != 0
					w.hold(false, b, T/2)
					w.hold(true, b, T/2)
				}
			}
			for f, phy := range []int{3, 4, 3} {
				w.mark()
				bits(-1, 32)
				hdr := 1<<12 | 2<<10 | phy<<5 | 1 // read, register 1
				bits(hdr, 14)
				bits(2, 2) // turnaround
				bits(0x7949+f, 16)
				w.hold(true, true, 5*us)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(1<<12|2<<10|3<<5|1, 0x3fff)}, 2},
			{[]PatternElem{symStart, symData(1<<12|2<<10|3<<5|1, 0x3fff), symData(0x794b, 0xffff)}, 1}}},
		{name: "Wiegand", p: func() SerialParams { p := symParams(serWiegand, 1000); p.Bits = 26; return p }(), wave: func() *symWave {
			w := &symWave{}
			w.hold(true, true, 20*ms)
			for _, card := range []int{0x1234, 0x4321, 0x1234} {
				w.mark()
				v := 0<<25 | 0x2a<<17 | card<<1 | 1
				for i := 25; i >= 0; i-- {
					if v>>i&1 != 0 {
						w.hold(true, false, 50*us) // D1
					} else {
						w.hold(false, true, 50*us) // D0
					}
					w.hold(true, true, 950*us)
				}
				w.hold(true, true, 50*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symAny}, 3}, {[]PatternElem{symStart, symData(0x2a, 0xff), symData(0x1234, 0xffff)}, 2}}},
	}
}

// Every symbol-trigger protocol on synthetic buses through the planner and
// the block's Go model (bit-exact with the RTL): the frame start fires once
// a frame, and unit values fire only on the frames that carry them.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestSymbolTriggerProtocols(t *testing.T) {
	for _, tc := range symbolCases() {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.wave()
			for _, r := range tc.reqs {
				p := tc.p
				p.Pattern = r.pattern
				got := symMatches(t, p, w)
				if len(got) != r.want {
					t.Fatalf("pattern %v: %d matches %v, want %d (frames at %v)", r.pattern, len(got), got, r.want, w.marks)
				}
				// each match falls in a frame, after its mark and before the next
				for _, m := range got {
					in := false
					for i, s := range w.marks {
						end := len(w.a)
						if i+1 < len(w.marks) {
							end = w.marks[i+1]
						}
						in = in || (m >= s && m < end)
					}
					if !in {
						t.Fatalf("pattern %v: match at %d outside the frames %v", r.pattern, m, w.marks)
					}
				}
			}
		})
	}
}
