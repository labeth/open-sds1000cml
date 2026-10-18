// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Generators of the IR multi decoder's protocols (proto_gen_ir_test.go's
// irLine): pulse-distance frames of each protocol's timing, bits in the
// order sent.

// irPD appends a pulse-distance frame: a leader (when lead > 0), the bits,
// each a pulse of p and a pause of zero or one seconds, a stop pulse, then
// rest seconds of space.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func irPD(l irLine, lead, leadSpace float64, bits []int, p, zero, one, rest float64) {
	if lead > 0 {
		l.mark(lead)
		l.space(leadSpace)
	}
	for _, b := range bits {
		l.mark(p)
		if b == 1 {
			l.space(one)
		} else {
			l.space(zero)
		}
	}
	l.mark(p)
	l.space(rest)
}

// lsbBits is n bits of v, least significant first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func lsbBits(v, n int) []int {
	b := make([]int, n)
	for i := range b {
		b[i] = v >> i & 1
	}
	return b
}

// msbBits is n bits of v, most significant first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func msbBits(v, n int) []int {
	b := make([]int, n)
	for i := range b {
		b[i] = v >> (n - 1 - i) & 1
	}
	return b
}

// irSamsung32 appends a Samsung32 frame: 4.5 ms leader and space, a 16-bit
// address and 16-bit command least significant first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func irSamsung32(l irLine, addr, cmd int) {
	irPD(l, 4.5e-3, 4.5e-3, append(lsbBits(addr, 16), lsbBits(cmd, 16)...), 550e-6, 550e-6, 1650e-6, 50e-3)
}

// irJVC appends a JVC frame (NEC timing, 16 bits: a 4-bit address and a
// 12-bit command, least significant first), its leader unless repeat.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func irJVC(l irLine, addr, cmd int, repeat bool) {
	lead := 9e-3
	if repeat {
		lead = 0
	}
	irPD(l, lead, 4.5e-3, append(lsbBits(addr, 4), lsbBits(cmd, 12)...), 560e-6, 560e-6, 1690e-6, 25e-3)
}

// irNEC16 appends an NEC16 frame: the NEC leader, an 8-bit address, a
// sync (a pulse and a 4.5 ms pause), an 8-bit command.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func irNEC16(l irLine, addr, cmd int) {
	l.mark(9e-3)
	l.space(4.5e-3)
	bit := func(b int) {
		l.mark(560e-6)
		if b == 1 {
			l.space(1690e-6)
		} else {
			l.space(560e-6)
		}
	}
	for _, b := range lsbBits(addr, 8) {
		bit(b)
	}
	l.mark(560e-6)
	l.space(4.5e-3)
	for _, b := range lsbBits(cmd, 8) {
		bit(b)
	}
	l.mark(560e-6)
	l.space(50e-3)
}

// irNEC42 appends an NEC42 frame: 13-bit address and its inverse, 8-bit
// command and its inverse, least significant first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func irNEC42(l irLine, addr, cmd int) {
	b := append(lsbBits(addr, 13), lsbBits(^addr&0x1fff, 13)...)
	b = append(append(b, lsbBits(cmd, 8)...), lsbBits(^cmd&0xff, 8)...)
	irPD(l, 9e-3, 4.5e-3, b, 560e-6, 560e-6, 1690e-6, 50e-3)
}

// irKaseikyo appends a Kaseikyo (Panasonic) frame: 16-bit manufacturer
// code, its 4-bit parity (the xor of its nibbles), genre 1 and 2, a 10-bit
// command and 2-bit id, and the xor of bytes 2 to 4; least significant
// first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func irKaseikyo(l irLine, maker, genre1, genre2, cmd, id int) {
	par := (maker ^ maker>>4 ^ maker>>8 ^ maker>>12) & 15
	var bytes [6]int
	bytes[0], bytes[1] = maker&0xff, maker>>8
	bytes[2] = par | genre1<<4
	bytes[3] = genre2 | (cmd&15)<<4
	bytes[4] = cmd>>4&0x3f | id<<6
	bytes[5] = bytes[2] ^ bytes[3] ^ bytes[4]
	var b []int
	for _, v := range bytes {
		b = append(b, lsbBits(v, 8)...)
	}
	irPD(l, 3380e-6, 1690e-6, b, 423e-6, 423e-6, 1269e-6, 74e-3)
}

// irDenon appends a Denon (Sharp) frame pair: 5-bit address and 8 command
// bits and 2 extension bits, most significant first, no leader; 45 ms on
// the same frame with the command and extension inverted.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func irDenon(l irLine, addr, cmd int) {
	b := append(msbBits(addr, 5), msbBits(cmd, 10)...)
	irPD(l, 0, 0, b, 310e-6, 745e-6, 1780e-6, 45e-3)
	b = append(msbBits(addr, 5), msbBits(^cmd&0x3ff, 10)...)
	irPD(l, 0, 0, b, 310e-6, 745e-6, 1780e-6, 60e-3)
}

// irMatsushita appends a Matsushita frame: 3.5 ms leader and space, a
// 6-bit custom code, 6-bit command and 12-bit address... as IRMP reads it:
// the command's 12 bits then the address's, least significant first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func irMatsushita(l irLine, cmd, addr int) {
	irPD(l, 3488e-6, 3488e-6, append(lsbBits(cmd, 12), lsbBits(addr, 12)...), 872e-6, 872e-6, 2616e-6, 50e-3)
}

// irSamsung37 appends a Samsung frame: 4.5 ms leader and space, a 16-bit
// address, a sync (a pulse and a 4.5 ms space), a 4-bit id and the 8-bit
// command and its inverse, least significant first.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func irSamsung37(l irLine, addr, id, cmd int) {
	l.mark(4.5e-3)
	l.space(4.5e-3)
	bit := func(b int) {
		l.mark(550e-6)
		if b == 1 {
			l.space(1650e-6)
		} else {
			l.space(550e-6)
		}
	}
	for _, b := range lsbBits(addr, 16) {
		bit(b)
	}
	l.mark(550e-6)
	l.space(4.5e-3)
	for _, b := range append(append(lsbBits(id, 4), lsbBits(cmd, 8)...), lsbBits(^cmd&0xff, 8)...) {
		bit(b)
	}
	l.mark(550e-6)
	l.space(50e-3)
}
