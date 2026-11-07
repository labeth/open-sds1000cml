// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"testing"

	core "github.com/labeth/open-dso-core/engine"
)

// TestSerialTriggerComposesWithMask proves the observeOK fix: with the serial
// trigger armed on a NON-matching pattern, a serial-rejected (held) frame must
// NOT be mask-tested — so it can never trip mask stop-on-fail. (Before the fix,
// the mask gate ran on `lock` regardless of the serial veto and could freeze the
// scope on a frame the serial trigger explicitly rejected.)
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-014
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
	if f, fresh := e.Consume(); !fresh || f.TriggerAt < 0 {
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
	e.SetSerialParams(SerialParams{Proto: core.SerUART, ChA: 0, Bytes: []int{0x11, 0x22, 0x33, 0x44, 0x55}})
	e.SetSerialMode(SerialTrigger)
	beforeFail := e.maskFail.Load()
	for k := range 5 {
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
