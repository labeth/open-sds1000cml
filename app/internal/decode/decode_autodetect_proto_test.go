// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math/rand"
	"testing"
	"time"
)

// detectCase is one capture the detection must name: its two channels (nil
// for an idle one), the sample time and the protocol and first line wanted.
type detectCase struct {
	name   string
	c1, c2 []uint8
	colT   float64
	proto  string
	chA    int
}

// idleLike is a flat channel as long as c (a probe on an idle line).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func idleLike(c []uint8) []uint8 {
	out := make([]uint8, len(c))
	for i := range out {
		out[i] = 128
	}
	return out
}

// protoDetectCases are captures of every protocol beyond the first ten, as
// a scope would record them at a timebase that shows a few frames.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func protoDetectCases() []detectCase {
	var cs []detectCase
	one := func(name, proto string, sr float64, w *timeline) {
		c := w.codes()
		cs = append(cs, detectCase{name, c, idleLike(c), 1 / sr, proto, 0})
		cs = append(cs, detectCase{name + " on C2", idleLike(c), c, 1 / sr, proto, 1})
	}
	{
		w := newTimeline(1e6)
		w.idle(300e-6)
		linFrame(w, 19200, 13, 0x3c, []int{1, 2, 3, 4, 5, 6, 7, 8}, true)
		linFrame(w, 19200, 13, 0x12, []int{0xA5, 0x5A}, false)
		one("LIN 19200", "lin", 1e6, w)
	}
	{
		w := newTimeline(5e6)
		w.idle(40e-6)
		slots := make([]int, 120)
		for i := range slots {
			slots[i] = i * 3 & 0xff
		}
		dmxPacket(w, 100, 12, 0, slots)
		dmxPacket(w, 100, 12, 0, slots)
		one("DMX512", "dmx512", 5e6, w)
	}
	{
		w := newTimeline(1e6)
		w.idle(200e-6)
		for _, b := range []int{0x90, 0x3C, 0x64, 0x40, 0x64, 0x43, 0x64, 0x80, 0x3C, 0x00, 0xB0, 0x07, 0x50, 0xF8, 0xC0, 0x05} {
			tlUART(w, b, 31250, "none", 1)
		}
		w.idle(1e-3)
		one("MIDI", "midi", 1e6, w)
	}
	{
		w := newTimeline(2e6)
		w.idle(3e-3)
		modbusFrame(w, 19200, "even", 1, []int{0x01, 0x03, 0x00, 0x6B, 0x00, 0x03})
		modbusFrame(w, 19200, "even", 1, []int{0x01, 0x03, 0x06, 0x02, 0x2B, 0x00, 0x00, 0x00, 0x64})
		one("Modbus 8E1", "modbus", 2e6, w)
		w2 := newTimeline(2e6)
		w2.idle(3e-3)
		modbusFrame(w2, 9600, "none", 2, []int{0x11, 0x06, 0x00, 0x01, 0x00, 0x03})
		modbusFrame(w2, 9600, "none", 2, []int{0x11, 0x06, 0x00, 0x01, 0x00, 0x03})
		one("Modbus 8N2", "modbus", 2e6, w2)
	}
	{
		w := newTimeline(2e6)
		w.idle(100e-6)
		owReset(w, false, true)
		owByte(w, 0xCC, false)
		owByte(w, 0x44, false)
		w.idle(300e-6)
		owReset(w, false, true)
		owByte(w, 0x33, false)
		for _, v := range owROM(0x28, 1, 2, 3, 4, 5, 6) {
			owByte(w, v, false)
		}
		w.idle(100e-6)
		one("1-Wire", "onewire", 2e6, w)
	}
	{
		w := newTimeline(50e6)
		w.add(0, 60e-6)
		wsFrame(w, 800e3, []int{0xFF, 0, 0, 0, 0xFF, 0, 0, 0, 0xFF, 0x10, 0x20, 0x30})
		wsFrame(w, 800e3, []int{0xFF, 0, 0, 0, 0xFF, 0, 0, 0, 0xFF, 0x10, 0x20, 0x30})
		one("WS2812", "ws281x", 50e6, w)
	}
	{
		w := newTimeline(1e6)
		w.idle(1e-3)
		dhtReading(w, 1000, [4]int{0x02, 0x8C, 0x01, 0x5F}, -1)
		one("DHT22", "dht", 1e6, w)
	}
	{
		w := newTimeline(100e3)
		w.idle(5e-3)
		cecMessage(w, []int{0x40, 0x04}, []int{0, 0})
		cecMessage(w, []int{0x4F, 0x82, 0x10, 0x00}, []int{1, 1, 1, 1})
		one("HDMI-CEC", "cec", 100e3, w)
	}
	{
		w := newTimeline(1e6)
		w.add(0, 500e-6)
		j1850Frame(w, []int{0x68, 0x6A, 0xF1, 0x01, 0x00}, false)
		j1850Frame(w, []int{0x48, 0x6B, 0x10, 0x41, 0x00, 0xBE}, false)
		one("J1850 VPW", "j1850", 1e6, w)
	}
	ir := func(name, proto string, high bool, f func(l irLine)) {
		w := newTimeline(100e3)
		l := irLine{w: w, activeHigh: high}
		l.space(20e-3)
		f(l)
		one(name, proto, 100e3, w)
	}
	ir("IR NEC", "irnec", false, func(l irLine) { necFrame(l, 0x04, -1, 0x08); necRepeat(l) })
	ir("IR NEC emitter", "irnec", true, func(l irLine) { necFrame(l, 0x00, -1, 0x45) })
	ir("IR SIRC", "irsirc", false, func(l irLine) { sircFrame(l, 12, 0x15, 1, 0); sircFrame(l, 12, 0x15, 1, 0) })
	ir("IR RC-5", "irrc5", false, func(l irLine) { rc5Frame(l, 0, 0, 0x0C); rc5Frame(l, 0, 0, 0x0C) })
	ir("IR RC-6", "irrc6", false, func(l irLine) { rc6Frame(l, 0, 0, []int{0, 0x0C}); rc6Frame(l, 0, 0, []int{0, 0x0C}) })
	{
		w := newTimeline(100e3)
		w.idle(5e-3)
		daliFrame(w, 16, 0x0190, 10e-3)
		daliFrame(w, 8, 0x5A, 10e-3)
		one("DALI", "dali", 100e3, w)
	}
	{
		w := newTimeline(25e6)
		w.add(0, 20e-6)
		pdMessage(w, pdSOP, 0x1161, []uint32{0x0801912C}, false)
		pdMessage(w, pdSOP, 0x0041, nil, false)
		one("USB PD", "usbpd", 25e6, w)
	}
	{
		w := newTimeline(500e6)
		w.add(0, 50e-9)
		var s [][2]int
		for i := 0; i < 8; i++ {
			s = append(s, [2]int{i * 0x11111 & 0xffffff, 0xABCDEF - i})
		}
		spdifStream(w, 48e3, 0, s)
		one("S/PDIF 48 kHz", "spdif", 500e6, w)
	}
	two := func(name, proto string, sr float64, l twoLine) {
		a, b := l.a.codes(), l.b.codes()
		cs = append(cs, detectCase{name, a, b, 1 / sr, proto, 0})
		cs = append(cs, detectCase{name + " swapped", b, a, 1 / sr, proto, 1})
	}
	{
		l := newTwoLine(1e6)
		l.add(1, 1, 300e-6)
		for _, v := range []int{0x1C, 0xF0, 0x1C, 0x32, 0xF0, 0x32} {
			ps2Frame(l, v, false, false, 80e-6, 300e-6)
		}
		two("PS/2", "ps2", 1e6, l)
	}
	{
		s := &swdLine{t: newTwoLine(20e6), per: 1e-6}
		s.host(0, 0)
		s.lineReset()
		s.transfer(false, true, 0, 1, 0x2BA01477, false, 2)
		s.transfer(false, false, 8, 1, 0x000000F0, false, 2)
		s.transfer(true, true, 0xC, 1, 0x12345678, false, 2)
		two("SWD", "swd", 20e6, s.t)
	}
	{
		l := newTwoLine(25e6)
		l.add(1, 1, 2e-6)
		mdioFrame(l, 400e-9, 32, 1, 2, 0x01, 0x02, 0x796D, 0)
		mdioFrame(l, 400e-9, 32, 1, 1, 0x1F, 0x00, 0x1140, 0)
		mdioFrame(l, 400e-9, 32, 1, 2, 0x01, 0x01, 0x0000, 0)
		two("MDIO", "mdio", 25e6, l)
	}
	{
		// a screen of 5ms/div: one frame, its pulses a few percent of it
		l := newTwoLine(200e3)
		l.add(1, 1, 2e-3)
		wiegandFrame(l, wiegandBits(26, 12, 4660), 1e-3)
		two("Wiegand 26", "wiegand", 200e3, l)
	}
	return cs
}

