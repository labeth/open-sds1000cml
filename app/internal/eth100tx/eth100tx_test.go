package eth100tx

import (
	"fmt"
	"testing"
)

// ============================================================================
// PUBLISHED-SOURCE ANCHORS (the non-self-referential truth). Same discipline
// as internal/decode/truth_vectors_test.go: every value below is hardcoded
// from a citable standard/reference, NOT produced by this package's helpers.
// ============================================================================

// TestPublished4B5BTable pins every code group against the literal bit-strings
// printed in IEEE 802.3 Table 24-1 (identical to ANSI X3.263 FDDI TP-PMD).
// The strings here were transcribed from the standard, then compared to the
// package table — so the table cannot be "correct by construction".
func TestPublished4B5BTable(t *testing.T) {
	// nibble -> 5-char code string, straight from IEEE 802.3 Table 24-1.
	dataStr := [16]string{
		"11110", "01001", "10100", "10101",
		"01010", "01011", "01110", "01111",
		"10010", "10011", "10110", "10111",
		"11010", "11011", "11100", "11101",
	}
	for n := 0; n < 16; n++ {
		want := parseBits5(dataStr[n])
		if data4b5b[n] != want {
			t.Errorf("data 0x%X: table=%05b published=%s", n, data4b5b[n], dataStr[n])
		}
	}
	ctrl := []struct {
		name string
		got  uint8
		str  string
	}{
		{"I", codeI, "11111"}, {"J", codeJ, "11000"}, {"K", codeK, "10001"},
		{"T", codeT, "01101"}, {"R", codeR, "00111"}, {"H", codeH, "00100"},
		{"Q", codeQ, "00000"},
	}
	for _, c := range ctrl {
		if c.got != parseBits5(c.str) {
			t.Errorf("control %s: table=%05b published=%s", c.name, c.got, c.str)
		}
	}
}

func parseBits5(s string) uint8 {
	var v uint8
	for _, ch := range s {
		v <<= 1
		if ch == '1' {
			v |= 1
		}
	}
	return v
}

// TestPublishedCRC32Check anchors the FCS algorithm on the canonical CRC-32
// check value: the ASCII string "123456789" has CRC-32/ISO-HDLC (== IEEE 802.3
// FCS) = 0xCBF43926. This is THE published check value in the CRC RevEng
// catalogue / Rocksoft model for this exact CRC. Independent of any frame.
func TestPublishedCRC32Check(t *testing.T) {
	const want = 0xCBF43926
	got := CRC32([]byte("123456789"))
	if got != want {
		t.Fatalf("CRC-32 check value: got 0x%08X want 0x%08X", got, want)
	}
}

// TestPublishedCRC32Residue anchors FCS *append/verify* on the published
// residue: for CRC-32/ISO-HDLC (== IEEE 802.3 FCS), computing the CRC over a
// message with its correct FCS appended yields the catalogue residue
// 0x2144DF1C (CRC RevEng catalogue, CRC-32/ISO-HDLC "residue"). We build a
// message, append CRC32, and require the residue — proving both the value and
// the append convention without trusting the round-trip alone.
func TestPublishedCRC32Residue(t *testing.T) {
	const residue = 0x2144DF1C
	msg := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x11, 0x22, 0x33}
	full := append(append([]byte{}, msg...), fcsBytes(CRC32(msg))...)
	if got := CRC32(full); got != residue {
		t.Fatalf("CRC-32 residue: got 0x%08X want 0x%08X", got, residue)
	}
}

// TestScramblerRecurrenceDerivation documents+checks that output-from-stage-11
// of the x^11+x^9+1 LFSR obeys k[n]=k[n-9]^k[n-11], and that the polynomial is
// primitive (maximal period 2^11-1 = 2047). Both are properties of the
// published TP-PMD scrambler polynomial.
func TestScramblerRecurrenceDerivation(t *testing.T) {
	// Simulate the 11-stage shift register X[1..11], output = X[11], feedback
	// X'[1] = X[9]^X[11]. Collect the output sequence directly, independent of
	// the keystream() recurrence, then require they match.
	var x [12]byte // 1-indexed
	x[1], x[3], x[6], x[9], x[11] = 1, 1, 1, 1, 1
	N := 5000
	direct := make([]byte, N)
	for n := 0; n < N; n++ {
		direct[n] = x[11]
		fb := x[9] ^ x[11]
		for i := 11; i >= 2; i-- {
			x[i] = x[i-1]
		}
		x[1] = fb
	}
	// keystream() must reproduce direct[] when seeded with its first 11 bits.
	var seed [11]byte
	copy(seed[:], direct[:11])
	k := keystream(seed, N)
	for n := 0; n < N; n++ {
		if k[n] != direct[n] {
			t.Fatalf("recurrence mismatch at n=%d: keystream=%d shiftreg=%d", n, k[n], direct[n])
		}
	}
	// Maximal period: 2047, and no shorter.
	period := 0
	for p := 1; p <= 2047; p++ {
		match := true
		for n := 0; n < 2047; n++ {
			if direct[n] != direct[n+p] {
				match = false
				break
			}
		}
		if match {
			period = p
			break
		}
	}
	if period != 2047 {
		t.Fatalf("LFSR period = %d, want 2047 (poly not primitive?)", period)
	}
}

