// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"strings"
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
	before := c.snap().curX[0]
	turn(c, "adjust", 2)
	if c.snap().curX[0] <= before {
		t.Fatalf("Adjust did not move cursor A: %v -> %v", before, c.snap().curX[0])
	}
	press(t, c, "cursors")
	goRow(t, c, "curs.active")
	pickKey(t, c, "Cursor B")
	press(t, c, "cursors")
	a := c.snap().curX[0]
	b := c.snap().curX[1]
	turn(c, "adjust", -1)
	if c.snap().curX[0] != a || c.snap().curX[1] >= b {
		t.Fatalf("Adjust after Cursor B: A %v -> %v, B %v -> %v", a, c.snap().curX[0], b, c.snap().curX[1])
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

// The roll mode row is in use where roll runs: AUTO at 10 ms/div and slower.
// TRLC-LINKS: REQ-SDS-010
func TestRollRowInUseFromRollTimebase(t *testing.T) {
	c, _, _ := newC(t)
	avail := func() bool {
		for _, tab := range c.Sheets().Tabs {
			for _, r := range tab.Rows {
				if r.ID == "time.roll" {
					return r.Available
				}
			}
		}
		t.Fatal("no time.roll row")
		return false
	}
	for _, tc := range []struct {
		tdiv string
		want bool
	}{{"5ms", false}, {"10ms", true}, {"50ms", true}, {"100ms", true}} {
		if res := c.SheetSet("time.tdiv", nil, tc.tdiv); !res.OK {
			t.Fatalf("set %s: %+v", tc.tdiv, res)
		}
		if got := avail(); got != tc.want {
			t.Errorf("at %s/div the roll mode row in use = %v, want %v", tc.tdiv, got, tc.want)
		}
	}
}

// A time cursor's time from the trigger is where the screen shows it (and
// where its trace volts are read, windowOf): zoomed, the screen is centred
// on the trigger plus the pan, whatever the trigger position.
// TRLC-LINKS: REQ-SDS-217
func TestCursorTimesFollowTheZoomedScreen(t *testing.T) {
	const valid, winCols, tdiv = 40000, 10000, 1e-3 // a record of 4 screens
	g := geom{valid: valid, winCols: winCols, edgeX: 12000}
	sampleS := 10 * tdiv / winCols
	for _, tc := range []struct {
		zoom       int
		off, pos   float64
		curA, curB float64
	}{{1, 0, 0.3, 0.2, 0.7}, {10, 0, 0.3, 0.2, 0.7}, {10, 0.05, 0.5, 0.4, 0.9}, {5, -0.1, 0.8, 0.1, 0.6}} {
		s := &sv{zoom: tc.zoom, zoomOff: tc.off, records: float64(valid) / winCols}
		s.st.TdivS, s.st.TrigPosFrac = tdiv, tc.pos
		x := [2]float64{tc.curA, tc.curB}
		left, win := windowOf(g, s)
		for k, got := range []float64{cursorTimeAt(s, x[0]), cursorTimeAt(s, x[1])} {
			if back := cursorXOf(s, got); math.Abs(back-x[k]) > 1e-9 {
				t.Errorf("cursor %c at %g of the screen reads back at %g", 'A'+k, x[k], back)
			}
			want := (left + x[k]*float64(win) - g.edgeX) * sampleS
			if math.Abs(got-want) > 1e-9 {
				t.Errorf("zoom %d pan %g trigger at %g: cursor %c at %s, the screen shows it at %s",
					tc.zoom, tc.off, tc.pos, 'A'+k, si(got, "s"), si(want, "s"))
			}
		}
	}
}

// Zoomed with a pan, a time cursor's "At trigger" goes where the screen shows
// the trigger, not where the unzoomed trigger position would be.
// TRLC-LINKS: REQ-SDS-217
func TestCursorAtTriggerZoomed(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	f := toneFrame("")
	f.WinCols = f.Valid / 4 // a record of 4 screens
	r.show(f)
	r.eng.set(func(st *engine.Stats) { st.TrigPosFrac = 0.3 })
	for _, set := range [][2]string{{"curs.mode", "Time"}, {"time.zoom", "10x"}, {"time.zoom_pos", "1"}} {
		if res := c.SheetSet(set[0], nil, set[1]); !res.OK {
			t.Fatalf("%s %s: %+v", set[0], set[1], res)
		}
	}
	if res := c.SheetAction("curs.a", "At trigger"); !res.OK {
		t.Fatalf("At trigger: %+v", res)
	}
	// the pan is 1 % of 4 screens, 0.4 of a 10x zoomed screen: the trigger
	// shows at 0.5 - 0.4
	if got := c.snap().curX[0]; math.Abs(got-0.1) > 1e-9 {
		t.Fatalf("cursor A at %.3f of the screen, the trigger shows at 0.100", got)
	}
	if ta, _ := cursorTimes(c.snap()); math.Abs(ta) > 1e-12 {
		t.Fatalf("cursor A at the trigger reads %s", si(ta, "s"))
	}
}

// A fast Adjust spin (an accelerated event) moves the cursor by its step
// count, not one step an event.
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-213
func TestCursorAdjustAccelerates(t *testing.T) {
	c, _, _ := newC(t)
	c.SheetSet("curs.mode", nil, "Time")
	before := c.snap().curX[0]
	c.dispatch("adjust", 1, 10)
	if got := c.snap().curX[0]; got < before+0.099 || got > before+0.101 {
		t.Fatalf("10-step Adjust moved cursor A %v -> %v, want +0.10", before, got)
	}
}

// On an image that records 2.1 ms (a CAN, SENT or Manchester trigger's), the
// timebase goes no slower than the screen that record fills: the row and the
// S/DIV knob stop at 200us/div and say why.
// TRLC-LINKS: REQ-SDS-212
func TestTimebaseStopsAtTheImagesRecord(t *testing.T) {
	c, e, _ := newC(t)
	e.set(func(st *engine.Stats) { st.TdivS, st.TdivMaxS, st.RecordS = 200e-6, 200e-6, 2.097152e-3 })
	set(t, c, "time.tdiv", "1ms")
	if got := e.Snapshot().TdivS; got != 200e-6 {
		t.Fatalf("timebase %g, want 200us/div", got)
	}
	if n := c.View().Notice; !strings.Contains(n, "200us/div at most") {
		t.Fatalf("notice %q", n)
	}
	e.set(func(st *engine.Stats) { st.TdivMaxS = 0 })
	set(t, c, "time.tdiv", "1ms")
	if got := e.Snapshot().TdivS; got != 1e-3 {
		t.Fatalf("on the general image: %g, want 1ms/div", got)
	}
}

// A serial trigger armed at 500us/div on the packet image: the engine steps
// the timebase to 200us/div when the image is in, with no key turned. The
// panel says why and its S/DIV knob steps on from there.
// TRLC-LINKS: REQ-SDS-212
func TestTimebaseCapIsNoted(t *testing.T) {
	r := newViewRig(t)
	set(t, r.c, "time.tdiv", "500us")
	r.eng.set(func(st *engine.Stats) { st.TdivS, st.TdivMaxS, st.RecordS = 200e-6, 200e-6, 2.097152e-3 })
	r.show(uartFrame([]string{"Hi"}, 115200, 100e-6, 100e-9, 2000))
	if n := r.c.View().Notice; !strings.Contains(n, "200us/div at most on this trigger") {
		t.Fatalf("notice %q", n)
	}
	if got := r.c.tdivs[r.c.tdivIdx]; got != 200e-6 {
		t.Fatalf("the knob's place %g, want 200us/div", got)
	}
}
