// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"testing"
)

// The decoders run on the scope's single ARM core, inside BUS AUTO's
// search and on every decoded frame; these time each one on a signal it
// decodes (the detection corpus or the round-trip generators), so a change
// that makes one of them much dearer shows up.
// go test ./internal/decode -run - -bench Dec

// benchCase is the first detection case of proto.
// TRLC-LINKS: REQ-SDS-018
func benchCase(b *testing.B, proto string) detectCase {
	for _, c := range append(physDetectCases(), protoDetectCases()...) {
		if c.proto == proto && c.chA == 0 {
			return c
		}
	}
	b.Fatalf("no case for %s", proto)
	return detectCase{}
}

// TRLC-LINKS: REQ-SDS-018
func BenchmarkDec(b *testing.B) {
	line := func(proto string, dec func(c []uint8, ct float64) Result) {
		b.Run(proto, func(b *testing.B) {
			c := benchCase(b, proto)
			for b.Loop() {
				dec(c.c1, c.colT)
			}
		})
	}
	pair := func(proto string, dec func(x, y []uint8, ct float64) Result) {
		b.Run(proto, func(b *testing.B) {
			c := benchCase(b, proto)
			for b.Loop() {
				dec(c.c1, c.c2, c.colT)
			}
		})
	}
	lc, cc := LineCfg{Format: "hex"}, ClockedCfg{Format: "hex"}
	line("pwm", func(c []uint8, ct float64) Result { return DecodePWM(c, ct, lc) })
	line("rcencode", func(c []uint8, ct float64) Result { return DecodeRCEncode(c, ct, lc) })
	line("ook", func(c []uint8, ct float64) Result { return DecodeOOK(c, ct, lc) })
	line("miller", func(c []uint8, ct float64) Result { return DecodeMiller(c, ct, lc) })
	line("morse", func(c []uint8, ct float64) Result { return DecodeMorse(c, ct, lc) })
	line("dcf77", func(c []uint8, ct float64) Result { return DecodeDCF77(c, ct, lc) })
	line("em4100", func(c []uint8, ct float64) Result { return DecodeEM4100(c, ct, lc) })
	line("em4305", func(c []uint8, ct float64) Result { return DecodeEM4305(c, ct, lc) })
	line("t55xx", func(c []uint8, ct float64) Result { return DecodeT55xx(c, ct, lc) })
	line("swim", func(c []uint8, ct float64) Result { return DecodeSWIM(c, ct, lc) })
	line("sdq", func(c []uint8, ct float64) Result { return DecodeSDQ(c, ct, lc) })
	line("pjdl", func(c []uint8, ct float64) Result { return DecodePJDL(c, ct, lc) })
	line("dsi", func(c []uint8, ct float64) Result { return DecodeDSI(c, ct, lc) })
	line("qi", func(c []uint8, ct float64) Result { return DecodeQi(c, ct, lc) })
	line("irmp", func(c []uint8, ct float64) Result { return DecodeIRMP(c, ct, lc) })
	pair("quad", func(x, y []uint8, ct float64) Result { return DecodeQuadrature(x, y, ct, cc) })
	pair("stepper", func(x, y []uint8, ct float64) Result { return DecodeStepper(x, y, ct, cc) })
	pair("sdcard", func(x, y []uint8, ct float64) Result { return DecodeSDCard(x, y, ct, cc) })
	pair("pdi", func(x, y []uint8, ct float64) Result { return DecodePDI(x, y, ct, cc) })
	pair("cjtag", func(x, y []uint8, ct float64) Result { return DecodeCJTAG(x, y, ct, cc) })
	pair("maple", func(x, y []uint8, ct float64) Result { return DecodeMaple(x, y, ct, cc) })
	pair("caliper", func(x, y []uint8, ct float64) Result { return DecodeCaliper(x, y, ct, cc) })
	line("lin", func(c []uint8, ct float64) Result { return DecodeLIN(c, ct, LINCfg{Format: "hex"}) })
	line("dmx512", func(c []uint8, ct float64) Result { return DecodeDMX512(c, ct, DMXCfg{Format: "hex"}) })
	line("midi", func(c []uint8, ct float64) Result { return DecodeMIDI(c, ct, MIDICfg{Format: "hex"}) })
	line("modbus", func(c []uint8, ct float64) Result { return DecodeModbus(c, ct, ModbusCfg{Format: "hex"}) })
	line("onewire", func(c []uint8, ct float64) Result { return DecodeOneWire(c, ct, OneWireCfg{Format: "hex"}) })
	line("ws281x", func(c []uint8, ct float64) Result { return DecodeWS281x(c, ct, WS281xCfg{Format: "hex"}) })
	line("dht", func(c []uint8, ct float64) Result { return DecodeDHT(c, ct, DHTCfg{Format: "hex"}) })
	line("cec", func(c []uint8, ct float64) Result { return DecodeCEC(c, ct, CECCfg{Format: "hex"}) })
	line("j1850", func(c []uint8, ct float64) Result { return DecodeJ1850(c, ct, J1850Cfg{Format: "hex"}) })
	ir := IRCfg{Format: "hex"}
	line("irnec", func(c []uint8, ct float64) Result { return DecodeIRNEC(c, ct, ir) })
	line("irsirc", func(c []uint8, ct float64) Result { return DecodeIRSIRC(c, ct, ir) })
	line("irrc5", func(c []uint8, ct float64) Result { return DecodeIRRC5(c, ct, ir) })
	line("irrc6", func(c []uint8, ct float64) Result { return DecodeIRRC6(c, ct, ir) })
	line("dali", func(c []uint8, ct float64) Result { return DecodeDALI(c, ct, DALICfg{Format: "hex"}) })
	line("usbpd", func(c []uint8, ct float64) Result { return DecodeUSBPD(c, ct, USBPDCfg{Format: "hex"}) })
	line("spdif", func(c []uint8, ct float64) Result { return DecodeSPDIF(c, ct, SPDIFCfg{Format: "hex"}) })
	pair("ps2", func(x, y []uint8, ct float64) Result { return DecodePS2(x, y, ct, cc) })
	pair("swd", func(x, y []uint8, ct float64) Result { return DecodeSWD(x, y, ct, cc) })
	pair("mdio", func(x, y []uint8, ct float64) Result { return DecodeMDIO(x, y, ct, cc) })
	pair("wiegand", func(x, y []uint8, ct float64) Result { return DecodeWiegand(x, y, ct, cc) })

	gen := func(name string, codes []uint8, dec func([]uint8) Result) {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				dec(codes)
			}
		})
	}
	msg := []int{0x48, 0x65, 0x6C, 0x6C, 0x6F, 0x2C, 0x20, 0x77, 0x6F, 0x72, 0x6C, 0x64, 0x21, 0x0D, 0x0A, 0x55}
	gen("uart", uartWave(msg, 40), func(c []uint8) Result {
		return DecodeUART(c, 1e-6, UARTCfg{Bits: 8, Parity: "none", Format: "hex"})
	})
	{
		w, ct := bkBuild(msg, true, true, 8, 20, 100000, 40, 40)
		gen("manchester", w, func(c []uint8) Result {
			return DecodeManchester(c, ct, ManchesterCfg{IEEE: true, MSB: true, Format: "hex"})
		})
	}
	{
		nibs := []int{0x1, 0xA, 0x5, 0xF, 0x0, 0xC, 0x3, 0}
		nibs[7] = sentCRC4(nibs[1:7])
		gen("sent", sentWave([][]int{nibs, nibs, nibs, nibs}, 6, 0, 0), func(c []uint8) Result {
			return DecodeSENT(c, 1e-6, SENTCfg{})
		})
	}
	{
		_, wire := canStdFrame(0x123, 8, []int{1, 2, 3, 4, 5, 6, 7, 8})
		gen("canfd", canRender(append(append([]int{}, wire...), wire...), 40, true, 80, 80), func(c []uint8) Result {
			return DecodeCANFD(c, 1e-6, CANFDCfg{DominantLow: true})
		})
	}
	{
		words := []int{0x1234, 0xABCD, 0x0F0F, 0x5555, 0x00FF}
		cmd := []bool{true, false, false, true, false}
		par := make([]int, len(words))
		for i, wd := range words {
			par[i] = mil1553OddParity(wd)
		}
		gen("mil1553", mil1553Wave(words, cmd, par, 40), func(c []uint8) Result {
			return DecodeMIL1553(c, 1/(40*1e6), MIL1553Cfg{})
		})
	}
	{
		var w []uint8
		for _, wd := range [][]int{arincMakeWord(0312, 1, 0x2ABCD, 3), arincMakeWord(0107, 2, 0x15A3F, 1), arincMakeWord(0200, 0, 0x12345, 0)} {
			arincIdle(&w, 40*6)
			arincAppendWord(&w, wd, 40)
		}
		arincIdle(&w, 40*6)
		gen("arinc429", w, func(c []uint8) Result {
			return DecodeARINC429(c, 1/(40*100e3), ARINC429Cfg{})
		})
	}
	{
		pk := []usbPkt{{pid: 0xD, data: []int{0x12, 0x00}}, {pid: 0x3, data: []int{0x48, 0x69, 0xFF, 0x0F, 0x7E}}, {pid: 0x2}}
		gen("usbls", usbWave(append(append([]usbPkt{}, pk...), pk...), 40), func(c []uint8) Result {
			return DecodeUSBLS(c, 1.0/(40*1.5e6), USBLSCfg{})
		})
	}
	{
		f := brFlexFixCRC([]int{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0})
		gen("flexray", flexrayWave([][]int{f, f, f}, 20, 8), func(c []uint8) Result {
			return DecodeFlexRay(c, 5e-9, FlexRayCfg{})
		})
	}
	b.Run("spi", func(b *testing.B) {
		clk, data := spiWave(msg, 20)
		for b.Loop() {
			DecodeSPI(clk, data, 2e-7, SPICfg{MSB: true, Format: "hex"})
		}
	})
	b.Run("i2c", func(b *testing.B) {
		scl, sda := i2cWave(0x50, 0, msg, 20)
		for b.Loop() {
			DecodeI2C(scl, sda, 2e-7, I2CCfg{Format: "hex"})
		}
	})
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func BenchmarkAutodetect(b *testing.B) {
	cs := append(physDetectCases(), protoDetectCases()...)
	b.Run("corpus", func(b *testing.B) {
		for b.Loop() {
			for _, c := range cs[:len(cs):len(cs)] {
				Autodetect(c.c1, c.c2, c.colT, "hex")
			}
		}
	})
	b.Run("fast-scope", func(b *testing.B) {
		type cap struct {
			c1, c2 []uint8
			ss     float64
		}
		var caps []cap
		for _, sc := range scopeCaptures {
			c1, c2, ss := loadGzJSON(b, "testdata/"+sc.file)
			caps = append(caps, cap{c1, c2, ss})
		}
		for b.Loop() {
			for _, c := range caps {
				AutodetectFast(c.c1, c.c2, c.ss, "hex")
			}
		}
	})
}

