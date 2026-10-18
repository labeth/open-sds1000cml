// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

// physDecoders are the decoders beyond the first thirty, each on the line
// (and second line) of a record, with a hostile or plain configuration.
var physDecoders = []struct {
	name string
	dec  func(a, b []uint8, ct float64, rng *rand.Rand) Result
}{
	{"quad", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeQuadrature(a, b, ct, ClockedCfg{}) }},
	{"stepper", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeStepper(a, b, ct, ClockedCfg{}) }},
	{"pwm", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodePWM(a, ct, LineCfg{}) }},
	{"rcencode", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeRCEncode(a, ct, LineCfg{Variant: []int{0, 12, 24, -3}[rng.Intn(4)]})
	}},
	{"ook", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeOOK(a, ct, LineCfg{}) }},
	{"miller", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeMiller(a, ct, LineCfg{Baud: []int{0, 106000, -1, 1 << 30}[rng.Intn(4)]})
	}},
	{"morse", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeMorse(a, ct, LineCfg{Baud: []int{0, 10, -1, 1 << 30}[rng.Intn(4)]})
	}},
	{"dcf77", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeDCF77(a, ct, LineCfg{}) }},
	{"em4100", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeEM4100(a, ct, LineCfg{Baud: []int{0, 1953, -5, 1 << 30}[rng.Intn(4)]})
	}},
	{"em4305", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeEM4305(a, ct, LineCfg{Baud: []int{0, 125000, -1, 1 << 30}[rng.Intn(4)]})
	}},
	{"t55xx", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeT55xx(a, ct, LineCfg{Baud: []int{0, 125000, 1}[rng.Intn(3)]})
	}},
	{"swim", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeSWIM(a, ct, LineCfg{Baud: []int{0, 181818, -1, 1 << 30}[rng.Intn(4)]})
	}},
	{"sdq", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeSDQ(a, ct, LineCfg{Baud: []int{0, 98425, 1, 1 << 30}[rng.Intn(4)]})
	}},
	{"pjdl", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodePJDL(a, ct, LineCfg{Variant: []int{0, 1, 4, 9}[rng.Intn(4)]})
	}},
	{"dsi", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeDSI(a, ct, LineCfg{}) }},
	{"qi", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeQi(a, ct, LineCfg{}) }},
	{"irmp", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeIRMP(a, ct, LineCfg{}) }},
	{"sdcard", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeSDCard(a, b, ct, ClockedCfg{}) }},
	{"pdi", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodePDI(a, b, ct, ClockedCfg{}) }},
	{"cjtag", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeCJTAG(a, b, ct, ClockedCfg{}) }},
	{"maple", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeMaple(a, b, ct, ClockedCfg{}) }},
	{"caliper", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeCaliper(a, b, ct, ClockedCfg{Threshold: rng.Float64() * 300, HaveThr: rng.Intn(3) == 0})
	}},
}

// TestDecodeFuzzPhys: the decoders beyond the first thirty take the decoder
// fuzz's untrusted input (noise, a Nyquist toggle, a flat line, a ramp, a
// bursty walk, a two-level random line), degenerate sample times and a
// missing second line, and truncated and spliced copies of their own frames:
// none may panic or take over two seconds.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func TestDecodeFuzzPhys(t *testing.T) {
	rng := rand.New(rand.NewSource(int64(2718 + fuzzKnob("DECODE_FUZZ_SEED", 0))))
	frames := physDetectCases()
	sig := func(n, kind int) []uint8 {
		s := make([]uint8, n)
		switch kind {
		case 0:
			for i := range s {
				s[i] = uint8(rng.Intn(256))
			}
		case 1:
			for i := range s {
				s[i] = uint8(i % 2 * 255)
			}
		case 2:
			for i := range s {
				s[i] = 128
			}
		case 3:
			for i := range s {
				s[i] = uint8(i * 255 / max(1, n-1))
			}
		case 4, 5:
			v, p := 200, 15+kind*40
			for i := range s {
				if rng.Intn(p) == 0 {
					v = 240 - v
				}
				s[i] = uint8(v)
			}
		case 6: // a real frame of some protocol, cut and spliced
			f := frames[rng.Intn(len(frames))].c1
			cut := rng.Intn(len(f) + 1)
			s = append(append([]uint8{}, f[cut:]...), f[:rng.Intn(cut+1)]...)
		}
		return s
	}
	cts := []float64{0, -1, 1e-12, 2e-9, 1e-6, 1e-4, 1}
	for i := 0; i < 400*fuzzKnob("DECODE_FUZZ_MULT", 1); i++ {
		n := rng.Intn(20000)
		a := sig(n, rng.Intn(7))
		b := sig(len(a), rng.Intn(7))
		if rng.Intn(8) == 0 {
			b = nil
		}
		ct := cts[rng.Intn(len(cts))]
		d := physDecoders[rng.Intn(len(physDecoders))]
		t0 := time.Now()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("iter %d %s panicked: %v", i, d.name, r)
				}
			}()
			d.dec(a, b, ct, rng)
		}()
		if el := time.Since(t0); el > 2*time.Second {
			t.Fatalf("iter %d %s took %v: a decoder DoS on hostile input", i, d.name, el)
		}
	}
}

