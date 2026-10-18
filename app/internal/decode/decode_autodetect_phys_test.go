// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math/rand"
	"testing"
	"time"
)

// physDetectCases are captures of every sigrok protocol beyond the first
// thirty, as a scope would record them at a timebase that shows a few
// frames, on either channel (or in either channel order).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func physDetectCases() []detectCase {
	var cs []detectCase
	one := func(name, proto string, sr float64, w *timeline) {
		c := w.codes()
		cs = append(cs, detectCase{name, c, idleLike(c), 1 / sr, proto, 0})
		cs = append(cs, detectCase{name + " on C2", idleLike(c), c, 1 / sr, proto, 1})
	}
	two := func(name, proto string, sr float64, l twoLine) {
		a, b := l.a.codes(), l.b.codes()
		cs = append(cs, detectCase{name, a, b, 1 / sr, proto, 0})
		cs = append(cs, detectCase{name + " swapped", b, a, 1 / sr, proto, 1})
	}
	{
		w := newTimeline(2e6)
		w.add(0, 30e-6)
		var d []float64
		for i := 0; i < 24; i++ {
			d = append(d, 0.3+0.4*float64(i%7)/6)
		}
		pwmWave(w, 100e-6, d)
		one("PWM", "pwm", 2e6, w)
	}
	{
		const a = 350e-6
		w := newTimeline(200e3)
		w.add(0, 40*a)
		rcFrame2262(w, []int{0, 1, 2, 0, 0, 1, 1, 2, 0, 1, 0, 0}, a)
		rcFrame2262(w, []int{0, 1, 2, 0, 0, 1, 1, 2, 0, 1, 0, 0}, a)
		one("RC encode", "rcencode", 200e3, w)
	}
	{
		w := newTimeline(100e3)
		w.add(0, 20e-3)
		for i := 0; i < 2; i++ {
			oregonBurst(w, 3, []int{0xF, 0x8, 0x2, 0x4, 0x1, 0x3, 0xA, 0x0, 0x5, 0x3, 0x2, 0x0, 0x4, 0x5, 0x0}, 1/2048.0, false, 40e-3)
		}
		one("Oregon OOK", "ook", 100e3, w)
	}
	{
		T := 1.0 / 106000
		for _, pause := range []float64{2e-6, 0} {
			w := newTimeline(4e6)
			w.add(1, 40e-6)
			millerMessage(w, []int{1, 0, 1, 1, 0, 0, 1, 0, 0, 0, 1, 1, 1, 0, 1, 0, 1, 1, 0, 0}, T, pause, 60e-6)
			millerMessage(w, []int{0, 0, 1, 0, 1, 1, 1, 1, 0, 1, 0, 0, 1, 1, 0, 1}, T, pause, 60e-6)
			millerMessage(w, []int{1, 1, 0, 1, 0, 0, 1, 1, 0, 1, 0, 1, 1, 0, 0, 0}, T, pause, 60e-6)
			name := "Miller"
			if pause > 0 {
				name = "modified Miller"
			}
			one(name, "miller", 4e6, w)
		}
	}
	{
		w := newTimeline(2000)
		w.add(0, 0.5)
		morseText(w, "CQ CQ DE SM5ABC", 0.06, 10)
		one("Morse", "morse", 2000, w)
	}
	{
		w := newTimeline(1000)
		w.add(0, 0.3)
		dcf77Minute(w, 0x37, 0x12, 0x18, 0x7, 0x10, 0x26, false)
		dcf77Minute(w, 0x38, 0x12, 0x18, 0x7, 0x10, 0x26, false)
		one("DCF77", "dcf77", 1000, w)
	}
	{
		// a test source's code at 3 ms a second, as a search at 20 ms/div reads it
		w := newTimeline(125e3)
		w.add(0, 2e-3)
		dcf77MinuteAt(w, 3e-3, 0x37, 0x12, 0x18, 0x7, 0x10, 0x26, false)
		dcf77MinuteAt(w, 3e-3, 0x38, 0x12, 0x18, 0x7, 0x10, 0x26, false)
		one("DCF77 at 3 ms a second", "dcf77", 125e3, w)
	}
	{
		w := newTimeline(125e3)
		w.add(0, 20e-3)
		morseText(w, "CQ CQ DE SM5", 2e-3, 10)
		one("Morse at 2 ms a unit", "morse", 125e3, w)
	}
	{
		w := newTimeline(500e3)
		w.add(1, 768e-6)
		for i := 0; i < 3; i++ {
			em4100Frame(w, [5]int{0x01, 0x23, 0x45, 0x67, 0x89}, 256e-6, false)
		}
		one("EM4100", "em4100", 500e3, w)
	}
	for _, carrier := range []bool{true, false} {
		sr := 1e6
		if carrier {
			sr = 4e6 // a carrier's half cycle is 4 us
		}
		w := newTimeline(sr)
		f := rfidField{w, carrier}
		f.on(500 * rfidFC)
		em4305Command(f, em4305Bits(2, 5, [4]int{0xDE, 0xAD, 0xBE, 0xEF}, false))
		em4305Command(f, em4305Bits(1, -1, [4]int{0x12, 0x34, 0x56, 0x78}, false))
		name := "EM4305 envelope"
		if carrier {
			name = "EM4305 carrier"
		}
		one(name, "em4305", sr, w)
		w = newTimeline(sr)
		f = rfidField{w, carrier}
		f.on(500 * rfidFC)
		t55xxCommand(f, t55xxBits(2, -1, 0, 0x00148040, 1))
		t55xxCommand(f, t55xxBits(2, -1, 0, 0xDEADBEEF, 2))
		one("T55xx "+name[7:], "t55xx", sr, w)
	}
	{
		const T = 22 / 4e6
		w := newTimeline(20e6)
		w.add(1, 20e-6)
		swimSync(w, T)
		for _, f := range [][3]int{{0, 3, 1}, {0, 8, 2}, {0, 8, 0}, {0, 8, 0x50}, {0, 8, 0x0A}, {1, 8, 0x5A}, {1, 8, 0xC3}} {
			swimFrame(w, f[0], f[1], f[2], T, 1, false, 3*T)
		}
		one("SWIM", "swim", 20e6, w)
	}
	{
		T := 1 / 98425.0
		w := newTimeline(10e6)
		w.add(1, 30e-6)
		sdqBreak(w, T)
		for _, v := range []int{0x33, 0x01, 0xFE, 0x80, 0x55} {
			sdqByte(w, v, T)
		}
		w.add(1, 50e-6)
		one("SDQ", "sdq", 10e6, w)
	}
	{
		w := newTimeline(2e6)
		w.add(0, 500e-6)
		pjdlFrame(w, 1, pjonPacket(0x2A, 0x02, []int{0x11, 0x22, 0x33}, false), 1e-3)
		pjdlFrame(w, 1, pjonPacket(0x05, 0x00, []int{0x48, 0x69}, false), 1e-3)
		one("PJON", "pjdl", 2e6, w)
	}
	{
		w := newTimeline(100e3)
		w.add(0, 10e-3)
		for _, v := range []int{0x00, 0xFF, 0x80, 0x55} {
			dsiFrame(w, v, 12e-3)
		}
		one("DSI", "dsi", 100e3, w)
	}
	{
		w := newTimeline(200e3)
		w.add(0, 5e-3)
		qiPacket(w, []int{0x01, 0x80}, false, 8e-3)
		qiPacket(w, []int{0x03, 0x05}, false, 8e-3)
		one("Qi", "qi", 200e3, w)
	}
	ir := func(name string, f func(l irLine)) {
		w := newTimeline(100e3)
		l := irLine{w: w}
		l.space(20e-3)
		f(l)
		one(name, "irmp", 100e3, w)
	}
	ir("IR Samsung32", func(l irLine) { irSamsung32(l, 0x0707, 0xFD02); irSamsung32(l, 0x0707, 0xFD02) })
	ir("IR Kaseikyo", func(l irLine) { irKaseikyo(l, 0x2002, 0x8, 0x0, 0x123, 0x1) })
	ir("IR JVC", func(l irLine) { irJVC(l, 0x3, 0x5A1, false); irJVC(l, 0x3, 0x5A1, true) })
	ir("IR Denon", func(l irLine) { irDenon(l, 0x0A, 0x2C4) })
	{
		l := newTwoLine(1e6)
		l.add(0, 0, 300e-6)
		var moves []int
		for i := 0; i < 30; i++ {
			moves = append(moves, 1)
		}
		quadMoves(l, moves, 100e-6, 1e-3)
		two("Quadrature", "quad", 1e6, l)
	}
	{
		l := newTwoLine(1e6)
		l.add(0, 1, 300e-6)
		dirs := make([]byte, 0, 30)
		for i := 0; i < 30; i++ {
			dirs = append(dirs, byte(i/15))
		}
		stepperMoves(l, dirs, 200e-6, 1e-3)
		two("Stepper", "stepper", 1e6, l)
	}
	{
		l := newTwoLine(4e6)
		var bits []int
		for _, t := range [][]int{sdIdle(16), sdToken(true, 17, 0x1000, false), sdIdle(4), sdToken(false, 17, 0x900, false),
			sdIdle(16), sdToken(true, 13, 0x12340000, false), sdIdle(4), sdToken(false, 13, 0x900, false), sdIdle(16)} {
			bits = append(bits, t...)
		}
		clockBits(l, bits, 4e-6)
		two("SD card", "sdcard", 4e6, l)
	}
	{
		l := newTwoLine(2e6)
		bits := []int{1, 1, 1, 1, 1, 1, 1, 1}
		for _, v := range []int{0xC2, 0x59, 0x80, 0x07, 0x4C, 0xCA, 0x01, 0x00, 0x01, 0x55, 0x0C, 0x00, 0x00, 0x00, 0x01, 0x22} {
			bits = append(bits, pdiChar(v, false)...)
		}
		bits = append(bits, 1, 1, 1, 1, 1, 1, 1, 1)
		clockBits(l, bits, 4e-6)
		two("PDI", "pdi", 2e6, l)
	}
	{
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
		c.scan(false, 32, 0x12345678, 0x4BA00477)
		two("cJTAG", "cjtag", 2e6, c.l)
	}
	{
		m := &mapleLine{l: newTwoLine(20e6), st: 250e-9}
		m.set(1, 1)
		m.frame(mapleBytes(0x20, 0x00, 0x01, nil, false))
		for i := 0; i < 20; i++ {
			m.set(1, 1)
		}
		m.frame(mapleBytes(0x00, 0x20, 0x05, []int{0x00, 0x00, 0x00, 0x01}, false))
		m.set(1, 1)
		two("Maple", "maple", 20e6, m.l)
	}
	{
		l := newTwoLine(200e3)
		l.add(0, 0, 20e-3)
		for i := 0; i < 3; i++ {
			caliperFrame(l, 1234+i, false, false, 80e-6, 30e-3)
		}
		two("Caliper", "caliper", 200e3, l)
	}
	return cs
}

