// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"testing"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
)

// An Oregon sensor's burst cut by a 5 ms/div record reads as a Manchester
// line; the search holds that find while the slower looks see whether it is
// a burst. A slower look whose record cuts the burst too reads the same
// Manchester line: the search looks on (the next record, the slowest
// timebase) and names the Oregon burst a whole record holds, where it used
// to give the held Manchester find back at once.
// TRLC-LINKS: REQ-SDS-214
func TestSlowSearchLooksOnPastTheHeldFindAgain(t *testing.T) {
	c, eng, _ := newC(t)
	cut1, cut2, cutS := loadScopeCapture(t, "scope_ook_cut_5ms.json.gz")
	whole1, whole2, wholeS := loadScopeCapture(t, "scope_ook_search_20ms.json.gz")
	if r := decode.Autodetect(cut1, cut2, cutS, "hex"); r.Proto != "manchester" {
		t.Fatalf("the cut burst reads as %q, want manchester", r.Proto)
	}
	var seq uint64
	looks20 := 0
	c.SetFrameSource(func(fn func(*engine.Frame)) {
		tdiv := eng.Snapshot().TdivSet()
		seq++
		f := &engine.Frame{Seq: seq, TdivS: tdiv}
		switch {
		case math.Abs(tdiv-20e-3) < 1e-6 && looks20 == 0:
			// the first 20 ms/div record cuts the burst too
			f.C1, f.C2, f.SampleS = cut1, cut2, cutS
		case tdiv >= 20e-3*0.999:
			f.C1, f.C2, f.SampleS = whole1, whole2, wholeS
		default:
			f.C1, f.C2, f.SampleS = cut1, cut2, cutS
		}
		f.Valid = len(f.C1)
		fn(f)
	})
	// a look's detection reads the record on screen; the first at 20 ms/div
	// is the cut one
	auto := func() (decode.Result, float64, bool) {
		r, s, ok := c.detectOnScreen()
		if math.Abs(eng.Snapshot().TdivSet()-20e-3) < 1e-6 {
			looks20++
		}
		return r, s, ok
	}
	r, _, found, _, _ := c.findSlowBus(auto, func() bool { return true }, "", 0)
	if !found || r.Proto != "ook" {
		t.Fatalf("found %v %q, want the Oregon burst (ook)", found, r.Proto)
	}
}
