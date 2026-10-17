// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import "testing"

// Cursors keep their place on the signal while they are on. Turned on with
// both off the screen (placed at 500us/div, now 2us/div: -750us and +750us
// against a 20us screen) they come home onto the screen instead of showing
// "dt 1.5ms" with nothing to see.
// TRLC-LINKS: REQ-SDS-217
func TestCursorsTurnedOnOffScreenComeHome(t *testing.T) {
	c, _, _ := newC(t)
	c.setTdivNearest(500e-6)
	_ = c.snap() // the cursors anchor at their home on this screen
	c.setTdivNearest(2e-6)
	if s := c.snap(); s.curX[0] >= 0 && s.curX[1] <= 1 {
		t.Fatalf("setup: the cursors are on the 2us/div screen already: %v", s.curX)
	}
	c.setCursorMode(1)
	s := c.snap()
	for k := 0; k < 2; k++ {
		if s.curX[k] < 0 || s.curX[k] > 1 {
			t.Fatalf("time cursors turned on off the screen: %v", s.curX)
		}
	}
	// on, they keep their place on the signal across a timebase change
	c.setTdivNearest(500e-6)
	if s := c.snap(); s.curX[0] > 0.49 && s.curX[1] < 0.51 && s.curX[0] < 0.51 {
		return
	}
	t.Fatalf("cursors left the signal's place: %v", c.snap().curX)
}
