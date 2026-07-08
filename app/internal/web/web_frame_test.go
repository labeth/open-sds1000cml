package web

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"open-sds/app/internal/engine"
	"testing"
)

func TestFrameEndpoint(t *testing.T) {
	f := &engine.Frame{
		C1: make([]uint8, 2048), C2: make([]uint8, 2048),
		Seq: 7, Valid: 2048, WinCols: 2048, EdgeX: 1024, TdivS: 500e-6,
		DisplayedS: 500e-6, SampleS: 800e-9,
	}
	for i := range f.C1 {
		f.C1[i] = uint8(i % 256)
	}
	fs := &fakeScope{frame: f, fresh: true}
	s := New(fs, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/frame?since=0", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var rep frameReply
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Seq != 7 || rep.Unchanged || len(rep.C1) != screenCols || len(rep.C2) != screenCols {
		t.Fatalf("frame reply: seq=%d unchanged=%v len=%d/%d", rep.Seq, rep.Unchanged, len(rep.C1), len(rep.C2))
	}
	// Measurements + scale factors present.
	if rep.M1 == nil || rep.Cols != screenCols || rep.ColSpanS <= 0 {
		t.Fatalf("frame reply missing scale/meas: m1=%v cols=%d span=%v", rep.M1, rep.Cols, rep.ColSpanS)
	}

	// cols param scales the returned column count.
	req = httptest.NewRequest("GET", "/api/frame?since=0&cols=1280", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var rep2 frameReply
	json.Unmarshal(rec.Body.Bytes(), &rep2)
	if len(rep2.C1) != 1280 || rep2.Cols != 1280 {
		t.Fatalf("cols param ignored: len=%d cols=%d", len(rep2.C1), rep2.Cols)
	}
	// Clamp: absurd cols is bounded.
	req = httptest.NewRequest("GET", "/api/frame?since=0&cols=99999", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var rep3 frameReply
	json.Unmarshal(rec.Body.Bytes(), &rep3)
	if len(rep3.C1) != 4096 {
		t.Fatalf("cols not clamped: %d", len(rep3.C1))
	}

	// since == current seq → unchanged short-circuit, no samples.
	req = httptest.NewRequest("GET", "/api/frame?since=7", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	rep = frameReply{}
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if !rep.Unchanged || rep.C1 != nil {
		t.Fatalf("expected unchanged reply, got %+v", rep)
	}
}

func TestDeepFrameCentersOnTrigger(t *testing.T) {
	// full=1 on a deep decimated frame (Valid>WinCols) serves the record
	// RE-CENTERED on the trigger: fixed length = Valid, edge at posFrac, the
	// sample under the anchor is the trigger sample, and the record end that runs
	// past the capture is blank (-1). This is what makes the trigger — not the
	// frame — the stable anchor.
	const depth, winCols = 6144, 2048
	f := &engine.Frame{
		C1: make([]uint8, depth), C2: make([]uint8, depth),
		Seq: 3, Valid: depth, WinCols: winCols, EdgeX: 2000,
		TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 500e-6 * 10 / winCols,
	}
	for i := range f.C1 {
		f.C1[i] = uint8(i % 256) // ramp so alignment is checkable
	}
	fs := &fakeScope{frameGen: func() *engine.Frame { return f }, stats: engine.Stats{Running: true, TrigPosFrac: 0.5}}
	s := New(fs, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/frame?since=0&full=1", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var rep frameReply
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.C1) != depth || rep.Depth != depth {
		t.Fatalf("deep serve length = %d (depth %d), want %d (fixed, no jitter)", len(rep.C1), rep.Depth, depth)
	}
	if rep.EdgeFrac != 0.5 {
		t.Fatalf("edge_frac = %v, want 0.5 (trigger centred/stable)", rep.EdgeFrac)
	}
	if math.Abs(rep.WinFrac-float64(winCols)/float64(depth)) > 1e-9 {
		t.Fatalf("win_frac = %v, want %v", rep.WinFrac, float64(winCols)/float64(depth))
	}
	// The sample under the anchor (col depth/2) is the trigger sample (EdgeX).
	if got, want := rep.C1[depth/2], int16(2000%256); got != want {
		t.Fatalf("anchor sample = %d, want the trigger sample %d (record re-centred on edge)", got, want)
	}
	// EdgeX=2000 < depth/2 ⇒ the record is shifted right, so the LEFT end is blank.
	if rep.C1[0] != -1 {
		t.Fatalf("left margin = %d, want -1 (blank scrollable margin)", rep.C1[0])
	}
	// col_span_s is the whole-record time (keeps client Nyquist/Δt correct).
	if math.Abs(rep.ColSpanS-float64(depth)*f.SampleS) > 1e-12 {
		t.Fatalf("col_span_s = %v, want %v (whole record)", rep.ColSpanS, float64(depth)*f.SampleS)
	}
}

func TestWindowMapping(t *testing.T) {
	sig := make([]uint8, 100)
	for i := range sig {
		sig[i] = uint8(i)
	}

	// Window exactly covering the record: nearest-sample mode.
	out := window(sig, 100, 100, -1, false, 10, 0.5)
	for x, v := range out {
		if want := int16(x * 10); v != want {
			t.Fatalf("out[%d] = %d, want %d", x, v, want)
		}
	}

	// Window wider than the record: clamp to the full record and fill every
	// column with real data (no gaps) rather than leave off-record blanks.
	out = window(sig, 100, 200, -1, false, 10, 0.5)
	for x, v := range out {
		if v < 0 {
			t.Fatalf("column %d is a gap; a too-wide window should show the full record", x)
		}
	}
	if out[0] != 0 || out[9] < 89 {
		t.Fatalf("full-record span wrong: %v", out)
	}

	// Interpolation: half-sample positions land between neighbours.
	out = window(sig, 100, 10, -1, true, 20, 0.5)
	// pos = 45 + x*0.5 → out[1] interpolates sig[45..46] at 45.5 → 45.5 → 46 or 45
	if out[1] < 45 || out[1] > 46 {
		t.Fatalf("interp out[1] = %d, want ≈45.5", out[1])
	}
}

func TestWindowNoEndGaps(t *testing.T) {
	// The record IS the window (WinCols == Valid) with the edge off-centre:
	// the window must clamp into the record and fill every column with real
	// data (no -1 end gaps), rather than run off the near end.
	sig := make([]uint8, 2048)
	for i := range sig {
		sig[i] = uint8(i % 256)
	}
	// edge at 935 (off-centre) — previously produced ~89 leading gaps.
	// Allow at most a single fractional overshoot at the extreme right.
	out := window(sig, 2048, 2048, 935, false, 800, 0.5)
	gaps := 0
	for _, v := range out {
		if v < 0 {
			gaps++
		}
	}
	if gaps > 1 {
		t.Fatalf("%d end-gap columns (edge off-centre must clamp into the record)", gaps)
	}
}

func TestWindowRailExtendCentres(t *testing.T) {
	// Repeat-rail: an edge near a record END (no crossing near the middle, the
	// sub-period case) must still land at posFrac. With WinCols==Valid the old
	// left-clamp shoved it off-centre; repeat-rail keeps it centred by extending
	// the rail off-record instead of clamping the window.
	sig := make([]uint8, 2048)
	for i := range sig {
		if i >= 935 { // a single rising step at 935 (the only crossing)
			sig[i] = 255
		}
	}
	out := window(sig, 2048, 2048, 935, false, 800, 0.5)
	// Find the rising crossing in the rendered window.
	cross := -1
	for x := 1; x < len(out); x++ {
		if out[x-1] < 128 && out[x] >= 128 {
			cross = x
			break
		}
	}
	if cross < 0 {
		t.Fatal("edge vanished from the rendered window")
	}
	// posFrac 0.5 → the edge must sit at ~col 400 of 800 (dead centre), NOT at
	// the clamped ~col 365 the old code produced.
	if cross < 396 || cross > 404 {
		t.Fatalf("edge at col %d of 800 (frac %.3f), want ≈400/0.500", cross, float64(cross)/800)
	}
	for _, v := range out {
		if v < 0 {
			t.Fatal("repeat-rail must never gap (-1)")
		}
	}
}
