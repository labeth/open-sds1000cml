package decode

// Independent, spec-derived known-answer vectors for the four protocols
// libsigrokdecode cannot oracle (SENT, MIL-STD-1553B, ARINC 429, Manchester).
// Every CRC/parity/encoding value in this file is HARDCODED from a published
// source (cited per vector) or derived step-by-step from the spec's stated
// algorithm — never computed by the decoder's own helpers (sentCRC4,
// mil1553OddParity, arincMakeWord). That breaks the circularity the oracle
// review flagged: the round-trip suites proved decoder-consistency, not
// spec-conformance. The waveform GENERATORS from the round-trip tests are
// reused (their wire encodings were themselves verified against the cited
// spec structure), but always fed literal field/check values.
//
// Sources (all values independently re-derived before hardcoding):
//   SENT      Allegro AN296177 Tables 18/28; Microchip DS70005145B Ex. 4-1;
//             Melexis SENTAnalyzer SENTSimulationDataGenerator.cpp.
//   1553      MIL-STD-1553B ¶4.3.x as reproduced in DDC's Designer's Guide;
//             Alta Data tutorial p.8 ("01 R 04 10" = 0x0890); the standard
//             command/status example words (RT3 T SA1 WC1 = 0x1C21; status
//             0x1800). Parity bits derived from the quoted odd rule.
//   ARINC 429 AIM ARINC 429 tutorial (word layout, label reversal);
//             GE/Condor ARINC Protocol Tutorial Fig. 7 (which as printed
//             VIOLATES its own odd-parity rule — used here as the
//             parity-error vector, plus a one-bit-corrected valid variant).
//   Manchester Atmel app note 9164 "Manchester Coding Basics" (byte 0xC5,
//             IEEE 802.3 mapping: 1 = low->high); G.E. Thomas is the inverse.

import (
	"fmt"
	"testing"
)

func TestTruthSENT(t *testing.T) {
	const tick = 20 // samples per SENT tick
	// Allegro AN296177 Table 18: six-data-nibble sequences with the published
	// RECOMMENDED (J2716-2010 §5.4.2.2, zero-augmented) CRC-4. legacyCRC is
	// the 2008 §5.4.2.1 value (no augmentation), derived independently — the
	// recommended-only decoder must REJECT frames sealed with it.
	vectors := []struct {
		data      []int
		crc       int // published recommended CRC-4
		legacyCRC int // derived legacy CRC-4 — must NOT validate
	}{
		{[]int{0x5, 0x3, 0xE, 0x5, 0x3, 0xE}, 0xF, 0xC},
		{[]int{0x7, 0x4, 0x8, 0x7, 0x4, 0x8}, 0x3, 0x5},
		{[]int{0x4, 0xA, 0xC, 0x4, 0xA, 0xC}, 0xA, 0x3},
		{[]int{0x7, 0x8, 0xF, 0x7, 0x8, 0xF}, 0x5, 0xF},
		{[]int{0x9, 0x1, 0xD, 0x9, 0x1, 0xD}, 0x6, 0xA},
		{[]int{0x0, 0x0, 0x0, 0x0, 0x0, 0x0}, 0x5, 0xF},
	}
	for _, v := range vectors {
		v := v
		t.Run(fmt.Sprintf("allegro-%X-crc-%X", v.data, v.crc), func(t *testing.T) {
			// Status nibble deliberately non-zero: J2716 covers the DATA
			// nibbles only (Melexis MLX90324 p.29, Microchip §3.0) — a
			// decoder that folded status into the CRC would reject this.
			nibs := append(append([]int{0x3}, v.data...), v.crc)
			r := DecodeSENT(sentWave([][]int{nibs}, tick, 0, 0), 1e-6, SENTCfg{Nibbles: 8})
			if !r.OK {
				t.Fatalf("decode failed on a published-CRC frame: %s", r.Error)
			}
			if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", nibs) {
				t.Fatalf("nibbles: got %v want %v", r.Bytes, nibs)
			}
			last := r.Spans[len(r.Spans)-1]
			if last.Kind != "crc" || last.Val != v.crc {
				t.Fatalf("CRC span: kind %q val %X, want crc %X", last.Kind, last.Val, v.crc)
			}
			if n := countSpans(r, "frame-error"); n != 0 {
				t.Fatalf("%d frame-errors on a published-valid frame", n)
			}

			// The LEGACY-sealed frame must be flagged: sentCRC4 implements
			// only the recommended variant (a legacy value validating too
			// would mean the augmentation step was silently dropped).
			bad := append(append([]int{0x3}, v.data...), v.legacyCRC)
			rb := DecodeSENT(sentWave([][]int{bad}, tick, 0, 0), 1e-6, SENTCfg{Nibbles: 8})
			if rb.OK || countSpans(rb, "frame-error") != 1 {
				t.Fatalf("legacy-CRC frame not rejected: ok=%v errors=%d", rb.OK, countSpans(rb, "frame-error"))
			}
		})
	}

	t.Run("melexis-simulator-frame", func(t *testing.T) {
		// Melexis SENTAnalyzer's vendor-authored simulator: pulses
		// {27,17,22,14,20,12} ticks = data nibbles {F,5,A,2,8,0}, CRC pulse
		// 21 ticks = CRC 9, status 0, with a pause pulse. Confirms both the
		// CRC and the 12+value tick encoding against vendor tooling.
		nibs := []int{0x0, 0xF, 0x5, 0xA, 0x2, 0x8, 0x0, 0x9}
		w := sentWave([][]int{nibs}, tick, 90, 0)
		r := DecodeSENT(w, 1e-6, SENTCfg{Nibbles: 8, PausePulse: true})
		if !r.OK {
			t.Fatalf("decode failed: %s", r.Error)
		}
		if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", nibs) {
			t.Fatalf("nibbles: got %v want %v", r.Bytes, nibs)
		}
	})

	t.Run("allegro-prefix-vectors", func(t *testing.T) {
		// AN296177 Table 28: running CRCs of the prefixes of {5,D,5,9,0,A}.
		// Exercises data lengths 1..6 via SENTCfg.Nibbles (status+data+crc).
		prefixes := []struct {
			data []int
			crc  int
		}{
			{[]int{0x5}, 0x9},
			{[]int{0x5, 0xD}, 0xE},
			{[]int{0x5, 0xD, 0x5}, 0xB},
			{[]int{0x5, 0xD, 0x5, 0x9}, 0x7},
			{[]int{0x5, 0xD, 0x5, 0x9, 0x0}, 0x4},
			{[]int{0x5, 0xD, 0x5, 0x9, 0x0, 0xA}, 0x8},
		}
		for _, p := range prefixes {
			nibs := append(append([]int{0x1}, p.data...), p.crc)
			r := DecodeSENT(sentWave([][]int{nibs}, tick, 0, 0), 1e-6, SENTCfg{Nibbles: len(nibs)})
			if !r.OK || countSpans(r, "frame-error") != 0 {
				t.Fatalf("prefix %X (crc %X): ok=%v errors=%d err=%s", p.data, p.crc, r.OK, countSpans(r, "frame-error"), r.Error)
			}
		}
	})
}

