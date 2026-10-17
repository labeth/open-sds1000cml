// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// parityAdd records one Go decode as a parity vector (TestJSDecoderParity).
type parityAdd func(proto string, codes []uint8, colT float64, cfg map[string]interface{}, r Result)

// parityAdd2 is parityAdd for a two-line protocol.
type parityAdd2 func(proto string, c1, c2 []uint8, colT float64, cfg map[string]interface{}, r Result)

// protoParityVectors adds the Go<->JS parity vectors of the protocols beyond
// the first ten: each family's clean frames, frames with their checks
// failing, and corrupted copies (corrupt) that drive the reject paths.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-073
func protoParityVectors(add parityAdd, add2 parityAdd2, corrupt func([]uint8) []uint8) {
	type cfg = map[string]interface{}
	type base struct {
		proto string
		colT  float64
		cfg   cfg
		w     []uint8
		dec   func([]uint8) Result
	}
	var bases []base

	// ---- UART family
	{
		sr := 1e6
		w := newTimeline(sr)
		w.idle(150e-6)
		linFrame(w, 19200, 13, 0x3c, []int{1, 2, 3, 4, 5, 6, 7, 8}, true)
		linFrame(w, 19200, 13, 0x12, []int{0xA5, 0x5A}, false)
		linFrame(w, 19200, 13, 0x21, []int{0x00}, true) // classic on a non-diagnostic ID
		linFrame(w, 19200, 13, 0x20, nil, false)
		lin := w.codes()
		bases = append(bases,
			base{"lin", 1 / sr, cfg{}, lin, func(c []uint8) Result { return DecodeLIN(c, 1/sr, LINCfg{}) }},
			base{"lin", 1 / sr, cfg{"baud": 19200}, lin, func(c []uint8) Result { return DecodeLIN(c, 1/sr, LINCfg{Baud: 19200}) }})
		wd := newTimeline(10e6)
		wd.idle(40e-6)
		dmxPacket(wd, 100, 12, 0, []int{1, 2, 3, 0xFF, 0x80, 0x7F})
		dmxPacket(wd, 92, 8, 0x17, []int{9})
		dmx := wd.codes()
		bases = append(bases, base{"dmx512", 1e-7, cfg{}, dmx, func(c []uint8) Result { return DecodeDMX512(c, 1e-7, DMXCfg{}) }})
		wm := newTimeline(sr)
		wm.idle(100e-6)
		for _, b := range []int{0x90, 0x3C, 0x64, 0x3E, 0x64, 0xFE, 0xB0, 0x07, 0x7F, 0x12, 0xF0, 0x7E, 0x00, 0xF7, 0xF2, 0x10} {
			tlUART(wm, b, 31250, "none", 1)
		}
		wm.idle(500e-6)
		midi := wm.codes()
		bases = append(bases, base{"midi", 1e-6, cfg{}, midi, func(c []uint8) Result { return DecodeMIDI(c, 1e-6, MIDICfg{}) }})
		for _, par := range []string{"even", "none", "odd"} {
			stops := 1
			if par == "none" {
				stops = 2
			}
			wb := newTimeline(2e6)
			wb.idle(3e-3)
			modbusFrame(wb, 19200, par, stops, []int{0x01, 0x03, 0x00, 0x6B, 0x00, 0x03})
			modbusFrame(wb, 19200, par, stops, []int{0x11, 0x83, 0x02})
			mb := wb.codes()
			bases = append(bases, base{"modbus", 5e-7, cfg{}, mb, func(c []uint8) Result { return DecodeModbus(c, 5e-7, ModbusCfg{}) }})
		}
	}

	// ---- pulse-coded single wire
	{
		w := newTimeline(4e6)
		w.idle(100e-6)
		rom := owROM(0x28, 0xFF, 0x4C, 0x60, 0x91, 0x16, 0x05)
		owReset(w, false, true)
		owByte(w, 0x55, false)
		for _, v := range rom {
			owByte(w, v, false)
		}
		owByte(w, 0x44, false)
		owReset(w, false, true)
		owByte(w, 0xF0, false)
		owSearchROM(w, rom[:3])
		owReset(w, false, true)
		owByte(w, 0x69, false)
		for _, v := range rom {
			owByte(w, v^1, true)
		}
		owReset(w, true, true)
		owByte(w, 0xBE, true)
		w.idle(100e-6)
		ow := w.codes()
		bases = append(bases, base{"onewire", 2.5e-7, cfg{}, ow, func(c []uint8) Result { return DecodeOneWire(c, 2.5e-7, OneWireCfg{}) }})
		for _, rate := range []float64{800e3, 400e3} {
			ww := newTimeline(50e6)
			ww.add(0, 60e-6)
			wsFrame(ww, rate, []int{0xFF, 0x00, 0x80, 0x12, 0x34, 0x56})
			wsFrame(ww, rate, []int{0x01, 0x02, 0x03, 0x04})
			ws := ww.codes()
			bases = append(bases, base{"ws281x", 2e-8, cfg{}, ws, func(c []uint8) Result { return DecodeWS281x(c, 2e-8, WS281xCfg{}) }})
		}
		wd := newTimeline(2e6)
		wd.idle(1e-3)
		dhtReading(wd, 1000, [4]int{0x02, 0x8C, 0x01, 0x5F}, -1)
		wd.idle(1e-3)
		dhtReading(wd, 18000, [4]int{0x2D, 0x00, 0x17, 0x00}, 0x11)
		dht := wd.codes()
		bases = append(bases, base{"dht", 5e-7, cfg{}, dht, func(c []uint8) Result { return DecodeDHT(c, 5e-7, DHTCfg{}) }})
		wc := newTimeline(200e3)
		wc.idle(5e-3)
		cecMessage(wc, []int{0x40, 0x04}, []int{0, 0})
		cecMessage(wc, []int{0x4F, 0x82, 0x10, 0x00}, []int{1, 1, 0, 1})
		cecMessage(wc, []int{0x01}, []int{1})
		cec := wc.codes()
		bases = append(bases, base{"cec", 5e-6, cfg{}, cec, func(c []uint8) Result { return DecodeCEC(c, 5e-6, CECCfg{}) }})
		wj := newTimeline(1e6)
		wj.add(0, 500e-6)
		j1850Frame(wj, []int{0x68, 0x6A, 0xF1, 0x01, 0x00}, false)
		j1850Frame(wj, []int{0x48, 0x6B, 0x10, 0x41, 0x00, 0xBE}, false)
		j1850Frame(wj, []int{0x68, 0x6A, 0xF1}, true)
		j18 := wj.codes()
		bases = append(bases, base{"j1850", 1e-6, cfg{}, j18, func(c []uint8) Result { return DecodeJ1850(c, 1e-6, J1850Cfg{}) }})
	}

	// ---- infrared remotes, each as a receiver's output and a modulated drive
	for _, lf := range []struct {
		high bool
		hz   float64
	}{{false, 0}, {true, 38e3}} {
		const sr = 500e3
		gen := func(f func(l irLine)) []uint8 {
			w := newTimeline(sr)
			l := irLine{w, lf.high, lf.hz}
			l.space(20e-3)
			f(l)
			return w.codes()
		}
		nec := gen(func(l irLine) { necFrame(l, 0x04, -1, 0x08); necRepeat(l); necFrame(l, 0x34, 0x12, 0x5A) })
		sirc := gen(func(l irLine) {
			sircFrame(l, 12, 0x15, 1, 0)
			sircFrame(l, 15, 0x33, 0xA4, 0)
			sircFrame(l, 20, 0x7F, 0x1A, 0xC3)
		})
		rc5 := gen(func(l irLine) { rc5Frame(l, 0, 0, 0x0C); rc5Frame(l, 1, 0x1F, 0x3F); rc5Frame(l, 0, 5, 0x50) })
		rc6 := gen(func(l irLine) { rc6Frame(l, 0, 0, []int{0, 0x0C}); rc6Frame(l, 6, 1, []int{0x80, 0x0F, 4, 0x31}) })
		bases = append(bases,
			base{"irnec", 1 / sr, cfg{}, nec, func(c []uint8) Result { return DecodeIRNEC(c, 1/sr, IRCfg{}) }},
			base{"irsirc", 1 / sr, cfg{}, sirc, func(c []uint8) Result { return DecodeIRSIRC(c, 1/sr, IRCfg{}) }},
			base{"irrc5", 1 / sr, cfg{}, rc5, func(c []uint8) Result { return DecodeIRRC5(c, 1/sr, IRCfg{}) }},
			base{"irrc6", 1 / sr, cfg{}, rc6, func(c []uint8) Result { return DecodeIRRC6(c, 1/sr, IRCfg{}) }})
	}

	// ---- biphase
	{
		wd := newTimeline(200e3)
		wd.idle(5e-3)
		daliFrame(wd, 16, 0x0190, 3e-3)
		daliFrame(wd, 8, 0x5A, 10e-3)
		daliFrame(wd, 24, 0x123456, 10e-3)
		dali := wd.codes()
		bases = append(bases, base{"dali", 5e-6, cfg{}, dali, func(c []uint8) Result { return DecodeDALI(c, 5e-6, DALICfg{}) }})
		wp := newTimeline(25e6)
		wp.add(0, 20e-6)
		pdMessage(wp, pdSOP, 0x1161, []uint32{0x0801912C}, false)
		pdMessage(wp, pdSOP, 0x0041, nil, false)
		pdMessage(wp, pdSOPPrime, 0x1042, []uint32{0x12345678}, true)
		pdMessage(wp, pdHardReset, 0, nil, false)
		pd := wp.codes()
		bases = append(bases, base{"usbpd", 4e-8, cfg{}, pd, func(c []uint8) Result { return DecodeUSBPD(c, 4e-8, USBPDCfg{}) }})
		ws := newTimeline(250e6)
		ws.add(0, 50e-9)
		var smp [][2]int
		for i := 0; i < 5; i++ {
			smp = append(smp, [2]int{(i * 0x1357) & 0xffffff, 0xFFFFF0 - i*0x10})
		}
		spdifStream(ws, 48e3, 190, smp)
		sp := ws.codes()
		bases = append(bases, base{"spdif", 4e-9, cfg{}, sp, func(c []uint8) Result { return DecodeSPDIF(c, 4e-9, SPDIFCfg{}) }})
	}

	// ---- USB full speed (tokens' CRC5, data CRC16)
	{
		dp, _ := oracleUSBFSWave(120e6, [][]int{
			usblsPacketBits(0xD, usblsTokenBits(0x15, 0xE)),
			usblsPacketBits(0x3, usblsDataBits([]int{0x80, 0x06, 0x00, 0x01}, 0)),
			usblsPacketBits(0x9, usblsTokenBits(0x01, 0x1)[:15]),
			usblsPacketBits(0xB, usblsDataBits([]int{0xFF, 0x00}, 0x1)),
		}, 12)
		fs := bitsToCodes(dp)
		bases = append(bases, base{"usbls", 1 / 120e6, cfg{}, fs, func(c []uint8) Result { return DecodeUSBLS(c, 1/120e6, USBLSCfg{}) }})
	}

	// ---- clocked two-line: clean, and each line corrupted in turn
	{
		type base2 struct {
			proto  string
			colT   float64
			c1, c2 []uint8
			dec    func(a, b []uint8) Result
		}
		var b2 []base2
		lp := newTwoLine(1e6)
		lp.add(1, 1, 300e-6)
		ps2Frame(lp, 0x1C, false, false, 80e-6, 200e-6)
		ps2Frame(lp, 0xF0, false, false, 80e-6, 100e-6)
		ps2Frame(lp, 0xED, true, false, 80e-6, 500e-6)
		ps2Frame(lp, 0x33, false, true, 80e-6, 500e-6)
		b2 = append(b2, base2{"ps2", 1e-6, lp.a.codes(), lp.b.codes(), func(a, b []uint8) Result { return DecodePS2(a, b, 1e-6, ClockedCfg{}) }})
		s := &swdLine{t: newTwoLine(20e6), per: 1e-6}
		s.host(0, 0)
		s.lineReset()
		s.transfer(false, true, 0, 1, 0x2BA01477, false, 2)
		s.transfer(false, false, 8, 1, 0x000000F0, false, 0)
		s.transfer(true, true, 0xC, 2, 0, false, 2)
		s.transfer(true, true, 0xC, 1, 0xDEADBEEF, true, 2)
		b2 = append(b2, base2{"swd", 5e-8, s.t.a.codes(), s.t.b.codes(), func(a, b []uint8) Result { return DecodeSWD(a, b, 5e-8, ClockedCfg{}) }})
		lm := newTwoLine(25e6)
		lm.add(1, 1, 2e-6)
		mdioFrame(lm, 400e-9, 32, 1, 2, 0x01, 0x02, 0x796D, 0)
		mdioFrame(lm, 400e-9, 20, 0, 3, 0x02, 0x01, 0xBEEF, 0)
		mdioFrame(lm, 400e-9, 32, 1, 2, 0x05, 0x02, 0xFFFF, 1)
		b2 = append(b2, base2{"mdio", 4e-8, lm.a.codes(), lm.b.codes(), func(a, b []uint8) Result { return DecodeMDIO(a, b, 4e-8, ClockedCfg{}) }})
		lw := newTwoLine(100e3)
		lw.add(1, 1, 10e-3)
		wiegandFrame(lw, wiegandBits(26, 12, 4660), 2e-3)
		wiegandFrame(lw, wiegandBits(34, 1000, 65535), 1e-3)
		wiegandFrame(lw, "1010110011", 2e-3)
		b2 = append(b2, base2{"wiegand", 1e-5, lw.a.codes(), lw.b.codes(), func(a, b []uint8) Result { return DecodeWiegand(a, b, 1e-5, ClockedCfg{}) }})
		for _, b := range b2 {
			add2(b.proto, b.c1, b.c2, b.colT, cfg{}, b.dec(b.c1, b.c2))
			for it := 0; it < 12; it++ {
				c1, c2 := b.c1, b.c2
				if it%2 == 0 {
					c1 = corrupt(c1)
				} else {
					c2 = corrupt(c2)
				}
				if len(c1) != len(c2) { // a truncated line: both end there
					m := min(len(c1), len(c2))
					c1, c2 = c1[:m], c2[:m]
				}
				add2(b.proto, c1, c2, b.colT, cfg{}, b.dec(c1, c2))
			}
		}
	}

	for _, b := range bases {
		add(b.proto, b.w, b.colT, b.cfg, b.dec(b.w))
		for it := 0; it < 16; it++ {
			w := corrupt(b.w)
			add(b.proto, w, b.colT, b.cfg, b.dec(w))
		}
	}
}