// TestAutodetectNamesPhysProtocols: each protocol's capture is named as
// itself, on its channel, by the detection the scope runs (AutodetectFast),
// and by no other protocol.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func TestAutodetectNamesPhysProtocols(t *testing.T) {
	for _, tc := range physDetectCases() {
		t0 := time.Now()
		r, _ := AutodetectFast(tc.c1, tc.c2, tc.colT, "hex")
		el := time.Since(t0)
		// (either line of a quadrature encoder may be A: the other order
		// reads the direction reversed)
		if r.Proto != tc.proto || (r.Roles.ChA != tc.chA && tc.proto != "quad") {
			t.Errorf("%s: named %q on C%d (%s), want %q on C%d", tc.name, r.Proto, r.Roles.ChA+1, r.Src, tc.proto, tc.chA+1)
			continue
		}
		t.Logf("%-20s %-8s %7d samples  %v", tc.name, r.Proto, len(tc.c1), el.Round(time.Microsecond))
	}
}

// TestAutodetectPhysStableUnderNoise: the captures with converter noise and
// a slow edge are still named right.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func TestAutodetectPhysStableUnderNoise(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	noisy := func(c []uint8) []uint8 {
		out := make([]uint8, len(c))
		prev := float64(c[0])
		for i, v := range c {
			prev += (float64(v) - prev) * 0.5
			x := prev + rng.NormFloat64()*2
			out[i] = uint8(min(255, max(0, x)))
		}
		return out
	}
	for _, tc := range physDetectCases() {
		r, _ := AutodetectFast(noisy(tc.c1), noisy(tc.c2), tc.colT, "hex")
		if r.Proto != tc.proto {
			t.Errorf("%s with noise: named %q (%s), want %q", tc.name, r.Proto, r.Src, tc.proto)
		}
	}
}

