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

	for _, b := range bases {
		add(b.proto, b.w, b.colT, b.cfg, b.dec(b.w))
		for it := 0; it < 16; it++ {
			w := corrupt(b.w)
			add(b.proto, w, b.colT, b.cfg, b.dec(w))
		}
	}
}
