// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"os/exec"
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/testenv"
)

// A reply names the frame its measurements were taken on and that frame's
// edge-window read: running, a reading is reused for the frames of the next
// 100 ms, and MEASURE's statistics count it once.
// TRLC-LINKS: REQ-SDS-217
func TestReplyNamesTheMeasuredFrame(t *testing.T) {
	mk := func(seq, read uint64) *sample.Frame {
		f := &sample.Frame{
			Traces: []sample.Trace{{Codes8: make([]uint8, 2048), Valid: 2048}, {Ch: 1, Codes8: make([]uint8, 2048), Valid: 2048}},
			Seq:    seq, WinPoints: 2048, TriggerAt: 1024,
			SampleS: 2e-9, Snapshot: sample.Settings{TdivS: 1e-6, DisplayedS: 1e-6},
			Details: []sample.Detail{{Role: sample.Edge, Read: read, Frame: &sample.Frame{}}},
		}
		for i := range f.Codes8(0) {
			f.Codes8(0)[i] = uint8(60 + 120*(i/100%2))
		}
		return f
	}
	s := newServer(&fakeScope{}, nil, nil, nil)
	off, vpc := s.vertScales()
	if rep := s.buildReply(mk(5, 3), 2048, true, 0, off, vpc, 0.5, true); rep.MeasSeq != 5 || rep.EdgeRead != 3 {
		t.Fatalf("measured frame %d read %d, want 5 and 3", rep.MeasSeq, rep.EdgeRead)
	}
	if rep := s.buildReply(mk(6, 3), 2048, true, 0, off, vpc, 0.5, true); rep.MeasSeq != 5 {
		t.Fatalf("running, the next frame's reading names frame %d, want the reused 5", rep.MeasSeq)
	}
	if rep := s.buildReply(mk(7, 4), 2048, true, 0, off, vpc, 0.5, false); rep.MeasSeq != 7 || rep.EdgeRead != 4 {
		t.Fatalf("stopped: frame %d read %d, want 7 and 4", rep.MeasSeq, rep.EdgeRead)
	}
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "measure_stats.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("statistics (page): %v\n%s", err, out)
	}
}
