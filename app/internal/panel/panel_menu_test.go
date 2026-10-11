// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
)

// The sheets' rows drive the instrument as the old menu pages did: the
// cursors, the decoder, the holdoff, super-res, the roll mode.

// TRLC-LINKS: REQ-SDS-136, REQ-SDS-212
func TestCursorRows(t *testing.T) {
	c, _, _ := newC(t)
	press(t, c, "cursors")
	if st := c.PanelStatus(); st.Sheet.Tab != "curs" || st.Sheet.Row.ID != "curs.mode" {
		t.Fatalf("Cursors key: %+v", st.Sheet)
	}
	pickKey(t, c, "Time")
	if d, _ := c.Snapshot(); !d.CurOn || d.CurType != 0 {
		t.Fatal("Time did not enable the time cursors")
	}
	press(t, c, "cursors") // back to SCOPE: Adjust moves cursor A
	before := c.curX[0]
	turn(c, "adjust", 2)
	if c.curX[0] <= before {
		t.Fatalf("Adjust did not move cursor A: %v -> %v", before, c.curX[0])
	}
	press(t, c, "cursors")
	goRow(t, c, "curs.active")
	pickKey(t, c, "Cursor B")
	press(t, c, "cursors")
	a := c.curX[0]
	b := c.curX[1]
	turn(c, "adjust", -1)
	if c.curX[0] != a || c.curX[1] >= b {
		t.Fatalf("Adjust after Cursor B: A %v -> %v, B %v -> %v", a, c.curX[0], b, c.curX[1])
	}
	c.SheetSet("curs.mode", nil, "Volts")
	if d, _ := c.Snapshot(); d.CurType != 1 {
		t.Fatal("Volts did not switch to the volts cursors")
	}
}

// The BUS tab: Auto first after Off, each protocol's rows in use, clock and
// data on different channels whatever is toggled, the strip format row.
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-214
func TestDecodeRows(t *testing.T) {
	c, _, _ := newC(t)
	press(t, c, "utility")
	if st := c.PanelStatus(); st.Sheet.Tab != "bus" || st.FKeys[0].Label != "Off" || st.FKeys[1].Label != "Auto" {
		t.Fatalf("Utility: %+v keys %v", st.Sheet, keyLabels(c))
	}
	inUse := func(id string) bool { return rowByID[id].relevant(c.snap()) }
	pickKey(t, c, "UART")
	if !inUse("bus.baud") || !inUse("bus.source") || inUse("bus.scl") {
		t.Fatal("UART rows")
	}
	pickKey(t, c, "I2C")
	d, _ := c.Snapshot()
	if d.DecProto != framecalc.ProtoI2C || !inUse("bus.scl") || !inUse("bus.sda") || d.DecChA == d.DecChB {
		t.Fatalf("I2C: proto %d roles %d/%d", d.DecProto, d.DecChA, d.DecChB)
	}
	c.SheetSet("bus.scl", nil, "CH2")
	if d, _ := c.Snapshot(); d.DecChA == d.DecChB {
		t.Fatalf("SCL and SDA share C%d", d.DecChA+1)
	}
	c.SheetSet("bus.sda", nil, "CH2")
	if d, _ := c.Snapshot(); d.DecChA == d.DecChB || d.DecChB != 1 {
		t.Fatalf("SDA toggle: %d/%d", d.DecChA, d.DecChB)
	}
	pickKey(t, c, "SPI")
	if d, _ := c.Snapshot(); d.DecProto != framecalc.ProtoSPI || !inUse("bus.spi_mode") || d.DecChA == d.DecChB {
		t.Fatalf("SPI: proto %d", d.DecProto)
	}
	goRow(t, c, "bus.strip_format")
	pickKey(t, c, "String")
	if d, _ := c.Snapshot(); d.DecFormat != 1 {
		t.Fatalf("strip format %d", d.DecFormat)
	}
	turn(c, "horizpos", 1)
	if d, _ := c.Snapshot(); d.DecFormat != 2 {
		t.Fatalf("strip format after Position %d", d.DecFormat)
	}
}

