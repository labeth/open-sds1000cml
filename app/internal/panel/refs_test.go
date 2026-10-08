// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"testing"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// A reference is the screen at the save: from a deep stopped record it holds
// the window around the trigger, with the edge at the trigger position, and
// the save's analog V/div.
// TRLC-LINKS: REQ-SDS-138
func TestRefSavesTheScreenWindow(t *testing.T) {
	c, eng, fe := newC(t)
	eng.stats.TrigPosFrac = .25
	n := 1 << 20
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n, WinCols: 8000, EdgeX: 700000}
	for i := range f.C1 {
		f.C1[i], f.C2[i] = 128, 128
		if i >= 700000 {
			f.C1[i] = 200
		}
	}
	c.SetFrameSource(func(fn func(*engine.Frame)) { fn(f) })
	c.captureRef(0)
	r := c.RefView()[0]
	if len(r.C1) != RefCols || !r.Show {
		t.Fatalf("ref %d columns, show %v", len(r.C1), r.Show)
	}
	edge := -1
	for x, v := range r.C1 {
		if v == 200 {
			edge = x
			break
		}
	}
	if edge != RefCols/4 {
		t.Fatalf("edge at column %d, want %d", edge, RefCols/4)
	}
	if r.Vdiv[0] != analog.AnalogVdiv(fe.idx[0]) {
		t.Fatalf("saved V/div %v", r.Vdiv[0])
	}
}

// A soft-key press cycles a list option past its end back to the start; the
// ADJUST knob stops at the ends.
// TRLC-LINKS: REQ-SDS-136
func TestSoftkeyPressWrapsOptions(t *testing.T) {
	c, _, _ := newC(t)
	c.button(btnHorizMenu)
	var seen []int
	for i := 0; i < 6; i++ {
		c.button(btnF3)
		seen = append(seen, c.zoom)
	}
	if want := []int{2, 5, 10, 20, 50, 1}; fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("zoom presses %v, want %v", seen, want)
	}
	c.zoom = 50
	c.menuCycle(2, +1) // the ADJUST knob
	if c.zoom != 50 {
		t.Fatalf("knob past the end: zoom %d", c.zoom)
	}
}

// The pulse page shows and steps the qualifiers the engine uses, set on the
// web or over SCPI, and its width ladder reaches past 50 us.
// TRLC-LINKS: REQ-SDS-136
func TestPulsePageFollowsEngine(t *testing.T) {
	c, eng, _ := newC(t)
	eng.stats.TrigType = 1
	eng.stats.TrigQual = engine.TrigQual{PulseLvl: .5, PulseMinNs: 20000, PulseMaxNs: 150000, PulseCond: 2, SlopeLo: .2, SlopeHi: .8, SlopeMinNs: 100, SlopeMaxNs: 1000}
	c.button(btnTrigMenu)
	c.button(btnTrigMenu) // the qualifier page
	v := c.MenuView()
	if v.Items[1].Value != "20 us" || v.Items[2].Value != "150 us" {
		t.Fatalf("page shows %v", v.Items)
	}
	c.button(btnF3) // W max: 150 us -> next step from the engine's value
	if c.pulseMax != 200000 || c.pulseMin != 20000 {
		t.Fatalf("stepped to min %v max %v", c.pulseMin, c.pulseMax)
	}
	if got := stepNs(5e9, 1, false); got != 1e10 {
		t.Fatalf("ladder top %v", got)
	}
	if got := stepNs(1e10, 1, true); got != 10 {
		t.Fatalf("press past the top %v", got)
	}
}
