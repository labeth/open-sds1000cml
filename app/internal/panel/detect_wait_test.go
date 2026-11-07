// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import (
	"strings"
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/panel/framecalc"
)

// envelopeOf is f as the live peak-detect readout at a slow timebase: one
// (min, max) pair per bucket of the capture, time-ordered.
// TRLC-LINKS: REQ-SDS-214
func envelopeOf(f *sample.Frame, bucket int) *sample.Frame {
	n := f.Len() / bucket
	c1, c2 := make([]uint8, 2*n), make([]uint8, 2*n)
	for b := range n {
		lo, hi := uint8(255), uint8(0)
		for _, v := range f.Codes8(0)[b*bucket : (b+1)*bucket] {
			lo, hi = min(lo, v), max(hi, v)
		}
		c1[2*b], c1[2*b+1] = lo, hi
		c2[2*b], c2[2*b+1] = 210, 210
	}
	return &sample.Frame{
		WinPoints: 2 * n,
		TriggerAt: float64(n),
		SampleS:   f.SampleS * float64(bucket) / 2,
		Content:   sample.PeakPairs,
		Flags:     sample.Ordered,
		Snapshot:  sample.Settings{TdivS: f.Snapshot.TdivS},
		Traces:    []sample.Trace{{Codes8: c1, Valid: 2 * n}, {Ch: 1, Codes8: c2, Valid: 2 * n}},
	}
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
	c.SetFrameSource(func(fn func(*sample.Frame)) {
		calls++
		if calls <= 3 {
			fn(env)
			return
		}
		fn(raw)
	})
	if _, _, found := (&Controller{frameFn: func(fn func(*sample.Frame)) { fn(env) }}).detectOnScreen(); found {
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