// The holdoff row steps its ladder and Position push turns it off.
// TRLC-LINKS: REQ-SDS-136
func TestTriggerHoldoffRow(t *testing.T) {
	c, eng, _ := newC(t)
	press(t, c, "trigmenu")
	goRow(t, c, "trig.holdoff")
	if rowText(c, "trig.holdoff") != "Off" {
		t.Fatalf("holdoff shows %q", rowText(c, "trig.holdoff"))
	}
	pickKey(t, c, "Up")
	if last := eng.calls[len(eng.calls)-1]; last != (call{"holdoff", 100, 0}) {
		t.Fatalf("Up did not step holdoff to 100us: %v", last)
	}
	if rowText(c, "trig.holdoff") == "Off" {
		t.Fatal("holdoff value not shown after set")
	}
	press(t, c, "horizpospush")
	if eng.Snapshot().HoldoffS != 0 {
		t.Fatalf("Position push left holdoff %g", eng.Snapshot().HoldoffS)
	}
}

// The ACQ tab drives super-res: On arms it (resuming Run) and lights
// ACQUIRE, its rows set the channel, grid and stop rule, Show switches to
// the stacked trace, the gate rows move the gate, Off cancels.
// TRLC-LINKS: REQ-SDS-140, REQ-SDS-212
func TestSuperresRows(t *testing.T) {
	c, eng, _ := newC(t)
	eng.SetRunning(false)
	fr := smallFrame()
	fr.SampleS = 1e-9
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(fr) })
	press(t, c, "acquire")
	goRow(t, c, "acq.superres")
	pickKey(t, c, "On")
	if !c.SuperresView().Active || !eng.Snapshot().Running || eng.lastLEDs()&ledAcquire == 0 {
		t.Fatalf("On: active %v running %v lamps %#04x", c.SuperresView().Active, eng.Snapshot().Running, eng.lastLEDs())
	}
	defer c.srCancel("cancelled")
	if rowText(c, "acq.sr_channel") != "C1" || rowText(c, "acq.sr_grid") != "x32" || rowText(c, "acq.sr_stop") != "Bits" || rowText(c, "acq.sr_target") != "4" {
		t.Fatal("super-res defaults")
	}
	c.SheetSet("acq.sr_stop", nil, "Stacks")
	if rowText(c, "acq.sr_target") != "500" {
		t.Fatalf("stacks target %q", rowText(c, "acq.sr_target"))
	}
	c.SheetSet("acq.sr_channel", nil, "C2")
	if c.SuperresView().Align != 1 && c.srCh != 1 {
		t.Fatal("channel C2 not taken")
	}
	c.SheetSet("acq.sr_show", nil, "Stack")
	if c.SuperresView().Focus != 3 {
		t.Fatalf("Show Stack: focus %d", c.SuperresView().Focus)
	}
	c.SheetSet("acq.sr_show", nil, "Live")
	lo0 := c.SuperresView().GateLo
	goRow(t, c, "acq.sr_gate_start")
	pickKey(t, c, "Up")
	if lo := c.SuperresView().GateLo; lo <= lo0 {
		t.Fatalf("gate start did not move right: %d -> %d", lo0, lo)
	}
	goRow(t, c, "acq.superres")
	pickKey(t, c, "Off")
	if c.SuperresView().Active || eng.lastLEDs()&ledAcquire != 0 {
		t.Fatal("Off did not cancel super-res")
	}
}

// The TIME tab's roll mode: chunked by default, stream on demand.
// TRLC-LINKS: REQ-SDS-010
func TestRollStreamRow(t *testing.T) {
	c, eng, _ := newC(t)
	press(t, c, "horizmenu")
	goRow(t, c, "time.roll")
	if rowText(c, "time.roll") != "Chunked" {
		t.Fatalf("roll %q", rowText(c, "time.roll"))
	}
	pickKey(t, c, "Stream")
	if !eng.Snapshot().RollStream {
		t.Fatal("Stream not selected")
	}
	pickKey(t, c, "Chunked")
	if eng.Snapshot().RollStream {
		t.Fatal("Chunked not selected")
	}
}

// A fast Adjust spin (an accelerated event) moves the cursor by its step
// count, not one step an event (bug hunt 2026-10-10).
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-213
func TestCursorAdjustAccelerates(t *testing.T) {
	c, _, _ := newC(t)
	c.SheetSet("curs.mode", nil, "Time")
	before := c.curX[0]
	c.dispatch("adjust", 1, 10)
	if got := c.curX[0]; got < before+0.099 || got > before+0.101 {
		t.Fatalf("10-step Adjust moved cursor A %v -> %v, want +0.10", before, got)
	}
}
