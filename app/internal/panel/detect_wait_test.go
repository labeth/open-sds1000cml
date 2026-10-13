// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
)

// envelopeOf is f as the live peak-detect readout at a slow timebase: one
// (min, max) pair per bucket of the capture, time-ordered.
// TRLC-LINKS: REQ-SDS-214
func envelopeOf(f *engine.Frame, bucket int) *engine.Frame {
	n := f.Valid / bucket
	c1, c2 := make([]uint8, 2*n), make([]uint8, 2*n)
	for b := 0; b < n; b++ {
		lo, hi := uint8(255), uint8(0)
		for _, v := range f.C1[b*bucket : (b+1)*bucket] {
			lo, hi = min(lo, v), max(hi, v)
		}
		c1[2*b], c1[2*b+1] = lo, hi
		c2[2*b], c2[2*b+1] = 210, 210
	}
	return &engine.Frame{C1: c1, C2: c2, Valid: 2 * n, WinCols: 2 * n, EdgeX: float64(n), SampleS: f.SampleS * float64(bucket) / 2,
		TdivS: f.TdivS, PeakDetect: true, Ordered: true}
}

// BUS's auto-detect waits for the sampled frame its decode lease asks for:
// at a slow timebase the live envelope holds a few points a UART bit and no
// bus is found in it (500 us/div with the UART on screen).
// TRLC-LINKS: REQ-SDS-214
func TestBusDetectWaitsForSamples(t *testing.T) {
	c, _, _ := newC(t)
	raw := uartFrame([]string{"Hi U\\x55\\xaa"}, 115200, 300e-6, 20e-9, 200000)
	env := envelopeOf(raw, 1500)
	calls := 0
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		calls++
		if calls <= 3 {
			fn(env)
			return
		}
		fn(raw)
	})
	if _, found := (&Controller{frameFn: func(fn func(*engine.Frame)) { fn(env) }}).lastAuto(); found {
		t.Fatal("the test's envelope is decodable: it proves nothing")
	}
	c.runDetect()
	c.mu.Lock()
	proto, notice := c.decProto, c.ui.notice
	c.mu.Unlock()
	if proto != framecalc.ProtoUART || !strings.Contains(notice, "UART 115200") {
		t.Fatalf("detected %d, notice %q", proto, notice)
	}
}
