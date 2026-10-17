// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Generators of the biphase protocols (proto_gen_test.go).

// daliFrame appends a DALI frame of nbits (8, 16 or 24) of v, most
// significant first, on a bus idling high, then idle.
// TRLC-LINKS: REQ-SDS-018
func daliFrame(w *timeline, nbits, v int, idleS float64) {
	const h = 1 / 2400.0
	bit := func(b int) {
		if b == 1 {
			w.add(0, h)
			w.add(1, h)
		} else {
			w.add(1, h)
			w.add(0, h)
		}
	}
	bit(1)
	for k := nbits - 1; k >= 0; k-- {
		bit(v >> k & 1)
	}
	w.add(1, idleS)
}

// pdEnc5 is the 4b5b line symbol of a nibble (pd4b5b's inverse) and the
// K-codes: 16 Sync-1, 17 Sync-2, 18 Sync-3, 19 RST-1, 20 RST-2, 21 EOP.
var pdEnc5 = [22]int{0x1E, 0x09, 0x14, 0x15, 0x0A, 0x0B, 0x0E, 0x0F, 0x12, 0x13, 0x16, 0x17, 0x1A, 0x1B, 0x1C, 0x1D,
	0x18, 0x11, 0x06, 0x07, 0x19, 0x0D}

// bmcLine writes biphase-mark bits: a transition at every bit's start and
// another mid-bit for a 1.
type bmcLine struct {
	w   *timeline
	lvl byte
	bit float64
}

// TRLC-LINKS: REQ-SDS-018
func (b *bmcLine) put(v int) {
	b.lvl ^= 1
	if v == 1 {
		b.w.add(b.lvl, b.bit/2)
		b.lvl ^= 1
		b.w.add(b.lvl, b.bit/2)
		return
	}
	b.w.add(b.lvl, b.bit)
}

// TRLC-LINKS: REQ-SDS-018
func (b *bmcLine) sym(s int) {
	for k := 0; k < 5; k++ {
		b.put(pdEnc5[s] >> k & 1)
	}
}

// pdMessage appends a USB PD message at 300 kbit/s: preamble, the ordered
// set (four K-codes of pdEnc5), the header and data objects with their
// CRC-32 (spoiled when badCRC), EOP, then idle.
// TRLC-LINKS: REQ-SDS-018
func pdMessage(w *timeline, sop [4]int, header int, objs []uint32, badCRC bool) {
	b := &bmcLine{w: w, bit: 1 / 300e3}
	for i := 0; i < 64; i++ {
		b.put(i & 1)
	}
	for _, k := range sop {
		b.sym(k)
	}
	bytes := []int{header & 0xff, header >> 8}
	for _, o := range objs {
		bytes = append(bytes, int(o&0xff), int(o>>8&0xff), int(o>>16&0xff), int(o>>24))
	}
	crc := crc32IEEE(bytes)
	if badCRC {
		crc ^= 0x10
	}
	bytes = append(bytes, int(crc&0xff), int(crc>>8&0xff), int(crc>>16&0xff), int(crc>>24))
	for _, v := range bytes {
		b.sym(v & 0xf)
		b.sym(v >> 4)
	}
	b.sym(21)
	b.lvl ^= 1
	w.add(b.lvl, 1/300e3) // the closing transition, then the line rests
	w.add(b.lvl, 40e-6)
}

// pdSOP and pdHardReset are ordered sets in pdEnc5's K-code numbers.
var (
	pdSOP       = [4]int{16, 16, 16, 17}
	pdSOPPrime  = [4]int{16, 16, 18, 18}
	pdHardReset = [4]int{19, 19, 19, 20}
)

// spdifStream appends S/PDIF frames (two subframes each) of the samples
// (pairs: channel A, channel B) at fs, the first frame opening a block.
// TRLC-LINKS: REQ-SDS-018
func spdifStream(w *timeline, fs float64, frame0 int, samples [][2]int) {
	cell := 1 / (64 * fs)
	lvl := byte(0)
	run := func(halves int) {
		lvl ^= 1
		w.add(lvl, float64(halves)*cell/2)
	}
	for f, s := range samples {
		for ch := 0; ch < 2; ch++ {
			switch {
			case ch == 1:
				run(3)
				run(2)
				run(1)
				run(2)
			case (frame0+f)%192 == 0:
				run(3)
				run(1)
				run(1)
				run(3)
			default:
				run(3)
				run(3)
				run(1)
				run(1)
			}
			v := s[ch]
			ones := 0
			for k := 0; k < 28; k++ {
				b := 0
				switch {
				case k < 24:
					b = v >> k & 1
				case k == 27:
					b = ones & 1
				}
				ones += b
				if b == 1 {
					run(1)
					run(1)
				} else {
					run(2)
				}
			}
		}
	}
}
