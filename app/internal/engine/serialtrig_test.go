package engine

import (
	"testing"

	"open-sds/app/internal/decode"
)

// ---- synthetic protocol waveforms (mirrors internal/decode/decode_test.go) ---

func uartWave(bytes []int, spb int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	var w []uint8
	push := func(bit, k int) {
		v := lo
		if bit == 1 {
			v = hi
		}
		for j := 0; j < k; j++ {
			w = append(w, v)
		}
	}
	push(1, spb*8)
	for _, b := range bytes {
		push(0, spb) // start
		for c := 0; c < 8; c++ {
			push((b>>c)&1, spb) // LSB first
		}
		push(1, spb) // stop
		push(1, spb) // idle
	}
	push(1, spb*8)
	return w
}

func spiWave(bytes []int, h int) (clk, data []uint8) {
	lo, hi := uint8(40), uint8(210)
	seg := func(c, d uint8, n int) {
		for i := 0; i < n; i++ {
			clk = append(clk, c)
			data = append(data, d)
		}
	}
	seg(lo, lo, h*20)
	for _, b := range bytes {
		for k := 7; k >= 0; k-- { // MSB first
			bit := lo
			if (b>>k)&1 == 1 {
				bit = hi
			}
			seg(lo, bit, h)
			seg(hi, bit, h)
		}
	}
	seg(lo, lo, h*20)
	return clk, data
}

func i2cWave(addr7, rw int, data []int, h int) (scl, sda []uint8) {
	lo, hi := uint8(40), uint8(210)
	seg := func(c, d uint8, n int) {
		for i := 0; i < n; i++ {
			scl = append(scl, c)
			sda = append(sda, d)
		}
	}
	pushByte := func(v int) {
		for k := 7; k >= 0; k-- {
			b := lo
			if (v>>k)&1 == 1 {
				b = hi
			}
			seg(lo, b, h)
			seg(hi, b, h)
		}
		seg(lo, lo, h) // ACK
		seg(hi, lo, h)
	}
	seg(hi, hi, h*4)
	seg(hi, lo, h) // START
	pushByte(addr7<<1 | (rw & 1))
	for _, d := range data {
		pushByte(d)
	}
	seg(lo, lo, h)
	seg(hi, lo, h/2)
	seg(hi, hi, h*2) // STOP
	seg(hi, hi, h*4)
	return scl, sda
}

// ---- serialQualify: full decode + match on a synthetic frame -----------------

func TestSerialQualifyUART(t *testing.T) {
	w := uartWave([]int{0x11, 0x22, 0x55, 0x33}, 40)
	f := &Frame{C1: w, C2: make([]uint8, len(w)), Valid: len(w), SampleS: 1e-6}
	e := &Engine{}

	// byte present → match, anchored inside the record.
	e.SetSerialParams(SerialParams{Proto: serUART, ChA: 0, Bytes: []int{0x55}})
	ok, anchor := e.serialQualify(f, f.Valid, f.SampleS)
	if !ok {
		t.Fatal("UART 0x55 should match")
	}
	if anchor < 0 || anchor >= f.Valid {
		t.Fatalf("anchor %d out of [0,%d)", anchor, f.Valid)
	}
	// the 0x55 is the 3rd byte — its anchor must be past the first two bytes.
	if anchor < 40*8 {
		t.Fatalf("anchor %d too early for the 3rd byte", anchor)
	}

	// absent byte → no match.
	e.SetSerialParams(SerialParams{Proto: serUART, ChA: 0, Bytes: []int{0x99}})
	if ok, _ := e.serialQualify(f, f.Valid, f.SampleS); ok {
		t.Fatal("UART 0x99 should NOT match")
	}

	// a 2-byte sequence in order → match; reversed → no match.
	e.SetSerialParams(SerialParams{Proto: serUART, ChA: 0, Bytes: []int{0x22, 0x55}})
	if ok, _ := e.serialQualify(f, f.Valid, f.SampleS); !ok {
		t.Fatal("sequence 22 55 should match")
	}
	e.SetSerialParams(SerialParams{Proto: serUART, ChA: 0, Bytes: []int{0x55, 0x22}})
	if ok, _ := e.serialQualify(f, f.Valid, f.SampleS); ok {
		t.Fatal("reversed sequence 55 22 should NOT match")
	}

	// empty pattern → any decodable byte matches.
	e.SetSerialParams(SerialParams{Proto: serUART, ChA: 0})
	if ok, _ := e.serialQualify(f, f.Valid, f.SampleS); !ok {
		t.Fatal("empty pattern should match any UART traffic")
	}
}