// TestBreakPhysNoFalseFrames: the checked protocols name no frame on lines
// that carry none (a random two-level line at many rates): a frame is OK
// only on its CRC, checksum, parities or inverse.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func TestBreakPhysNoFalseFrames(t *testing.T) {
	rng := rand.New(rand.NewSource(101))
	checked := map[string]bool{"ook": true, "em4100": true, "em4305": true, "sdcard": true, "pjdl": true, "qi": true,
		"maple": true}
	falses := map[string]int{}
	for it := 0; it < 150; it++ {
		n := 2000 + rng.Intn(30000)
		a, b := make([]uint8, n), make([]uint8, n)
		va, vb, p := 200, 200, 3+rng.Intn(400)
		for i := range a {
			if rng.Intn(p) == 0 {
				va = 240 - va
			}
			if rng.Intn(p) == 0 {
				vb = 240 - vb
			}
			a[i], b[i] = uint8(va), uint8(vb)
		}
		ct := []float64{2e-9, 1e-7, 1e-6, 1e-5}[rng.Intn(4)]
		for _, d := range physDecoders {
			if checked[d.name] && d.dec(a, b, ct, rng).OK {
				falses[d.name]++
			}
		}
	}
	for name, k := range falses {
		if k > 3 { // a 7-bit or 8-bit check passes a random frame now and then
			t.Errorf("%s named %d frames in 150 random records", name, k)
		}
	}
	// the protocols without a check: the detection does not name them on
	// random line pairs either
	named := map[string]int{}
	for it := 0; it < 60; it++ {
		n := 4000 + rng.Intn(20000)
		a, b := make([]uint8, n), make([]uint8, n)
		va, vb, p := 200, 200, 3+rng.Intn(200)
		for i := range a {
			if rng.Intn(p) == 0 {
				va = 240 - va
			}
			if rng.Intn(p) == 0 {
				vb = 240 - vb
			}
			a[i], b[i] = uint8(va), uint8(vb)
		}
		ct := []float64{2e-9, 1e-7, 1e-6, 1e-5}[rng.Intn(4)]
		if r := Autodetect(a, b, ct, "hex"); r.Proto != "off" {
			named[r.Proto]++
		}
	}
	phys := map[string]bool{}
	for _, d := range physDecoders {
		phys[d.name] = true
	}
	for p, k := range named {
		if phys[p] && k > 1 {
			t.Errorf("random line pairs named %s %d times", p, k)
		}
	}
}

