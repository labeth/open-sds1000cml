// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Generators of the two-wire buses beyond the first thirty
// (proto_gen_test.go): line a the clock (TCKC, SDCKA), line b the data.

// clockBits appends bits on line b, each over one clock period per on line
// a: the clock low for the first half (the data set there) and high for the
// second (read at its rise).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func clockBits(l twoLine, bits []int, per float64) {
	for _, b := range bits {
		l.add(0, byte(b), per/2)
		l.add(1, byte(b), per/2)
	}
}

// sdToken is an SD command (host) or response's bits: start 0, the
// transmission bit, six index bits, the 32-bit argument, CRC-7 (spoiled by
// badCRC) and the end bit.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func sdToken(host bool, idx int, arg uint32, badCRC bool) []int {
	b := []int{0, 0}
	if host {
		b[1] = 1
	}
	for i := 5; i >= 0; i-- {
		b = append(b, idx>>i&1)
	}
	for i := 31; i >= 0; i-- {
		b = append(b, int(arg>>i&1))
	}
	c := crc7(b)
	if badCRC {
		c ^= 0x11
	}
	for i := 6; i >= 0; i-- {
		b = append(b, c>>i&1)
	}
	return append(b, 1)
}

// sdR2 is an R2 response's 136 bits: start, transmission 0, six reserved
// 1s, the 120-bit register (reg's bytes) with its CRC-7 and the end bit.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func sdR2(reg [15]int) []int {
	b := []int{0, 0, 1, 1, 1, 1, 1, 1}
	var r []int
	for _, v := range reg {
		for i := 7; i >= 0; i-- {
			r = append(r, v>>i&1)
		}
	}
	c := crc7(r)
	b = append(b, r...)
	for i := 6; i >= 0; i-- {
		b = append(b, c>>i&1)
	}
	return append(b, 1)
}

// sdIdle is n clock periods of the CMD line high.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func sdIdle(n int) []int {
	b := make([]int, n)
	for i := range b {
		b[i] = 1
	}
	return b
}

// pdiChar is a PDI character's bits: start 0, eight bits least significant
// first, even parity (flipped by badPar) and two stop 1s.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func pdiChar(v int, badPar bool) []int {
	b := []int{0}
	p := 0
	for i := 0; i < 8; i++ {
		b = append(b, v>>i&1)
		p ^= v >> i & 1
	}
	if badPar {
		p ^= 1
	}
	return append(b, p, 1, 1)
}

// cjtagLine writes compact JTAG on TCKC (a) and TMSC (b) at a TCKC period
// per.
type cjtagLine struct {
	l   twoLine
	per float64
	tms byte // TMSC's level
}

// rise writes one TCKC cycle with TMSC at v (set a quarter period after
// TCKC falls: a change at the fall itself would count as an escape edge).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (c *cjtagLine) rise(v int) {
	c.l.add(0, c.tms, c.per/4)
	c.tms = byte(v)
	c.l.add(0, c.tms, c.per/4)
	c.l.add(1, c.tms, c.per/2)
}

// escape holds TCKC high while TMSC toggles n times, then one cycle low.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (c *cjtagLine) escape(n int) {
	c.l.add(0, c.tms, c.per/2)
	c.l.add(1, c.tms, c.per/2)
	for i := 0; i < n; i++ {
		c.tms ^= 1
		c.l.add(1, c.tms, c.per/2)
	}
	c.l.add(1, c.tms, c.per/2)
}

// tck writes an OScan1 TCK: nTDI, TMS and TDO on TMSC (first omits nTDI,
// as the first after the activation code does).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (c *cjtagLine) tck(tdi, tms, tdo int, first bool) {
	if !first {
		c.rise(1 - tdi)
	}
	c.rise(tms)
	c.rise(tdo)
}

// scan shifts n bits of tdi (out on tdo) through IR (ir) or DR from
// Run-Test/Idle and back to it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (c *cjtagLine) scan(ir bool, n int, tdi, tdo uint32) {
	c.tck(0, 1, 0, false) // select DR
	if ir {
		c.tck(0, 1, 0, false) // select IR
	}
	c.tck(0, 0, 0, false) // capture
	c.tck(0, 0, 0, false) // shift
	for i := 0; i < n; i++ {
		tms := 0
		if i == n-1 {
			tms = 1 // exit1 with the last bit
		}
		c.tck(int(tdi>>i&1), tms, int(tdo>>i&1), false)
	}
	c.tck(0, 1, 0, false) // update
	c.tck(0, 0, 0, false) // run-test/idle
}

// mapleLine writes Maple bus frames on SDCKA (a) and SDCKB (b), a step of
// st seconds each line change.
type mapleLine struct {
	l      twoLine
	st     float64
	la, lb byte
}

// set moves the lines to a, b over one step.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (m *mapleLine) set(a, b byte) {
	m.la, m.lb = a, b
	m.l.add(a, b, m.st)
}

// frame writes a start pattern, the bytes (each bit at a fall of the
// clocking line, the other line holding it) and the end pattern.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func (m *mapleLine) frame(bytes []int) {
	m.set(1, 1)
	m.set(0, 1)
	for i := 0; i < 4; i++ {
		m.set(0, 0)
		m.set(0, 1)
	}
	m.set(1, 1)
	k := 0
	for _, v := range bytes {
		for i := 7; i >= 0; i-- {
			bit := byte(v >> i & 1)
			if k%2 == 0 {
				// SDCKA clocks: SDCKB holds the bit (it may fall only
				// before a frame's first bit)
				m.set(m.la, bit)
				if m.la == 0 {
					m.set(1, bit)
				}
				m.set(0, bit)
			} else {
				// SDCKB clocks: SDCKA holds the bit
				m.set(bit, m.lb)
				if m.lb == 0 {
					m.set(bit, 1)
				}
				m.set(bit, 0)
			}
			k++
		}
	}
	// end: SDCKA high, then it falls twice with SDCKB low, then both rise
	m.set(1, 0)
	m.set(0, 0)
	m.set(1, 0)
	m.set(0, 0)
	m.set(1, 1)
	m.set(1, 1)
}

// mapleBytes is a Maple frame's bytes: the length in words, source,
// destination, command, the data words' bytes and the checksum.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func mapleBytes(src, dst, cmd int, data []int, badCk bool) []int {
	b := []int{len(data) / 4, src, dst, cmd}
	b = append(b, data...)
	x := 0
	for _, v := range b {
		x ^= v
	}
	if badCk {
		x ^= 0x40
	}
	return append(b, x)
}

// caliperFrame writes a caliper's 24 bits (the count, then the flags,
// least significant first) at a clock period per, then rest seconds of
// clock low.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func caliperFrame(l twoLine, count int, neg, inch bool, per, rest float64) {
	flags := 0
	if neg {
		flags |= 1 << 4
	}
	if inch {
		flags |= 1 << 7
	}
	var bits []int
	for i := 0; i < 16; i++ {
		bits = append(bits, count>>i&1)
	}
	for i := 0; i < 8; i++ {
		bits = append(bits, flags>>i&1)
	}
	clockBits(l, bits, per)
	l.add(0, 0, rest)
}
