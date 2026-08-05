// Package eth100tx is a software GOLDEN MODEL of the 100BASE-TX (IEEE 802.3
// clause 24/25 + ANSI X3.263 TP-PMD) physical-layer coding chain, implemented
// BOTH ways (encode and decode). It is the ORACLE for the in-fabric RTL PHY
// decoder: 100BASE-TX has no libsigrokdecode / no app-suite reference, so — as
// the repo already does for Manchester/SENT/ARINC/MIL-1553 (see
// app/docs/decode-oracle.md, truth_vectors_test.go) — correctness is anchored
// on PUBLISHED sources hardcoded in the tests, and the RTL is held bit-for-bit
// to this model in simulation.
//
// TX chain (this file, EncodeFrame):
//
//	MAC frame bytes  --preamble(0x55x7)+SFD(0xD5)+frame+FCS(CRC-32)-->
//	MII nibble stream (low nibble first)
//	  --4B5B (IEEE 802.3 Table 24-1), first octet replaced by SSD /J/K/,
//	    ESD /T/R/ appended, /I/ IDLE fill both sides-->
//	5-bit code groups  --serialize MSB-first--> 125 Mbit/s NRZ plaintext bits
//	  --stream-cipher scramble (LFSR x^11+x^9+1, keystream k[n]=k[n-9]^k[n-11])-->
//	scrambled bits  --MLT-3 (1=level transition, 0=hold; levels cycle 0,+1,0,-1)-->
//	ternary symbols @125 Mbaud  --oversample 600 MSa/s (4.8 samp/sym)-->
//	sample codes (int amplitudes; +LVL/0/-LVL).
//
// RX chain (this file, DecodeSamples) reverses every stage:
//
//	slice (2 thresholds) -> CDR (transition-run recovery @ ~4.8 samp/sym) ->
//	MLT-3 decode (level change=1) -> descramble (idle-lock the LFSR: idle
//	plaintext is all-ones, so scrambled idle EXPOSES the keystream) ->
//	4B5B align on /J/K/ + decode to nibbles until /T/R/ -> strip preamble/SFD ->
//	MAC frame + FCS verify.
//
// Ordering note (pinned from IEEE 802.3, exercised by the round-trip test):
// scrambling is applied to the 4B5B-encoded NRZ stream on TX and REMOVED from
// the recovered NRZ stream BEFORE 4B5B decode on RX.
package eth100tx

import "fmt"

// ---------------------------------------------------------------------------
// 4B/5B code table — IEEE 802.3 Table 24-1 (a.k.a. ANSI X3.263 FDDI TP-PMD).
// Values reproduced from the published table; the *_test.go file re-asserts
// every entry against the literal bit-strings printed in the standard, so this
// table is not self-referential.
// ---------------------------------------------------------------------------

// Data code groups: 4-bit nibble value -> 5-bit code (bit4..bit0).
var data4b5b = [16]uint8{
	0x1E, // 0x0 -> 11110
	0x09, // 0x1 -> 01001
	0x14, // 0x2 -> 10100
	0x15, // 0x3 -> 10101
	0x0A, // 0x4 -> 01010
	0x0B, // 0x5 -> 01011
	0x0E, // 0x6 -> 01110
	0x0F, // 0x7 -> 01111
	0x12, // 0x8 -> 10010
	0x13, // 0x9 -> 10011
	0x16, // 0xA -> 10110
	0x17, // 0xB -> 10111
	0x1A, // 0xC -> 11010
	0x1B, // 0xD -> 11011
	0x1C, // 0xE -> 11100
	0x1D, // 0xF -> 11101
}

// Control code groups.
const (
	codeI = 0x1F // IDLE      11111
	codeJ = 0x18 // SSD1      11000
	codeK = 0x11 // SSD2      10001
	codeT = 0x0D // ESD1      01101
	codeR = 0x07 // ESD2      00111
	codeH = 0x04 // Halt      00100
	codeQ = 0x00 // Quiet     00000
)

// rev4b5b maps a 5-bit code back to its data nibble (0..15) or a control tag.
var rev4b5b = func() map[uint8]int {
	m := map[uint8]int{}
	for n, c := range data4b5b {
		m[c] = n
	}
	return m
}()

// CodeGroup is one 5-bit symbol on the wire with a human label.
type CodeGroup struct {
	Bits  uint8  // 5-bit value, bit4..bit0
	Label string // "I","J","K","T","R","H","Q", or hex nibble "0".."F"
}

func dataCG(nib int) CodeGroup { return CodeGroup{data4b5b[nib&0xF], fmt.Sprintf("%X", nib&0xF)} }

