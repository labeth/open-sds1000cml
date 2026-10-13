// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"os/exec"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/testenv"
)

// Auto-measurement logic now lives in internal/measure (with its own thorough
// suite); the web layer only re-exports it. This covers the web-local helper.

// TRLC-LINKS: REQ-SDS-163
func TestResampleEnv(t *testing.T) {
	v := make([]uint8, 800)
	for i := range v {
		v[i] = uint8(i % 256)
	}
	out := resampleEnv(v, 800, 400)
	if len(out) != 400 || out[0] != 0 || out[399] != int16(v[399*800/400]) {
		t.Fatalf("resample: len=%d out[399]=%d", len(out), out[399])
	}
}

// A reply names the frame its measurements were taken on and that frame's
// edge-window read: running, a reading is reused for the frames of the next
// 100 ms, and MEASURE's statistics count it once.
// TRLC-LINKS: REQ-SDS-217
func TestReplyNamesTheMeasuredFrame(t *testing.T) {
	mk := func(seq, read uint64) *engine.Frame {
		f := &engine.Frame{C1: make([]uint8, 2048), C2: make([]uint8, 2048), Seq: seq, Valid: 2048, WinCols: 2048, EdgeX: 1024,
			SampleS: 2e-9, TdivS: 1e-6, DisplayedS: 1e-6, EdgeRead: read}
		for i := range f.C1 {
			f.C1[i] = uint8(60 + 120*(i/100%2))
		}
		return f
	}
	s := New(&fakeScope{}, nil, nil, nil)
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
	if out, err := exec.Command("node", "measure_stats.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("statistics (page): %v\n%s", err, out)
	}
}
