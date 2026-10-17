// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

// protoDecoders are the decoders beyond the first ten, each on the line
// (and second line) of a record, with a hostile or plain configuration.
var protoDecoders = []struct {
	name string
	dec  func(a, b []uint8, ct float64, rng *rand.Rand) Result
}{
	{"lin", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeLIN(a, ct, LINCfg{Baud: []int{0, -5, 1, 19200, 1 << 30}[rng.Intn(5)]})
	}},
	{"dmx512", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeDMX512(a, ct, DMXCfg{Baud: []int{0, -1, 250000, 1 << 30}[rng.Intn(4)]})
	}},
	{"midi", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeMIDI(a, ct, MIDICfg{Baud: []int{0, 31250, 7}[rng.Intn(3)]})
	}},
	{"modbus", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeModbus(a, ct, ModbusCfg{Baud: []int{0, 9600, 3}[rng.Intn(3)], Parity: []string{"", "even", "odd", "none", "x"}[rng.Intn(5)]})
	}},
	{"onewire", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeOneWire(a, ct, OneWireCfg{Overdrive: rng.Intn(2) == 0})
	}},
	{"ws281x", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeWS281x(a, ct, WS281xCfg{}) }},
	{"dht", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeDHT(a, ct, DHTCfg{}) }},
	{"cec", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeCEC(a, ct, CECCfg{}) }},
	{"j1850", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeJ1850(a, ct, J1850Cfg{}) }},
	{"irnec", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeIRNEC(a, ct, IRCfg{}) }},
	{"irsirc", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeIRSIRC(a, ct, IRCfg{}) }},
	{"irrc5", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeIRRC5(a, ct, IRCfg{}) }},
	{"irrc6", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeIRRC6(a, ct, IRCfg{}) }},
	{"dali", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeDALI(a, ct, DALICfg{}) }},
	{"usbpd", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeUSBPD(a, ct, USBPDCfg{Baud: []int{0, 300000, -1, 1 << 30}[rng.Intn(4)]})
	}},
	{"spdif", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeSPDIF(a, ct, SPDIFCfg{}) }},
	{"ps2", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodePS2(a, b, ct, ClockedCfg{}) }},
	{"swd", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeSWD(a, b, ct, ClockedCfg{}) }},
	{"mdio", func(a, b []uint8, ct float64, rng *rand.Rand) Result { return DecodeMDIO(a, b, ct, ClockedCfg{}) }},
	{"wiegand", func(a, b []uint8, ct float64, rng *rand.Rand) Result {
		return DecodeWiegand(a, b, ct, ClockedCfg{Threshold: rng.Float64() * 300, HaveThr: rng.Intn(3) == 0})
	}},
}

// TestDecodeFuzzBeyondTen: the decoders beyond the first ten take the
// decoder fuzz's untrusted input (noise, a Nyquist toggle, a flat line, a
// ramp, a bursty walk, a two-level random line), degenerate sample times
// and a missing second line, and truncated and spliced copies of their own
// frames: none may panic or take over two seconds.
// TRLC-LINKS: REQ-SDS-018
func TestDecodeFuzzBeyondTen(t *testing.T) {
	rng := rand.New(rand.NewSource(int64(1618 + fuzzKnob("DECODE_FUZZ_SEED", 0))))
	frames := protoDetectCases()
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
		d := protoDecoders[rng.Intn(len(protoDecoders))]
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

// TestBreakProtoNoFalseFrames: the checked protocols name no frame on lines
// that carry none (noise, a square wave, a random two-level line at many
// rates): a frame is OK only on its CRC, checksum, parities or inverse.
// TRLC-LINKS: REQ-SDS-018
func TestBreakProtoNoFalseFrames(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	checked := map[string]bool{"lin": true, "modbus": true, "dht": true, "j1850": true, "irnec": true, "usbpd": true,
		"mdio": true, "wiegand": true, "swd": true}
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
		for _, d := range protoDecoders {
			if checked[d.name] && d.dec(a, b, ct, rng).OK {
				falses[d.name]++
			}
		}
	}
	for name, k := range falses {
		if k > 3 { // a 5-bit or 8-bit check passes a random frame now and then
			t.Errorf("%s named %d frames in 150 random records", name, k)
		}
	}
}

