package web

import (
	"math"
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

	// buildReply is the one shared reply path (behind /api/frame.bin); test it
	// directly for the windowed reply + measurements + scale factors.
	rep := refReply(s, screenCols, false, 0)
	if rep.Seq != 7 || rep.Unchanged || len(rep.C1) != screenCols || len(rep.C2) != screenCols {
		t.Fatalf("frame reply: seq=%d unchanged=%v len=%d/%d", rep.Seq, rep.Unchanged, len(rep.C1), len(rep.C2))
	}
	if rep.M1 == nil || rep.Cols != screenCols || rep.ColSpanS <= 0 {
		t.Fatalf("frame reply missing scale/meas: m1=%v cols=%d span=%v", rep.M1, rep.Cols, rep.ColSpanS)
	}

	// cols param scales the returned column count.
	rep2 := refReply(s, 1280, false, 0)
	if len(rep2.C1) != 1280 || rep2.Cols != 1280 {
		t.Fatalf("cols param ignored: len=%d cols=%d", len(rep2.C1), rep2.Cols)
	}
	// Clamp: absurd cols is bounded by the HANDLER (the /api/frame.bin path).
	got, _, _ := getBin(t, s, "/api/frame.bin?since=0&cols=99999")
	if got.Cols != 4096 {
		t.Fatalf("cols not clamped: %d", got.Cols)
	}

	// since == current seq → unchanged short-circuit, no samples.
	rep = refReply(s, screenCols, false, 7)
	if !rep.Unchanged || rep.C1 != nil {
		t.Fatalf("expected unchanged reply, got %+v", rep)
	}
}

