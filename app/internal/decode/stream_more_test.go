// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math/rand"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
func idleOf(a, b byte) *[2]byte { return &[2]byte{a, b} }

// moreStreamCases are the protocols past the thirtieth, at their bench
// sources' rates: the gap-free ones (TestStreamDecoderGapFree) and the ones
// too fast for it (TestStreamRecordCaptures).
var moreStreamCases = []streamCase{
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "quad", baud: 10000, lines: 2, idle: idleOf(0, 0), gen: func(l twoLine, i int, rng *rand.Rand) {
		moves := []int{1, 1, 1}
		for k := 0; k < i%5; k++ {
			moves = append(moves, -1)
		}
		quadMoves(l, moves, 100e-6, 400e-6+rng.Float64()*500e-6)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeQuadrature(a, b, s, ClockedCfg{Threshold: 128, HaveThr: true, ThresholdB: 128, HaveThrB: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "stepper", baud: 5000, lines: 2, idle: idleOf(0, 1), gen: func(l twoLine, i int, rng *rand.Rand) {
		dirs := []byte{1, 1, 1}
		for k := 0; k < i%4; k++ {
			dirs = append(dirs, 0)
		}
		stepperMoves(l, dirs, 200e-6, 600e-6+rng.Float64()*1e-3)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeStepper(a, b, s, ClockedCfg{Threshold: 128, HaveThr: true, ThresholdB: 128, HaveThrB: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "rcencode", baud: 2857, lines: 1, idle: idleOf(0, 0), gen: func(l twoLine, i int, rng *rand.Rand) {
		trits := []int{i % 3, 1, 2, 0, 0, 1, 1, 2, 0, 1, 0, (i / 3) % 3}
		rcFrame2262(l.a, trits, 350e-6)
		l.b.add(0, l.a.t-l.b.t)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeRCEncode(a, s, LineCfg{Threshold: 128, HaveThr: true, Variant: 12})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "ook", baud: 1024, lines: 1, idle: idleOf(0, 0), frames: 30, gen: func(l twoLine, i int, rng *rand.Rand) {
		oregonBurst(l.a, 3, []int{0xF, 0x8, 0x2, 0x4, 0x1, i & 0xf, 0xA, 0x0, 0x5, 0x3, 0x2, 0x0, 0x4, 0x5, 0x0}, 1/2048.0, false,
			40e-3+rng.Float64()*20e-3)
		l.b.add(0, l.a.t-l.b.t)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeOOK(a, s, LineCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "morse", baud: 500, lines: 1, idle: idleOf(0, 0), gen: func(l twoLine, i int, rng *rand.Rand) {
		morseText(l.a, string([]byte{'A' + byte(i%26), 'B', 'S'}), 2e-3, 10+rng.Float64()*10) // rest in units
		l.b.add(0, l.a.t-l.b.t)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeMorse(a, s, LineCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "dcf77", baud: 1, lines: 1, idle: idleOf(0, 0), frames: 5, gen: func(l twoLine, i int, rng *rand.Rand) {
		if i == 0 { // a lead-in minute: no marker before it
			dcf77Minute(l.a, 0x00, 0x00, 0x01, 0x01, 0x01, 0x00, false)
		}
		dcf77Minute(l.a, 0x10+i, 0x12, 0x18, 0x7, 0x10, 0x26, i == 2)
		l.b.add(0, l.a.t-l.b.t)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeDCF77(a, s, LineCfg{Threshold: 128, HaveThr: true, Baud: 1})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "em4100", baud: 1953, lines: 1, gen: func(l twoLine, i int, rng *rand.Rand) {
		em4100Frame(l.a, [5]int{0x01, i & 0xff, 0x45, 0x67, 0x89}, 256e-6, i%7 == 3) // a tag repeats its frames back to back
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeEM4100(a, s, LineCfg{Threshold: 128, HaveThr: true, Baud: 1953})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "em4305", baud: 125000, lines: 1, frames: 30, gen: func(l twoLine, i int, rng *rand.Rand) {
		f := rfidField{l.a, false}
		em4305Command(f, em4305Bits(2, i%16, [4]int{0xDE, i & 0xff, 0xBE, 0xEF}, i%5 == 2))
		f.on(rng.Float64() * 2e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeEM4305(a, s, LineCfg{Threshold: 128, HaveThr: true, Baud: 125000})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "em4305-carrier", baud: 125000, lines: 1, frames: 20, gen: func(l twoLine, i int, rng *rand.Rand) {
		f := rfidField{l.a, true}
		f.on(500 * rfidFC)
		em4305Command(f, em4305Bits(2, i%16, [4]int{0xDE, i & 0xff, 0xBE, 0xEF}, false))
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeEM4305(a, s, LineCfg{Threshold: 128, HaveThr: true, Baud: 125000})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "t55xx", baud: 125000, lines: 1, frames: 30, gen: func(l twoLine, i int, rng *rand.Rand) {
		f := rfidField{l.a, false}
		f.on(500 * rfidFC)
		t55xxCommand(f, t55xxBits(2, -1, 0, 0xDEAD0000|uint32(i), i%8))
		f.on(rng.Float64() * 2e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeT55xx(a, s, LineCfg{Threshold: 128, HaveThr: true, Baud: 125000})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "pjdl", baud: 22727, lines: 1, idle: idleOf(0, 0), gen: func(l twoLine, i int, rng *rand.Rand) {
		pjdlFrame(l.a, 1, pjonPacket(0x2A, 0x02, []int{0x11, i & 0xff, 0x33}, i%9 == 4), 1e-3+rng.Float64()*1e-3)
		l.b.add(0, l.a.t-l.b.t)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodePJDL(a, s, LineCfg{Threshold: 128, HaveThr: true, Variant: 1})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "dsi", baud: 600, lines: 1, idle: idleOf(0, 0), gen: func(l twoLine, i int, rng *rand.Rand) {
		dsiFrame(l.a, i&0xff, 12e-3+rng.Float64()*10e-3)
		l.b.add(0, l.a.t-l.b.t)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeDSI(a, s, LineCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "qi", baud: 2000, lines: 1, idle: idleOf(0, 0), gen: func(l twoLine, i int, rng *rand.Rand) {
		qiPacket(l.a, []int{0x03, i & 0xff}, i%7 == 2, 8e-3+rng.Float64()*8e-3)
		l.b.add(0, l.a.t-l.b.t)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeQi(a, s, LineCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "caliper", baud: 12500, lines: 2, idle: idleOf(0, 0), gen: func(l twoLine, i int, rng *rand.Rand) {
		caliperFrame(l, 1000+i*37, i%3 == 1, i%5 == 2, 80e-6, 12e-3+rng.Float64()*10e-3)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeCaliper(a, b, s, ClockedCfg{Threshold: 128, HaveThr: true, ThresholdB: 128, HaveThrB: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "irmp", baud: 1786, lines: 1, frames: 40, gen: func(l twoLine, i int, rng *rand.Rand) {
		ir := irLine{l.a, false, 0}
		switch i % 4 {
		case 0:
			irSamsung32(ir, 0x0707, 0xFD02^i<<8)
		case 1:
			irKaseikyo(ir, 0x2002, 0x8, 0x0, 0x100+i, 0x1)
		case 2:
			irDenon(ir, 0x0A, 0x200+i)
		default:
			irJVC(ir, 0x3, 0x5A0+i, false)
			irJVC(ir, 0x3, 0x5A0+i, true) // a repeat: no leader, read after its frame
		}
		ir.space(100e-3 + rng.Float64()*30e-3)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeIRMP(a, s, LineCfg{Threshold: 128, HaveThr: true})
	}},
	// The records' protocols.
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "pwm", baud: 10000, lines: 1, idle: idleOf(0, 0), gen: func(l twoLine, i int, rng *rand.Rand) {
		pwmWave(l.a, 100e-6, []float64{0.25 + float64(i%8)/16})
		l.b.add(0, l.a.t-l.b.t)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodePWM(a, s, LineCfg{Threshold: 128, HaveThr: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "miller", baud: 106000, lines: 1, idle: idleOf(1, 1), gen: func(l twoLine, i int, rng *rand.Rand) {
		var bits []int
		for k := 0; k < 16; k++ {
			bits = append(bits, (i>>k^k)&1)
		}
		millerMessage(l.a, bits, 1/106000.0, 2e-6, 60e-6+rng.Float64()*40e-6)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeMiller(a, s, LineCfg{Threshold: 128, HaveThr: true, Baud: 106000})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "swim", baud: 363636, lines: 1, idle: idleOf(1, 1), gen: func(l twoLine, i int, rng *rand.Rand) {
		const T = 22 / 8e6
		gap := 3 * T
		swimFrame(l.a, 0, 3, 1, T, 1, false, gap)
		swimFrame(l.a, 0, 8, 1, T, 1, false, gap)
		swimFrame(l.a, 0, 8, 0x00, T, 1, false, gap)
		swimFrame(l.a, 0, 8, 0x50, T, 1, false, gap)
		swimFrame(l.a, 0, 8, i&0xff, T, 1, false, gap)
		swimFrame(l.a, 1, 8, 0x5A, T, 1, false, 40*T+rng.Float64()*40*T)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeSWIM(a, s, LineCfg{Threshold: 128, HaveThr: true, Baud: 363636})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "sdq", baud: 98425, lines: 1, idle: idleOf(1, 1), gen: func(l twoLine, i int, rng *rand.Rand) {
		T := 1 / 98425.0
		sdqBreak(l.a, T)
		for _, v := range []int{0x33, i & 0xff, 0xFE} {
			sdqByte(l.a, v, T)
		}
		l.a.add(1, 50e-6+rng.Float64()*50e-6)
		oneLine(l)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeSDQ(a, s, LineCfg{Threshold: 128, HaveThr: true, Baud: 98425})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "sdcard", baud: 250000, lines: 2, idle: idleOf(1, 1), gen: func(l twoLine, i int, rng *rand.Rand) {
		bits := append(sdToken(true, 17, uint32(0x1000+i), i%7 == 3), sdIdle(10+rng.Intn(10))...)
		clockBits(l, bits, 4e-6)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeSDCard(a, b, s, ClockedCfg{Threshold: 128, HaveThr: true, ThresholdB: 128, HaveThrB: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "cjtag", baud: 250000, lines: 2, idle: idleOf(0, 0), gen: func(l twoLine, i int, rng *rand.Rand) {
		// each frame selects OScan1 again: escape, activation code, scans
		c := &cjtagLine{l: l, per: 4e-6}
		for k := 0; k < 6; k++ {
			c.rise(0)
		}
		c.escape(6)
		for _, b := range []int{1, 1, 0, 0, 1, 0, 0, 1, 1, 0, 0, 0, 1} {
			c.rise(b)
		}
		c.tck(0, 0, 0, true)
		c.scan(false, 8, uint32(i&0xff), 0x3C)
		for k := 0; k < 6+rng.Intn(6); k++ {
			c.tck(0, 0, 0, false)
		}
		c.escape(8)
		c.l.add(0, c.tms, 40e-6)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeCJTAG(a, b, s, ClockedCfg{Threshold: 128, HaveThr: true, ThresholdB: 128, HaveThrB: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "maple", baud: 2000000, lines: 2, idle: idleOf(1, 1), gen: func(l twoLine, i int, rng *rand.Rand) {
		m := &mapleLine{l: l, st: 250e-9}
		m.frame(mapleBytes(0x00, 0x20, 0x05, []int{0x00, 0x00, 0x00, 0x01, i & 0xff, 0x34, 0x56, 0x78}, i%7 == 3))
		for k := 0; k < 20+rng.Intn(20); k++ {
			m.set(1, 1)
		}
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodeMaple(a, b, s, ClockedCfg{Threshold: 128, HaveThr: true, ThresholdB: 128, HaveThrB: true})
	}},
	// TRLC-LINKS: REQ-SDS-018, REQ-SDS-260
	{name: "pdi", baud: 250000, lines: 2, idle: idleOf(1, 1), gen: func(l twoLine, i int, rng *rand.Rand) {
		var bits []int
		for _, x := range []int{0x4C, 0xCA, 0x01, 0x00, 0x01, i & 0xff} { // STS a4 i1
			bits = append(bits, pdiChar(x, false)...)
		}
		for k := 0; k < 4+rng.Intn(8); k++ {
			bits = append(bits, 1)
		}
		clockBits(l, bits, 4e-6)
	}, dec: func(a, b []uint8, s float64) Result {
		return DecodePDI(a, b, s, ClockedCfg{Threshold: 128, HaveThr: true, ThresholdB: 128, HaveThrB: true})
	}},
}

// TestStreamRecordPDIOpensInsideACharacter reads records cut at many places
// from a scope capture of a PDI programmer whose instructions run back to
// back: a record opened inside a character must show no frame read out of
// step, only the four instructions the source sends.
// TRLC-LINKS: REQ-SDS-260
func TestStreamRecordPDIOpensInsideACharacter(t *testing.T) {
	c1, c2, ss := loadGzJSON(t, "testdata/scope_pdi_500us.json.gz")
	feature, longest, quiet, _ := StreamTiming("pdi", 250000)
	dec := func(a, b []uint8) Result { return DecodePDI(a, b, ss, ClockedCfg{}) }
	sent := map[string]bool{"C2 59": true, "80 7": true, "4C CA 1 0 1 55": true, "C 0 0 0 1 22": true}
	got := 0
	for off := 0; off < len(c1)/2; off += 1201 {
		fr := StreamRecordFromIdle(c1[off:], c2[off:], dec, int(quiet/ss), int(StreamRecordHead("pdi", feature, longest)/ss), 0, false)
		for _, f := range fr {
			k := strings.TrimSpace(frameKey(f))
			if !sent[k] {
				t.Fatalf("record from %d: frame at %d reads %q", off, f.I0, k)
			}
		}
		got += len(fr)
	}
	if got < 300 {
		t.Fatalf("only %d frames", got)
	}
}