// ---------------------------------------------------------------------------
// Sample-amplitude model
// ---------------------------------------------------------------------------

// Amplitude levels emitted per MLT-3 ternary symbol. Realistic 3-level TP-PMD
// signalling is nominally +/-1 V about 0; we use +/-1000 (mV) so integer
// sample codes carry the sign directly and slicing thresholds are obvious.
const (
	AmpPos = 1000
	AmpZer = 0
	AmpNeg = -1000

	// OversampleNum/Den = 600 MSa/s / 125 Mbaud = 4.8 samples per symbol.
	OversampleNum = 600
	OversampleDen = 125
)

// ---------------------------------------------------------------------------
// CRC-32 — IEEE 802.3 FCS (== CRC-32/ISO-HDLC: poly 0x04C11DB7 reflected =
// 0xEDB88320, init 0xFFFFFFFF, refin/refout true, xorout 0xFFFFFFFF).
// ---------------------------------------------------------------------------

var crc32Table = func() [256]uint32 {
	var t [256]uint32
	for i := 0; i < 256; i++ {
		c := uint32(i)
		for k := 0; k < 8; k++ {
			if c&1 != 0 {
				c = 0xEDB88320 ^ (c >> 1)
			} else {
				c >>= 1
			}
		}
		t[i] = c
	}
	return t
}()

// crc32Raw is the running-register CRC (no final XOR) — used to check the
// published residue property. crc32Update returns the raw register.
func crc32Raw(data []byte) uint32 {
	c := uint32(0xFFFFFFFF)
	for _, b := range data {
		c = crc32Table[(c^uint32(b))&0xFF] ^ (c >> 8)
	}
	return c
}

// CRC32 is the IEEE 802.3 FCS value of data (the value placed in the FCS field,
// little-endian on the wire per 802.3 §3.2.9).
func CRC32(data []byte) uint32 { return crc32Raw(data) ^ 0xFFFFFFFF }

// fcsBytes returns the 4 FCS octets in on-the-wire order (LSB of the CRC value
// first — 802.3 transmits FCS bit-reversed/LSB-first per octet, but the octet
// values here are the standard little-endian encoding).
func fcsBytes(crc uint32) []byte {
	return []byte{byte(crc), byte(crc >> 8), byte(crc >> 16), byte(crc >> 24)}
}

// ---------------------------------------------------------------------------
// Scrambler — 11-bit LFSR, feedback polynomial x^11 + x^9 + 1 (TP-PMD).
// Output taken from stage 11 gives keystream recurrence k[n]=k[n-9]^k[n-11]
// (derived in eth100tx_test.go). Additive stream cipher: cipher = plain ^ key.
// ---------------------------------------------------------------------------

// keystream generates n keystream bits from an 11-bit seed (seed[0]=k[0] ...
// seed[10]=k[10]). The seed must be non-zero (all-zero is the LFSR lock-up).
func keystream(seed [11]byte, n int) []byte {
	k := make([]byte, n)
	for i := 0; i < 11 && i < n; i++ {
		k[i] = seed[i] & 1
	}
	for i := 11; i < n; i++ {
		k[i] = k[i-9] ^ k[i-11]
	}
	return k
}

// DefaultSeed is an arbitrary non-zero LFSR seed for the encoder. Any non-zero
// seed works; the receiver recovers it by idle-lock, so its value is not
// load-bearing for interop.
var DefaultSeed = [11]byte{1, 0, 1, 1, 0, 0, 1, 0, 1, 1, 1}

// ---------------------------------------------------------------------------
// TX
// ---------------------------------------------------------------------------

// Transmission carries every intermediate stage of one encoded frame so tests
// and the vector dumper can pin the RTL to each boundary bit-for-bit.
type Transmission struct {
	Frame        []byte      // MAC frame (dst..payload), NO preamble/SFD/FCS
	FCS          uint32      // computed IEEE 802.3 FCS value
	MIINibbles   []byte      // preamble+SFD+frame+FCS, low-nibble-first
	CodeGroups   []CodeGroup // idle... J K <data...> T R idle...
	PlainBits    []byte      // 4B5B serial NRZ bits (MSB-first per code group)
	Keystream    []byte      // LFSR keystream aligned to PlainBits
	ScrambledBits []byte     // PlainBits ^ Keystream (= MLT-3 input)
	Symbols      []int8      // MLT-3 ternary symbols (-1/0/+1), one per bit
	Samples      []int       // 600 MSa/s amplitude codes
	SymForSample []int       // symbol index each sample came from (sim aid)
}

