// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import "open-sds/app/internal/decode"

// physSymbolCases are synthetic buses of the sigrok protocols beyond the
// first thirty with requests and the frames each must match (symbolCases):
// each request's frame start is the one the protocol's decoder opens.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-257
func physSymbolCases() []symCase {
	us, ms := 1e-6, 1e-3
	withBits := func(p SerialParams, bits int) SerialParams { p.Bits = bits; return p }
	return []symCase{
		{name: "Quadrature", p: symParams(serQuad, 50000), wave: func() *symWave {
			w := &symWave{}
			seq := [4][2]bool{{false, false}, {true, false}, {true, true}, {false, true}}
			ph := 0
			w.hold(false, false, 200*us)
			for _, burst := range [][2]int{{10, 1}, {6, -1}, {4, 1}} {
				w.mark()
				for c := 0; c < burst[0]; c++ {
					for q := 0; q < 4; q++ {
						ph = ((ph+burst[1])%4 + 4) % 4
						w.hold(seq[ph][0], seq[ph][1], 5*us)
					}
				}
				w.hold(seq[ph][0], seq[ph][1], 200*us)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symAny}, 3}, {[]PatternElem{symStart, symData(1, 1)}, 2},
			{[]PatternElem{symStart, symData(0, 1), symData(0, 1)}, 1}}},
		{name: "Stepper", p: symParams(serStepper, 50000), wave: func() *symWave {
			w := &symWave{}
			w.hold(false, true, 200*us)
			for _, burst := range []struct {
				n   int
				dir bool
			}{{8, true}, {5, false}, {6, true}} {
				w.hold(false, burst.dir, 20*us)
				w.mark()
				for i := 0; i < burst.n; i++ {
					w.hold(true, burst.dir, 5*us)
					w.hold(false, burst.dir, 15*us)
				}
				w.hold(false, burst.dir, 200*us)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(1, 1)}, 2}, {[]PatternElem{symStart, symData(0, 1)}, 1}}},
		{name: "PWM", p: symParams(serPWM, 50000), wave: func() *symWave {
			w := &symWave{}
			w.hold(false, true, 30*us)
			for _, d := range []float64{0.2, 0.6, 0.3, 0.8, 0.55, 0.1, 0.4} {
				w.mark()
				w.hold(true, true, d*20*us)
				w.hold(false, true, (1-d)*20*us)
			}
			w.hold(true, true, 1*us)
			return w
		}, reqs: []symReq{{[]PatternElem{symStart, symData(0x80, 0x80)}, 3}, {[]PatternElem{symStart, symData(0x00, 0xc0)}, 2},
			{[]PatternElem{symStart, symData(0x80, 0xc0)}, 2}}},
		{name: "RC encode", p: withBits(symParams(serRCEncode, 10000), 12), wave: func() *symWave {
			w := &symWave{}
			a := 100 * us
			w.hold(false, true, 40*a)
			for _, tr := range [][]int{{0, 1, 2, 0, 0, 1, 1, 2, 0, 1, 0, 0}, {1, 1, 2, 0, 0, 1, 1, 2, 0, 1, 0, 0}, {0, 1, 2, 0, 0, 1, 1, 2, 0, 1, 0, 0}} {
				w.mark()
				pulse := func(hi, lo float64) { w.hold(true, true, hi*a); w.hold(false, true, lo*a) }
				for _, t := range tr {
					switch t {
					case 0:
						pulse(1, 3)
						pulse(1, 3)
					case 1:
						pulse(3, 1)
						pulse(3, 1)
					default:
						pulse(1, 3)
						pulse(3, 1)
					}
				}
				pulse(1, 31)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0, 3)}, 2}, {[]PatternElem{symStart, symData(1, 3), symData(1, 3)}, 1}}},
		{name: "Oregon OOK", p: withBits(symParams(serOOK, 8192), 3), wave: func() *symWave {
			w := &symWave{}
			hp := 1 / 8192.0 / 2
			w.hold(false, true, 2*ms)
			for f := 0; f < 3; f++ {
				nib := []int{0xF, 0x8, 0x2, 0x4, 0x1, 0x3, 0xA, 0x0, 0x5 + f}
				sum := 0
				for _, x := range nib {
					sum += x
				}
				nib = append(nib, sum&15, sum>>4&15)
				bits := []int{}
				for i := 0; i < 24; i++ {
					bits = append(bits, 1)
				}
				bits = append(bits, 0, 1, 0, 1)
				for _, x := range nib {
					for k := 0; k < 4; k++ {
						bits = append(bits, x>>k&1)
					}
				}
				w.mark()
				for _, b := range bits {
					w.hold(b == 1, true, hp)
					w.hold(b == 0, true, hp)
				}
				w.hold(false, true, 3*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0xF, 0xF), symData(0x8, 0xF)}, 3}}},
		{name: "Miller", p: symParams(serMiller, 106000), wave: func() *symWave {
			w := &symWave{}
			T := 1 / 106000.0
			w.hold(true, true, 40*us)
			for _, msg := range [][]int{{1, 0, 1, 1, 0, 0, 1, 0, 0, 0, 1, 1, 1, 0, 1, 0}, {0, 0, 1, 0, 1, 1, 1, 1, 0, 1, 0, 0, 1, 1, 0, 1}, {1, 0, 1, 1, 0, 0, 1, 0, 1, 1, 0, 0, 0, 1, 0, 1}} {
				w.mark()
				all := append([]int{0}, msg...)
				var at []float64
				prev := 1
				for i, b := range all {
					t0 := float64(i) * T
					switch {
					case b == 1:
						at = append(at, t0+T/2)
					case prev == 0 || i == 0:
						at = append(at, t0)
					}
					prev = b
				}
				cur := 0.0
				for _, t := range at {
					w.hold(true, true, t-cur)
					w.hold(false, true, 1.5*us)
					cur = t + 1.5*us
				}
				w.hold(true, true, float64(len(all))*T-cur+60*us)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x9a, 0xff)}, 2}, {[]PatternElem{symStart, symAny, symData(0xb8, 0xff)}, 1}}},
		{name: "Morse", p: symParams(serMorse, 1000), wave: func() *symWave {
			w := &symWave{}
			u := 1 * ms
			w.hold(false, true, 10*u)
			for _, word := range []string{"SOS", "TEST", "SOS"} {
				w.mark()
				for _, c := range word {
					code := decode.MorseOf(byte(c))
					for k := 0; k < len(code); k++ {
						d := u
						if code[k] == '-' {
							d = 3 * u
						}
						w.hold(true, true, d)
						if k+1 < len(code) {
							w.hold(false, true, u)
						}
					}
					w.hold(false, true, 3*u)
				}
				w.hold(false, true, 4*u)
			}
			w.hold(false, true, 3*u)
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData('S', 0xff)}, 2}, {[]PatternElem{symStart, symData('T', 0xff), symData('E', 0xff)}, 1},
			{[]PatternElem{symData('O', 0xff)}, 2}}},
		{name: "DCF77", p: symParams(serDCF77, 1000), wave: func() *symWave {
			w := &symWave{}
			sec := 1 * ms
			w.hold(false, true, 0.9*sec)
			w.hold(true, true, 0.1*sec) // second 58
			w.hold(false, true, 1.9*sec)
			for _, mn := range []int{0x37, 0x38, 0x37} {
				w.mark()
				var b [59]int
				b[20] = 1
				put := func(at, n, v int) {
					for k := 0; k < n; k++ {
						b[at+k] = v >> k & 1
					}
				}
				put(21, 7, mn)
				put(29, 6, 0x12)
				put(36, 6, 0x18)
				put(42, 3, 7)
				put(45, 5, 0x10)
				put(50, 8, 0x26)
				par := func(at, from, to int) {
					for q := from; q <= to; q++ {
						b[at] ^= b[q]
					}
				}
				par(28, 21, 27)
				par(35, 29, 34)
				par(58, 36, 57)
				for s := 0; s < 59; s++ {
					hi := 0.1
					if b[s] == 1 {
						hi = 0.2
					}
					w.hold(true, true, hi*sec)
					w.hold(false, true, (1-hi)*sec)
				}
				w.hold(false, true, sec)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x37, 0x7f)}, 2}, {[]PatternElem{symStart, symData(0x38, 0x7f), symData(0x12, 0x3f)}, 1}}},
		{name: "EM4100", p: symParams(serEM4100, 3906), wave: func() *symWave {
			w := &symWave{}
			hp := 128 * us
			w.hold(true, true, 3*hp)
			for _, x := range []int{0, 1, 0, 0} { // the tail of a frame before
				w.hold(x == 1, true, hp)
				w.hold(x == 0, true, hp)
			}
			for _, tag := range [][5]int{{0x01, 0x23, 0x45, 0x67, 0x89}, {0x0A, 0x23, 0x45, 0x67, 0x89}, {0x01, 0xFF, 0x00, 0x5C, 0xE1}} {
				w.mark()
				bits := []int{1, 1, 1, 1, 1, 1, 1, 1, 1}
				col := 0
				for k := 0; k < 10; k++ {
					nib := tag[k/2] >> (4 * (1 - k%2)) & 15
					col ^= nib
					p := 0
					for i := 3; i >= 0; i-- {
						bits = append(bits, nib>>i&1)
						p ^= nib >> i & 1
					}
					bits = append(bits, p)
				}
				for i := 3; i >= 0; i-- {
					bits = append(bits, col>>i&1)
				}
				bits = append(bits, 0)
				for _, x := range bits {
					w.hold(x == 1, true, hp)
					w.hold(x == 0, true, hp)
				}
			}
			w.hold(false, true, 3*hp)
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x01, 0xff)}, 2}, {[]PatternElem{symStart, symData(0x01, 0xff), symData(0xff, 0xff)}, 1}}},
		{name: "EM4305", p: symParams(serEM4305, 125000), wave: func() *symWave {
			w := &symWave{}
			fc := 8 * us
			w.hold(true, true, 400*fc)
			for _, cmd := range []struct {
				c, a int
				word [4]int
			}{{2, 5, [4]int{0xDE, 0xAD, 0xBE, 0xEF}}, {1, -1, [4]int{0x12, 0x34, 0x56, 0x78}}, {2, 6, [4]int{0x01, 0x02, 0x03, 0x04}}} {
				w.mark()
				w.hold(false, true, 55*fc)
				bits := emBits(cmd.c, cmd.a, cmd.word)
				for _, b := range bits {
					if b == 1 {
						w.hold(true, true, 32*fc)
					} else {
						w.hold(true, true, 18*fc)
						w.hold(false, true, 20*fc)
					}
				}
				w.hold(true, true, 400*fc)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(2, 7)}, 2}, {[]PatternElem{symStart, symData(2, 7), symData(6, 15)}, 1}}},
		{name: "T55xx", p: withBits(symParams(serT55xx, 125000), 38), wave: func() *symWave {
			w := &symWave{}
			fc := 8 * us
			w.hold(true, true, 400*fc)
			for _, c := range []struct {
				op   int
				word uint32
				blk  int
			}{{2, 0x00148040, 0}, {2, 0xDEADBEEF, 5}, {3, 0x00148040, 1}} {
				w.mark()
				w.hold(false, true, 30*fc)
				bits := []int{c.op >> 1 & 1, c.op & 1, 0}
				for i := 31; i >= 0; i-- {
					bits = append(bits, int(c.word>>i&1))
				}
				bits = append(bits, c.blk>>2&1, c.blk>>1&1, c.blk&1)
				for _, b := range bits {
					on := 24.0
					if b == 1 {
						on = 56
					}
					w.hold(true, true, on*fc)
					w.hold(false, true, 25*fc)
				}
				w.hold(true, true, 400*fc)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(2, 3)}, 2}, {[]PatternElem{symStart, symData(2, 3), symData(0, 1), symData(0xDE, 0xff)}, 1}}},
		{name: "SWIM", p: symParams(serSWIM, 181818), wave: func() *symWave {
			w := &symWave{}
			T := 22 / 4e6
			w.hold(true, true, 20*us)
			frame := func(dir, nb, v int) {
				bits := []int{dir}
				p := 0
				for i := nb - 1; i >= 0; i-- {
					bits = append(bits, v>>i&1)
					p ^= v >> i & 1
				}
				bits = append(bits, p, 1)
				for _, b := range bits {
					lo := 2.0
					if b == 0 {
						lo = 20
					}
					w.hold(false, true, lo/22*T)
					w.hold(true, true, (22-lo)/22*T)
				}
				w.hold(true, true, 3*T)
			}
			for _, cmd := range []int{1, 2, 1} {
				w.mark()
				frame(0, 3, cmd)
				frame(0, 8, 1)
				frame(0, 8, 0x00)
				frame(0, 8, 0x50)
				frame(0, 8, 0x0A)
				frame(cmd-1, 8, 0xA0+cmd)
				w.hold(true, true, 10*T)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart, symData(1, 7)}, 2}, {[]PatternElem{symStart, symData(2, 7)}, 1}}},
		{name: "SDQ", p: symParams(serSDQ, 98425), wave: func() *symWave {
			w := &symWave{}
			T := 1 / 98425.0
			w.hold(true, true, 30*us)
			for _, bytes := range [][]int{{0x33, 0x01}, {0xCC, 0x44}, {0x33, 0x55}} {
				w.mark()
				w.hold(false, true, 2*T)
				w.hold(true, true, T)
				for _, v := range bytes {
					for i := 0; i < 8; i++ {
						lo := 0.75
						if v>>i&1 == 1 {
							lo = 0.25
						}
						w.hold(false, true, lo*T)
						w.hold(true, true, (1-lo)*T)
					}
				}
				w.hold(true, true, 50*us)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x33, 0xff)}, 2}, {[]PatternElem{symStart, symData(0xCC, 0xff), symData(0x44, 0xff)}, 1}}},
		{name: "PJON", p: withBits(symParams(serPJDL, 22727), 1), wave: func() *symWave {
			w := &symWave{}
			data, pad := 44*us, 116*us
			w.hold(false, true, 1*ms)
			for _, f := range [][]int{{0x2A, 0x02, 0x08, 0x40, 0x11, 0x22, 0x33, 0x3C}, {0x05, 0x00, 0x07, 0x16, 0x48, 0x69, 0x09}, {0x2A, 0x00, 0x06, 0x65, 0xFF, 0x7D}} {
				w.mark()
				for i := 0; i < 3; i++ {
					w.hold(true, true, pad)
					w.hold(false, true, data)
				}
				for _, v := range f {
					w.hold(true, true, pad)
					w.hold(false, true, data)
					for k := 0; k < 8; k++ {
						w.hold(v>>k&1 == 1, true, data)
					}
				}
				w.hold(false, true, 1*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x2A, 0xff)}, 2}, {[]PatternElem{symStart, symData(0x2A, 0xff), symData(0x02, 0xff)}, 1}}},
		{name: "DSI", p: func() SerialParams { p := symParams(serDSI, 600); p.Inverted = true; return p }(), wave: func() *symWave {
			w := &symWave{}
			half := 1 / 1200.0
			w.hold(false, true, 10*ms)
			for _, v := range []int{0x55, 0x80, 0x55} {
				w.mark()
				bits := []int{0}
				for i := 7; i >= 0; i-- {
					bits = append(bits, v>>i&1)
				}
				for _, b := range bits {
					w.hold(b == 1, true, half)
					w.hold(b == 0, true, half)
				}
				w.hold(false, true, 12*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x55, 0xff)}, 2}}},
		{name: "Qi", p: symParams(serQi, 2000), wave: func() *symWave {
			w := &symWave{}
			lvl := false
			w.hold(lvl, true, 5*ms)
			put := func(v int) {
				lvl = !lvl
				if v == 1 {
					w.hold(lvl, true, 250*us)
					lvl = !lvl
					w.hold(lvl, true, 250*us)
					return
				}
				w.hold(lvl, true, 500*us)
			}
			for _, pkt := range [][]int{{0x01, 0x80}, {0x03, 0x05}, {0x01, 0x40}} {
				w.mark()
				for i := 0; i < 11; i++ {
					put(1)
				}
				x := 0
				for _, v := range pkt {
					x ^= v
				}
				for _, v := range append(append([]int{}, pkt...), x) {
					put(0)
					p := 1
					for k := 0; k < 8; k++ {
						put(v >> k & 1)
						p ^= v >> k & 1
					}
					put(p)
					put(1)
				}
				lvl = !lvl
				w.hold(lvl, true, 8*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart, symAny}, 3}, {[]PatternElem{symStart, symData(0x01, 0xff)}, 2}, {[]PatternElem{symStart, symData(0x01, 0xff), symData(0x40, 0xff)}, 1}}},
		{name: "SD card", p: symParams(serSDCard, 1000000), wave: func() *symWave {
			w := &symWave{}
			bits := []int{}
			idle := func(n int) {
				for i := 0; i < n; i++ {
					bits = append(bits, 1)
				}
			}
			token := func(host bool, idx int, arg uint32) {
				t := []int{0, 0}
				if host {
					t[1] = 1
				}
				for i := 5; i >= 0; i-- {
					t = append(t, idx>>i&1)
				}
				for i := 31; i >= 0; i-- {
					t = append(t, int(arg>>i&1))
				}
				c := 0
				for _, b := range t {
					fb := (c>>6 ^ b) & 1
					c = c << 1 & 0x7f
					if fb != 0 {
						c ^= 0x09
					}
				}
				for i := 6; i >= 0; i-- {
					t = append(t, c>>i&1)
				}
				bits = append(append(bits, t...), 1)
			}
			idle(16)
			var marks []int
			for _, tk := range []struct {
				host bool
				idx  int
				arg  uint32
			}{{true, 17, 0x1000}, {false, 17, 0x900}, {true, 13, 0x12340000}, {false, 13, 0x900}, {true, 17, 0x2000}, {false, 17, 0x900}} {
				marks = append(marks, len(bits))
				token(tk.host, tk.idx, tk.arg)
				idle(12)
			}
			mi := 0
			for i, b := range bits {
				if mi < len(marks) && marks[mi] == i {
					w.mark()
					mi++
				}
				w.hold(false, b == 1, 0.5*us)
				w.hold(true, b == 1, 0.5*us)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart, symAny}, 6}, {[]PatternElem{symStart, symData(0x40|17, 0xff)}, 2}, {[]PatternElem{symStart, symData(0x40|13, 0xff), symData(0x12, 0xff)}, 1}}},
		{name: "PDI", p: symParams(serPDI, 250000), wave: func() *symWave {
			w := &symWave{}
			bits := []int{1, 1, 1, 1, 1, 1, 1, 1}
			char := func(v int) {
				bits = append(bits, 0)
				p := 0
				for i := 0; i < 8; i++ {
					bits = append(bits, v>>i&1)
					p ^= v >> i & 1
				}
				bits = append(bits, p, 1, 1)
			}
			var marks []int
			for _, ins := range [][]int{{0xC2, 0x59}, {0x80, 0x07}, {0xC2, 0x5A}} {
				marks = append(marks, len(bits))
				for _, v := range ins {
					char(v)
				}
				bits = append(bits, 1, 1, 1, 1)
			}
			mi := 0
			for i, b := range bits {
				if mi < len(marks) && marks[mi] == i {
					w.mark()
					mi++
				}
				w.hold(false, b == 1, 2*us)
				w.hold(true, b == 1, 2*us)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart, symData(0xC2, 0xff)}, 2}, {[]PatternElem{symStart, symData(0xC2, 0xff), symData(0x5A, 0xff)}, 1}}},
		{name: "cJTAG", p: symParams(serCJTAG, 250000), wave: func() *symWave {
			w := &symWave{}
			tms := false
			per := 4 * us
			rise := func(v bool) {
				w.hold(false, tms, per/4)
				tms = v
				w.hold(false, tms, per/4)
				w.hold(true, tms, per/2)
			}
			for i := 0; i < 12; i++ { // traffic before: the escape follows TMSC rises
				rise(i%3 == 0)
			}
			for e := 0; e < 3; e++ {
				w.mark()
				w.hold(false, tms, per/2)
				w.hold(true, tms, per/2)
				for i := 0; i < 6; i++ {
					tms = !tms
					w.hold(true, tms, per/2)
				}
				w.hold(true, tms, per/2)
				for i, b := range []bool{true, true, false, false, true, false, false, true, true, false, false, false, true} {
					_ = i
					rise(b)
				}
				for i := 0; i < 30; i++ { // OScan1 TCKs at run-test/idle
					rise(i%3 == 0)
				}
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}}},
		{name: "Maple", p: symParams(serMaple, 1000000), wave: func() *symWave {
			w := &symWave{}
			st := 250e-9
			la, lb := true, true
			set := func(a, b bool) { la, lb = a, b; w.hold(a, b, st) }
			for i := 0; i < 20; i++ {
				set(true, true)
			}
			for _, f := range [][]int{{0x00, 0x20, 0x00, 0x01, 0x21}, {0x01, 0x00, 0x20, 0x05, 0x00, 0x00, 0x00, 0x01, 0x25}, {0x00, 0x20, 0x00, 0x01, 0x21}} {
				w.mark()
				set(false, true)
				for i := 0; i < 4; i++ {
					set(false, false)
					set(false, true)
				}
				set(true, true)
				k := 0
				for _, v := range f {
					for i := 7; i >= 0; i-- {
						bit := v>>i&1 == 1
						if k%2 == 0 {
							set(la, bit)
							if !la {
								set(true, bit)
							}
							set(false, bit)
						} else {
							set(bit, lb)
							if !lb {
								set(bit, true)
							}
							set(bit, false)
						}
						k++
					}
				}
				set(true, false)
				set(false, false)
				set(true, false)
				set(false, false)
				set(true, true)
				for i := 0; i < 20; i++ {
					set(true, true)
				}
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}}},
		{name: "Caliper", p: symParams(serCaliper, 12500), wave: func() *symWave {
			w := &symWave{}
			w.hold(false, false, 20*ms)
			for _, v := range []int{1234, 4321, 1234} {
				w.mark()
				for i := 0; i < 24; i++ {
					bit := i < 16 && v>>i&1 == 1
					w.hold(false, bit, 40*us)
					w.hold(true, bit, 40*us)
				}
				w.hold(false, false, 12*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(1234&0xff, 0xff), symData(1234>>8, 0xff)}, 2}}},
		{name: "IR Samsung32", p: withBits(symParams(serIRMP, 1818), decode.IRSamsung32), wave: func() *symWave {
			w := &symWave{}
			w.hold(true, true, 20*ms)
			for _, addr := range []int{0x0707, 0xE0E0, 0x0707} {
				w.mark()
				w.runs(false, 4.5*ms, 4.5*ms)
				for _, v := range []int{addr, 0xFD02} {
					for i := 0; i < 16; i++ {
						sp := 550 * us
						if v>>i&1 != 0 {
							sp = 1650 * us
						}
						w.runs(false, 550*us, sp)
					}
				}
				w.runs(false, 550*us)
				w.hold(true, true, 40*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x0707, 0xffff)}, 2}, {[]PatternElem{symStart, symData(0xE0E0, 0xffff), symData(0xFD02, 0xffff)}, 1}}},
		{name: "IR Kaseikyo", p: withBits(symParams(serIRMP, 2364), decode.IRKaseikyo), wave: func() *symWave {
			w := &symWave{}
			w.hold(true, true, 20*ms)
			for _, cmd := range []int{0x123, 0x0F0, 0x123} {
				w.mark()
				maker := 0x2002
				par := (maker ^ maker>>4 ^ maker>>8 ^ maker>>12) & 15
				by := []int{maker & 0xff, maker >> 8, par | 8<<4, cmd & 15 << 4, cmd>>4&0x3f | 1<<6, 0}
				by[5] = by[2] ^ by[3] ^ by[4]
				w.runs(false, 3380*us, 1690*us)
				for _, v := range by {
					for i := 0; i < 8; i++ {
						sp := 423 * us
						if v>>i&1 != 0 {
							sp = 1269 * us
						}
						w.runs(false, 423*us, sp)
					}
				}
				w.runs(false, 423*us)
				w.hold(true, true, 74*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart}, 3}, {[]PatternElem{symStart, symData(0x2002, 0xffff), symData(0x8523, 0xffff)}, 2}}},
		{name: "IR Denon", p: withBits(symParams(serIRMP, 3226), decode.IRDenon), wave: func() *symWave {
			w := &symWave{}
			w.hold(true, true, 20*ms)
			for _, cmd := range []int{0x2C4, 0x016, 0x2C4} {
				for rep := 0; rep < 2; rep++ {
					if rep == 0 {
						w.mark()
					}
					v := 0x0A<<10 | cmd
					if rep == 1 {
						v ^= 0x3ff
					}
					for i := 14; i >= 0; i-- {
						sp := 745 * us
						if v>>i&1 != 0 {
							sp = 1780 * us
						}
						w.runs(false, 310*us, sp)
					}
					w.runs(false, 310*us)
					w.hold(true, true, 45*ms)
				}
				w.hold(true, true, 20*ms)
			}
			return w
		}, reqs: []symReq{{[]PatternElem{symStart, symData(0x0A, 0x1f), symData(0x2C4, 0x3ff)}, 2}}},
	}
}

// emBits are an EM4305 Login (addr < 0) or Write's bits (symbol test
// buses): the logic 0, the command and parity, the address, the word's
// bytes and parities, column parity and the stop 0.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-257
func emBits(cmd, addr int, word [4]int) []int {
	b := []int{0}
	par := func(v []int) int {
		p := 0
		for _, x := range v {
			p ^= x
		}
		return p
	}
	c := []int{cmd >> 2 & 1, cmd >> 1 & 1, cmd & 1}
	b = append(append(b, c...), par(c))
	if addr >= 0 {
		a := []int{addr & 1, addr >> 1 & 1, addr >> 2 & 1, addr >> 3 & 1, 0, 0}
		b = append(append(b, a...), par(a))
	}
	col := 0
	for _, v := range word {
		var r []int
		for i := 7; i >= 0; i-- {
			r = append(r, v>>i&1)
		}
		b = append(append(b, r...), par(r))
		col ^= v
	}
	for i := 7; i >= 0; i-- {
		b = append(b, col>>i&1)
	}
	return append(b, 0)
}
