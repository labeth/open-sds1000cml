// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import "testing"

// The saver box stays on the screen and moves one pixel each way every step,
// bouncing off the edges.
// TRLC-LINKS: REQ-SDS-221
func TestSaverDrifts(t *testing.T) {
	px, py := SaverPos(0)
	if px != 0 || py != 0 {
		t.Fatalf("step 0 at (%d,%d)", px, py)
	}
	reached := [2]bool{}
	for step := 1; step < 20000; step++ {
		x, y := SaverPos(step)
		if x < 0 || y < 0 || x+saverW > W || y+saverBoxH > H {
			t.Fatalf("step %d off the screen at (%d,%d)", step, x, y)
		}
		if d := (x - px) * (y - py); d != 1 && d != -1 {
			t.Fatalf("step %d moved (%d,%d) to (%d,%d)", step, px, py, x, y)
		}
		reached[0] = reached[0] || x+saverW == W
		reached[1] = reached[1] || y+saverBoxH == H
		px, py = x, y
	}
	if !reached[0] || !reached[1] {
		t.Fatalf("the box never reached the right or bottom edge: %v", reached)
	}
}

// The saver picture is the live mini scope and its lines in the box, and
// black everywhere else, whatever the screen showed before.
// TRLC-LINKS: REQ-SDS-221
func TestDrawSaver(t *testing.T) {
	f := goldenYTFrame()
	hud := goldenYTHUD()
	hud.Running = true
	for _, step := range []int{0, 137, 1450} {
		m := NewMemSurface()
		m.Fill(colC1) // the workspace that was shown
		DrawSaver(m, f, hud, step)
		x0, y0 := SaverPos(step)
		var c1, ok int
		for y := 0; y < H; y++ {
			for x := 0; x < W; x++ {
				c := m.At(x, y)
				in := x >= x0 && x < x0+saverW && y >= y0 && y < y0+saverBoxH
				if c != 0 && !in {
					t.Fatalf("step %d: pixel (%d,%d) lit outside the box at (%d,%d)", step, x, y, x0, y0)
				}
				switch {
				case c == colC1 && y < y0+saverH:
					c1++ // the trace
				case c == colOK && y >= y0+saverH:
					ok++ // RUN
				}
			}
		}
		if c1 < saverW/2 || ok == 0 {
			t.Fatalf("step %d: %d trace pixels, %d RUN pixels", step, c1, ok)
		}
	}
}
