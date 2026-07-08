package engine

import (
	"testing"

	"open-sds/app/internal/decode"
)

// adversarial matchI2C span sequences (repeated-START, missing R/W, cross-txn data).
func TestMatchI2CEdges(t *testing.T) {
	sp := func(kind string, val, i0 int, text string) decode.Span {
		return decode.Span{Kind: kind, Val: val, I0: i0, Text: text}
	}
	// A repeated-START bus: [S a50 W d.AA][S a51 R d.BB]P
	rep := []decode.Span{
		sp("start", 0, 0, "S"), sp("addr", 0x50, 5, "50"), sp("rw", 0, 10, "W"), sp("data", 0xAA, 15, "AA"),
		sp("start", 0, 20, "S"), sp("addr", 0x51, 25, "51"), sp("rw", 0, 30, "R"), sp("data", 0xBB, 35, "BB"),
		sp("stop", 0, 40, "P"),
	}
	check := func(name string, p SerialParams, wantOK bool, wantAnchor int) {
		ok, a := matchI2C(rep, p)
		if ok != wantOK || (wantOK && a != wantAnchor) {
			t.Fatalf("%s: got (ok=%v anchor=%d) want (ok=%v anchor=%d)", name, ok, a, wantOK, wantAnchor)
		}
	}
	// addr 0x50 write, data 0xAA → match, anchored on the 0x50 addr span
	check("50W+AA", SerialParams{Proto: serI2C, Addr: 0x50, RW: 0, Bytes: []int{0xAA}}, true, 5)
	// addr 0x51 read, data 0xBB → match on the second transaction
	check("51R+BB", SerialParams{Proto: serI2C, Addr: 0x51, RW: 1, Bytes: []int{0xBB}}, true, 25)
	// addr 0x50 with data 0xBB → NO match (0xBB belongs to the 0x51 transaction)
	check("50+BB-cross", SerialParams{Proto: serI2C, Addr: 0x50, RW: 2, Bytes: []int{0xBB}}, false, 0)
	// addr 0x50 read → NO match (this txn is a write)
	check("50R-dir", SerialParams{Proto: serI2C, Addr: 0x50, RW: 1}, false, 0)
	// any address, any dir, no data → match the first transaction
	check("any", SerialParams{Proto: serI2C, Addr: -1, RW: 2}, true, 5)

	// addr as the LAST span, no following rw:
	trailing := []decode.Span{sp("start", 0, 0, "S"), sp("addr", 0x22, 5, "22")}
	if ok, a := matchI2C(trailing, SerialParams{Proto: serI2C, Addr: 0x22, RW: 2}); !ok || a != 5 {
		t.Fatalf("trailing addr, RW=any should match (ok=%v a=%d)", ok, a)
	}
	if ok, _ := matchI2C(trailing, SerialParams{Proto: serI2C, Addr: 0x22, RW: 0}); ok {
		t.Fatal("trailing addr with NO rw span must NOT match a W qualifier")
	}
	// empty spans
	if ok, _ := matchI2C(nil, SerialParams{Proto: serI2C, Addr: -1, RW: 2}); ok {
		t.Fatal("empty spans must not match")
	}
}

// TestSerialTriggerComposesWithMask proves the observeOK fix: with the serial
// trigger armed on a NON-matching pattern, a serial-rejected (held) frame must
// NOT be mask-tested — so it can never trip mask stop-on-fail. (Before the fix,
// the mask gate ran on `lock` regardless of the serial veto and could freeze the
// scope on a frame the serial trigger explicitly rejected.)
func TestSerialTriggerComposesWithMask(t *testing.T) {
	fb := newFakeBus()
	fb.wave = func(i int) (uint8, uint8) { // period-256 square: locks, decodes to ~2 bytes
		var c1 uint8 = 56
		if (i/128)%2 == 0 {
			c1 = 200
		}
		return c1, 255 - c1
	}
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	e.oneFrame(false)
	if f, fresh := e.Consume(); !fresh || f.EdgeX < 0 {
		t.Fatalf("no lock baseline (fresh=%v)", fresh)
	}
	// a tight mask the square wave violates immediately
	win := e.band.WinCols()
	lo, hi := make([]uint8, win), make([]uint8, win)
	for j := range lo {
		lo[j], hi[j] = 0, 100 // hi=100, the 200-code level violates
	}
	e.SetMask(&Mask{Lo: lo, Hi: hi, WinCols: win, Ch: 0})

	// Baseline: without serial, stop-on-fail fires on the violating locked frame.
	e.SetMaskMode(MaskStopFail)
	e.oneFrame(false)
	if !e.maskStopped.Load() {
		t.Fatal("baseline: mask stop-on-fail must fire on the violating frame")
	}
	e.Consume() // drain the force-published failing frame

	// Reset, then arm serial on a pattern too long to appear (~2 bytes decode, 5
	// wanted) → every frame is serial-REJECTED.
	e.SetRunning(true) // clears the stop-on-fail latch
	e.SetMaskMode(MaskStopFail)
	e.SetSerialParams(SerialParams{Proto: serUART, ChA: 0, Bytes: []int{0x11, 0x22, 0x33, 0x44, 0x55}})
	e.SetSerialMode(SerialTrigger)
	beforeFail := e.maskFail.Load()
	for k := 0; k < 5; k++ {
		e.oneFrame(true) // NORM: a non-matching frame must hold
		if _, fresh := e.Consume(); fresh {
			t.Fatalf("frame %d: serial NORM must not publish a non-matching frame", k)
		}
	}
	if e.maskStopped.Load() || !e.running.Load() {
		t.Fatal("serial-rejected frames must NOT trip mask stop-on-fail")
	}
	if got := e.maskFail.Load() - beforeFail; got != 0 {
		t.Fatalf("serial-rejected frames must NOT be mask-tested (%d fails counted)", got)
	}
}
