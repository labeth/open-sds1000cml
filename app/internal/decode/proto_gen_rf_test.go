// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Generators of the radio and timing line codes (proto_gen_test.go):
// remote-control encoder frames, Oregon Scientific bursts, Miller messages,
// Morse text and DCF77 minutes. Each line idles low, marks high.

// rcFrame2262 appends a PT2262 frame of twelve trits (0, 1, 2 for F) at a
// short pulse of a seconds (a long one is three), then its sync pulse and
// the 31 short pulses of low after it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func rcFrame2262(w *timeline, trits []int, a float64) {
	pulse := func(hi, lo float64) {
		w.add(1, hi*a)
		w.add(0, lo*a)
	}
	for _, t := range trits {
		switch t {
		case 0:
			pulse(1, 3)
			pulse(1, 3)
		case 1:
			pulse(3, 1)
			pulse(3, 1)
		default:
			pulse(1, 3)
			pulse(3, 1)
		}
	}
	pulse(1, 31)
}

// rcFrame1527 appends an EV1527 frame of 24 bits (bit i of v first) at a
// short pulse of a seconds, then its sync.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func rcFrame1527(w *timeline, v int, a float64) {
	for i := 0; i < 24; i++ {
		if v>>i&1 == 1 {
			w.add(1, 3*a)
			w.add(0, a)
		} else {
			w.add(1, a)
			w.add(0, 3*a)
		}
	}
	w.add(1, a)
	w.add(0, 31*a)
}

// manchesterOOK appends bits as Manchester half bits of hp seconds: a 1 high
// then low, a 0 low then high.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func manchesterOOK(w *timeline, bits []int, hp float64) {
	for _, b := range bits {
		w.add(byte(b), hp)
		w.add(byte(1-b), hp)
	}
}

// oregonBurst appends an Oregon Scientific burst (protocol 3, or 2.1 with
// each bit sent inverted then as is) of the nibbles given and their
// checksum (the sum of the nibbles, low nibble first; 2.1 less ten when
// minus10), at a half bit of hp seconds, then idle.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func oregonBurst(w *timeline, ver int, nibbles []int, hp float64, minus10 bool, idle float64) {
	sum := 0
	for _, x := range nibbles {
		sum += x
	}
	if minus10 {
		sum -= 10
	}
	nib := append(append([]int{}, nibbles...), sum&15, sum>>4&15)
	var bits []int
	pre := 24
	if ver == 21 {
		pre = 16
	}
	for i := 0; i < pre; i++ {
		bits = append(bits, 1)
	}
	bits = append(bits, 0, 1, 0, 1)
	for _, x := range nib {
		for k := 0; k < 4; k++ {
			bits = append(bits, x>>k&1)
		}
	}
	if ver == 21 {
		var d []int
		for _, b := range bits {
			d = append(d, 1-b, b)
		}
		bits = d
	}
	manchesterOOK(w, bits, hp)
	w.add(0, idle)
}

// millerMessage appends a Miller-coded message at a bit of T seconds: the
// start 0, then bits; with pause > 0 each transition is a low pulse of
// pause seconds on a high line (modified Miller), else a level change.
// Then idle seconds of no transition.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func millerMessage(w *timeline, bits []int, T, pause, idle float64) {
	all := append([]int{0}, bits...)
	// the transitions' times from the message's start
	var at []float64
	prev := 1
	for i, b := range all {
		t0 := float64(i) * T
		switch {
		case b == 1:
			at = append(at, t0+T/2)
		case prev == 0 || i == 0:
			at = append(at, t0)
		}
		prev = b
	}
	level := byte(0)
	if k := len(w.bits); k > 0 {
		level = w.bits[k-1]
	}
	cur := 0.0
	for _, t := range at {
		if pause > 0 {
			w.add(1, t-cur)
			w.add(0, pause)
			cur = t + pause
			continue
		}
		w.add(level, t-cur)
		level ^= 1
		cur = t
	}
	if pause > 0 {
		level = 1
	}
	w.add(level, float64(len(all))*T-cur+idle)
}

// morseText appends text keyed in Morse at a unit of u seconds (a word gap
// for a space), then idle units of rest.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func morseText(w *timeline, text string, u float64, rest float64) {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == ' ' {
			w.add(0, 4*u) // 3 already after the letter: 7 in all
			continue
		}
		code := MorseOf(c)
		for k := 0; k < len(code); k++ {
			if code[k] == '.' {
				w.add(1, u)
			} else {
				w.add(1, 3*u)
			}
			if k+1 < len(code) {
				w.add(0, u)
			}
		}
		w.add(0, 3*u)
	}
	w.add(0, rest*u)
}

// dcf77Minute appends one DCF77 minute (60 seconds, no pulse in the last)
// with the fields given (BCD) and their parities; badPar flips the minute's
// parity.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func dcf77Minute(w *timeline, min, hr, day, dow, mon, yr int, badPar bool) {
	dcf77MinuteAt(w, 1, min, hr, day, dow, mon, yr, badPar)
}

// dcf77MinuteAt is dcf77Minute with a second of sec seconds (a test
// source's code, faster than the broadcast's).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
func dcf77MinuteAt(w *timeline, sec float64, min, hr, day, dow, mon, yr int, badPar bool) {
	var b [59]int
	b[20] = 1
	put := func(at, wd, v int) {
		for q := 0; q < wd; q++ {
			b[at+q] = v >> q & 1
		}
	}
	put(21, 7, min)
	put(29, 6, hr)
	put(36, 6, day)
	put(42, 3, dow)
	put(45, 5, mon)
	put(50, 8, yr)
	par := func(from, to int) int {
		p := 0
		for q := from; q <= to; q++ {
			p ^= b[q]
		}
		return p
	}
	b[28] = par(21, 27)
	if badPar {
		b[28] ^= 1
	}
	b[35] = par(29, 34)
	b[58] = par(36, 57)
	for s := 0; s < 59; s++ {
		hi := 0.1
		if b[s] == 1 {
			hi = 0.2
		}
		w.add(1, hi*sec)
		w.add(0, (1-hi)*sec)
	}
	w.add(0, sec)
}
