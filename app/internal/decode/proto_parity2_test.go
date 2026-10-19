// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// physParityVectors adds the Go<->JS parity vectors of the sigrok protocols
// beyond the first thirty: each decoder's clean records, records whose
// checks fail, and corrupted copies (corrupt) that drive the reject paths.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-073
func physParityVectors(add parityAdd, add2 parityAdd2, corrupt func([]uint8) []uint8) {
	type cfg = map[string]interface{}
	type base struct {
		proto string
		colT  float64
		cfg   cfg
		w     []uint8
		dec   func([]uint8) Result
	}
	var bases []base
	line := func(proto string, sr float64, c cfg, w *timeline, dec func([]uint8, float64, LineCfg) Result, lc LineCfg) {
		bases = append(bases, base{proto, 1 / sr, c, w.codes(), func(x []uint8) Result { return dec(x, 1/sr, lc) }})
	}

	// ---- motion
	{
		w := newTimeline(2e6)
		w.add(0, 30e-6)
		pwmWave(w, 100e-6, []float64{0.25, 0.5, 0.75, 0.1, 0.9, 0.333, 0.05})
		pwmWave(w, 37e-6, []float64{0.2, 0.4, 0.6})
		line("pwm", 2e6, cfg{}, w, DecodePWM, LineCfg{})
	}
	// ---- radio and timing line codes
	{
		const a = 350e-6
		w := newTimeline(200e3)
		w.add(0, 40*a)
		rcFrame2262(w, []int{0, 1, 2, 0, 0, 1, 1, 2, 0, 1, 0, 0}, a)
		rcFrame2262(w, []int{2, 2, 2, 2, 2, 2, 0, 0, 0, 1, 1, 1}, a)
		line("rcencode", 200e3, cfg{}, w, DecodeRCEncode, LineCfg{})
		w = newTimeline(200e3)
		w.add(0, 40*a)
		rcFrame1527(w, 0x5A3C81, a)
		rcFrame1527(w, 0xA5A5A5, a)
		line("rcencode", 200e3, cfg{"variant": 24}, w, DecodeRCEncode, LineCfg{Variant: 24})
		line("rcencode", 200e3, cfg{}, w, DecodeRCEncode, LineCfg{})

		const hp = 1 / 2048.0
		w = newTimeline(100e3)
		w.add(0, 20e-3)
		oregonBurst(w, 3, []int{0xF, 0x8, 0x2, 0x4, 0x1, 0x3, 0xA, 0x0, 0x5, 0x3, 0x2, 0x0, 0x4, 0x5, 0x0}, hp, false, 40e-3)
		oregonBurst(w, 21, []int{0x1, 0xA, 0x2, 0xD, 0x2, 0x7, 0x1, 0x8, 0x2, 0x6, 0x1, 0x0, 0x4, 0x6}, hp/2, true, 40e-3)
		oregonBurst(w, 3, []int{0x1, 0xD, 0x2, 0x0, 0x1, 0x3, 0xA, 0x0, 0x5, 0x3}, hp, true, 40e-3) // a wrong checksum
		line("ook", 100e3, cfg{}, w, DecodeOOK, LineCfg{})

		T := 1.0 / 106000
		for _, pause := range []float64{2e-6, 0} {
			w = newTimeline(4e6)
			w.add(1, 40e-6)
			millerMessage(w, []int{1, 0, 1, 1, 0, 0, 1, 0, 0, 0, 1, 1, 1, 0, 1, 0}, T, pause, 60e-6)
			millerMessage(w, []int{0, 0, 0, 0, 1, 1, 1, 1, 0, 1}, T, pause, 60e-6)
			line("miller", 4e6, cfg{}, w, DecodeMiller, LineCfg{})
		}

		w = newTimeline(2000)
		w.add(0, 0.5)
		morseText(w, "SOS CQ DE SM5 73", 0.1, 12)
		line("morse", 2000, cfg{}, w, DecodeMorse, LineCfg{})

		w = newTimeline(1000)
		w.add(0, 1.3)
		dcf77Minute(w, 0, 0, 1, 1, 1, 0, false)
		dcf77Minute(w, 0x37, 0x12, 0x18, 0x7, 0x10, 0x26, true)
		line("dcf77", 1000, cfg{}, w, DecodeDCF77, LineCfg{})

		// a test source's code at 5 ms a second: the second read off it
		w = newTimeline(100e3)
		w.add(0, 7e-3)
		dcf77MinuteAt(w, 5e-3, 0x59, 0x23, 0x31, 0x5, 0x12, 0x99, false)
		dcf77MinuteAt(w, 5e-3, 0x00, 0x00, 0x01, 0x6, 0x01, 0x00, false)
		line("dcf77", 100e3, cfg{}, w, DecodeDCF77, LineCfg{})

		w = newTimeline(20e3)
		w.add(0, 30e-3)
		morseText(w, "CQ TEST 5NN", 2e-3, 10)
		line("morse", 20e3, cfg{}, w, DecodeMorse, LineCfg{})
	}
	// ---- 125 kHz RFID
	{
		w := newTimeline(500e3)
		w.add(1, 768e-6)
		em4100Frame(w, [5]int{0x01, 0x23, 0x45, 0x67, 0x89}, 256e-6, false)
		em4100Frame(w, [5]int{0x0A, 0xFF, 0x00, 0x5C, 0xE1}, 256e-6, true)
		line("em4100", 500e3, cfg{}, w, DecodeEM4100, LineCfg{})
		for _, carrier := range []bool{true, false} {
			w = newTimeline(1e6)
			f := rfidField{w, carrier}
			f.on(500 * rfidFC)
			em4305Command(f, em4305Bits(1, -1, [4]int{0x12, 0x34, 0x56, 0x78}, false))
			em4305Command(f, em4305Bits(2, 5, [4]int{0xDE, 0xAD, 0xBE, 0xEF}, true))
			line("em4305", 1e6, cfg{}, w, DecodeEM4305, LineCfg{})
			w = newTimeline(1e6)
			f = rfidField{w, carrier}
			f.on(500 * rfidFC)
			t55xxCommand(f, t55xxBits(2, 0x51243648, 0, 0x00148040, 0))
			t55xxCommand(f, t55xxBits(2, -1, 1, 0xDEADBEEF, 5))
			line("t55xx", 1e6, cfg{}, w, DecodeT55xx, LineCfg{})
		}
	}
	// ---- single-wire buses
	{
		const T = 22 / 4e6
		w := newTimeline(20e6)
		w.add(1, 20e-6)
		swimSync(w, T)
		swimFrame(w, 0, 3, 1, T, 1, false, 3*T)
		swimFrame(w, 0, 8, 1, T, 1, false, 3*T)
		swimFrame(w, 0, 8, 0x00, T, 1, false, 3*T)
		swimFrame(w, 0, 8, 0x7F, T, 1, true, 3*T)
		swimFrame(w, 0, 8, 0x60, T, 1, false, 3*T)
		swimFrame(w, 1, 8, 0x5A, T, 0, false, 3*T)
		line("swim", 20e6, cfg{}, w, DecodeSWIM, LineCfg{})

		Tq := 1 / 98425.0
		w = newTimeline(10e6)
		w.add(1, 30e-6)
		sdqBreak(w, Tq)
		for _, v := range []int{0x33, 0x01, 0xFE} {
			sdqByte(w, v, Tq)
		}
		w.add(1, 50e-6)
		line("sdq", 10e6, cfg{}, w, DecodeSDQ, LineCfg{})

		w = newTimeline(2e6)
		w.add(0, 500e-6)
		pjdlFrame(w, 1, pjonPacket(0x2A, 0x02, []int{0x11, 0x22, 0x33}, false), 1e-3)
		pjdlFrame(w, 1, pjonPacket(0x05, 0x00, []int{0x48, 0x69}, true), 1e-3)
		line("pjdl", 2e6, cfg{}, w, DecodePJDL, LineCfg{})

		w = newTimeline(100e3)
		w.add(0, 10e-3)
		for _, v := range []int{0x00, 0xFF, 0x80, 0x55} {
			dsiFrame(w, v, 12e-3)
		}
		line("dsi", 100e3, cfg{}, w, DecodeDSI, LineCfg{})

		w = newTimeline(200e3)
		w.add(0, 5e-3)
		qiPacket(w, []int{0x01, 0x80}, false, 8e-3)
		qiPacket(w, []int{0x02, 0x01}, true, 8e-3)
		line("qi", 200e3, cfg{}, w, DecodeQi, LineCfg{})
	}
	// ---- IR multi
	{
		const sr = 200e3
		w := irOracleLine(sr, func(l irLine) {
			irSamsung32(l, 0x0707, 0xFD02)
			l.space(60e-3)
			irNEC16(l, 0x12, 0x34)
			irNEC42(l, 0x1ABC, 0x5D)
			irJVC(l, 0x3, 0x5A1, false)
			irJVC(l, 0x3, 0x5A1, true)
			l.space(60e-3)
			irKaseikyo(l, 0x2002, 0x8, 0x0, 0x123, 0x1)
			irDenon(l, 0x0A, 0x2C4)
			l.space(60e-3)
			irMatsushita(l, 0x123, 0x456)
			irSamsung37(l, 0x0707, 0x5, 0x9A)
		})
		line("irmp", sr, cfg{}, w, DecodeIRMP, LineCfg{})
	}

	for _, b := range bases {
		add(b.proto, b.w, b.colT, b.cfg, b.dec(b.w))
		for it := 0; it < 16*parityMult(); it++ {
			w := corrupt(b.w)
			add(b.proto, w, b.colT, b.cfg, b.dec(w))
		}
	}

	// ---- two lines: clean, and each line corrupted in turn
	type base2 struct {
		proto  string
		colT   float64
		c1, c2 []uint8
		dec    func(a, b []uint8) Result
	}
	var b2 []base2
	two := func(proto string, sr float64, l twoLine, dec func(a, b []uint8, colT float64, cfg ClockedCfg) Result) {
		b2 = append(b2, base2{proto, 1 / sr, l.a.codes(), l.b.codes(), func(a, b []uint8) Result { return dec(a, b, 1/sr, ClockedCfg{}) }})
	}
	{
		l := newTwoLine(1e6)
		l.add(0, 0, 500e-6)
		quadMoves(l, []int{1, 1, 1, 1, 1, -1, -1, 1, -1}, 100e-6, 1e-3)
		quadMoves(l, []int{-1, -1, -1}, 60e-6, 500e-6)
		two("quad", 1e6, l, DecodeQuadrature)
		l = newTwoLine(1e6)
		l.add(0, 1, 300e-6)
		stepperMoves(l, []byte{1, 1, 1, 0, 0, 1, 0}, 200e-6, 2e-3)
		two("stepper", 1e6, l, DecodeStepper)

		l = newTwoLine(4e6)
		var bits []int
		for _, t := range [][]int{sdIdle(10), sdToken(true, 8, 0x1AA, false), sdIdle(4), sdToken(false, 8, 0x1AA, false),
			sdIdle(10), sdToken(true, 2, 0, false), sdIdle(4),
			sdR2([15]int{0x03, 0x53, 0x44, 0x53, 0x44, 0x31, 0x36, 0x47, 0x80, 0x12, 0x34, 0x56, 0x78, 0x01, 0x23}),
			sdIdle(10), sdToken(true, 17, 0x1000, true), sdIdle(10)} {
			bits = append(bits, t...)
		}
		clockBits(l, bits, 4e-6)
		two("sdcard", 4e6, l, DecodeSDCard)

		l = newTwoLine(2e6)
		bits = []int{1, 1, 1, 1, 1, 1, 1, 1}
		for _, v := range []int{0xC2, 0x59, 0x0C, 0x00, 0x00, 0x00, 0x01, 0xA0, 0x02, 0x24, 0x10, 0x20, 0x30} {
			bits = append(bits, pdiChar(v, false)...)
		}
		bits = append(bits, pdiChar(0x6B, true)...)
		bits = append(bits, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1)
		clockBits(l, bits, 4e-6)
		two("pdi", 2e6, l, DecodePDI)

		c := &cjtagLine{l: newTwoLine(2e6), per: 4e-6}
		for i := 0; i < 4; i++ {
			c.rise(0)
		}
		c.escape(6)
		for _, b := range []int{1, 1, 0, 0, 1, 0, 0, 1, 1, 0, 0, 0, 1} {
			c.rise(b)
		}
		c.tck(0, 0, 0, true)
		c.scan(true, 4, 0x5, 0x1)
		c.scan(false, 16, 0x1234, 0xBEEF)
		c.escape(8)
		c.rise(0)
		two("cjtag", 2e6, c.l, DecodeCJTAG)

		m := &mapleLine{l: newTwoLine(20e6), st: 250e-9}
		m.set(1, 1)
		m.frame(mapleBytes(0x20, 0x00, 0x01, nil, false))
		m.frame(mapleBytes(0x00, 0x20, 0x05, []int{0x00, 0x00, 0x00, 0x01}, true))
		m.set(1, 1)
		two("maple", 20e6, m.l, DecodeMaple)

		l = newTwoLine(1e6)
		l.add(0, 0, 20e-3)
		caliperFrame(l, 1234, false, false, 80e-6, 30e-3)
		caliperFrame(l, 2001, true, true, 80e-6, 30e-3)
		two("caliper", 1e6, l, DecodeCaliper)
	}
	for _, b := range b2 {
		add2(b.proto, b.c1, b.c2, b.colT, cfg{}, b.dec(b.c1, b.c2))
		for it := 0; it < 12*parityMult(); it++ {
			c1, c2 := b.c1, b.c2
			if it%2 == 0 {
				c1 = corrupt(c1)
			} else {
				c2 = corrupt(c2)
			}
			if len(c1) != len(c2) {
				n := min(len(c1), len(c2))
				c1, c2 = c1[:n], c2[:n]
			}
			add2(b.proto, c1, c2, b.colT, cfg{}, b.dec(c1, c2))
		}
	}
}
