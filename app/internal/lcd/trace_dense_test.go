// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"testing"

	"open-sds/app/internal/engine"
)

// A record denser than the screen draws each column's full min..max: a
// one-sample spike in a 1 M-sample stopped record stays on screen.
// TRLC-LINKS: REQ-SDS-021
func TestDenseTraceKeepsSpikes(t *testing.T) {
	sig := make([]uint8, 1<<20)
	for i := range sig {
		sig[i] = 128
	}
	spike := 123457 // one sample, between two columns' sample points
	sig[spike] = 228
	sf := NewMemSurface()
	win := len(sig)
	drawTrace(sf, sig, win, float64(win)/2, false, 0xffff, .5)
	x := spike * W / win
	top := sampleToY(228)
	found := false
	for dx := -1; dx <= 1; dx++ {
		if sf.At(x+dx, top) == 0xffff {
			found = true
		}
	}
	if !found {
		t.Fatalf("spike at sample %d (column %d) not drawn up to y=%d", spike, x, top)
	}
}

// Running, a frame from another timebase is not drawn under the new label.
// TRLC-LINKS: REQ-SDS-021
func TestStaleTimebaseFrameNotDrawn(t *testing.T) {
	f := &engine.Frame{Seq: 1, C1: make([]uint8, 2048), C2: make([]uint8, 2048), Valid: 2048, WinCols: 2048, SampleS: 1e-3, TdivS: 0.1, EdgeX: -1}
	for i := range f.C1 {
		f.C1[i], f.C2[i] = 200, 200
	}
	count := func(hud HUD) int {
		sf := NewMemSurface()
		Render(sf, f, hud, true)
		n := 0
		for x := 0; x < W; x++ {
			if sf.At(x, sampleToY(200)) == colC1 {
				n++
			}
		}
		return n
	}
	hud := HUD{Running: true, TdivS: 0.1, C1VdivV: 1, C2VdivV: 1, TwoChan: true, RecordS: 137}
	if count(hud) < W/2 {
		t.Fatal("matching frame not drawn")
	}
	hud.TdivS = 10
	if n := count(hud); n != 0 {
		t.Fatalf("stale frame drawn across %d columns", n)
	}
}