// TestAutodetectNamesEveryProtocol: each protocol's capture is named as
// itself, on its channel, by the detection the scope runs (AutodetectFast),
// and by no other protocol: the evidence each needs keeps them apart (NEC
// and SIRC, RC-5, DALI and Manchester, 1-Wire, DHT and WS281x, PS/2 and
// I2C, SWD and MDIO and SPI, the UART-framed ones and UART).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func TestAutodetectNamesEveryProtocol(t *testing.T) {
	for _, tc := range protoDetectCases() {
		t0 := time.Now()
		r, _ := AutodetectFast(tc.c1, tc.c2, tc.colT, "hex")
		el := time.Since(t0)
		if r.Proto != tc.proto || r.Roles.ChA != tc.chA {
			t.Errorf("%s: named %q on C%d (%s), want %q on C%d", tc.name, r.Proto, r.Roles.ChA+1, r.Src, tc.proto, tc.chA+1)
			continue
		}
		t.Logf("%-16s %-8s %6d samples  %v", tc.name, r.Proto, len(tc.c1), el.Round(time.Microsecond))
	}
}

// TestAutodetectPlainUARTIsNoFramedProtocol: a UART carrying text at the
// rates LIN, DMX512, MIDI and Modbus run at stays a UART.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func TestAutodetectPlainUARTIsNoFramedProtocol(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, baud := range []float64{19200, 250000, 31250, 9600} {
		sr := 64 * baud
		w := newTimeline(sr)
		w.idle(20 / baud)
		for i := 0; i < 24; i++ {
			tlUART(w, 0x20+rng.Intn(0x5f), baud, "none", 1)
			if i%6 == 5 {
				w.idle(30 / baud)
			}
		}
		w.idle(20 / baud)
		c := w.codes()
		r, _ := AutodetectFast(c, idleLike(c), 1/sr, "hex")
		if r.Proto != "uart" {
			t.Errorf("UART %v Bd text named %q (%s)", baud, r.Proto, r.Text)
		}
	}
}

// TestAutodetectStableUnderNoise: the protocols' captures with converter
// noise (two codes) and a slow edge are still named right.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-238
func TestAutodetectStableUnderNoise(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	noisy := func(c []uint8) []uint8 {
		out := make([]uint8, len(c))
		prev := float64(c[0])
		for i, v := range c {
			prev += (float64(v) - prev) * 0.5 // a slow edge
			x := prev + rng.NormFloat64()*2
			out[i] = uint8(min(255, max(0, x)))
		}
		return out
	}
	for _, tc := range protoDetectCases() {
		r, _ := AutodetectFast(noisy(tc.c1), noisy(tc.c2), tc.colT, "hex")
		if r.Proto != tc.proto {
			t.Errorf("%s with noise: named %q (%s), want %q", tc.name, r.Proto, r.Src, tc.proto)
		}
	}
}
