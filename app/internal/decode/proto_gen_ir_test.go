// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Generators of the infrared remote protocols (proto_gen_test.go): marks
// and spaces on an irLine, written active-low (a receiver's output) unless
// activeHigh, a mark modulated at carrierHz when that is set.

// irLine writes marks and spaces.
type irLine struct {
	w          *timeline
	activeHigh bool
	carrierHz  float64
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
func (l irLine) level(mark bool) byte {
	if mark == l.activeHigh {
		return 1
	}
	return 0
}

// mark appends a mark of d seconds.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
func (l irLine) mark(d float64) {
	if l.carrierHz <= 0 {
		l.w.add(l.level(true), d)
		return
	}
	half := 0.5 / l.carrierHz
	for t := 0.0; t < d-1e-12; t += 2 * half {
		on := min(half, d-t)
		l.w.add(l.level(true), on)
		if off := min(half, d-t-on); off > 0 {
			l.w.add(l.level(false), off)
		}
	}
}

// space appends a space of d seconds.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
func (l irLine) space(d float64) { l.w.add(l.level(false), d) }

// necFrame appends an NEC frame of addr and cmd (addrHi < 0: the address's
// inverse) and the 40 ms after it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
func necFrame(l irLine, addr, addrHi, cmd int) {
	if addrHi < 0 {
		addrHi = addr ^ 0xff
	}
	l.mark(9e-3)
	l.space(4.5e-3)
	for _, v := range []int{addr, addrHi, cmd, cmd ^ 0xff} {
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
}

// necRepeat appends an NEC repeat code and the 96 ms after it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
func necRepeat(l irLine) {
	l.mark(9e-3)
	l.space(2.25e-3)
	l.mark(562.5e-6)
	l.space(96e-3)
}

// sircFrame appends a SIRC frame of nbits (12, 15 or 20) and the gap to 45 ms.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
func sircFrame(l irLine, nbits, cmd, addr, ext int) {
	bits := make([]int, 0, nbits)
	push := func(v, n int) {
		for i := 0; i < n; i++ {
			bits = append(bits, v>>i&1)
		}
	}
	push(cmd, 7)
	switch nbits {
	case 12:
		push(addr, 5)
	case 15:
		push(addr, 8)
	case 20:
		push(addr, 5)
		push(ext, 8)
	}
	l.mark(2.4e-3)
	used := 2.4e-3
	for _, b := range bits {
		l.space(0.6e-3)
		d := 0.6e-3
		if b == 1 {
			d = 1.2e-3
		}
		l.mark(d)
		used += 0.6e-3 + d
	}
	l.space(45e-3 - used)
}

// rc5Frame appends an RC-5 frame (address 5 bits, command 6 or 7 bits:
// RC-5X) and 100 ms of idle.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
func rc5Frame(l irLine, toggle, addr, cmd int) {
	s2 := 1 - (cmd>>6)&1
	bits := []int{1, s2, toggle}
	for k := 4; k >= 0; k-- {
		bits = append(bits, addr>>k&1)
	}
	for k := 5; k >= 0; k-- {
		bits = append(bits, cmd>>k&1)
	}
	const h = 889e-6
	for _, b := range bits {
		if b == 1 {
			l.space(h)
			l.mark(h)
		} else {
			l.mark(h)
			l.space(h)
		}
	}
	l.space(100e-3)
}

// rc6Frame appends an RC-6 frame of the mode and payload bytes and 100 ms
// of idle.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
func rc6Frame(l irLine, mode, toggle int, payload []int) {
	const t = 444e-6
	bit := func(b int, u float64) {
		if b == 1 {
			l.mark(u)
			l.space(u)
		} else {
			l.space(u)
			l.mark(u)
		}
	}
	l.mark(6 * t)
	l.space(2 * t)
	bit(1, t)
	for k := 2; k >= 0; k-- {
		bit(mode>>k&1, t)
	}
	bit(toggle, 2*t)
	for _, v := range payload {
		for k := 7; k >= 0; k-- {
			bit(v>>k&1, t)
		}
	}
	l.space(100e-3)
}