// TestWorkedPreambleCodeGroups is a fully-worked {bytes -> code groups} vector
// derived by hand from IEEE 802.3 Table 24-1: the 8-octet preamble+SFD
// (0x55 x7, 0xD5) with the first octet replaced by SSD /J/K/. Every code group
// below was written out by applying the published table to each nibble
// (low-nibble-first), NOT by running the encoder.
func TestWorkedPreambleCodeGroups(t *testing.T) {
	// 0x55 -> nibbles 5,5 ; first octet -> /J//K/. Remaining 0x55 -> 5,5 ->
	// 01011 01011. SFD 0xD5 -> nibbles 5,D -> 01011 11011.
	want := []string{
		"J", "K", // octet0 0x55
		"5", "5", // octet1 0x55
		"5", "5", // octet2
		"5", "5", // octet3
		"5", "5", // octet4
		"5", "5", // octet5
		"5", "5", // octet6
		"5", "D", // SFD 0xD5
	}
	tx := EncodeFrame([]byte{0x00}, EncodeOpts{LeadIdle: 4, TrailIdle: 2})
	// Locate first J.
	start := -1
	for i, c := range tx.CodeGroups {
		if c.Label == "J" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("no J in code groups")
	}
	for i, w := range want {
		got := tx.CodeGroups[start+i]
		if got.Label != w {
			t.Fatalf("code group %d: got %s want %s", i, got.Label, w)
		}
		// And its bits must equal the published table value.
		if !bitsMatchLabel(got) {
			t.Fatalf("code group %d label %s bits 0x%02X inconsistent with table", i, got.Label, got.Bits)
		}
	}
}

func bitsMatchLabel(c CodeGroup) bool {
	switch c.Label {
	case "J":
		return c.Bits == codeJ
	case "K":
		return c.Bits == codeK
	case "I":
		return c.Bits == codeI
	case "T":
		return c.Bits == codeT
	case "R":
		return c.Bits == codeR
	}
	var n int
	fmt.Sscanf(c.Label, "%X", &n)
	return c.Bits == data4b5b[n&0xF]
}

// ============================================================================
// ROUND-TRIP: encode -> decode recovers the frame and verifies FCS. This
// exercises slice/CDR/MLT-3/descramble(idle-lock)/4B5B-align/frame/FCS on the
// synthetic 600 MSa/s stream.
// ============================================================================

func testFrames() map[string][]byte {
	return map[string][]byte{
		// Minimal DA/SA/EtherType + short payload.
		"arp-like": {
			0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, // dst broadcast
			0x00, 0x1A, 0x2B, 0x3C, 0x4D, 0x5E, // src
			0x08, 0x06, // EtherType ARP
			0x00, 0x01, 0x08, 0x00, 0x06, 0x04, 0x00, 0x01,
		},
		"ip-icmp": {
			0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
			0x66, 0x77, 0x88, 0x99, 0xAA, 0xBB,
			0x08, 0x00, // IPv4
			0x45, 0x00, 0x00, 0x1C, 0xAB, 0xCD, 0x00, 0x00,
			0x40, 0x01, 0x00, 0x00, 0x0A, 0x00, 0x00, 0x01,
		},
		"all-zero-payload": append([]byte{
			0x01, 0x00, 0x5E, 0x00, 0x00, 0x01,
			0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE,
			0x88, 0xB5,
		}, make([]byte, 20)...),
		"stress-55-AA": {
			0x55, 0xAA, 0x55, 0xAA, 0x55, 0xAA,
			0xAA, 0x55, 0xAA, 0x55, 0xAA, 0x55,
			0xFF, 0xFF,
			0x55, 0x55, 0xAA, 0xAA, 0x00, 0xFF, 0xFF, 0x00,
		},
	}
}

func TestRoundTrip(t *testing.T) {
	for name, frame := range testFrames() {
		frame := frame
		t.Run(name, func(t *testing.T) {
			tx := EncodeFrame(frame, EncodeOpts{})
			rx := DecodeSamples(tx.Samples)
			if rx.Err != nil {
				t.Fatalf("decode error: %v", rx.Err)
			}
			if !rx.FCSOK {
				t.Fatalf("FCS not OK (residue check failed)")
			}
			if len(rx.Frame) != len(frame) {
				t.Fatalf("frame len got %d want %d", len(rx.Frame), len(frame))
			}
			for i := range frame {
				if rx.Frame[i] != frame[i] {
					t.Fatalf("byte %d: got 0x%02X want 0x%02X", i, rx.Frame[i], frame[i])
				}
			}
			if rx.FCS != tx.FCS {
				t.Fatalf("recovered FCS 0x%08X != tx FCS 0x%08X", rx.FCS, tx.FCS)
			}
		})
	}
}