func TestDeepFrameServesRawRecord(t *testing.T) {
	// full=1 on a deep decimated frame (Valid>WinCols) serves the record VERBATIM
	// — NOT re-centered — reporting the trigger's REAL position (edge_frac =
	// EdgeX/Valid). The web centers/windows it client-side (and homes deep records
	// once, since they are phase-stable), so the display, the super-res gate and
	// the raw-fed stacker share one coordinate system (the raw record). No blank
	// -1 margins: every column is a real captured sample.
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

	rep := refReply(s, screenCols, true, 0)
	if len(rep.C1) != depth || rep.Depth != depth {
		t.Fatalf("deep serve length = %d (depth %d), want %d (whole record)", len(rep.C1), rep.Depth, depth)
	}
	if want := 2000.0 / depth; math.Abs(rep.EdgeFrac-want) > 1e-9 {
		t.Fatalf("edge_frac = %v, want %v (EdgeX/Valid — the REAL edge position)", rep.EdgeFrac, want)
	}
	if math.Abs(rep.WinFrac-float64(winCols)/float64(depth)) > 1e-9 {
		t.Fatalf("win_frac = %v, want %v", rep.WinFrac, float64(winCols)/float64(depth))
	}
	// Verbatim: column i is raw sample i, so the sample at the edge column (EdgeX)
	// is the trigger sample, and there are NO -1 margins.
	if got, want := rep.C1[2000], int16(2000%256); got != want {
		t.Fatalf("edge-column sample = %d, want the trigger sample %d (record served verbatim)", got, want)
	}
	if rep.C1[0] == -1 || rep.C1[depth-1] == -1 {
		t.Fatalf("found a -1 margin (%d/%d); the raw record has no blank margins", rep.C1[0], rep.C1[depth-1])
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

func TestDtSTrueCapturePitch(t *testing.T) {
	// dt_s must report the TRUE capture time per served point. On the
	// 1–200 ns/div bands col_span_s is a display nominal (the window is sized
	// at 1 ns/sample while the hardware captures every 2 ns, spec 04 §6), so
	// an exporter or decoder using col_span_s/cols there reconstructs a time
	// axis compressed 2×. dt_s carries min(WinCols,Valid)·SampleS/cols instead.

	// Windowed native-fast, class 0x20 shape: 200 ns/div, 2000-sample window
	// at the real 2 ns pitch, DisplayedS carrying the 1 ns nominal.
	f := &engine.Frame{
		C1: make([]uint8, 4000), C2: make([]uint8, 4000),
		Seq: 1, Valid: 4000, WinCols: 2000, EdgeX: -1, Interp: true,
		TdivS: 200e-9, DisplayedS: 200e-9, SampleS: 2e-9,
	}
	s := New(&fakeScope{frame: f, fresh: true}, nil, nil, nil)
	rep := refReply(s, 800, false, 0)
	if want := 2000 * 2e-9 / 800; math.Abs(rep.DtS-want) > 1e-18 {
		t.Fatalf("windowed dt_s = %v, want %v (true pitch)", rep.DtS, want)
	}
	if nominal := rep.ColSpanS / float64(rep.Cols); math.Abs(rep.DtS-2*nominal) > 1e-18 {
		t.Fatalf("dt_s = %v, want 2× the nominal col pitch %v on the 0x20 shape", rep.DtS, nominal)
	}
	// The exporter reads dt_s from the binary transport's JSON header — pin
	// that the corrected value (not the nominal) survives the wire.
	if got, _, _ := getBin(t, s, "/api/frame.bin?since=0&cols=800"); math.Abs(got.DtS-2000*2e-9/800) > 1e-18 {
		t.Fatalf("dt_s over the binary transport = %v, want %v", got.DtS, 2000*2e-9/800)
	}

	// ETS-shaped frame (equivalent-time publish: Valid == WinCols == nCols,
	// SampleS = 10·tdiv/nCols is the reconstructed column pitch): dt_s must
	// agree with col_span_s/cols — ETS spans are honest, no 2× correction.
	fe := &engine.Frame{
		C1: make([]uint8, 2500), C2: make([]uint8, 2500),
		Seq: 9, Valid: 2500, WinCols: 2500, EdgeX: 1250, Interp: true,
		TdivS: 50e-9, DisplayedS: 50e-9, SampleS: 10 * 50e-9 / 2500,
	}
	se := New(&fakeScope{frame: fe, fresh: true}, nil, nil, nil)
	repe := refReply(se, 800, false, 0)
	if math.Abs(repe.DtS-repe.ColSpanS/float64(repe.Cols)) > 1e-18 {
		t.Fatalf("ETS-shaped dt_s = %v, want col pitch %v (no correction)", repe.DtS, repe.ColSpanS/float64(repe.Cols))
	}

	// Windowed with a short record (Valid < WinCols): the window clamps to
	// Valid samples and dt_s must clamp with it.
	f2 := &engine.Frame{
		C1: make([]uint8, 1000), C2: make([]uint8, 1000),
		Seq: 2, Valid: 1000, WinCols: 3000, EdgeX: -1,
		TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9,
	}
	s2 := New(&fakeScope{frame: f2, fresh: true}, nil, nil, nil)
	if rep2, want := refReply(s2, 800, false, 0), 1000*800e-9/800; math.Abs(rep2.DtS-want) > 1e-18 {
		t.Fatalf("clamped dt_s = %v, want %v", rep2.DtS, want)
	}

	// Deep serve: every point is one hardware sample, dt_s == SampleS and
	// agrees exactly with col_span_s/cols.
	f3 := &engine.Frame{
		C1: make([]uint8, 6144), C2: make([]uint8, 6144),
		Seq: 3, Valid: 6144, WinCols: 2048, EdgeX: 100,
		TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 2.4414e-6,
	}
	s3 := New(&fakeScope{frame: f3, fresh: true}, nil, nil, nil)
	rep3 := refReply(s3, 800, true, 0)
	if rep3.Depth != 6144 || rep3.DtS != f3.SampleS {
		t.Fatalf("deep dt_s = %v (depth %d), want SampleS %v", rep3.DtS, rep3.Depth, f3.SampleS)
	}
	if math.Abs(rep3.DtS-rep3.ColSpanS/float64(rep3.Cols)) > 1e-18 {
		t.Fatalf("deep dt_s %v disagrees with col_span_s/cols %v", rep3.DtS, rep3.ColSpanS/float64(rep3.Cols))
	}

	// Envelope frames aggregate many acquisitions — no per-point time, dt_s
	// stays 0 (omitted on the wire).
	f4 := &engine.Frame{
		Seq: 4, IsEnv: true, EnvCols: 800, Valid: 800,
		EnvMin: make([]uint8, 800), EnvMax: make([]uint8, 800),
		EnvMin2: make([]uint8, 800), EnvMax2: make([]uint8, 800),
		TdivS: 10e-3, DisplayedS: 10e-3,
	}
	s4 := New(&fakeScope{frame: f4, fresh: true}, nil, nil, nil)
	if rep4 := refReply(s4, 800, false, 0); !rep4.IsEnv || rep4.DtS != 0 {
		t.Fatalf("envelope dt_s = %v (is_env=%v), want 0", rep4.DtS, rep4.IsEnv)
	}
}
