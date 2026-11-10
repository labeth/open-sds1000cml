// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"fmt"
	"time"

	"github.com/labeth/open-dso-core/render"
	"github.com/labeth/open-dso-core/scopeapp"
	"github.com/labeth/open-sds1000cml/app/internal/lcd"
)

// lcdScreen is the device panel as the core's display loop draws it: the
// RGB565 back buffer, the afterglow layer, the page-flipped copy to fb0
// and the backlight on GPIO7.
type lcdScreen struct {
	fb            *lcd.FB
	back, persist *render.Image565
}

var _ scopeapp.Screen = (*lcdScreen)(nil)

// openLCD brings the panel up (spec 07 §1.1) and gives the board's display
// its framebuffer; an error leaves the scope running headless.
// TRLC-LINKS: REQ-SDS-168
func (c *composition) openLCD() (scopeapp.Screen, error) {
	if err := lcd.Bringup(logf); err != nil {
		return nil, err
	}
	fb, err := lcd.OpenFB()
	if err != nil {
		return nil, fmt.Errorf("lcd: %w", err)
	}
	if c.lcd != nil {
		c.lcd.Attach(fb)
	}
	logf("lcd: renderer up on /dev/fb0")
	return &lcdScreen{fb: fb, back: lcd.NewBackBuffer(), persist: lcd.NewBackBuffer()}, nil
}

// TRLC-LINKS: REQ-SDS-168
func (s *lcdScreen) Canvas() render.Canvas { return s.back }

// TRLC-LINKS: REQ-SDS-168
func (s *lcdScreen) Layer() render.Layer { return s.persist }

// TRLC-LINKS: REQ-SDS-168
func (s *lcdScreen) Present() { s.fb.Present(s.back) }

// TRLC-LINKS: REQ-SDS-168
func (s *lcdScreen) PresentRows(y0, y1 int) { s.fb.PresentRows(s.back, y0, y1) }

// TRLC-LINKS: REQ-SDS-168, REQ-SDS-221
func (s *lcdScreen) SetLight(on bool) error { return lcd.SetBacklight(on) }

// lcdEngine is what the display loop asks of the acquisition on this
// unit: the render tick from the board's tuning knob (as the engine's),
// the cycle's pace and quiet lock (the display shares the one core with
// arm-settle and drain), and the count of pictures put up (the status's
// lcd_frames).
type lcdEngine struct{ c *composition }

var _ scopeapp.DisplayEngine = lcdEngine{}

// RenderPeriod is the display's tick: the board's render knob.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-127
func (e lcdEngine) RenderPeriod() time.Duration {
	return time.Duration(max(e.c.acq.Tuning().RenderMs.Load(), 1)) * time.Millisecond
}

// SetDisplayPace asks each capture to take at least d.
// TRLC-LINKS: REQ-SDS-021
func (e lcdEngine) SetDisplayPace(d time.Duration) {
	if q := e.c.quiet; q != nil {
		q.SetDisplayPace(d)
	}
}

// QuietRLock holds off the cycle's load-sensitive windows while a picture
// is drawn.
// TRLC-LINKS: REQ-SDS-021
func (e lcdEngine) QuietRLock() {
	if q := e.c.quiet; q != nil {
		q.QuietRLock()
	}
}

// TRLC-LINKS: REQ-SDS-021
func (e lcdEngine) QuietRUnlock() {
	if q := e.c.quiet; q != nil {
		q.QuietRUnlock()
	}
}

// NoteLCDFrame counts a picture put up.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-127
func (e lcdEngine) NoteLCDFrame() { e.c.lcdFrames.Add(1) }
