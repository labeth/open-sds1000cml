// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math/rand"
	"testing"

	"open-sds/app/internal/engine"
)

// Panel chaos: random button presses and knob steps in every order, the
// exact dispatch the SIGIO decoder and /api/panel drive, while the LCD and
// the remote read the state and frames are analysed concurrently. The
// controller must never panic (a panic kills the whole app on the device)
// and its state must stay inside its bounds afterwards.
// TRLC-LINKS: REQ-SDS-135, REQ-SDS-136, REQ-SDS-211
func TestPanelChaos(t *testing.T) {
	var buttons []int
	for _, n := range []string{"runstop", "single", "auto", "ch1vdivpush", "ch2vdivpush", "triglvlpush", "utility",
		"adjustpush", "f1", "f2", "f3", "f4", "f5", "trigmenu", "acquire", "display", "horizmenu", "menu", "measure",
		"cursors", "math", "ref", "ch1", "ch2", "print", "saverecall", "set50", "defaultsetup", "force", "help",
		"ch1pospush", "ch2pospush", "tdivpush", "horizpospush"} {
		code, ok := nameCode(n)
		if !ok {
			t.Fatalf("no button %s", n)
		}
		buttons = append(buttons, code)
	}
	var knobs []string
	for k := range knobNames {
		knobs = append(knobs, k)
	}
	frames := []*engine.Frame{uartFrame([]string{"Hi\r\n"}, 460800, 60e-6, 100e-9, 8000), toneFrame(""), smallFrame()}
	for _, seed := range []int64{7, 4242, 987654321} {
		eng := &fakeEng{}
		c := New(eng, nil, -1, []float64{100e-9, 1e-6, 50e-6, 500e-6, 5e-3, 100e-3}, 500e-6, t.Logf)
		var seq uint64
		cur := frames[0]
		c.SetFrameSource(func(fn func(*engine.Frame)) { fn(cur) })
		rng := rand.New(rand.NewSource(seed))
		// the LCD render goroutine and the remote read these while keys dispatch
		stopRender := make(chan struct{})
		renderDone := make(chan struct{})
		go func() {
			defer close(renderDone)
			for {
				select {
				case <-stopRender:
					return
				default:
					_, _ = c.Snapshot()
					_ = c.PanelStatus()
					_ = c.SuperresView()
					_ = c.RefView()
					_ = c.MaskStatus()
					_ = c.MatchEditor()
				}
			}
		}()
		for i := 0; i < 4000; i++ {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("seed %d action %d panicked: %v", seed, i, r)
					}
				}()
				switch rng.Intn(5) {
				case 0, 1:
					c.button(buttons[rng.Intn(len(buttons))])
				case 2, 3:
					dir := 1
					if rng.Intn(2) == 0 {
						dir = -1
					}
					c.resync()
					c.dispatch(knobs[rng.Intn(len(knobs))], dir, 1+rng.Intn(3))
				default:
					seq++
					f := *frames[rng.Intn(len(frames))]
					f.Seq = seq
					c.OnFrame(&f)
				}
			}()
			c.mu.Lock()
			busy := c.autosetBusy
			c.mu.Unlock()
			if busy { // AUTO ran without a front end: let it finish before the next key
				c.button(btnAuto)
			}
		}
		close(stopRender)
		<-renderDone
		c.srCancel("cancelled")
		c.mu.Lock()
		u := c.ui
		if u.ws < 0 || int(u.ws) >= 5 || u.tab < 0 || u.tab >= numTabs || u.rowAt[u.tab] < 0 || u.rowAt[u.tab] >= len(sheetTabs[u.tab].rows) {
			t.Fatalf("seed %d: UI out of range: ws %d tab %d row %d", seed, u.ws, u.tab, u.rowAt[u.tab])
		}
		if c.tdivIdx < 0 || c.tdivIdx >= len(c.tdivs) || c.zoom < 1 {
			t.Fatalf("seed %d: tdivIdx %d zoom %d", seed, c.tdivIdx, c.zoom)
		}
		c.mu.Unlock()
		_, _ = c.Snapshot() // the view still builds
	}
}
