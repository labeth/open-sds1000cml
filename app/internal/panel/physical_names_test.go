// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"testing"
	"time"
)

// Every physical name the /api/panel and SCPI inject surface knows reaches
// the dispatcher as a real key: none is unassigned any more.
// TRLC-LINKS: REQ-SDS-135
func TestPhysicalNamesReachTheDispatcher(t *testing.T) {
	for _, name := range []string{"run", "runstop", "single", "auto", "ch1vdivpush", "ch2vdivpush", "triglvlpush",
		"utility", "util", "adjustpush", "intensity", "f1", "f2", "f3", "f4", "f5", "trigmenu", "trigger", "acquire",
		"acq", "display", "disp", "horizmenu", "horiz", "menu", "measure", "meas", "cursors", "cursor", "math", "ref",
		"ch1", "ch2", "print", "hardcopy", "saverecall", "set50", "defaultsetup", "force", "help", "ch1pospush",
		"ch2pospush", "tdivpush", "horizpospush"} {
		c, _, _ := newC(t)
		if !c.InjectButton(name) {
			t.Fatalf("%s rejected", name)
		}
		select {
		case dispatch := <-c.inject:
			dispatch()
		default:
			t.Fatalf("%s never reached the dispatch queue", name)
		}
	}
	c, _, _ := newC(t)
	if c.InjectButton("nosuchkey") || c.InjectKnob("nosuchknob", 1, 1) {
		t.Fatal("an unknown name was accepted")
	}
}

// TRLC-LINKS: REQ-SDS-136
func TestHoldoffStepsToNearestSettingInRequestedDirection(t *testing.T) {
	for _, tc := range []struct {
		current   float64
		direction int
		want      float64
	}{
		{0.0025, 1, 0.01}, {0.0025, -1, 0.001}, {0.001, 1, 0.01}, {0.001, -1, 0.0001}, {0, -1, 0}, {1, 1, 1},
	} {
		if got := nextHoldoff(tc.current, tc.direction, false); got != tc.want {
			t.Errorf("holdoff %g dir %d: %g, want %g", tc.current, tc.direction, got, tc.want)
		}
	}
	// A press wraps past the ends (bug hunt #2: F5 stuck at 1 s).
	if got := nextHoldoff(1, 1, true); got != 0 {
		t.Errorf("press past 1 s: %g, want Off", got)
	}
	if got := nextHoldoff(0, -1, true); got != 1 {
		t.Errorf("press below Off: %g, want 1 s", got)
	}
}

// The SRAM image has no ETS and no decimated depth: those rows are not in
// use there and refuse without an engine command; the mode row offers
// Precision and names it.
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-212
func TestSRAMAcquisitionRowsUseBackendCapabilities(t *testing.T) {
	c, eng, _ := newC(t)
	eng.stats.BandKind = "sram"
	eng.stats.MemDepth = 1048576
	before := len(eng.calls)
	for _, id := range []string{"acq.ets", "acq.depth"} {
		if rowByID[id].relevant(c.snap()) {
			t.Errorf("%s in use on SRAM", id)
		}
		m := rowModel(c.snap(), rowByID[id])
		if res := c.SheetSet(id, float64(m.Options[1].Value), ""); res.OK {
			t.Errorf("%s set on SRAM", id)
		}
	}
	if len(eng.calls) != before {
		t.Fatalf("unsupported SRAM controls issued engine commands: %v", eng.calls[before:])
	}
	eng.stats.AcqMode = 4
	if got := rowText(c, "acq.mode"); got != "Precision" {
		t.Fatalf("precision mode shows %q", got)
	}
	if res := c.SheetSet("acq.mode", nil, "Normal"); !res.OK || eng.Snapshot().AcqMode != 0 {
		t.Fatalf("Normal from Precision: %+v", res)
	}
}

// The wired fixed-function keys: the position pushes zero the offset, the
// horizontal push centres the trigger, FORCE asks the engine for one
// untriggered capture.
// TRLC-LINKS: REQ-SDS-135
func TestWiredFixedFunctionKeys(t *testing.T) {
	c, eng, fe := newC(t)
	fe.offReqV = [2]float64{1, -2}
	press(t, c, "ch1pospush", "ch2pospush")
	if fe.offReqV != [2]float64{0, 0} {
		t.Fatalf("offsets %v after position pushes", fe.offReqV)
	}
	eng.stats.TrigPosFrac = 0.3
	press(t, c, "horizpospush")
	if got := eng.calls[len(eng.calls)-1]; got.what != "trigpos" || got.a != 50 {
		t.Fatalf("horizontal push: %+v", got)
	}
	press(t, c, "set50") // no frame: nothing to measure, nothing changes
	press(t, c, "force") // the fake engine has no ForceTrigger: ignored
}

