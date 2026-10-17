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

	for _, b := range bases {
		add(b.proto, b.w, b.colT, b.cfg, b.dec(b.w))
		for it := 0; it < 16; it++ {
			w := corrupt(b.w)
			add(b.proto, w, b.colT, b.cfg, b.dec(w))
		}
	}
}