// TestStageBoundariesMatch checks that the RX-recovered intermediate stages
// equal the TX-side stages bit-for-bit — these ARE the signal definitions the
// RTL must reproduce.
func TestStageBoundariesMatch(t *testing.T) {
	frame := testFrames()["arp-like"]
	tx := EncodeFrame(frame, EncodeOpts{})
	rx := DecodeSamples(tx.Samples)
	if rx.Err != nil {
		t.Fatalf("decode error: %v", rx.Err)
	}
	// Recovered symbols must equal TX symbols.
	if len(rx.Symbols) != len(tx.Symbols) {
		t.Fatalf("symbol count rx=%d tx=%d", len(rx.Symbols), len(tx.Symbols))
	}
	for i := range tx.Symbols {
		if rx.Symbols[i] != tx.Symbols[i] {
			t.Fatalf("symbol %d rx=%d tx=%d", i, rx.Symbols[i], tx.Symbols[i])
		}
	}
	// Recovered scrambled bits must equal TX scrambled bits.
	for i := range tx.ScrambledBits {
		if rx.ScrambledBits[i] != tx.ScrambledBits[i] {
			t.Fatalf("scrambled bit %d rx=%d tx=%d", i, rx.ScrambledBits[i], tx.ScrambledBits[i])
		}
	}
	// Descrambled plaintext from the lock offset onward must equal TX plaintext.
	for i := rx.LockOffset; i < len(tx.PlainBits); i++ {
		if rx.PlainBits[i] != tx.PlainBits[i] {
			t.Fatalf("plain bit %d rx=%d tx=%d", i, rx.PlainBits[i], tx.PlainBits[i])
		}
	}
}

// TestIdleLock proves the descrambler recovers the exact keystream by idle
// observation, matching the encoder's keystream from the lock point on.
func TestIdleLock(t *testing.T) {
	tx := EncodeFrame(testFrames()["ip-icmp"], EncodeOpts{})
	rx := DecodeSamples(tx.Samples)
	if rx.Err != nil {
		t.Fatalf("decode error: %v", rx.Err)
	}
	if rx.LockOffset != 0 {
		t.Logf("descrambler locked at bit offset %d", rx.LockOffset)
	}
	// Reconstruct RX keystream from recovered plain vs scrambled, compare to TX.
	for i := rx.LockOffset; i < len(tx.Keystream); i++ {
		rxKey := rx.ScrambledBits[i] ^ rx.PlainBits[i]
		if rxKey != tx.Keystream[i] {
			t.Fatalf("keystream %d rx=%d tx=%d", i, rxKey, tx.Keystream[i])
		}
	}
}

// TestFCSCorruptionDetected: flipping a payload byte must break the residue.
func TestFCSCorruptionDetected(t *testing.T) {
	frame := append([]byte{}, testFrames()["arp-like"]...)
	// A structurally valid PHY stream carrying a one-bit-wrong FCS field: the
	// PHY layers decode cleanly, but the residue check must reject it.
	bad := encodeWithWrongFCS(frame)
	rx := DecodeSamples(bad)
	if rx.Err != nil {
		t.Fatalf("PHY decode should succeed on a well-formed stream, got %v", rx.Err)
	}
	if rx.FCSOK {
		t.Fatalf("corrupted-FCS frame reported FCSOK=true (residue check failed to catch it)")
	}
	// And the good stream must still pass, proving the check isn't stuck-false.
	if !DecodeSamples(EncodeFrame(frame, EncodeOpts{}).Samples).FCSOK {
		t.Fatalf("good frame reported FCSOK=false")
	}
}

// encodeWithWrongFCS builds a valid PHY stream but with a deliberately wrong
// FCS field (flip one FCS bit) so the receiver's residue check must fail.
func encodeWithWrongFCS(frame []byte) []int {
	fcs := CRC32(frame) ^ 0x00000001 // one-bit-wrong FCS
	mac := make([]byte, 0, 8+len(frame)+4)
	for i := 0; i < 7; i++ {
		mac = append(mac, 0x55)
	}
	mac = append(mac, 0xD5)
	mac = append(mac, frame...)
	mac = append(mac, fcsBytes(fcs)...)
	nibs := bytesToNibbles(mac)
	var cgs []CodeGroup
	for i := 0; i < 32; i++ {
		cgs = append(cgs, CodeGroup{codeI, "I"})
	}
	cgs = append(cgs, CodeGroup{codeJ, "J"}, CodeGroup{codeK, "K"})
	for _, n := range nibs[2:] {
		cgs = append(cgs, dataCG(int(n)))
	}
	cgs = append(cgs, CodeGroup{codeT, "T"}, CodeGroup{codeR, "R"})
	for i := 0; i < 8; i++ {
		cgs = append(cgs, CodeGroup{codeI, "I"})
	}
	var plain []byte
	for _, c := range cgs {
		for b := 4; b >= 0; b-- {
			plain = append(plain, (c.Bits>>uint(b))&1)
		}
	}
	ks := keystream(DefaultSeed, len(plain))
	scr := make([]byte, len(plain))
	for i := range plain {
		scr[i] = plain[i] ^ ks[i]
	}
	samples, _ := oversample(mlt3Encode(scr))
	return samples
}