func TestSerialQualifySPI(t *testing.T) {
	clk, data := spiWave([]int{0xA5, 0x3C, 0x55}, 20)
	f := &Frame{C1: clk, C2: data, Valid: len(clk), SampleS: 2e-7}
	e := &Engine{}
	e.SetSerialParams(SerialParams{Proto: serSPI, ChA: 0, ChB: 1, MSB: true, Bytes: []int{0x3C}})
	if ok, a := e.serialQualify(f, f.Valid, f.SampleS); !ok || a < 0 {
		t.Fatalf("SPI 0x3C should match (ok=%v anchor=%d)", ok, a)
	}
	e.SetSerialParams(SerialParams{Proto: serSPI, ChA: 0, ChB: 1, MSB: true, Bytes: []int{0x77}})
	if ok, _ := e.serialQualify(f, f.Valid, f.SampleS); ok {
		t.Fatal("SPI 0x77 should NOT match")
	}
}

func TestSerialQualifyI2C(t *testing.T) {
	scl, sda := i2cWave(0x50, 0 /*write*/, []int{0xDE, 0xAD}, 20)
	f := &Frame{C1: scl, C2: sda, Valid: len(scl), SampleS: 2e-7}
	e := &Engine{}

	// address 0x50 WRITE → match, anchored on the transaction start.
	e.SetSerialParams(SerialParams{Proto: serI2C, ChA: 0, ChB: 1, Addr: 0x50, RW: 0})
	ok, anchor := e.serialQualify(f, f.Valid, f.SampleS)
	if !ok {
		t.Fatal("I2C write to 0x50 should match")
	}
	if anchor < 0 || anchor >= f.Valid {
		t.Fatalf("I2C anchor %d out of range", anchor)
	}

	// same address but READ direction → no match (this transaction is a write).
	e.SetSerialParams(SerialParams{Proto: serI2C, ChA: 0, ChB: 1, Addr: 0x50, RW: 1})
	if ok, _ := e.serialQualify(f, f.Valid, f.SampleS); ok {
		t.Fatal("I2C read qualifier should NOT match a write transaction")
	}

	// different address → no match.
	e.SetSerialParams(SerialParams{Proto: serI2C, ChA: 0, ChB: 1, Addr: 0x22, RW: 2})
	if ok, _ := e.serialQualify(f, f.Valid, f.SampleS); ok {
		t.Fatal("I2C addr 0x22 should NOT match a 0x50 transaction")
	}

	// address + required data byte present → match; absent data → no match.
	e.SetSerialParams(SerialParams{Proto: serI2C, ChA: 0, ChB: 1, Addr: 0x50, RW: 0, Bytes: []int{0xAD}})
	if ok, _ := e.serialQualify(f, f.Valid, f.SampleS); !ok {
		t.Fatal("I2C 0x50 write containing 0xAD should match")
	}
	e.SetSerialParams(SerialParams{Proto: serI2C, ChA: 0, ChB: 1, Addr: 0x50, RW: 0, Bytes: []int{0xBE}})
	if ok, _ := e.serialQualify(f, f.Valid, f.SampleS); ok {
		t.Fatal("I2C 0x50 write NOT containing 0xBE should not match")
	}

	// any-address (Addr<0, RW any) → match.
	e.SetSerialParams(SerialParams{Proto: serI2C, ChA: 0, ChB: 1, Addr: -1, RW: 2})
	if ok, _ := e.serialQualify(f, f.Valid, f.SampleS); !ok {
		t.Fatal("I2C any-address should match a transaction")
	}
}

func TestSerialQualifyPassAndReject(t *testing.T) {
	e := &Engine{}
	f := &Frame{C1: make([]uint8, 100), C2: make([]uint8, 100), Valid: 100, SampleS: 1e-6}
	// unconfigured (proto off) → pass through, no re-anchor.
	e.SetSerialParams(SerialParams{Proto: 0})
	if ok, a := e.serialQualify(f, f.Valid, f.SampleS); !ok || a != -1 {
		t.Fatalf("unconfigured serial must pass through (ok=%v a=%d)", ok, a)
	}
	// too few samples → reject.
	e.SetSerialParams(SerialParams{Proto: serUART, ChA: 0, Bytes: []int{0x55}})
	if ok, _ := e.serialQualify(f, 4, f.SampleS); ok {
		t.Fatal("valid<8 must reject")
	}
	// flat/undecodable record → reject (no OK decode).
	if ok, _ := e.serialQualify(f, f.Valid, f.SampleS); ok {
		t.Fatal("flat record should not match a byte pattern")
	}
}

