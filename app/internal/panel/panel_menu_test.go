package panel

import (
	"open-sds/app/internal/engine"
	"testing"
)

func TestCursorMenu(t *testing.T) {
	c, _, _ := newC(t)
	// HORIZONTAL once → timebase page; twice → cursor page.
	c.menuButton(btnHorizMenu)
	c.menuButton(btnHorizMenu)
	if v := c.MenuView(); v.Title != "CURSOR" {
		t.Fatalf("second HORIZ did not open the cursor page: %q", v.Title)
	}
	// F1 toggles cursors on.
	c.menuButton(btnF1)
	if !c.MenuView().CurOn {
		t.Fatal("F1 did not enable cursors")
	}
	// ADJUST moves the active (A) X-cursor; default 0.35 → +2 steps ≈ 0.37.
	before := c.MenuView().CurX[0]
	c.menuAdjust(+1)
	c.menuAdjust(+1)
	if got := c.MenuView().CurX[0]; got <= before {
		t.Fatalf("ADJUST did not move cursor A: %v → %v", before, got)
	}
	// F3 switches the active cursor to B; ADJUST then moves B, not A.
	aFixed := c.MenuView().CurX[0]
	c.menuButton(btnF3)
	c.menuAdjust(-1)
	v := c.MenuView()
	if v.CurSel != 1 || v.CurX[0] != aFixed {
		t.Fatalf("active cursor switch failed: sel=%d A=%v(want %v)", v.CurSel, v.CurX[0], aFixed)
	}
	// F2 flips to volts cursors.
	c.menuButton(btnF2)
	if c.MenuView().CurType != 1 {
		t.Fatal("F2 did not switch to volts cursors")
	}
}

func TestDecodeMenu(t *testing.T) {
	c, _, _ := newC(t)
	c.menuButton(btnMenuOnOff) // MAIN menu
	if v := c.MenuView(); v.Title != "MENU" {
		t.Fatalf("MENU not open: %q", v.Title)
	}
	c.menuButton(btnF5) // slot 4 -> Decode
	if v := c.MenuView(); v.Title != "DECODE" {
		t.Fatalf("DECODE page not open: %q", v.Title)
	}
	// Auto is FIRST after Off (most used) — one F1 press reaches it, and it shows
	// only Proto + the format slot.
	c.menuButton(btnF1)
	if v := c.MenuView(); v.DecProto != 1 || v.Items[0].Value != "Auto" || v.Items[1].Label != "Show" {
		t.Fatalf("Auto not first/labelled: proto=%d items=%+v", v.DecProto, v.Items)
	}
	// Auto fills slots 0..1, so F3 (slot 2) is inert.
	sel := c.MenuView().Sel
	c.menuButton(btnF3)
	if got := c.MenuView().Sel; got != sel {
		t.Fatalf("F3 moved highlight onto an empty Auto slot: %d -> %d", sel, got)
	}
	// Auto -> UART.
	c.menuButton(btnF1)
	if v := c.MenuView(); v.DecProto != 2 || v.Items[1].Label != "Baud" || v.Items[2].Label != "Source" {
		t.Fatalf("UART not selected/labelled: proto=%d items=%+v", v.DecProto, v.Items)
	}
	// UART -> I2C. Clock and data must never share a channel (slot 1=SCL, 2=SDA).
	c.menuButton(btnF1)
	v := c.MenuView()
	if v.DecProto != 3 || v.Items[1].Label != "SCL" || v.Items[2].Label != "SDA" {
		t.Fatalf("I2C not selected/labelled: proto=%d items=%+v", v.DecProto, v.Items)
	}
	if v.DecChA == v.DecChB {
		t.Fatalf("SCL/SDA share a channel at start: A=%d B=%d", v.DecChA, v.DecChB)
	}
	c.menuButton(btnF2) // toggle SCL
	if v := c.MenuView(); v.DecChA == v.DecChB {
		t.Fatalf("SCL/SDA share a channel after SCL toggle: A=%d B=%d", v.DecChA, v.DecChB)
	}
	c.menuButton(btnF3) // toggle SDA
	if v := c.MenuView(); v.DecChA == v.DecChB {
		t.Fatalf("SCL/SDA share a channel after SDA toggle: A=%d B=%d", v.DecChA, v.DecChB)
	}
	// I2C carries a "Show" (byte format) slot at slot 3, cycling Hex/ASCII/Both.
	if v := c.MenuView(); v.Items[3].Label != "Show" || v.Items[3].Value != "Hex" {
		t.Fatalf("I2C Show slot missing/wrong: %+v", v.Items[3])
	}
	c.menuButton(btnF4) // slot 3 = Show -> ASCII
	if v := c.MenuView(); v.DecFormat != 1 || v.Items[3].Value != "ASCII" {
		t.Fatalf("Show did not cycle to ASCII: fmt=%d items=%+v", v.DecFormat, v.Items[3])
	}
	// I2C fills slots 0..3, so F5 (slot 4) is inert.
	sel = c.MenuView().Sel
	c.menuButton(btnF5)
	if got := c.MenuView().Sel; got != sel {
		t.Fatalf("F5 moved the highlight onto an empty I2C slot: %d -> %d", sel, got)
	}
	// I2C -> SPI: real 4th slot (Mode) plus Show on slot 4.
	c.menuButton(btnF1)
	if v := c.MenuView(); v.DecProto != 4 || v.Items[3].Label != "Mode" || v.Items[4].Label != "Show" {
		t.Fatalf("SPI slots wrong: proto=%d items=%+v", v.DecProto, v.Items)
	}
	c.menuButton(btnF5) // slot 4 = Show live in SPI
	if got := c.MenuView().Sel; got != 4 {
		t.Fatalf("F5 in SPI did not reach the Show slot: Sel=%d", got)
	}
}

