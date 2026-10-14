// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"testing"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// zoomFrame is a live envelope frame, flat at code level, with a view window
// over the 100 values about its trigger: a square between codes 60 and 200,
// 10 cycles across.
// TRLC-LINKS: REQ-SDS-021
func zoomFrame(level uint8) (*engine.Frame, *engine.ViewWindow) {
	const n, vn, period = 4096, 1540, 154
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n, WinCols: n, EdgeX: n / 2, Seq: 1,
		PeakDetect: true, Ordered: true, Trigd: true, TdivS: 200e-6, DisplayedS: 200e-6, SampleS: 488e-9}
	for i := range n {
		f.C1[i], f.C2[i] = level, level
	}
	v := &engine.ViewWindow{C1: make([]uint8, vn), C2: make([]uint8, vn), Valid: vn, From: n/2 - 50, To: n/2 + 50, PeakDetect: true}
	for i := range vn {
		c := uint8(60)
		if i%period < period/2 {
			c = 200
		}
		v.C1[i], v.C2[i] = c, level
	}
	return f, v
}

// highRuns counts the stretches of the plot where C1 is drawn on row y.
// TRLC-LINKS: REQ-SDS-021
func highRuns(sf *MemSurface, y int) int {
	runs, in := 0, false
	for x := 0; x < W-railW; x++ {
		on := sf.At(x, y) == colC1
		if on && !in {
			runs++
		}
		in = on
	}
	return runs
}

// Zoomed into a live envelope, the scope draws the frame's view window - the
// zoomed stretch read again at full detail - not the screen's buckets
// stretched: 50x of a 4096-value envelope is 81 of its values, 8 cycles of
// the view's square; a view of another stretch is not used.
// TRLC-LINKS: REQ-SDS-021
func TestZoomDrawsTheViewWindow(t *testing.T) {
	hud := HUD{C1VdivV: 1, C2VdivV: 1, TdivS: 200e-6, TrigPosFrac: 0.5, ShowC1: true, ChanExplicit: true, Running: true, Zoom: 50}
	f, v := zoomFrame(128)
	f.Views[engine.ViewScope] = v
	got, vwin, vxc, ok := zoomView(f, hud, f.Valid)
	if !ok || got != v || vwin != 1247 || vxc < 769 || vxc > 771 {
		t.Fatalf("zoomView: ok %v, %d values from centre %.1f; want 1247 (81 values x 15.4) about 770", ok, vwin, vxc)
	}
	render := func(f *engine.Frame, h HUD) *MemSurface {
		sf := NewMemSurface()
		Render(sf, f, h, true)
		return sf
	}
	if r := highRuns(render(f, hud), sampleToY(200)); r < 8 || r > 9 {
		t.Fatalf("zoomed: %d high stretches of the square, want 8-9", r)
	}
	f.Views[engine.ViewScope] = nil
	if r := highRuns(render(f, hud), sampleToY(200)); r != 0 {
		t.Fatalf("no view: %d high stretches, want the flat envelope", r)
	}
	moved := *v
	moved.From, moved.To = v.From+30, v.To+30 // the zoom moved since it was read
	f.Views[engine.ViewScope] = &moved
	if _, _, _, ok := zoomView(f, hud, f.Valid); ok {
		t.Fatal("a view of another stretch was used")
	}
	hud.Zoom = 1
	f.Views[engine.ViewScope] = v
	if _, _, _, ok := zoomView(f, hud, f.Valid); ok {
		t.Fatal("the view was used unzoomed")
	}
}

// AC coupling moves the zoom's detail by the whole trace's DC, so it sits
// where the unzoomed trace does.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-096
func TestZoomViewCoupledAsTheTrace(t *testing.T) {
	f, v := zoomFrame(150) // the trace's mean 150: AC moves it down by 22
	f.Views[engine.ViewScope] = v
	hud := HUD{C1VdivV: 1, C2VdivV: 1, TdivS: 200e-6, TrigPosFrac: 0.5, ShowC1: true, ChanExplicit: true, Running: true, Zoom: 50, Cpl1: analog.CplAC}
	sf := NewMemSurface()
	Render(sf, f, hud, true)
	if r := highRuns(sf, sampleToY(178)); r < 8 || r > 9 {
		t.Fatalf("AC: %d high stretches on the shifted row, want 8-9", r)
	}
	if r := highRuns(sf, sampleToY(200)); r != 0 {
		t.Fatalf("AC: the detail drawn unshifted (%d stretches)", r)
	}
}

// The trigger pointer marks where the screen shows the trigger: zoomed, the
// screen is centred on the trigger plus the pan, and a trigger panned off the
// screen has no pointer (the web's marker alike).
// TRLC-LINKS: REQ-SDS-021
func TestTriggerPointerFollowsZoom(t *testing.T) {
	const n = 40000
	f := &engine.Frame{C1: make([]uint8, n), Valid: n, WinCols: n / 4, EdgeX: 12000, TdivS: 1e-3}
	p := plotArea{w: 560}
	for _, tc := range []struct {
		zoom     int
		off, pos float64
		want     int
	}{{1, 0, 0.3, 168}, {10, 0, 0.3, 280}, {10, 0.01, 0.3, 56}, {10, -0.01, 0.8, 504}, {10, 0.05, 0.5, -1}} {
		hud := HUD{Zoom: tc.zoom, ZoomOff: tc.off, TrigPosFrac: tc.pos}
		if got := trigX(p, f, hud); got != tc.want {
			t.Errorf("zoom %d pan %g trigger at %g: pointer at column %d, the trigger shows at %d", tc.zoom, tc.off, tc.pos, got, tc.want)
		}
	}
}