// TestBreakPhysChecksFlagged: each check flags its broken frame, and the
// decode is not OK on that frame alone.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func TestBreakPhysChecksFlagged(t *testing.T) {
	type tc struct {
		name string
		r    Result
	}
	var cases []tc
	{
		w := newTimeline(100e3)
		w.add(0, 20e-3)
		oregonBurst(w, 3, []int{0x1, 0xD, 0x2, 0x0, 0x1, 0x3, 0xA, 0x0, 0x5, 0x3}, 1/2048.0, true, 40e-3)
		cases = append(cases, tc{"Oregon checksum", DecodeOOK(w.codes(), 1e-5, LineCfg{})})
	}
	{
		w := newTimeline(500e3)
		w.add(1, 768e-6)
		em4100Frame(w, [5]int{1, 2, 3, 4, 5}, 256e-6, true)
		w.add(0, 1e-3)
		cases = append(cases, tc{"EM4100 row parity", DecodeEM4100(w.codes(), 2e-6, LineCfg{})})
	}
	{
		w := newTimeline(4e6)
		f := rfidField{w, true}
		f.on(500 * rfidFC)
		em4305Command(f, em4305Bits(2, 5, [4]int{0xDE, 0xAD, 0xBE, 0xEF}, true))
		cases = append(cases, tc{"EM4305 parity", DecodeEM4305(w.codes(), 2.5e-7, LineCfg{})})
	}
	{
		l := newTwoLine(4e6)
		var bits []int
		for _, b := range [][]int{sdIdle(8), sdToken(true, 17, 0x1000, true), sdIdle(8)} {
			bits = append(bits, b...)
		}
		clockBits(l, bits, 4e-6)
		cases = append(cases, tc{"SD CRC-7", DecodeSDCard(l.a.codes(), l.b.codes(), 2.5e-7, ClockedCfg{})})
	}
	{
		w := newTimeline(2e6)
		w.add(0, 500e-6)
		pjdlFrame(w, 1, pjonPacket(0x05, 0x00, []int{0x48, 0x69}, true), 1e-3)
		r := DecodePJDL(w.codes(), 5e-7, LineCfg{})
		r.OK = false // the header checks: only the frame's end CRC fails
		cases = append(cases, tc{"PJON CRC", r})
	}
	{
		w := newTimeline(200e3)
		w.add(0, 5e-3)
		qiPacket(w, []int{0x02, 0x01}, true, 8e-3)
		cases = append(cases, tc{"Qi checksum", DecodeQi(w.codes(), 5e-6, LineCfg{})})
	}
	{
		m := &mapleLine{l: newTwoLine(20e6), st: 250e-9}
		m.set(1, 1)
		m.frame(mapleBytes(0x01, 0x20, 0x09, []int{0xFF, 0x00, 0xA5, 0x5A}, true))
		m.set(1, 1)
		cases = append(cases, tc{"Maple checksum", DecodeMaple(m.l.a.codes(), m.l.b.codes(), 5e-8, ClockedCfg{})})
	}
	{
		w := newTimeline(1000)
		w.add(0, 1.3)
		dcf77Minute(w, 0, 0, 1, 1, 1, 0, false)
		dcf77Minute(w, 0x37, 0x12, 0x18, 0x7, 0x10, 0x26, true)
		r := DecodeDCF77(w.codes(), 1e-3, LineCfg{})
		r.OK = false // the hour and date check: the minute's parity is broken
		cases = append(cases, tc{"DCF77 parity", r})
	}
	{
		const T = 22 / 4e6
		w := newTimeline(20e6)
		w.add(1, 20e-6)
		swimFrame(w, 0, 3, 1, T, 1, true, 3*T)
		cases = append(cases, tc{"SWIM parity", DecodeSWIM(w.codes(), 5e-8, LineCfg{})})
	}
	ir := func(name string, f func(l irLine)) {
		w := newTimeline(100e3)
		l := irLine{w: w}
		l.space(20e-3)
		f(l)
		cases = append(cases, tc{name, DecodeIRMP(w.codes(), 1e-5, LineCfg{})})
	}
	ir("Samsung inverse", func(l irLine) {
		l.mark(4.5e-3)
		l.space(4.5e-3)
		bits := append(append(lsbBits(0x0707, 16), -1), append(append(lsbBits(5, 4), lsbBits(0x9A, 8)...), lsbBits(0x9A, 8)...)...)
		for _, b := range bits {
			l.mark(550e-6)
			switch b {
			case -1:
				l.space(4.5e-3)
			case 1:
				l.space(1650e-6)
			default:
				l.space(550e-6)
			}
		}
		l.mark(550e-6)
		l.space(50e-3)
	})
	ir("Kaseikyo parity", func(l irLine) {
		b := append(lsbBits(0x2002, 16), lsbBits(0xF, 4)...) // a wrong parity nibble
		b = append(b, make([]int, 28)...)
		irPD(l, 3380e-6, 1690e-6, b, 423e-6, 423e-6, 1269e-6, 74e-3)
	})
	ir("Denon repeat", func(l irLine) {
		irPD(l, 0, 0, append(msbBits(0x0A, 5), msbBits(0x2C4, 10)...), 310e-6, 745e-6, 1780e-6, 45e-3)
		irPD(l, 0, 0, append(msbBits(0x0A, 5), msbBits(0x2C4, 10)...), 310e-6, 745e-6, 1780e-6, 60e-3) // not inverted
	})
	for _, c := range cases {
		if c.r.OK || !strings.Contains(c.r.Text, "!") {
			t.Errorf("%s: ok=%v %q: the broken check must be flagged and the decode not OK", c.name, c.r.OK, c.r.Text)
		}
	}
}

// TestBreakPhysRecordEnds: a capture cut by the record's end at every point
// never decodes to more units than the whole capture holds.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func TestBreakPhysRecordEnds(t *testing.T) {
	dec := map[string]func(a, b []uint8, ct float64) Result{}
	for _, d := range physDecoders {
		d := d
		dec[d.name] = func(a, b []uint8, ct float64) Result { return d.dec(a, b, ct, rand.New(rand.NewSource(0))) }
	}
	for _, c := range physDetectCases() {
		if c.chA != 0 {
			continue
		}
		f := dec[c.proto]
		full := f(c.c1, c.c2, c.colT)
		step := max(1, len(c.c1)/37)
		for cut := step; cut < len(c.c1); cut += step {
			var b []uint8
			if c.c2 != nil {
				b = c.c2[:cut]
			}
			r := f(c.c1[:cut], b, c.colT)
			if len(r.Bytes) > len(full.Bytes) {
				t.Errorf("%s cut at %d: %d units from a record holding %d", c.name, cut, len(r.Bytes), len(full.Bytes))
				break
			}
		}
	}
}
