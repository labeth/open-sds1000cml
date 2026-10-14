// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
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
// TRLC-LINKS: REQ-SDS-139
func (c *Controller) maskBuildStart() {
	c.mu.Lock()
	if c.maskBuilding || c.frameFn == nil {
		c.mu.Unlock()
		return
	}
	n, tol := c.maskN, maskTols[c.maskTol%len(maskTols)]
	c.maskBuilding = true
	gen := c.maskGen
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
	go c.maskBuildRun(gen, n, tol[0], tol[1], ch, st.TrigPosFrac)
}

// maskBuildRun accumulates the per-column envelope over n distinct locked
// frames (window()-consistent mapping, same as the engine test point) and
// installs the dilated mask, unless the mask was cleared since the build
// started (gen). Runs off the panel goroutine; only touches the engine
// through thread-safe entry points.
// TRLC-LINKS: REQ-SDS-139
func (c *Controller) maskBuildRun(gen uint64, n, tolT, tolV, ch int, posFrac float64) {
	if math.IsNaN(posFrac) || posFrac < 0 || posFrac > 1 {
		posFrac = 0.5
	}
	var lo, hi []uint8
	win, got := 0, 0
	var sampleS, tdivS float64
	var peak bool
	var lastSeq uint64
	start := time.Now()
	for tries := 0; got < n && tries < n*20 && !c.maskCleared(gen); tries++ {
		// no locked frame at all: the trigger is off the signal; say so now,
		// not after n*20 tries (2.5 min at 128 frames)
		if got == 0 && time.Since(start) > maskNoLockWait {
			break
		}
		time.Sleep(60 * time.Millisecond)
		ok := false
		c.frameFn(func(f *engine.Frame) {
			if f == nil || f.Seq == lastSeq || f.EdgeX < 0 || f.SampleS <= 0 || f.IsEnv || (f.PeakDetect && !f.Ordered && c.eng.Snapshot().AcqMode != engine.AcqPeak) {
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
			if win != w || sampleS != f.SampleS || tdivS != f.TdivS || peak != f.PeakDetect {
				// A stopped full record can precede the first live preview.
				// Start a new envelope when acquisition geometry changes.
				got = 0
				sampleS, tdivS, peak = f.SampleS, f.TdivS, f.PeakDetect
				win = w
				lo = make([]uint8, win)
				hi = make([]uint8, win)
				for j := range lo {
					lo[j] = 255
				}
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
	cleared := c.maskGen != gen
	c.mu.Unlock()
	if cleared { // the mask was cleared while this one was built
		return
	}
	if got < 8 || win == 0 {
		c.maskSetMsg("MASK: build failed - no locked frames: put the trigger level on the signal")
		return
	}
	m := engine.BuildMaskFromEnvelope(lo, hi, win, tolT, tolV, ch)
	if m == nil {
		c.maskSetMsg("MASK: build failed")
		return
	}
	m.FrameIdent, m.TdivS, m.SampleS, m.PeakDetect = true, tdivS, sampleS, peak
	c.eng.SetMask(m)
	c.noteMaskSetup()
	c.mu.Lock()
	auto := c.maskAutoTest
	c.maskAutoTest = false
	c.mu.Unlock()
	if auto { // the COMPARE MASK row builds and starts counting in one push
		c.eng.SetMaskMode(1)
		c.maskSetMsg("")
		return
	}
	c.maskSetMsg(fmt.Sprintf("MASK: ready (%d frames, C%d) - set Mode to Test", got, ch+1))
}

// maskNoLockWait is how long a build waits for its first locked frame.
var maskNoLockWait = 5 * time.Second

// maskCleared reports whether the mask was cleared since gen.
// TRLC-LINKS: REQ-SDS-139
func (c *Controller) maskCleared(gen uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maskGen != gen
}

// clearMask removes the mask: its test goes off, its counts and captured
// failures are zeroed, and a build or master stack under way, which would
// install a mask when done, is stopped. DEFAULT SETUP clears it this way.
// TRLC-LINKS: REQ-SDS-139, REQ-SDS-218
func (c *Controller) clearMask() {
	c.mu.Lock()
	c.maskGen++
	c.maskAutoTest = false
	c.masterPend = nil
	stacking := c.stack != nil
	c.mu.Unlock()
	if stacking {
		c.stackCancel("", false)
	}
	c.eng.SetMaskMode(0)
	c.eng.SetMask(nil)
	c.eng.ClearMaskFails()
	c.maskSetMsg("")
}

// ClearMask is clearMask for the web pages (their Clear mask: /api/mask with
// no envelope), run on the panel goroutine like a key.
// TRLC-LINKS: REQ-SDS-139, REQ-SDS-162
func (c *Controller) ClearMask() {
	c.onPanel(func() {
		c.clearMask()
		c.afterInput()
	})
}

// clearMaskAction is Clear mask on the COMPARE sheet and the MASK row's push
// after stop on fail: clearMask, said on the LCD.
// TRLC-LINKS: REQ-SDS-139, REQ-SDS-218
func (c *Controller) clearMaskAction() error {
	c.mu.Lock()
	stacking := c.stack != nil
	c.mu.Unlock()
	c.clearMask()
	if stacking {
		c.toast("Mask cleared, and the master being stacked cancelled", 1600*time.Millisecond)
	} else {
		c.toast("Mask cleared", 1200*time.Millisecond)
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-139
func (c *Controller) maskSetMsg(s string) {
	c.mu.Lock()
	c.maskMsg = s
	c.mu.Unlock()
}

// MaskStatus returns the build/status line for the LCD HUD ("" when idle).
// TRLC-LINKS: REQ-SDS-139
func (c *Controller) MaskStatus() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maskMsg
}
