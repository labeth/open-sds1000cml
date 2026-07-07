package panel

import (
	"math/rand"
	"testing"
)

// Panel chaos: random button presses and knob steps in every order — the
// exact dispatch the SIGIO decoder and /api/panel drive. The controller must
// never panic (a panic kills the whole app on the device) and its shadow
// state must stay inside physical bounds afterwards.
func TestPanelChaos(t *testing.T) {
	buttons := []int{
		btnRunStop, btnSingle, btnAuto, btnCh1VdivPush, btnCh2VdivPush,
		btnTrigLvlPush, btnUtility, btnAdjustPsh,
		btnF1, btnF2, btnF3, btnF4, btnF5,
		btnTrigMenu, btnAcquire, btnDisplay, btnHorizMenu, btnMenuOnOff,
		btnCursors, btnRef,
	}
	var knobs []string
	for k := range knobNames {
		knobs = append(knobs, k)
	}
	for _, seed := range []int64{7, 4242, 987654321} {
		eng := &fakeEng{}
		c := New(eng, nil, -1, []float64{100e-9, 1e-6, 50e-6, 500e-6, 5e-3, 100e-3}, 500e-6, t.Logf)
		rng := rand.New(rand.NewSource(seed))
		// the LCD render goroutine reads these ~20 Hz while buttons dispatch
		stopRender := make(chan struct{})
		renderDone := make(chan struct{})
		go func() {
			defer close(renderDone)
			for {
				select {
				case <-stopRender:
					return
				default:
					_ = c.MenuView()
					_ = c.SuperresView()
					_ = c.RefView()
					_ = c.MaskStatus()
				}
			}
		}()
		for i := 0; i < 5000; i++ {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("seed %d action %d panicked: %v", seed, i, r)
					}
				}()
				if rng.Intn(2) == 0 {
					c.button(buttons[rng.Intn(len(buttons))])
				} else {
					dir := 1
					if rng.Intn(2) == 0 {
						dir = -1
					}
					c.resync()
					c.dispatch(knobs[rng.Intn(len(knobs))], dir, 1+rng.Intn(3))
				}
			}()
		}
		close(stopRender)
		<-renderDone
		// state invariants after the storm
		c.mu.Lock()
		if c.menuPage < pgNone || c.menuPage > pgMask {
			t.Fatalf("seed %d: menuPage out of range: %d", seed, c.menuPage)
		}
		if c.menuSel < 0 || c.menuSel > 4 {
			t.Fatalf("seed %d: menuSel out of range: %d", seed, c.menuSel)
		}
		if c.tdivIdx < 0 || c.tdivIdx >= len(c.tdivs) {
			t.Fatalf("seed %d: tdivIdx out of range: %d", seed, c.tdivIdx)
		}
		if c.zoom < 1 {
			t.Fatalf("seed %d: zoom < 1: %d", seed, c.zoom)
		}
		c.mu.Unlock()
		// the menu view must still render without panicking
		_ = c.MenuView()
	}
}