func TestTruthMIL1553(t *testing.T) {
	const spb = 40
	colTimeS := 1.0 / (float64(spb) * 1e6)
	// Published words; parity bits derived in one step from the quoted odd
	// rule (¶4.3.3.5.1.6: ones(word)+parity must be odd):
	//   0x0890 (Alta p.8: RT1 R SA4 WC16)  popcount 3 -> parity 0
	//   0x1C21 (RT3 T SA1 WC1 command)     popcount 5 -> parity 0
	//   0x1800 (RT3 status, flags clear)   popcount 2 -> parity 1
	words := []int{0x0890, 0x1C21, 0x1800}
	parity := []int{0, 0, 1}
	cmd := []bool{true, true, true} // status words share the command sync (¶4.3.3.5.2.1)

	w := mil1553Wave(words, cmd, parity, spb)
	r := DecodeMIL1553(w, colTimeS, MIL1553Cfg{Bitrate: 1_000_000})
	if !r.OK {
		t.Fatalf("decode failed on published words: %s", r.Error)
	}
	if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", words) {
		t.Fatalf("words: got %04X want %04X", r.Bytes, words)
	}
	if n := countSpans(r, "frame-error"); n != 0 {
		t.Fatalf("%d parity errors on derived-parity words", n)
	}
	csyncs := 0
	for _, sp := range r.Spans {
		if sp.Kind == "start" && sp.Text == "csync" {
			csyncs++
		}
	}
	if csyncs != 3 {
		t.Fatalf("command syncs: %d, want 3", csyncs)
	}

	// One flipped parity bit -> exactly one flagged word, the others clean.
	bad := []int{0, 1, 1} // word 1's parity now EVEN-summed: must flag
	rb := DecodeMIL1553(mil1553Wave(words, cmd, bad, spb), colTimeS, MIL1553Cfg{Bitrate: 1_000_000})
	if n := countSpans(rb, "frame-error"); n != 1 {
		t.Fatalf("flipped parity: %d frame-errors, want exactly 1", n)
	}

	// A data word carries the INVERSE sync (¶4.3.3.5.1.1): re-encode 0x0890
	// as a data word and require a dsync span.
	rd := DecodeMIL1553(mil1553Wave([]int{0x0890}, []bool{false}, []int{0}, spb), colTimeS, MIL1553Cfg{Bitrate: 1_000_000})
	if !rd.OK || len(rd.Spans) == 0 || rd.Spans[0].Text != "dsync" {
		t.Fatalf("data-sync word: ok=%v first span %+v, want dsync", rd.OK, rd.Spans)
	}
}