func TestSetSerialModeResetsCount(t *testing.T) {
	e := &Engine{}
	e.serialMatches.Store(7)
	e.SetSerialMode(SerialTrigger) // off→on resets
	if e.serialMatches.Load() != 0 {
		t.Fatal("arming should reset the match counter")
	}
	e.serialMatches.Store(5)
	e.SetSerialMode(SerialTrigger) // on→on does NOT reset
	if e.serialMatches.Load() != 5 {
		t.Fatal("re-arming while already on must not reset")
	}
	if e.SetSerialMode(99); e.serialMode.Load() != 0 {
		t.Fatal("invalid mode must clamp to off")
	}
}

// ---- match-logic units on hand-built spans (no decoding) ---------------------

func TestMatchBytesUnit(t *testing.T) {
	// contiguous bytes ~10 samples wide, abutting (like a continuous UART stream)
	spans := []decode.Span{
		{Kind: "data", Val: 0x11, I0: 10, I1: 19},
		{Kind: "data", Val: 0x22, I0: 20, I1: 29},
		{Kind: "gap"},
		{Kind: "data", Val: 0x33, I0: 30, I1: 39},
	}
	if ok, a := matchBytes(spans, []int{0x22}); !ok || a != 20 {
		t.Fatalf("single byte: ok=%v a=%d want 20", ok, a)
	}
	if ok, a := matchBytes(spans, []int{0x22, 0x33}); !ok || a != 20 {
		t.Fatalf("contiguous sequence anchors on first byte: ok=%v a=%d want 20", ok, a)
	}
	if ok, _ := matchBytes(spans, []int{0x33, 0x22}); ok {
		t.Fatal("out-of-order sequence must not match")
	}
	if ok, a := matchBytes(spans, nil); !ok || a != 10 {
		t.Fatalf("empty pattern → first byte: ok=%v a=%d", ok, a)
	}
	if ok, _ := matchBytes(nil, nil); ok {
		t.Fatal("no bytes at all must not match")
	}

	// error bytes are NOT matchable data (must not fire on corrupted traffic)
	errSpans := []decode.Span{{Kind: "frame-error", Val: 0x55, I0: 5, I1: 14}, {Kind: "parity-error", Val: 0x33, I0: 15, I1: 24}}
	if ok, _ := matchBytes(errSpans, []int{0x55}); ok {
		t.Fatal("frame-error byte must NOT satisfy a data trigger")
	}
	if ok, _ := matchBytes(errSpans, nil); ok {
		t.Fatal("empty pattern must NOT fire on a record of only error bytes")
	}

	// CONTIGUITY: two matching bytes separated by a large idle gap must NOT form
	// a sequence (different transmissions), but MUST still match individually.
	gapped := []decode.Span{
		{Kind: "data", Val: 0x41, I0: 100, I1: 190},   // width ~90
		{Kind: "data", Val: 0x42, I0: 9000, I1: 9090}, // thousands of samples later
	}
	if ok, _ := matchBytes(gapped, []int{0x41, 0x42}); ok {
		t.Fatal("a 2-byte pattern must NOT bridge a large idle gap")
	}
	if ok, a := matchBytes(gapped, []int{0x42}); !ok || a != 9000 {
		t.Fatalf("single byte across a gap still matches: ok=%v a=%d", ok, a)
	}
}

func TestIndexSeqUnit(t *testing.T) {
	hay := []int{1, 2, 3, 2, 3, 4}
	if indexSeq(hay, []int{2, 3, 4}) != 3 {
		t.Fatal("should find at 3")
	}
	if indexSeq(hay, []int{9}) != -1 {
		t.Fatal("absent → -1")
	}
	if indexSeq(hay, nil) != 0 {
		t.Fatal("empty needle → 0")
	}
	if indexSeq([]int{1}, []int{1, 2}) != -1 {
		t.Fatal("needle longer than hay → -1")
	}
}
