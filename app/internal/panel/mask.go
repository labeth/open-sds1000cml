package panel

import (
	"fmt"
	"math"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// Device-side mask testing flow (docs/zonemask-plan.md §3): the MASK page
// (re-press ACQUIRE past REF) builds a golden envelope from N live locked
// frames on the TRIGGER SOURCE channel, dilates it by the selected tolerance
// preset and installs it in the engine. Zones stay web-only in v1 (drawing
// rectangles with knobs is a poor fit; the mask flow is the device-native use).

// maskTols are the Tol softkey presets: ±samples horizontal / ±codes vertical.
// The floor rule (plan §1.4): horizontal must cover trigger-point jitter,
// vertical ≥ ~3× the noise σ.
var maskTols = [][2]int{{3, 6}, {5, 8}, {8, 12}, {12, 20}}

// maskBuildStart launches a build with the page's current settings. Refuses
// while one is running, without a frame source, or when the trigger-source
// channel is not DC-coupled (zone/mask test RAW capture codes; AC/GND are
// display-only transforms here — what the user sees would not be what is
// tested; same guard as the web).
func (c *Controller) maskBuildStart() {
	c.mu.Lock()
	if c.maskBuilding || c.frameFn == nil {
		c.mu.Unlock()
		return
	}
	n, tol := c.maskN, maskTols[c.maskTol%len(maskTols)]
	c.maskBuilding = true
	c.mu.Unlock()
	st := c.eng.Snapshot()
	ch := st.TrigSource & 1
	if c.fe != nil && c.fe.Coupling(ch) != analog.CplDC {
		c.maskSetMsg(fmt.Sprintf("MASK: set C%d coupling to DC first", ch+1))
		c.mu.Lock()
		c.maskBuilding = false
		c.mu.Unlock()
		return
	}
	c.eng.SetRunning(true) // the build needs live frames
	c.maskSetMsg(fmt.Sprintf("MASK: building 0/%d", n))
	go c.maskBuildRun(n, tol[0], tol[1], ch, st.TrigPosFrac)
}

// maskBuildRun accumulates the per-column envelope over n distinct locked
// frames (window()-consistent mapping, same as the engine test point) and
// installs the dilated mask. Runs off the panel goroutine; only touches the
// engine through thread-safe entry points.
func (c *Controller) maskBuildRun(n, tolT, tolV, ch int, posFrac float64) {
	if posFrac <= 0 || posFrac > 1 {
		posFrac = 0.5
	}
	var lo, hi []uint8
	win, got := 0, 0
	var lastSeq uint64
	for tries := 0; got < n && tries < n*20; tries++ {
		time.Sleep(60 * time.Millisecond)
		ok := false
		c.frameFn(func(f *engine.Frame) {
			if f == nil || f.Seq == lastSeq || f.EdgeX < 0 || f.SampleS <= 0 || f.IsEnv {
				return
			}
			lastSeq = f.Seq
			sig := f.C1
			if ch == 1 {
				sig = f.C2
			}
			valid := f.Valid
			if valid > len(sig) {
				valid = len(sig)
			}
			if valid == 0 {
				return
			}
			w := f.WinCols
			if w <= 0 || w > valid {
				w = valid
			}
			if win == 0 {
				win = w
				lo = make([]uint8, win)
				hi = make([]uint8, win)
				for j := range lo {
					lo[j] = 255
				}
			}
			if w != win {
				return // band changed mid-build: skip the frame
			}
			left := int(math.Round(f.EdgeX - float64(win)*posFrac))
			for j := 0; j < win; j++ {
				s := left + j
				if s < 0 || s >= valid {
					continue
				}
				v := sig[s]
				if v < lo[j] {
					lo[j] = v
				}
				if v > hi[j] {
					hi[j] = v
				}
			}
			ok = true
		})
		if ok {
			got++
			if got%8 == 0 {
				c.maskSetMsg(fmt.Sprintf("MASK: building %d/%d", got, n))
			}
		}
	}
	c.mu.Lock()
	c.maskBuilding = false
	c.mu.Unlock()
	if got < 8 || win == 0 {
		c.maskSetMsg("MASK: build failed - no locked frames (trigger on-signal?)")
		return
	}
	m := engine.BuildMaskFromEnvelope(lo, hi, win, tolT, tolV, ch)
	if m == nil {
		c.maskSetMsg("MASK: build failed")
		return
	}
	c.eng.SetMask(m)
	c.maskSetMsg(fmt.Sprintf("MASK: ready (%d frames, C%d) - set Mode to Test", got, ch+1))
}

func (c *Controller) maskSetMsg(s string) {
	c.mu.Lock()
	c.maskMsg = s
	c.mu.Unlock()
}

// MaskStatus returns the build/status line for the LCD HUD ("" when idle).
func (c *Controller) MaskStatus() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maskMsg
}