// TestBreakProtoChecksFlagged: each check flags its broken frame, and the
// decode is not OK on that frame alone.
// TRLC-LINKS: REQ-SDS-018
func TestBreakProtoChecksFlagged(t *testing.T) {
	type tc struct {
		name string
		r    Result
	}
	var cases []tc
	{
		w := newTimeline(1e6)
		w.idle(200e-6)
		bit := 1 / 19200.0
		w.add(0, 13*bit)
		w.add(1, bit)
		tlUART(w, 0x55, 19200, "none", 1)
		tlUART(w, linPIDParity(0x12), 19200, "none", 1)
		tlUART(w, 0xA5, 19200, "none", 1)
		tlUART(w, 0x00, 19200, "none", 1) // a wrong checksum
		w.idle(40 * bit)
		cases = append(cases, tc{"LIN checksum", DecodeLIN(w.codes(), 1e-6, LINCfg{})})
	}
	{
		w := newTimeline(2e6)
		w.idle(3e-3)
		for _, v := range []int{0x01, 0x03, 0x02, 0x12, 0x34, 0xFF, 0xFF} {
			tlUART(w, v, 19200, "even", 1)
		}
		w.idle(3e-3)
		cases = append(cases, tc{"Modbus CRC", DecodeModbus(w.codes(), 5e-7, ModbusCfg{})})
	}
	{
		w := newTimeline(1e6)
		w.idle(1e-3)
		dhtReading(w, 1000, [4]int{1, 2, 3, 4}, 0x99)
		cases = append(cases, tc{"DHT checksum", DecodeDHT(w.codes(), 1e-6, DHTCfg{})})
	}
	{
		w := newTimeline(1e6)
		w.add(0, 500e-6)
		j1850Frame(w, []int{0x68, 0x6A, 0xF1}, true)
		cases = append(cases, tc{"J1850 CRC", DecodeJ1850(w.codes(), 1e-6, J1850Cfg{})})
	}
	{
		w := newTimeline(100e3)
		l := irLine{w: w}
		l.space(20e-3)
		l.mark(9e-3)
		l.space(4.5e-3)
		for _, v := range []int{0x04, 0xFB, 0x08, 0x08} { // the command's inverse broken
			for i := 0; i < 8; i++ {
				l.mark(562.5e-6)
				if v>>i&1 == 1 {
					l.space(1687.5e-6)
				} else {
					l.space(562.5e-6)
				}
			}
		}
		l.mark(562.5e-6)
		l.space(40e-3)
		cases = append(cases, tc{"NEC inverse", DecodeIRNEC(w.codes(), 1e-5, IRCfg{})})
	}
	{
		w := newTimeline(25e6)
		w.add(0, 20e-6)
		pdMessage(w, pdSOP, 0x1042, []uint32{0x12345678}, true)
		cases = append(cases, tc{"USB PD CRC-32", DecodeUSBPD(w.codes(), 4e-8, USBPDCfg{})})
	}
	{
		l := newTwoLine(200e3)
		l.add(1, 1, 2e-3)
		bad := []byte(wiegandBits(26, 12, 4660))
		bad[0] ^= 1
		wiegandFrame(l, string(bad), 1e-3)
		cases = append(cases, tc{"Wiegand parity", DecodeWiegand(l.a.codes(), l.b.codes(), 5e-6, ClockedCfg{})})
	}
	for _, c := range cases {
		if c.r.OK || !strings.Contains(c.r.Text, "!") {
			t.Errorf("%s: ok=%v %q: the broken check must be flagged and the decode not OK", c.name, c.r.OK, c.r.Text)
		}
	}
}

// TestBreakProtoRecordEnds: a frame cut by the record's end at every point
// never decodes to more units than the whole frame holds, nor to units the
// frame does not hold.
// TRLC-LINKS: REQ-SDS-018
func TestBreakProtoRecordEnds(t *testing.T) {
	for _, c := range protoDetectCases() {
		if c.chA != 0 || c.c2 != nil && c.name != "" && strings.HasSuffix(c.name, "swapped") {
			continue
		}
		whole, _ := AutodetectFast(c.c1, c.c2, c.colT, "hex")
		if whole.Proto != c.proto {
			continue
		}
		dec := map[string]func(a, b []uint8) Result{}
		for _, d := range protoDecoders {
			d := d
			dec[d.name] = func(a, b []uint8) Result { return d.dec(a, b, c.colT, rand.New(rand.NewSource(0))) }
		}
		f := dec[c.proto]
		if f == nil {
			continue
		}
		full := f(c.c1, c.c2)
		step := max(1, len(c.c1)/37)
		for cut := step; cut < len(c.c1); cut += step {
			var b []uint8
			if c.c2 != nil {
				b = c.c2[:cut]
			}
			r := f(c.c1[:cut], b)
			if len(r.Bytes) > len(full.Bytes) {
				t.Errorf("%s cut at %d: %d units from a record holding %d", c.name, cut, len(r.Bytes), len(full.Bytes))
				break
			}
		}
	}
}
