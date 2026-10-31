// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import "unsafe"

// bitWords spreads a byte's bits into eight samples, bit 0 first, as two
// little-endian words: 0 for a low bit, 255 for a high one.
var bitWords = func() (t [256][2]uint32) {
	for b := range t {
		for k := range 8 {
			if b>>k&1 != 0 {
				t[b][k/4] |= 0xff << (8 * (k % 4))
			}
		}
	}
	return t
}()

// unpackBits appends digital words' (stream_bits.v: {CH2 bits 15..0, CH1
// bits 15..0}, bit 0 the earliest period, little-endian on the wire) sixteen
// samples a word of line ch (0 CH1, 1 CH2) to dst, 0 or 255 each, so the
// decoders read them as they read sliced 8-bit samples. Word stores: the ARM
// compiler writes binary.LittleEndian's a byte at a time.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func unpackBits(dst []uint8, raw []byte, ch int) []uint8 {
	n := len(raw) / 4
	at := len(dst)
	if cap(dst)-at < 16*n {
		dst = append(dst[:cap(dst)], make([]uint8, at+16*n-cap(dst))...)
	}
	dst = dst[:at+16*n]
	if n == 0 {
		return dst
	}
	o := unsafe.Pointer(&dst[at])
	src := unsafe.Pointer(&raw[2*ch])
	for i := range n {
		lo := &bitWords[*(*uint8)(unsafe.Add(src, 4*i))]
		hi := &bitWords[*(*uint8)(unsafe.Add(src, 4*i+1))]
		w := (*[4]uint32)(unsafe.Add(o, 16*i))
		w[0], w[1], w[2], w[3] = lo[0], lo[1], hi[0], hi[1]
	}
	return dst
}
