// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"testing"

	"open-sds/app/internal/engine"
)

// A frame with no crossing (forced in AUTO) shows the screen centred on the
// record, its trigger point at the trigger position: a sampled 10ms/div
// record at 10 % kept 60 % of the screen.
// TRLC-LINKS: REQ-SDS-021
func TestTraceWindowWithoutAnEdge(t *testing.T) {
	f := &engine.Frame{Valid: 1000, WinCols: 1000, EdgeX: -1}
	for _, pos := range []float64{0.1, 0.5, 0.9} {
		win, xc, pf := traceWindow(f, HUD{TrigPosFrac: pos}, 1000)
		if left := xc - pf*float64(win); left != 0 || win != 1000 {
			t.Fatalf("pos %v: window from %v, %d samples; want the record's 0, 1000", pos, left, win)
		}
	}
}