// EncodeOpts tunes idle padding.
type EncodeOpts struct {
	LeadIdle  int // idle code groups before SSD (default 32; >= ~3 needed for lock)
	TrailIdle int // idle code groups after ESD (default 8)
	Seed      [11]byte
	haveSeed  bool
}

func (o EncodeOpts) seed() [11]byte {
	if o.haveSeed {
		return o.Seed
	}
	return DefaultSeed
}

// WithSeed sets an explicit LFSR seed.
func (o EncodeOpts) WithSeed(s [11]byte) EncodeOpts { o.Seed = s; o.haveSeed = true; return o }

// bytesToNibbles splits octets low-nibble-first (MII order, 802.3 §22.2.3).
func bytesToNibbles(b []byte) []byte {
	out := make([]byte, 0, len(b)*2)
	for _, x := range b {
		out = append(out, x&0x0F, (x>>4)&0x0F)
	}
	return out
}

// EncodeFrame runs the full TX chain and returns all stages.
func EncodeFrame(frame []byte, opts EncodeOpts) Transmission {
	if opts.LeadIdle == 0 {
		opts.LeadIdle = 32
	}
	if opts.TrailIdle == 0 {
		opts.TrailIdle = 8
	}

	fcs := CRC32(frame)

	// MII stream = 7x preamble + SFD + frame + FCS, split to nibbles.
	mac := make([]byte, 0, 8+len(frame)+4)
	for i := 0; i < 7; i++ {
		mac = append(mac, 0x55)
	}
	mac = append(mac, 0xD5)
	mac = append(mac, frame...)
	mac = append(mac, fcsBytes(fcs)...)
	nibs := bytesToNibbles(mac)

	// Code groups. SSD /J/K/ REPLACES the first preamble octet (its two
	// nibbles 0x5,0x5), so we skip nibs[0:2] and prepend J,K.
	cgs := make([]CodeGroup, 0, opts.LeadIdle+2+len(nibs)-2+2+opts.TrailIdle)
	for i := 0; i < opts.LeadIdle; i++ {
		cgs = append(cgs, CodeGroup{codeI, "I"})
	}
	cgs = append(cgs, CodeGroup{codeJ, "J"}, CodeGroup{codeK, "K"})
	for _, n := range nibs[2:] {
		cgs = append(cgs, dataCG(int(n)))
	}
	cgs = append(cgs, CodeGroup{codeT, "T"}, CodeGroup{codeR, "R"})
	for i := 0; i < opts.TrailIdle; i++ {
		cgs = append(cgs, CodeGroup{codeI, "I"})
	}

	// Serialize MSB-first.
	plain := make([]byte, 0, len(cgs)*5)
	for _, c := range cgs {
		for b := 4; b >= 0; b-- {
			plain = append(plain, (c.Bits>>uint(b))&1)
		}
	}

	// Scramble.
	ks := keystream(opts.seed(), len(plain))
	scr := make([]byte, len(plain))
	for i := range plain {
		scr[i] = plain[i] ^ ks[i]
	}

	// MLT-3 encode.
	syms := mlt3Encode(scr)

	// Oversample to 600 MSa/s.
	samples, srcSym := oversample(syms)

	return Transmission{
		Frame:         frame,
		FCS:           fcs,
		MIINibbles:    nibs,
		CodeGroups:    cgs,
		PlainBits:     plain,
		Keystream:     ks,
		ScrambledBits: scr,
		Symbols:       syms,
		Samples:       samples,
		SymForSample:  srcSym,
	}
}

// mlt3Levels is the cyclic MLT-3 level sequence advanced on every 1-bit.
var mlt3Levels = [4]int8{0, +1, 0, -1}

func mlt3Encode(bits []byte) []int8 {
	out := make([]int8, len(bits))
	idx := 0 // start at level 0
	for i, b := range bits {
		if b == 1 {
			idx = (idx + 1) & 3
		}
		out[i] = mlt3Levels[idx]
	}
	return out
}

// oversample expands each ternary symbol to a run of amplitude codes averaging
// 4.8 samples/symbol via a floor-accumulator (pattern 4,5,5,5,5,...).
func oversample(syms []int8) (samples []int, srcSym []int) {
	amp := func(s int8) int {
		switch {
		case s > 0:
			return AmpPos
		case s < 0:
			return AmpNeg
		default:
			return AmpZer
		}
	}
	prevEdge := 0
	for i, s := range syms {
		endEdge := (i + 1) * OversampleNum / OversampleDen
		n := endEdge - prevEdge
		prevEdge = endEdge
		for j := 0; j < n; j++ {
			samples = append(samples, amp(s))
			srcSym = append(srcSym, i)
		}
	}
	return samples, srcSym
}