// TestAutodetectPhysStealsNothing: the captures of the first thirty
// protocols are named as they were (protoDetectCases), plain clocks are no
// PWM or encoder, a Manchester line is no Miller.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-256
func TestAutodetectPhysStealsNothing(t *testing.T) {
	phys := map[string]bool{}
	for _, c := range physDetectCases() {
		phys[c.proto] = true
	}
	for _, tc := range protoDetectCases() {
		r, _ := AutodetectFast(tc.c1, tc.c2, tc.colT, "hex")
		if phys[r.Proto] {
			t.Errorf("%s named %q", tc.name, r.Proto)
		}
	}
	// a clock, and the same clock on both channels
	w := newTimeline(10e6)
	for i := 0; i < 200; i++ {
		w.add(1, 1e-6)
		w.add(0, 1e-6)
	}
	clk := w.codes()
	for _, pair := range [][2][]uint8{{clk, idleLike(clk)}, {clk, clk}} {
		if r, _ := AutodetectFast(pair[0], pair[1], 1e-7, "hex"); phys[r.Proto] {
			t.Errorf("a clock named %q", r.Proto)
		}
	}
	// Manchester data
	bits := []int{1, 0, 1, 0, 1, 0, 1, 1}
	for i := 0; i < 64; i++ {
		bits = append(bits, (i*7+3)%5&1)
	}
	w = newTimeline(4e6)
	w.add(0, 20e-6)
	for _, b := range bits {
		w.add(byte(b), 5e-6)
		w.add(byte(1-b), 5e-6)
	}
	w.add(0, 20e-6)
	m := w.codes()
	if r, _ := AutodetectFast(m, idleLike(m), 2.5e-7, "hex"); phys[r.Proto] {
		t.Errorf("Manchester named %q", r.Proto)
	}
}
