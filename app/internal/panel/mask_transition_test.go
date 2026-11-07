// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import (
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// TRLC-LINKS: REQ-SDS-139
func TestMaskBuildRestartsFromStoppedRecordToLivePreview(t *testing.T) {
	c, e, _ := newC(t)
	var seq uint64
	c.SetFrameSource(func(fn func(*sample.Frame)) {
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
		fn(&sample.Frame{Seq: seq, WinPoints: n, TriggerAt: float64(n / 2), SampleS: 2e-9, Snapshot: sample.Settings{TdivS: 1e-6}, Traces: []sample.Trace{{Codes8: sig, Valid: n}, {Ch: 1, Codes8: sig, Valid: n}}})
	})
	c.maskBuildRun(c.maskGen, 8, 1, 2, 0, .5)
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

type maskRecordingEng struct {
	*fakeEng
	mask *engine.Mask
}

// TRLC-LINKS: REQ-SDS-139
func (e *maskRecordingEng) SetMask(m *engine.Mask) { e.mask = m }

// TRLC-LINKS: REQ-SDS-139
func TestMaskBuildAtLeftTriggerPosition(t *testing.T) {
	_, base, fe := newC(t)
	e := &maskRecordingEng{fakeEng: base}
	c := New(e, fe, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	var seq uint64
	c.SetFrameSource(func(fn func(*sample.Frame)) {
		seq++
		fn(&sample.Frame{Seq: seq, WinPoints: 4, TriggerAt: 2, SampleS: 2e-9, Snapshot: sample.Settings{TdivS: 1e-6}, Traces: []sample.Trace{{Codes8: []uint8{40, 60, 180, 200}, Valid: 4}, {Ch: 1, Valid: 4}}})
	})
	c.maskBuildRun(c.maskGen, 8, 0, 0, 0, 0)
	if e.mask == nil || e.mask.Lo[0] != 180 || e.mask.Hi[0] != 180 || e.mask.Lo[2] != 0 || e.mask.Hi[2] != 255 {
		t.Fatalf("left-position mask used a centred window: %+v", e.mask)
	}
}
