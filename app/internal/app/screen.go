// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"fmt"
	"time"

	"github.com/labeth/open-dso-core/render"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/scopeapp"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
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

// viewLease asks the engine for the zoomed part of the live envelope at
// full detail (the frame's view window) while the LCD's Y-T is zoomed in.
// TRLC-LINKS: REQ-SDS-021
func (c *composition) viewLease(f *sample.Frame, hud *render.HUD) {
	if a, b, ok := scopeViewLease(*hud, f); ok {
		c.e.LeaseViewWindow(engine.ViewScope, a, b, render.PlotColumns(lcd.Screen, hud.UI.Layout), time.Second)
	}
}

// scopeViewLease is the stretch of live envelope frame f the LCD's zoom
// shows, in screen widths from the trigger, while zoomed in Y-T (X-Y draws no
// zoomed stretch, and its reads cost frames).
// TRLC-LINKS: REQ-SDS-021
func scopeViewLease(hud render.HUD, f *sample.Frame) (a, b float64, ok bool) {
	if hud.Zoom <= 1 || hud.ViewMode != 0 || f == nil || f.Content != sample.PeakPairs || f.WinPoints <= 0 || f.Len() <= 0 {
		return 0, 0, false
	}
	c := hud.ZoomOff * float64(f.Len()) / float64(min(f.WinPoints, f.Len()))
	h := 0.5 / float64(hud.Zoom)
	return c - h, c + h, true
}