// TRLC-LINKS: REQ-SDS-018
func BenchmarkSliceChannel(b *testing.B) {
	c := benchCase(b, "dmx512").c1
	for b.Loop() {
		sliceChannel(c, 0, false)
	}
}

// TRLC-LINKS: REQ-SDS-018
func BenchmarkNthValue(b *testing.B) {
	v := make([]float64, 4000)
	for i := range v {
		v[i] = float64((i * 7919) % 1013)
	}
	for b.Loop() {
		nthValue(v, len(v)/2)
		nthValue(v, len(v)/10)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func BenchmarkScorePhys(b *testing.B) {
	var rs []Result
	for _, c := range physDetectCases() {
		if c.chA == 0 {
			rs = append(rs, Autodetect(c.c1, c.c2, c.colT, "hex"))
		}
	}
	for b.Loop() {
		for _, r := range rs {
			scorePhys(r)
		}
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func BenchmarkMorseOf(b *testing.B) {
	for b.Loop() {
		for _, c := range []byte("SOS CQ DE 73 ?") {
			MorseOf(c)
		}
	}
}

// TRLC-LINKS: REQ-SDS-244, REQ-SDS-247
func BenchmarkSBUSFrames(b *testing.B) {
	o := sbusOpts(7e-3, 1e6)
	o.frames = 6
	r := DecodeUART(sbusLine(o), 1e-6, UARTCfg{Baud: sbusBaud, Bits: 8, Parity: "even", Inverted: true})
	if sbusFrames(r, 1e-6) < 2 {
		b.Fatal("no S.BUS frames")
	}
	for b.Loop() {
		sbusFrames(r, 1e-6)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214
func BenchmarkCrossingsEnd(b *testing.B) {
	c := benchCase(b, "dmx512").c1
	for b.Loop() {
		crossingsEnd(c, 1<<30)
	}
}

// TRLC-LINKS: REQ-SDS-229
func BenchmarkSampleEdges(b *testing.B) {
	wide := readCapture(b, "spi_sample_104mhz.bin.gz")
	c := SampleLineConfig{Ch: 1, Rise: true, Fall: true, Level: 128, Low: 110, High: 146, OtherLevel: 128}
	for b.Loop() {
		SampleEdges(wide, c)
	}
}

// TRLC-LINKS: REQ-SDS-229
func BenchmarkSampleSPI(b *testing.B) {
	wide := readCapture(b, "spi_sample_104mhz.bin.gz")
	c := SampleSPIConfig{ClockCh: 1, Level: 128, Low: 110, High: 146, DataLevel: 128, GapClocks: SampleSPIGap(104e6), MSB: true}
	for b.Loop() {
		SampleSPI(wide, c)
	}
}

// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func BenchmarkMeasureSPIEye(b *testing.B) {
	c1, c2 := lines(readCapture(b, "spi_sample_104mhz.bin.gz"))
	cfg := SPICfg{MSB: true, Threshold: 128, HaveThr: true, DataLag: SPIDataLag(1, 2e-9)}
	if _, ok := MeasureSPIEye(c2, c1, 2e-9, cfg); !ok {
		b.Fatal("no eye")
	}
	for b.Loop() {
		MeasureSPIEye(c2, c1, 2e-9, cfg)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func BenchmarkStreamDecoder(b *testing.B) {
	msg := []int{0x48, 0x65, 0x6C, 0x6C, 0x6F, 0x0D, 0x0A}
	var w []uint8
	for i := 0; i < 20; i++ {
		w = append(w, uartWave(msg, 40)...)
	}
	dec := func(a, _ []uint8) Result { return DecodeUART(a, 1e-6, UARTCfg{Baud: 25000, Bits: 8, Parity: "none"}) }
	for b.Loop() {
		d := NewStreamDecoder(dec, 400, 40000)
		for i := 0; i < len(w); i += 2048 {
			d.Push(w[i:min(i+2048, len(w))], nil)
		}
		d.Flush()
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func BenchmarkStreamGroup(b *testing.B) {
	var r Result
	for i := 0; i < 400; i++ { // 40 frames of a start and nine bytes
		x := i * 20
		kind := "data"
		if i%10 == 0 {
			kind = "sof"
		}
		r.Spans = append(r.Spans, Span{x, x + 15, "41", kind, 0x41})
	}
	a := make([]uint8, 8000+2000)
	dec := func([]uint8, []uint8) Result { return r }
	for b.Loop() {
		d := NewStreamDecoder(dec, 100, 1<<20)
		d.Push(a, nil)
	}
}
