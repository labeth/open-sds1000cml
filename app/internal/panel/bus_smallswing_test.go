// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// A bus that swings too little to decode at the V/div in use (a 3.3 V bus
// through a 10x probe the settings call 1x, after Default setup: a third of a
// division) is named as that, not as "no bus" with "check the connection".
// TRLC-LINKS: REQ-SDS-214
func TestNoBusNamesASmallSwing(t *testing.T) {
	c, eng, _ := newC(t)
	small := make([]uint8, 4000)
	for i := range small {
		small[i] = 128
		if (i/37)%2 == 1 {
			small[i] = 137 // nine codes: 0.36 div
		}
	}
	flat := make([]uint8, 4000)
	for i := range flat {
		flat[i] = 128
	}
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		fn(&engine.Frame{C1: small, C2: flat, Valid: len(small), WinCols: len(small), SampleS: 32e-9, TdivS: eng.Snapshot().TdivS})
	})
	c.setTdivNearest(500e-6)
	c.runDetect()
	if n := c.View().Notice; !strings.Contains(n, "CH1 swings only 0.4 div") || !strings.Contains(n, "AUTO") {
		t.Fatalf("a small swing: notice %q", n)
	}
	// after AUTO in BUS, which fitted the vertical, not AUTO again
	if n := c.smallSwing(true); !strings.Contains(n, "CH1 swings only 0.4 div") || strings.Contains(n, "AUTO") || !strings.Contains(n, "probe factor") {
		t.Fatalf("a small swing after AUTO: notice %q", n)
	}

	c, eng, _ = newC(t)
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		fn(&engine.Frame{C1: flat, C2: flat, Valid: len(flat), WinCols: len(flat), SampleS: 32e-9, TdivS: eng.Snapshot().TdivS})
	})
	c.setTdivNearest(500e-6)
	c.runDetect()
	if n := c.View().Notice; !strings.Contains(n, "no bus found on CH1 or CH2") {
		t.Fatalf("flat lines: notice %q", n)
	}
}
