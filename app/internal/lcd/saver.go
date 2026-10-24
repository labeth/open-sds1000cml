// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"os"
	"strconv"
	"time"

	"open-sds/app/internal/engine"
)

// The screen saver picture (ADR-SCREEN-SAVER): a small live scope with its run
// state, drifting over black so that no pixel holds one image.

// SaverStep is how often the saver moves one pixel each way (5 px/s).
const SaverStep = 200 * time.Millisecond

// The saver box: the mini scope and its two lines under it.
const (
	saverW    = 240
	saverH    = 144
	saverBoxH = saverH + 36
)

// SaverPos is the box's top-left corner after step steps: it bounces off the
// screen's edges one pixel each way per step, on a path that repeats only
// after hours.
// TRLC-LINKS: REQ-SDS-221
func SaverPos(step int) (x, y int) {
	return bounce(step, W-saverW), bounce(step, H-saverBoxH)
}

// bounce runs 0..span and back.
// TRLC-LINKS: REQ-SDS-221
func bounce(t, span int) int {
	if span <= 0 || t < 0 {
		return 0
	}
	t %= 2 * span
	if t > span {
		t = 2*span - t
	}
	return t
}

// DrawSaver draws the saver picture for a step: black, the live mini scope,
// its run state and time/div, and a running mask test's counts or the hint.
// TRLC-LINKS: REQ-SDS-221
func DrawSaver(sf *MemSurface, f *engine.Frame, hud HUD, step int) {
	clear(sf.Pix) // black: a memclr, not the colour fill's row copies
	x, y := SaverPos(step)
	drawMiniScope(sf, f, hud, x, y, saverW, saverH)
	state, col := "RUN", colOK
	switch {
	case !hud.Running:
		state, col = "STOP", colStale
	case hud.Single:
		state, col = "SINGLE", colTrig
	}
	DrawText(sf, x, y+saverH+8, state, col, 2)
	DrawText(sf, x+TextWidth(state, 2)+12, y+saverH+8, fmtTdiv(hud.TdivS)+"/div", colInfo, 2)
	line, lcol := "Any key wakes the screen", colDim
	if hud.MaskMode > 0 {
		line, lcol = "MASK pass "+strconv.FormatInt(hud.MaskPass, 10)+"  FAIL "+strconv.FormatInt(hud.MaskFail, 10), colInfo
		if hud.MaskFail > 0 || hud.MaskStopped {
			lcol = colWarn
		}
	}
	DrawText(sf, x, y+saverH+28, line, lcol, 1)
}

// SetBacklight switches the panel's light through GPIO 7, the display and
// backlight enable (spec 08 §10.1): the only light control this LCD has.
// TRLC-LINKS: REQ-SDS-221
func SetBacklight(on bool) error {
	v := []byte("0")
	if on {
		v = []byte("1")
	}
	return os.WriteFile("/sys/class/gpio/gpio7/value", v, 0o644)
}
