// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"testing"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
)

// ---- synthetic protocol waveforms (mirrors internal/decode/decode_test.go) ---

// TRLC-LINKS: REQ-SDS-013
func uartWave(bytes []int, spb int) []uint8 {
	lo, hi := uint8(40), uint8(210)
	var w []uint8
	push := func(bit, k int) {
		v := lo
		if bit == 1 {
			v = hi
		}
		for range k {
			w = append(w, v)
		}
	}
	push(1, spb*8)
	for _, b := range bytes {
		push(0, spb) // start
		for c := range 8 {
			push((b>>c)&1, spb) // LSB first
		}
		push(1, spb) // stop
		push(1, spb) // idle
	}
	push(1, spb*8)
	return w
}

// TRLC-LINKS: REQ-SDS-013
func spiWave(bytes []int, h int) (clk, data []uint8) {
	lo, hi := uint8(40), uint8(210)
	seg := func(c, d uint8, n int) {
		for range n {
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

// TRLC-LINKS: REQ-SDS-013
func i2cWave(addr7, rw int, data []int, h int) (scl, sda []uint8) {
	lo, hi := uint8(40), uint8(210)
	seg := func(c, d uint8, n int) {
		for range n {
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

// TRLC-LINKS: REQ-SDS-013
func TestSerialQualifyUART(t *testing.T) {
	w := uartWave([]int{0x11, 0x22, 0x55, 0x33}, 40)
	f := &sample.Frame{SampleS: 1e-6, Traces: []sample.Trace{{Codes8: w, Valid: len(w)}, {Ch: 1, Codes8: make([]uint8, len(w)), Valid: len(w)}}}
	e := &Engine{}

	// byte present → match, anchored inside the record.
	e.SetSerialParams(SerialParams{Proto: core.SerUART, ChA: 0, Bytes: []int{0x55}})
	ok, anchor := e.serialQualify(f, f.Len(), f.SampleS)
	if !ok {
		t.Fatal("UART 0x55 should match")
	}
	if anchor < 0 || anchor >= f.Len() {
		t.Fatalf("anchor %d out of [0,%d)", anchor, f.Len())
	}
	// the 0x55 is the 3rd byte — its anchor must be past the first two bytes.
	if anchor < 40*8 {
		t.Fatalf("anchor %d too early for the 3rd byte", anchor)
	}

	// absent byte → no match.
	e.SetSerialParams(SerialParams{Proto: core.SerUART, ChA: 0, Bytes: []int{0x99}})
	if ok, _ := e.serialQualify(f, f.Len(), f.SampleS); ok {
		t.Fatal("UART 0x99 should NOT match")
	}

	// a 2-byte sequence in order → match; reversed → no match.
	e.SetSerialParams(SerialParams{Proto: core.SerUART, ChA: 0, Bytes: []int{0x22, 0x55}})
	if ok, _ := e.serialQualify(f, f.Len(), f.SampleS); !ok {
		t.Fatal("sequence 22 55 should match")
	}
	e.SetSerialParams(SerialParams{Proto: core.SerUART, ChA: 0, Bytes: []int{0x55, 0x22}})
	if ok, _ := e.serialQualify(f, f.Len(), f.SampleS); ok {
		t.Fatal("reversed sequence 55 22 should NOT match")
	}

	// empty pattern → any decodable byte matches.
	e.SetSerialParams(SerialParams{Proto: core.SerUART, ChA: 0})
	if ok, _ := e.serialQualify(f, f.Len(), f.SampleS); !ok {
		t.Fatal("empty pattern should match any UART traffic")
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestSerialQualifySPI(t *testing.T) {
	clk, data := spiWave([]int{0xA5, 0x3C, 0x55}, 20)
	f := &sample.Frame{SampleS: 2e-7, Traces: []sample.Trace{{Codes8: clk, Valid: len(clk)}, {Ch: 1, Codes8: data, Valid: len(clk)}}}
	e := &Engine{}
	e.SetSerialParams(SerialParams{Proto: core.SerSPI, ChA: 0, ChB: 1, MSB: true, Bytes: []int{0x3C}})
	if ok, a := e.serialQualify(f, f.Len(), f.SampleS); !ok || a < 0 {
		t.Fatalf("SPI 0x3C should match (ok=%v anchor=%d)", ok, a)
	}
	e.SetSerialParams(SerialParams{Proto: core.SerSPI, ChA: 0, ChB: 1, MSB: true, Bytes: []int{0x77}})
	if ok, _ := e.serialQualify(f, f.Len(), f.SampleS); ok {
		t.Fatal("SPI 0x77 should NOT match")
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestSerialQualifyI2C(t *testing.T) {
	scl, sda := i2cWave(0x50, 0 /*write*/, []int{0xDE, 0xAD}, 20)
	f := &sample.Frame{SampleS: 2e-7, Traces: []sample.Trace{{Codes8: scl, Valid: len(scl)}, {Ch: 1, Codes8: sda, Valid: len(scl)}}}
	e := &Engine{}

	// address 0x50 WRITE → match, anchored on the transaction start.
	e.SetSerialParams(SerialParams{Proto: core.SerI2C, ChA: 0, ChB: 1, Addr: 0x50, RW: 0})
	ok, anchor := e.serialQualify(f, f.Len(), f.SampleS)
	if !ok {
		t.Fatal("I2C write to 0x50 should match")
	}
	if anchor < 0 || anchor >= f.Len() {
		t.Fatalf("I2C anchor %d out of range", anchor)
	}

	// same address but READ direction → no match (this transaction is a write).
	e.SetSerialParams(SerialParams{Proto: core.SerI2C, ChA: 0, ChB: 1, Addr: 0x50, RW: 1})
	if ok, _ := e.serialQualify(f, f.Len(), f.SampleS); ok {
		t.Fatal("I2C read qualifier should NOT match a write transaction")
	}

	// different address → no match.
	e.SetSerialParams(SerialParams{Proto: core.SerI2C, ChA: 0, ChB: 1, Addr: 0x22, RW: 2})
	if ok, _ := e.serialQualify(f, f.Len(), f.SampleS); ok {
		t.Fatal("I2C addr 0x22 should NOT match a 0x50 transaction")
	}

	// address + required data byte present → match; absent data → no match.
	e.SetSerialParams(SerialParams{Proto: core.SerI2C, ChA: 0, ChB: 1, Addr: 0x50, RW: 0, Bytes: []int{0xAD}})
	if ok, _ := e.serialQualify(f, f.Len(), f.SampleS); !ok {
		t.Fatal("I2C 0x50 write containing 0xAD should match")
	}
	e.SetSerialParams(SerialParams{Proto: core.SerI2C, ChA: 0, ChB: 1, Addr: 0x50, RW: 0, Bytes: []int{0xBE}})
	if ok, _ := e.serialQualify(f, f.Len(), f.SampleS); ok {
		t.Fatal("I2C 0x50 write NOT containing 0xBE should not match")
	}

	// any-address (Addr<0, RW any) → match.
	e.SetSerialParams(SerialParams{Proto: core.SerI2C, ChA: 0, ChB: 1, Addr: -1, RW: 2})
	if ok, _ := e.serialQualify(f, f.Len(), f.SampleS); !ok {
		t.Fatal("I2C any-address should match a transaction")
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestSerialQualifyPassAndReject(t *testing.T) {
	e := &Engine{}
	f := &sample.Frame{SampleS: 1e-6, Traces: []sample.Trace{{Codes8: make([]uint8, 100), Valid: 100}, {Ch: 1, Codes8: make([]uint8, 100), Valid: 100}}}
	// unconfigured (proto off) → pass through, no re-anchor.
	e.SetSerialParams(SerialParams{Proto: 0})
	if ok, a := e.serialQualify(f, f.Len(), f.SampleS); !ok || a != -1 {
		t.Fatalf("unconfigured serial must pass through (ok=%v a=%d)", ok, a)
	}
	// too few samples → reject.
	e.SetSerialParams(SerialParams{Proto: core.SerUART, ChA: 0, Bytes: []int{0x55}})
	if ok, _ := e.serialQualify(f, 4, f.SampleS); ok {
		t.Fatal("valid<8 must reject")
	}
	// flat/undecodable record → reject (no OK decode).
	if ok, _ := e.serialQualify(f, f.Len(), f.SampleS); ok {
		t.Fatal("flat record should not match a byte pattern")
	}
}

// TRLC-LINKS: REQ-SDS-013
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

// TRLC-LINKS: REQ-SDS-013
func TestSerialRegistryUnknownFailsClosed(t *testing.T) {
	e := New(Config{Bus: newFakeBus()})
	w := uartWave([]int{0x55}, 16)
	f := &sample.Frame{SampleS: 1e-6, Traces: []sample.Trace{{Codes8: w, Valid: len(w)}, {Ch: 1, Codes8: w, Valid: len(w)}}}
	e.SetSerialParams(SerialParams{Proto: 987654})
	if ok, _ := e.serialQualify(f, f.Len(), f.SampleS); ok {
		t.Fatal("unknown protocol qualified a trigger")
	}
}

// A record holding the pattern more than once triggers on the match a full
// screen fits around, not the first one at the record start (which leaves a
// flat 0 V tail off the record).
// TRLC-LINKS: REQ-SDS-013
func TestSerialQualifyPicksCentredMatch(t *testing.T) {
	msg := []int{0x48, 0x55, 0xAA, 0x0F, 0x0A}
	var bytes []int
	for range 4 {
		bytes = append(bytes, msg...)
	}
	w := uartWave(bytes, 40)
	f := &sample.Frame{SampleS: 1e-6, Traces: []sample.Trace{{Codes8: w, Valid: len(w)}, {Ch: 1, Codes8: make([]uint8, len(w)), Valid: len(w)}}}
	e := &Engine{}
	e.SetSerialParams(SerialParams{Proto: core.SerUART, ChA: 0, Bytes: []int{0x55, 0xAA}})
	_, first := e.serialQualify(f, f.Len(), f.SampleS)
	screen := len(w) / 2
	ok, anchor := e.serialQualifyAt(f, f.Len(), f.SampleS, softAnchor(f.Len(), screen, .5, -1))
	if !ok || anchor == first {
		t.Fatalf("ok=%v anchor=%d, want a later match than the first (%d)", ok, anchor, first)
	}
	if anchor < screen/2 || anchor > f.Len()-screen/2 {
		t.Fatalf("anchor %d leaves the screen off the record [%d,%d]", anchor, screen/2, f.Len()-screen/2)
	}
	// No match fits the span (a record little longer than the screen) → the
	// match nearest the target still triggers, leaving the least off-record.
	mid := f.Len() / 2
	ok, anchor = e.serialQualifyAt(f, f.Len(), f.SampleS, core.AnchorSpan{Target: mid, Lo: mid - 1, Hi: mid + 1})
	if !ok || anchor == first {
		t.Fatalf("fallback ok=%v anchor=%d, want the match nearest %d (not the first, %d)", ok, anchor, mid, first)
	}
	period := len(w) / 4
	if d := anchor - mid; d < -period/2-40 || d > period/2+40 {
		t.Fatalf("fallback anchor %d is %d from the target, over half a message period (%d)", anchor, d, period)
	}
	// Unconstrained, the first match triggers (the band pipeline's rule).
	if ok, a := e.serialQualify(f, f.Len(), f.SampleS); !ok || a != first {
		t.Fatalf("serialQualify ok=%v anchor=%d, want first %d", ok, a, first)
	}
}

// A serial match that would leave more than a sliver of the screen off the
// record does not trigger; a record shorter than the screen never blocks.
// TRLC-LINKS: REQ-SDS-013
func TestOffRecord(t *testing.T) {
	for _, tc := range []struct{ valid, win, anchor, want int }{
		{4096, 3937, 2048, 0},
		{4096, 3937, 1433, 1969 - 1433},
		{4096, 3937, 3131, 3131 - 1969 + 3937 - 4096},
		{4096, 5000, 100, 0},
	} {
		if got := offRecord(tc.valid, tc.win, .5, tc.anchor); got != tc.want {
			t.Errorf("offRecord(%d,%d,.5,%d)=%d want %d", tc.valid, tc.win, tc.anchor, got, tc.want)
		}
	}
}

// TestAutoGateLiveness: in AUTO the SRAM loop holds captures the armed serial
// or zone gate rejected and lets one liveness frame through per fallback count,
// like the native loop, so matched bytes stay where the trigger put them.
// TRLC-LINKS: REQ-SDS-013
func TestAutoGateLiveness(t *testing.T) {
	e := New(Config{Bus: newFakeBus()})
	if !e.autoGateLiveness() {
		t.Fatal("no gate armed: every capture publishes")
	}
	for _, gate := range []struct {
		name     string
		arm      func(on bool)
		fallback int
	}{
		{"serial", func(on bool) {
			if on {
				e.SetSerialMode(SerialTrigger)
			} else {
				e.SetSerialMode(SerialOff)
			}
		}, core.SerialFallback},
		{"zone", func(on bool) {
			if on {
				e.SetZoneMode(ZoneTrigger)
			} else {
				e.SetZoneMode(ZoneOff)
			}
		}, core.ZoneFallback},
	} {
		gate.arm(true)
		// a frame just published: the holds count to the fallback
		e.mu.Lock()
		e.lastPublish = e.clk.Now()
		e.mu.Unlock()
		for range 2 {
			for i := 1; i < gate.fallback; i++ {
				if e.autoGateLiveness() {
					t.Fatalf("%s: rejected capture %d published", gate.name, i)
				}
			}
			if !e.autoGateLiveness() {
				t.Fatalf("%s: no liveness frame after %d holds", gate.name, gate.fallback)
			}
		}
		// a slow timebase: captures seconds apart; a second after the last frame one is due, whatever the count
		e.mu.Lock()
		e.lastPublish = e.clk.Now().Add(-2 * core.AutoLiveMax)
		e.mu.Unlock()
		if !e.autoGateLiveness() {
			t.Fatalf("%s: no liveness frame %v after the last one", gate.name, 2*core.AutoLiveMax)
		}
		gate.arm(false)
	}
}