func TestTruthARINC429(t *testing.T) {
	const spb = 40
	colTimeS := 2.5e-7 // 100 kbit/s at spb=40
	// Build the wire bits DIRECTLY from the published 32-bit word (bit 1 =
	// LSB = first on the wire) — never via arincMakeWord, whose parity
	// computation is the circularity under test.
	wire := func(word uint32) []int {
		bits := make([]int, 32)
		for i := 0; i < 32; i++ {
			bits[i] = int(word>>i) & 1
		}
		return bits
	}
	run := func(word uint32) Result {
		var w []uint8
		arincIdle(&w, spb*6)
		arincAppendWord(&w, wire(word), spb)
		arincIdle(&w, spb*6)
		return DecodeARINC429(w, colTimeS, ARINC429Cfg{Bitrate: 100000})
	}
	field := func(r Result, kind string) string {
		for _, sp := range r.Spans {
			if sp.Kind == kind {
				return sp.Text
			}
		}
		return ""
	}

	t.Run("wikipedia-label-260-date-word", func(t *testing.T) {
		// Bit-exact from the published table: parity 1, SSM 00, data
		// 1000110001100010001b, SDI 00, label bits 00001101 (label 260o).
		// popcount 11 (odd) -> parity-valid.
		const word = 0x918C440D
		r := run(word)
		if !r.OK {
			t.Fatalf("decode failed: %s", r.Error)
		}
		if got := fmt.Sprintf("%v", r.Bytes); got != fmt.Sprintf("%v", []int{0x0D, 0x44, 0x8C, 0x91}) {
			t.Fatalf("bytes: got %02X", r.Bytes)
		}
		if l := field(r, "addr"); l != "260" {
			t.Fatalf("label %q, want 260 (octal, wire-reversed)", l)
		}
		if d := field(r, "data"); d != "46311" {
			t.Fatalf("data %q, want 46311 (hex of bits 29..11)", d)
		}
		if s := field(r, "rw"); s != "SSM0" {
			t.Fatalf("ssm %q, want SSM0", s)
		}
		if n := countSpans(r, "frame-error"); n != 0 {
			t.Fatalf("%d frame-errors on a parity-valid published word", n)
		}
	})

	t.Run("ge-tutorial-fig7-parity-misprint", func(t *testing.T) {
		// GE/Condor Fig. 7 (label 103o, 268 kt BNR) as PRINTED has even
		// popcount — violating the odd-parity rule stated on its own p.7.
		// The fields must still decode; the parity must be flagged.
		const word = 0x686000C2
		r := run(word)
		if l := field(r, "addr"); l != "103" {
			t.Fatalf("label %q, want 103", l)
		}
		if d := field(r, "data"); d != "21800" {
			t.Fatalf("data %q, want 21800", d)
		}
		if s := field(r, "rw"); s != "SSM3" {
			t.Fatalf("ssm %q, want SSM3", s)
		}
		if n := countSpans(r, "frame-error"); n != 1 {
			t.Fatalf("misprinted parity not flagged: %d frame-errors", n)
		}
	})

	t.Run("ge-tutorial-fig7-corrected", func(t *testing.T) {
		// Same word with bit 32 corrected per the tutorial's own rule.
		const word = 0xE86000C2
		r := run(word)
		if !r.OK || countSpans(r, "frame-error") != 0 {
			t.Fatalf("corrected word: ok=%v errors=%d", r.OK, countSpans(r, "frame-error"))
		}
		if l, s := field(r, "addr"), field(r, "rw"); l != "103" || s != "SSM3" {
			t.Fatalf("label/ssm %q/%q, want 103/SSM3", l, s)
		}
	})
}

func TestTruthManchester(t *testing.T) {
	const spb = 24
	colTimeS := 1e-6
	bitrate := int(1.0 / (float64(spb) * colTimeS))
	// Atmel app note 9164: byte 0xC5, MSB first, IEEE 802.3 mapping — a 1 is
	// low-then-high (rising mid-bit). Under G.E. Thomas (the inverse
	// convention, and MIL-STD-1553's ¶4.3.3.2 mapping) the SAME waveform
	// reads as the bitwise complement, 0x3A.
	w := manchesterWave(mBits([]int{0xC5}, true, 8), true, spb)
	r := DecodeManchester(w, colTimeS, ManchesterCfg{Bitrate: bitrate, IEEE: true, MSB: true})
	if !r.OK || len(r.Bytes) != 1 || r.Bytes[0] != 0xC5 {
		t.Fatalf("IEEE decode: ok=%v bytes=%X, want [C5]", r.OK, r.Bytes)
	}
	rt := DecodeManchester(w, colTimeS, ManchesterCfg{Bitrate: bitrate, IEEE: false, MSB: true})
	if !rt.OK || len(rt.Bytes) != 1 || rt.Bytes[0] != 0x3A {
		t.Fatalf("Thomas decode of the IEEE wave: ok=%v bytes=%X, want [3A]", rt.OK, rt.Bytes)
	}
}