func TestTriggerHoldoffSoftkey(t *testing.T) {
	c, eng, _ := newC(t)
	c.menuButton(btnTrigMenu) // open TRIGGER page
	if v := c.MenuView(); v.Items[4].Label != "Holdoff" || v.Items[4].Value != "Off" {
		t.Fatalf("holdoff softkey missing/wrong: %+v", v.Items[4])
	}
	// F5 (slot 4) steps the holdoff ladder Off -> 100us.
	c.menuButton(btnF5)
	last := eng.calls[len(eng.calls)-1]
	if last != (call{"holdoff", 100, 0}) { // 100us = 100e-6 * 1e6
		t.Fatalf("F5 did not step holdoff to 100us: %v", last)
	}
	// Reflect it back and confirm the menu formats it.
	eng.stats.HoldoffS = 100e-6
	if v := c.MenuView(); v.Items[4].Value == "Off" {
		t.Fatalf("holdoff value not shown after set: %+v", v.Items[4])
	}
}

// TestSuperresUX drives the device super-res state machine: UTILITY arms/cancels
// (like SINGLE), the SUPER-RES page maps to the softkeys, and ADJUST-push toggles
// the review view. Constant-Seq frame source → the stacker seeds once and idles
// on dedup, so the test sees only the synchronous transitions (the stacking
// numerics are covered by the golden-vector parity test).
func TestSuperresUX(t *testing.T) {
	c, eng, _ := newC(t)
	page := func() int { c.mu.Lock(); defer c.mu.Unlock(); return c.menuPage }

	n := 256
	sig := make([]uint8, n)
	for i := range sig {
		sig[i] = uint8(40 + (i*7)%160) // varied — not flat, not railed
	}
	fr := &engine.Frame{C1: sig, C2: sig, Valid: n, EdgeX: 32, SampleS: 1e-9, Seq: 0}
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(fr) })

	// UTILITY arms super-res.
	c.button(btnUtility)
	if !c.SuperresView().Active {
		t.Fatal("UTILITY did not arm super-res")
	}
	if page() != pgSuperres {
		t.Fatalf("arming did not open the SUPER-RES page: page=%d", page())
	}
	if got := eng.calls[len(eng.calls)-1]; got != (call{"run", 1, 0}) {
		t.Fatalf("arm did not resume RUN: %v", got)
	}
	if w := eng.leds[len(eng.leds)-1]; w&ledUtility == 0 {
		t.Errorf("UTILITY lamp not lit while active: %#x", w)
	}

	// Default menu: Channel C1 / Grid x32 / Stop on bits / Target +4.0b / Reset.
	want := []MenuItem{{"Channel", "C1"}, {"Grid", "x32"}, {"Stop on", "bits"}, {"Target", "+4.0b"}, {"Reset", ""}}
	v := c.MenuView()
	if v.Title != "SUPER-RES" || len(v.Items) != 5 {
		t.Fatalf("SUPER-RES menu wrong: title=%q items=%d", v.Title, len(v.Items))
	}
	for i, w := range want {
		if v.Items[i] != w {
			t.Errorf("slot %d = %+v, want %+v", i, v.Items[i], w)
		}
	}

	// F3 (slot 2) cycles the stop mode bits→stacks and reseeds a sensible target.
	c.menuButton(btnF3)
	if v = c.MenuView(); v.Items[2].Value != "stacks" || v.Items[3].Value != "500" {
		t.Errorf("stop-mode cycle: %+v / %+v", v.Items[2], v.Items[3])
	}

	// F1 (slot 0) toggles the aligned channel C1→C2 (rebuilds the stack).
	c.menuButton(btnF1)
	if v = c.MenuView(); v.Items[0].Value != "C2" {
		t.Errorf("channel toggle: %+v", v.Items[0])
	}

	// ADJUST/intensity push cycles focus: watch → gate-start → gate-end → review → watch.
	if f := c.SuperresView().Focus; f != 0 {
		t.Errorf("armed focus = %d, want 0 (watch)", f)
	}
	for i, want := range []int{1, 2, 3, 0} {
		c.button(btnAdjustPsh)
		if f := c.SuperresView().Focus; f != want {
			t.Errorf("ADJUST-push #%d → focus %d, want %d", i+1, f, want)
		}
	}

	// Manual gate: focus the start edge, nudge with ADJUST → the gate moves and the
	// stack re-seeds on the new (manual) gate.
	c.mu.Lock()
	c.srFocus = 1 // gate-start
	c.mu.Unlock()
	lo0 := c.SuperresView().GateLo
	if !c.srGateAdjust(3) {
		t.Error("srGateAdjust not consumed while a gate edge is focused")
	}
	if lo := c.SuperresView().GateLo; lo <= lo0 {
		t.Errorf("gate start did not move right: %d → %d", lo0, lo)
	}

	// UTILITY again cancels: mode off, page closed, lamp dark.
	c.button(btnUtility)
	if c.SuperresView().Active {
		t.Fatal("second UTILITY did not cancel super-res")
	}
	if page() != pgNone {
		t.Errorf("cancel did not close the menu: page=%d", page())
	}
	if w := eng.leds[len(eng.leds)-1]; w&ledUtility != 0 {
		t.Errorf("UTILITY lamp still lit after cancel: %#x", w)
	}
}
