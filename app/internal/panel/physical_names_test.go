// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"reflect"
	"testing"
	"time"
)

// TRLC-LINKS: REQ-SDS-135
func TestUnassignedPhysicalNamesReachDispatcherWithoutEffects(t *testing.T) {
	for _, name := range []string{"help", "tdivpush"} {
		t.Run(name, func(t *testing.T) {
			c, eng, fe := newC(t)
			before := c.MenuView()
			calls, analogCalls := len(eng.calls), len(fe.calls)
			if !c.InjectButton(name) {
				t.Fatal("physical key was rejected")
			}
			select {
			case dispatch := <-c.inject:
				dispatch()
			default:
				t.Fatal("accepted physical key never reached the dispatch queue")
			}
			if len(eng.calls) != calls || len(fe.calls) != analogCalls || !reflect.DeepEqual(before, c.MenuView()) {
				t.Fatal("unassigned key changed instrument state")
			}
		})
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
	// A softkey press wraps past the ends (bug hunt #2: F5 stuck at 1 s).
	if got := nextHoldoff(1, 1, true); got != 0 {
		t.Errorf("press past 1 s: %g, want Off", got)
	}
	if got := nextHoldoff(0, -1, true); got != 1 {
		t.Errorf("press below Off: %g, want 1 s", got)
	}
}

// TRLC-LINKS: REQ-SDS-136
func TestEmptyTriggerQualifierKeysDoNotMoveFocus(t *testing.T) {
	for _, tc := range []struct{ kind, firstEmpty int }{{1, 4}, {3, 3}} {
		c, eng, _ := newC(t)
		eng.stats.TrigType = tc.kind
		c.openMenu(pgTrigQ)
		before := c.MenuView()
		for slot := tc.firstEmpty; slot < 5; slot++ {
			c.menuCycle(slot, 1)
			if !reflect.DeepEqual(before, c.MenuView()) {
				t.Fatalf("type %d empty slot %d changed view", tc.kind, slot)
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-136
func TestSRAMAcquisitionMenuUsesBackendCapabilities(t *testing.T) {
	c, eng, _ := newC(t)
	eng.stats.BandKind = "sram"
	eng.stats.MemDepth = 1048576
	c.openMenu(pgAcq)
	before := len(eng.calls)
	c.menuCycle(2, 1)
	c.menuCycle(3, 1)
	if len(eng.calls) != before {
		t.Fatal("unsupported SRAM controls issued engine commands")
	}
	eng.stats.AcqMode = 4
	if c.MenuView().Items[0].Value != "Precision" {
		t.Fatal("precision mode mislabeled")
	}
	c.menuCycle(0, 1)
	if got := eng.calls[len(eng.calls)-1]; got.what != "acq" || got.a != 0 {
		t.Fatalf("precision did not wrap to normal: %+v", got)
	}
}

// The wired fixed-function keys: position pushes zero the offset, the
// horizontal push centres the trigger, FORCE asks the engine for one
// untriggered capture.
// TRLC-LINKS: REQ-SDS-135
func TestWiredFixedFunctionKeys(t *testing.T) {
	press := func(c *Controller, name string) {
		if !c.InjectButton(name) {
			t.Fatalf("%s rejected", name)
		}
		(<-c.inject)()
	}
	c, eng, fe := newC(t)
	fe.offReqV = [2]float64{1, -2}
	press(c, "ch1pospush")
	press(c, "ch2pospush")
	if fe.offReqV != [2]float64{0, 0} {
		t.Fatalf("offsets %v after position pushes", fe.offReqV)
	}
	press(c, "horizpospush")
	if got := eng.calls[len(eng.calls)-1]; got.what != "trigpos" || got.a != 50 {
		t.Fatalf("horizontal push: %+v", got)
	}
	press(c, "set50") // no frame: nothing to measure, nothing changes
	press(c, "force") // the fake engine has no ForceTrigger: ignored
}

// DEFAULT SETUP returns the setup to power-on: overlays off, both channels
// 1 V/div at 0 V, AUTO and running (bug hunt 2026-10-10: the key did nothing).
// TRLC-LINKS: REQ-SDS-136
func TestDefaultSetupKey(t *testing.T) {
	c, eng, _ := newC(t)
	c.mu.Lock()
	c.showMeas, c.mathMode, c.curOn, c.zoom, c.persist, c.viewMode = true, 2, true, 10, true, 1
	c.decProto = 1 // bug hunt #2: decode stayed on through DEFAULT SETUP
	c.refs[0] = refWave{has: true, show: true}
	c.mu.Unlock()
	if !c.InjectButton("defaultsetup") {
		t.Fatal("DEFAULT SETUP rejected")
	}
	(<-c.inject)()
	v := c.MenuView()
	if v.ShowMeas || v.MathMode != 0 || v.CurOn || v.Zoom != 1 || v.Persist || v.ViewMode != 0 || v.Open {
		t.Fatalf("overlays not reset: %+v", v)
	}
	if v.DecProto != 0 {
		t.Fatalf("decode still on after DEFAULT SETUP: proto %d", v.DecProto)
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

// Qualifier limits never invert: stepping W min past W max pushes W max up,
// and a slope High wrapped under Low pulls Low down (bug hunt #2: W min 200 us
// with W max 20 us, and High 5% under Low 40%, left triggers that never fired).
// TRLC-LINKS: REQ-SDS-136
func TestTriggerQualifierLimitsStayOrdered(t *testing.T) {
	c, eng, _ := newC(t)
	eng.stats.TrigType = 1
	c.openMenu(pgTrigQ)
	c.pressWrap = true
	for i := 0; i < 6; i++ {
		c.menuCycle(1, 1) // W min up
		if c.pulseMin >= c.pulseMax {
			t.Fatalf("W min %g >= W max %g", c.pulseMin, c.pulseMax)
		}
	}
	eng.stats.TrigType = 2
	for i := 0; i < 25; i++ {
		c.menuCycle(1, 1) // High up, wrapping
		if c.slopeLo >= c.slopeHi {
			t.Fatalf("Low %g >= High %g", c.slopeLo, c.slopeHi)
		}
		c.menuCycle(3, 1) // T max up, wrapping
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

// Depth labels match the web's binary units (bug hunt #2: LCD "524k").
// TRLC-LINKS: REQ-SDS-136
func TestDepthLabelBinary(t *testing.T) {
	for n, want := range map[int]string{524288: "512k", 1048576: "1M", 2048: "2k", 14336: "14k", 20480: "20k", 9000: "9k", 500: "500"} {
		if got := depthLabel(n); got != want {
			t.Errorf("depthLabel(%d) = %q, want %q", n, got, want)
		}
	}
}

// InjectButtonWait returns only once the button ran (SCPI *RST relies on
// it), and gives up when the panel goroutine never takes it.
// TRLC-LINKS: REQ-SDS-135
func TestInjectButtonWait(t *testing.T) {
	c, _, _ := newC(t)
	c.mu.Lock()
	c.showMeas = true
	c.mu.Unlock()
	go func() { (<-c.inject)() }()
	if !c.InjectButtonWait("defaultsetup", time.Second) {
		t.Fatal("wait failed")
	}
	if c.MenuView().ShowMeas {
		t.Fatal("returned before DEFAULT SETUP ran")
	}
	if c.InjectButtonWait("defaultsetup", 20*time.Millisecond) {
		t.Fatal("no panel goroutine, yet the wait reported success")
	}
}
