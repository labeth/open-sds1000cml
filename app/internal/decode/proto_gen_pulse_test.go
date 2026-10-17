// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Generators of the pulse-coded single-wire protocols (proto_gen_test.go).

// owReset appends a 1-Wire reset (standard or overdrive) and, if presence,
// a device's presence pulse, then the rest of the reset's high time.
// TRLC-LINKS: REQ-SDS-018
func owReset(w *timeline, od, presence bool) {
	k := 1.0
	if od {
		k = 0.1
	}
	w.add(0, 500e-6*k)
	if presence {
		w.add(1, 30e-6*k)
		w.add(0, 120e-6*k)
		w.add(1, 350e-6*k)
	} else {
		w.add(1, 500e-6*k)
	}
}

// owBitSlot appends one 1-Wire time slot.
// TRLC-LINKS: REQ-SDS-018
func owBitSlot(w *timeline, b int, od bool) {
	if od {
		if b == 1 {
			w.add(0, 1e-6)
			w.add(1, 9e-6)
		} else {
			w.add(0, 7.5e-6)
			w.add(1, 2.5e-6)
		}
		return
	}
	if b == 1 {
		w.add(0, 6e-6)
		w.add(1, 64e-6)
	} else {
		w.add(0, 60e-6)
		w.add(1, 10e-6)
	}
}

// owByte appends a byte least significant bit first.
// TRLC-LINKS: REQ-SDS-018
func owByte(w *timeline, v int, od bool) {
	for i := 0; i < 8; i++ {
		owBitSlot(w, v>>i&1, od)
	}
}

// owSearchROM appends a Search ROM's slots for one device's ROM: per bit
// the bit, its complement and the master's direction.
// TRLC-LINKS: REQ-SDS-018
func owSearchROM(w *timeline, rom []int) {
	for _, v := range rom {
		for i := 0; i < 8; i++ {
			b := v >> i & 1
			owBitSlot(w, b, false)
			owBitSlot(w, 1-b, false)
			owBitSlot(w, b, false)
		}
	}
}

// owROM is a ROM id with its CRC-8.
// TRLC-LINKS: REQ-SDS-018
func owROM(family int, serial ...int) []int {
	rom := append([]int{family}, serial...)
	return append(rom, crc8Maxim(rom))
}

// wsFrame appends a WS281x frame at rate (800e3 or 400e3) and an 80 us latch.
// TRLC-LINKS: REQ-SDS-018
func wsFrame(w *timeline, rate float64, b []int) {
	per := 1 / rate
	for _, v := range b {
		for i := 7; i >= 0; i-- {
			hi := 0.32 * per
			if v>>i&1 == 1 {
				hi = 0.68 * per
			}
			w.add(1, hi)
			w.add(0, per-hi)
		}
	}
	w.add(0, 80e-6)
}

// dhtReading appends the host's start (startUs low), the sensor's response
// and its five bytes, the checksum computed unless ck >= 0 replaces it.
// TRLC-LINKS: REQ-SDS-018
func dhtReading(w *timeline, startUs float64, b [4]int, ck int) {
	if ck < 0 {
		ck = (b[0] + b[1] + b[2] + b[3]) & 0xff
	}
	w.add(0, startUs*1e-6)
	w.add(1, 30e-6)
	w.add(0, 80e-6)
	w.add(1, 80e-6)
	for _, v := range append(b[:], ck) {
		for i := 7; i >= 0; i-- {
			w.add(0, 50e-6)
			if v>>i&1 == 1 {
				w.add(1, 70e-6)
			} else {
				w.add(1, 27e-6)
			}
		}
	}
	w.add(0, 50e-6)
	w.add(1, 5e-3)
}

// cecBit appends one CEC data bit.
// TRLC-LINKS: REQ-SDS-018
func cecBit(w *timeline, b int) {
	if b == 1 {
		w.add(0, 0.6e-3)
		w.add(1, 1.8e-3)
	} else {
		w.add(0, 1.5e-3)
		w.add(1, 0.9e-3)
	}
}

// cecMessage appends a CEC message: the start bit and blocks of the bytes
// with EOM on the last; acks gives each block's acknowledge bit as the
// follower leaves it (0: held low).
// TRLC-LINKS: REQ-SDS-018
func cecMessage(w *timeline, b []int, acks []int) {
	w.add(0, 3.7e-3)
	w.add(1, 0.8e-3)
	for i, v := range b {
		for k := 7; k >= 0; k-- {
			cecBit(w, v>>k&1)
		}
		eom := 0
		if i == len(b)-1 {
			eom = 1
		}
		cecBit(w, eom)
		cecBit(w, acks[i])
	}
	w.add(1, 10e-3)
}

// j1850Frame appends a J1850 VPW frame (idle low): SOF, the bytes with
// their CRC (spoiled when badCRC), EOD and the end of frame. The bits
// alternate between active and passive runs, so a whole number of bytes
// ends on an active one and the EOD is passive.
// TRLC-LINKS: REQ-SDS-018
func j1850Frame(w *timeline, b []int, badCRC bool) {
	crc := j1850CRC(b)
	if badCRC {
		crc ^= 0x5A
	}
	w.add(1, 200e-6)
	lvl := byte(0)
	for _, v := range append(append([]int{}, b...), crc) {
		for i := 7; i >= 0; i-- {
			bit := v >> i & 1
			d := 64e-6
			if (lvl == 1) == (bit == 0) {
				d = 128e-6
			}
			w.add(lvl, d)
			lvl ^= 1
		}
	}
	w.add(0, 600e-6)
}
