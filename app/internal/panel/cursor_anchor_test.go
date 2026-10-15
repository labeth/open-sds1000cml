package panel

import (
	"math"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// The cursors stay on the signal, not on the screen: a time cursor keeps its
// time from the trigger through a timebase and trigger position change, a
// voltage cursor its volts through a V/div and offset change, so dt and dV
// read as before; the screen shows them where that time and level now are,
// an arrow's side beyond the screen (the row says which side).
// TRLC-LINKS: REQ-SDS-217
func TestCursorsStayOnTheSignal(t *testing.T) {
	c, eng, fe := newC(t)
	eng.set(func(st *engine.Stats) { st.TdivS, st.TrigPosFrac = 100e-6, 0.5 })
	if res := c.SheetSet("curs.mode", nil, "Time"); !res.OK {
		t.Fatalf("cursors on: %+v", res)
	}
	for row, at := range map[string]string{"curs.a": "-100us", "curs.b": "200us"} {
		if res := c.SheetSet(row, nil, at); !res.OK {
			t.Fatalf("%s %s: %+v", row, at, res)
		}
	}
	ta, tb := cursorTimes(c.snap())
	if math.Abs(ta+100e-6) > 1e-12 || math.Abs(tb-200e-6) > 1e-12 {
		t.Fatalf("cursors at %s and %s, want -100us and +200us", si(ta, "s"), si(tb, "s"))
	}
	// a slower timebase: same times, nearer the trigger on the screen
	eng.set(func(st *engine.Stats) { st.TdivS = 200e-6 })
	s := c.snap()
	if a, b := cursorTimes(s); a != ta || b != tb {
		t.Fatalf("at 200us/div the cursors moved on the signal: %s, %s", si(a, "s"), si(b, "s"))
	}
	if math.Abs(s.curX[0]-0.45) > 1e-9 || math.Abs(s.curX[1]-0.6) > 1e-9 {
		t.Fatalf("at 200us/div the cursors show at %v of the screen, want 0.45 and 0.6", s.curX)
	}
	// a faster one with the trigger moved: B is beyond the right edge, kept
	eng.set(func(st *engine.Stats) { st.TdivS, st.TrigPosFrac = 10e-6, 0.2 })
	s = c.snap()
	if a, b := cursorTimes(s); a != ta || b != tb || s.curX[1] <= 1 || s.curX[0] >= 0 {
		t.Fatalf("at 10us/div: A %s at %g, B %s at %g of the screen: want both kept, A left of it, B right",
			si(a, "s"), s.curX[0], si(b, "s"), s.curX[1])
	}
	if got := rowText(c, "curs.b"); !strings.Contains(got, "right") || !strings.HasPrefix(got, "+200") {
		t.Fatalf("cursor B's row %q, want its time and that it is off the right", got)
	}
	// a knob step brings an off-screen cursor in from its edge, a hundredth
	// of the screen at the timebase now
	c.measCursor(1, -1, 1)
	if s := c.snap(); math.Abs(s.curX[1]-0.99) > 1e-9 {
		t.Fatalf("a step left from beyond the right edge: at %g of the screen, want 0.99", s.curX[1])
	}

	// volts: through a V/div and an offset change
	eng.set(func(st *engine.Stats) { st.TdivS, st.TrigPosFrac = 100e-6, 0.5 })
	if res := c.SheetSet("curs.mode", nil, "Volts"); !res.OK {
		t.Fatalf("volts cursors: %+v", res)
	}
	s = c.snap()
	c.mu.Lock()
	c.placeCursorLocked(s, 0, 0.25)  // +2 div
	c.placeCursorLocked(s, 1, 0.625) // -1 div
	c.mu.Unlock()
	va, vb := cursorVolts(c.snap(), 0)
	fe.mu.Lock()
	fe.idx[0]--         // a finer V/div
	fe.offReqV[0] = 0.1 // and an offset
	fe.mu.Unlock()
	s = c.snap()
	if a, b := cursorVolts(s, 0); a != va || b != vb {
		t.Fatalf("after V/div and offset the volts cursors read %s, %s, were %s, %s", si(a, "V"), si(b, "V"), si(va, "V"), si(vb, "V"))
	}
	if y := cursorYOf(s, va, 0); math.Abs(s.curY[0]-y) > 1e-12 || math.Abs(y-0.25) < 1e-3 {
		t.Fatalf("cursor A shows at %g of the screen down, its level is at %g (was 0.25 at the old V/div)", s.curY[0], y)
	}
	if dv := math.Abs(vb - va); dv <= 0 {
		t.Fatalf("dV %g", dv)
	}
}
