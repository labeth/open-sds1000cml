// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Generators of the single-wire buses beyond the first thirty
// (proto_gen_test.go): STM8 SWIM, TI SDQ, PJDL with PJON frames, DSI and
// Qi.

// swimFrame appends a SWIM frame at a bit of T seconds: the start bit
// (dir: 0 the host, 1 the device), nb bits of v most significant first,
// their even parity (flipped by badPar) and the acknowledge ack; a 0 is low
// for 20 of the bit's 22 SWIM clocks, a 1 for 2. Then gap seconds of idle.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func swimFrame(w *timeline, dir, nb, v int, T float64, ack int, badPar bool, gap float64) {
	bits := []int{dir}
	p := 0
	for i := nb - 1; i >= 0; i-- {
		bits = append(bits, v>>i&1)
		p ^= v >> i & 1
	}
	if badPar {
		p ^= 1
	}
	bits = append(bits, p, ack)
	for _, b := range bits {
		lo := 2.0
		if b == 0 {
			lo = 20
		}
		w.add(0, lo/22*T)
		w.add(1, (22-lo)/22*T)
	}
	w.add(1, gap)
}

// swimSync appends a SWIM synchronization frame: 128 SWIM clocks of low
// at a bit of T (22 clocks), then idle.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func swimSync(w *timeline, T float64) {
	w.add(0, 128.0/22*T)
	w.add(1, 4*T)
}

// sdqBreak appends an SDQ break (two bits low) and a bit of high.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func sdqBreak(w *timeline, T float64) {
	w.add(0, 2*T)
	w.add(1, T)
}

// sdqByte appends an SDQ byte, least significant bit first: a 1 low for a
// quarter bit, a 0 for three quarters.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func sdqByte(w *timeline, v int, T float64) {
	for i := 0; i < 8; i++ {
		lo := 0.75
		if v>>i&1 == 1 {
			lo = 0.25
		}
		w.add(0, lo*T)
		w.add(1, (1-lo)*T)
	}
}

// pjonPacket is a PJON packet: receiver id, header, length, the header's
// CRC-8, payload and the CRC-8 of all before it (badEnd spoils it).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func pjonPacket(rx, header int, payload []int, badEnd bool) []int {
	b := []int{rx, header, 4 + len(payload) + 1}
	b = append(b, crc8PJON(b))
	b = append(b, payload...)
	c := crc8PJON(b)
	if badEnd {
		c ^= 0x5A
	}
	return append(b, c)
}

// pjdlFrame appends a PJDL frame in mode m (the line idles low): three
// sync pads (high for the pad time, then a data time low), then each byte
// after its own sync pad, eight data times least significant bit first;
// then idle seconds.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func pjdlFrame(w *timeline, m int, b []int, idle float64) {
	data, pad := pjdlModes[m][0]*1e-6, pjdlModes[m][1]*1e-6
	for i := 0; i < 3; i++ {
		w.add(1, pad)
		w.add(0, data)
	}
	for _, v := range b {
		w.add(1, pad)
		w.add(0, data)
		for k := 0; k < 8; k++ {
			w.add(byte(v>>k&1), data)
		}
	}
	w.add(0, idle)
}

// dsiFrame appends a DSI frame on an idle-low line: the start bit 0 and
// eight bits of v most significant first, Manchester at 600 bit/s (a 1
// high then low), then idle seconds.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func dsiFrame(w *timeline, v int, idle float64) {
	const half = 1 / 1200.0
	bits := []int{0}
	for i := 7; i >= 0; i-- {
		bits = append(bits, v>>i&1)
	}
	for _, b := range bits {
		w.add(byte(b), half)
		w.add(byte(1-b), half)
	}
	w.add(0, idle)
}

// qiPacket appends a Qi packet at 2 kbit/s, biphase mark: eleven preamble
// 1s, then the header, message and checksum (spoiled when badCk), each a
// start 0, eight bits least significant first, odd parity and a stop 1;
// then a transition (the stop bit's end, which sigrok needs to see) and
// idle seconds.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func qiPacket(w *timeline, b []int, badCk bool, idle float64) {
	l := &bmcLine{w: w, bit: 500e-6}
	if k := len(w.bits); k > 0 {
		l.lvl = w.bits[k-1]
	}
	for i := 0; i < 11; i++ {
		l.put(1)
	}
	x := 0
	for _, v := range b {
		x ^= v
	}
	if badCk {
		x ^= 0x21
	}
	for _, v := range append(append([]int{}, b...), x) {
		l.put(0)
		p := 1
		for i := 0; i < 8; i++ {
			l.put(v >> i & 1)
			p ^= v >> i & 1
		}
		l.put(p)
		l.put(1)
	}
	w.add(l.lvl^1, idle)
}
