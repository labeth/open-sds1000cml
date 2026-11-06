// ENGMODEL-OWNER-UNIT: FU-APP-LCD

package lcd

import "testing"

// TestPaletteMatchesTokens holds the LCD colours to the values they had when
// they were generated into this package, so the move to the shared palette
// changed no pixel.
// TRLC-LINKS: REQ-SDS-021
func TestPaletteMatchesTokens(t *testing.T) {
	for name, c := range map[string]struct {
		got     uint16
		r, g, b uint8
	}{
		"axis": {colAxis, 80, 80, 110}, "screen": {colBG, 5, 8, 12}, "lcd-box": {colBoxBg, 6, 10, 22},
		"c1": {colC1, 245, 217, 10}, "c2": {colC2, 240, 108, 196}, "cursor": {colCursor, 230, 236, 242},
		"dim": {colDim, 124, 136, 148}, "grid": {colGrid, 24, 36, 48}, "text": {colInfo, 214, 221, 228},
		"math": {colMath, 185, 140, 255}, "lcd-menu": {colMenuBg, 8, 12, 28}, "run": {colOK, 63, 185, 80},
		"sr": {colSR, 53, 200, 232}, "stop": {colStale, 213, 94, 0}, "trigger": {colTrig, 242, 166, 59},
		"warn": {colWarn, 245, 162, 76},
	} {
		if want := rgb(c.r, c.g, c.b); c.got != want {
			t.Errorf("%s = %#04x, want %#04x", name, c.got, want)
		}
	}
}