// DEFAULT SETUP returns the setup to power-on: overlays off, decode off, the
// references hidden but kept, any sheet closed; the workspace stays (bug
// hunt 2026-10-10: the key did nothing; #2: decode stayed on).
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-211
func TestDefaultSetupKey(t *testing.T) {
	c, eng, _ := newC(t)
	press(t, c, "f4", "menu")
	c.mu.Lock()
	c.showMeas, c.mathMode, c.curOn, c.zoom, c.persist, c.viewMode = true, 2, true, 10, true, 1
	c.decProto = 1
	c.refs[0] = refWave{has: true, show: true}
	c.mu.Unlock()
	if !c.InjectButton("defaultsetup") {
		t.Fatal("DEFAULT SETUP rejected")
	}
	(<-c.inject)()
	d, v := c.Snapshot()
	if d.MathMode != 0 || d.CurOn || d.Zoom != 1 || d.Persist || d.ViewMode != 0 || v.Sheet != nil {
		t.Fatalf("overlays not reset: %+v", d)
	}
	if d.DecProto != 0 {
		t.Fatalf("decode still on after DEFAULT SETUP: proto %d", d.DecProto)
	}
	if v.WS != 3 || !d.ShowMeas || len(d.MeasItems) == 0 {
		t.Fatalf("MEASURE did not stay with its readouts: ws %d meas %v", v.WS, d.MeasItems)
	}
	c.mu.Lock()
	refShown, refKept := c.refs[0].show, c.refs[0].has
	c.mu.Unlock()
	if refShown || !refKept {
		t.Fatalf("REF A after DEFAULT SETUP: show=%v has=%v (want hidden, kept)", refShown, refKept)
	}
	if len(eng.calls) == 0 {
		t.Fatal("DEFAULT SETUP made no engine calls")
	}
}

// Qualifier limits never invert: stepping Min width past Max width pushes
// Max up, and a slope High stepped under Low pulls Low down (bug hunt #2:
// W min 200 us with W max 20 us, and High 5% under Low 40%, left triggers
// that never fired).
// TRLC-LINKS: REQ-SDS-136
func TestTriggerQualifierLimitsStayOrdered(t *testing.T) {
	c, _, _ := newC(t)
	c.SheetSet("trig.type", nil, "Pulse")
	press(t, c, "trigmenu")
	goRow(t, c, "trig.pulse_min")
	for i := 0; i < 8; i++ {
		pickKey(t, c, "Up")
		if c.pulseMin >= c.pulseMax {
			t.Fatalf("W min %g >= W max %g", c.pulseMin, c.pulseMax)
		}
	}
	c.SheetSet("trig.type", nil, "Slope")
	goRow(t, c, "trig.slope_high")
	for i := 0; i < 20; i++ {
		turn(c, "horizpos", -1)
		if c.slopeLo >= c.slopeHi {
			t.Fatalf("Low %g >= High %g", c.slopeLo, c.slopeHi)
		}
	}
	goRow(t, c, "trig.slope_max")
	for i := 0; i < 20; i++ {
		turn(c, "horizpos", -1)
		if c.slopeMin >= c.slopeMax {
			t.Fatalf("T min %g >= T max %g", c.slopeMin, c.slopeMax)
		}
	}
}

// TRLC-LINKS: REQ-SDS-136
func TestOrderHelpers(t *testing.T) {
	if mn, mx := orderNs(200e3, 20e3, false); mn != 200e3 || mx <= mn {
		t.Fatalf("min edited: %g %g", mn, mx)
	}
	if mn, mx := orderNs(200e3, 20e3, true); mx != 20e3 || mn >= mx {
		t.Fatalf("max edited: %g %g", mn, mx)
	}
	if lo, hi := orderFrac(0.4, 0.05, true); hi != 0.05 && lo >= hi {
		t.Fatalf("hi at floor: %g %g", lo, hi)
	}
	if lo, hi := orderFrac(0.95, 0.95, false); lo >= hi {
		t.Fatalf("lo at top: %g %g", lo, hi)
	}
}

// InjectButtonWait returns only once the button ran (SCPI *RST relies on
// it), and gives up when the panel goroutine never takes it.
// TRLC-LINKS: REQ-SDS-135
func TestInjectButtonWait(t *testing.T) {
	c, _, _ := newC(t)
	c.mu.Lock()
	c.mathMode = 2
	c.mu.Unlock()
	go func() { (<-c.inject)() }()
	if !c.InjectButtonWait("defaultsetup", time.Second) {
		t.Fatal("wait failed")
	}
	if d, _ := c.Snapshot(); d.MathMode != 0 {
		t.Fatal("returned before DEFAULT SETUP ran")
	}
	if c.InjectButtonWait("defaultsetup", 20*time.Millisecond) {
		t.Fatal("no panel goroutine, yet the wait reported success")
	}
}
