// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"open-sds/app/internal/engine"
	"testing"
)

// TRLC-LINKS: REQ-SDS-139
func TestMaskBuildRestartsFromStoppedRecordToLivePreview(t *testing.T) {
	c, e, _ := newC(t)
	var seq uint64
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		seq++
		n := 100
		if seq == 1 {
			n = 1000
		}
		sig := make([]uint8, n)
		for i := range sig {
			sig[i] = 100
			if i >= n/2 {
				sig[i] = 180
			}
		}
		fn(&engine.Frame{Seq: seq, C1: sig, C2: sig, Valid: n, WinCols: n, EdgeX: float64(n / 2), SampleS: 2e-9, TdivS: 1e-6})
	})
	c.maskBuildRun(8, 1, 2, 0, .5)
	found := false
	for _, call := range e.calls {
		if call.what == "mask" {
			found = true
			if call.a != 100 {
				t.Fatalf("mask geometry: %+v", call)
			}
		}
	}
	if !found {
		t.Fatalf("no live mask installed: %s", c.MaskStatus())
	}
}
