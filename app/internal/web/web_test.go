package web

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"

	"open-sds/app/internal/engine"
)

// fakeScope records setter calls and serves a canned frame.
type fakeScope struct {
	stats    engine.Stats
	frame    *engine.Frame
	frameGen func() *engine.Frame // when set, WithFrame serves this instead
	fresh    bool

	running  *bool
	norm     *bool
	tdiv     *float64
	trigCode *uint16
	slope    *bool
	source   *int
	offCh    int
	offCode  *uint16
	ets      *bool
	single   bool
	trigPos  float64
	memDepth int
	calls    [][2]any
}

func (f *fakeScope) SetOffsetDAC(ch int, code uint16) { f.offCh, f.offCode = ch, &code }
func (f *fakeScope) SetETS(on bool)                   { f.ets = &on }
func (f *fakeScope) SetSingle()                       { f.single = true }
func (f *fakeScope) SetTrigPosFrac(frac float64)      { f.trigPos = frac }
func (f *fakeScope) SetMemDepth(n int) int            { f.memDepth = n; return n }
func (f *fakeScope) SetFramePeriod(ms int) int        { return ms }
func (f *fakeScope) SetStreamMode(on bool) bool       { return on }

func (f *fakeScope) SetTrigType(t int) { f.calls = append(f.calls, [2]any{"trigtype", t}) }
func (f *fakeScope) SetAcqMode(m int)  { f.calls = append(f.calls, [2]any{"acqmode", m}) }
func (f *fakeScope) SetAvgCount(n int) { f.calls = append(f.calls, [2]any{"avgcount", n}) }
func (f *fakeScope) SetEresLen(l int)  { f.calls = append(f.calls, [2]any{"eres", l}) }
func (f *fakeScope) SetPulseParams(lvl, wMin, wMax float64, cond int) {
	f.calls = append(f.calls, [2]any{"pulse", []any{lvl, wMin, wMax, cond}})
}
func (f *fakeScope) SetSlopeParams(lo, hi, tMin, tMax float64, cond int) {
	f.calls = append(f.calls, [2]any{"slope", []any{lo, hi, tMin, tMax, cond}})
}
func (f *fakeScope) SetVideoParams(std, line int, neg bool) {
	f.calls = append(f.calls, [2]any{"video", []any{std, line, neg}})
}

func (f *fakeScope) lastCall() [2]any {
	if len(f.calls) == 0 {
		return [2]any{}
	}
	return f.calls[len(f.calls)-1]
}

// fakeAnalog records SetVdiv calls.
type fakeAnalog struct {
	ch, idx  int
	set      bool
	offCh    int
	offVolts float64
}

func (f *fakeAnalog) SetVdiv(ch, idx int) error {
	f.ch, f.idx, f.set = ch, idx, true
	return nil
}
func (f *fakeAnalog) Snapshot() ([2]int, bool) { return [2]int{f.idx, f.idx}, f.set }
func (f *fakeAnalog) SetOffset(ch int, volts float64) uint16 {
	c := uint16(10223 - int(volts*262))
	f.offCh, f.offVolts = ch, volts
	return c
}
func (f *fakeAnalog) OffsetVolts(ch int, code uint16) float64 {
	return float64(10223-int(code)) / 262
}
func (f *fakeAnalog) CalSource() string                    { return "defaults" }
func (f *fakeAnalog) DCVolts(ch int, mean float64) float64 { return 0 }

func (f *fakeScope) Snapshot() engine.Stats { return f.stats }
func (f *fakeScope) WithFrame(fn func(*engine.Frame)) {
	if f.frameGen != nil {
		fn(f.frameGen())
		return
	}
	fn(f.frame)
}
func (f *fakeScope) SetRunning(on bool)   { f.running = &on }
func (f *fakeScope) SetNorm(on bool)      { f.norm = &on }
func (f *fakeScope) SetTrigSlope(r bool)  { f.slope = &r }
func (f *fakeScope) SetTrigSource(ch int) { f.source = &ch }
func (f *fakeScope) SetTrigLevelCode(c uint16) uint16 {
	if c < engine.TrigCodeMin {
		c = engine.TrigCodeMin
	}
	f.trigCode = &c
	return c
}
func (f *fakeScope) SetTdiv(s float64) (engine.Band, bool) {
	b, ok := engine.PlanTdiv(s)
	if ok {
		f.tdiv = &s
	}
	return b, ok
}

func post(t *testing.T, s *Server, control string, value float64) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"control": control, "value": value})
	req := httptest.NewRequest("POST", "/api/set", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json reply: %v", err)
	}
	return out
}

func TestSetVerbs(t *testing.T) {
	fs := &fakeScope{}
	s := New(fs, nil, nil, nil)

	if out := post(t, s, "run", 0); out["ok"] != true || fs.running == nil || *fs.running {
		t.Fatalf("run 0: %v running=%v", out, fs.running)
	}
	if out := post(t, s, "norm", 1); out["ok"] != true || fs.norm == nil || !*fs.norm {
		t.Fatalf("norm 1: %v", out)
	}
	if out := post(t, s, "tdiv", 500e-6); out["ok"] != true || fs.tdiv == nil {
		t.Fatalf("tdiv: %v", out)
	}
	if out := post(t, s, "tdiv", 7e-3); out["ok"] != false {
		t.Fatalf("off-ladder tdiv accepted: %v", out)
	}
	if out := post(t, s, "triglevelcode", 30000); out["applied"] != 30000.0 {
		t.Fatalf("triglevelcode: %v", out)
	}
	if out := post(t, s, "trigslope", 0); out["ok"] != true || fs.slope == nil || *fs.slope {
		t.Fatalf("trigslope: %v", out)
	}
	if out := post(t, s, "trigsource", 1); out["ok"] != true || fs.source == nil || *fs.source != 1 {
		t.Fatalf("trigsource: %v", out)
	}
	if out := post(t, s, "bogus", 1); out["ok"] != false {
		t.Fatalf("unknown control accepted: %v", out)
	}
}

func TestVerticalVerbs(t *testing.T) {
	fs := &fakeScope{}
	fa := &fakeAnalog{}
	s := New(fs, fa, nil, nil)

	if out := post(t, s, "vdiv1", 0.1); out["ok"] != true || !fa.set || fa.ch != 0 || fa.idx != 5 {
		t.Fatalf("vdiv1 100mV: %v fa=%+v", out, fa)
	}
	if out := post(t, s, "vdiv2", 2.0); out["ok"] != true || fa.ch != 1 || fa.idx != 9 {
		t.Fatalf("vdiv2 2V: %v fa=%+v", out, fa)
	}
	if out := post(t, s, "vdiv1", 0.003); out["ok"] != false {
		t.Fatalf("off-ladder vdiv accepted: %v", out)
	}
	// With a front end present, offsets route through fe.SetOffset (which
	// re-anchors on V/div changes), not the engine directly.
	if out := post(t, s, "offset1", 1.0); out["ok"] != true || fa.offCh != 0 || fa.offVolts != 1.0 {
		t.Fatalf("offset1 1V: %v fa=%+v", out, fa)
	}
	if out := post(t, s, "offset2", -0.5); out["ok"] != true || fa.offCh != 1 || fa.offVolts != -0.5 {
		t.Fatalf("offset2 -0.5V: %v fa=%+v", out, fa)
	}
	if out := post(t, s, "offset1", 50); out["ok"] != false {
		t.Fatalf("offset 50V accepted: %v", out)
	}

	// Without a front end, vdiv verbs must fail cleanly.
	s2 := New(fs, nil, nil, nil)
	if out := post(t, s2, "vdiv1", 0.1); out["ok"] != false {
		t.Fatalf("vdiv without front end accepted: %v", out)
	}
}

func TestQualifierAndAcqVerbs(t *testing.T) {
	fs := &fakeScope{}
	s := New(fs, nil, nil, nil)

	if out := post(t, s, "trigtype", 2); out["ok"] != true || fs.lastCall()[0] != "trigtype" {
		t.Fatalf("trigtype: %v", out)
	}
	if out := post(t, s, "acqmode", 1); out["ok"] != true || fs.lastCall()[1] != 1 {
		t.Fatalf("acqmode: %v", out)
	}
	if out := post(t, s, "avgcount", 64); out["ok"] != true || fs.lastCall()[1] != 64 {
		t.Fatalf("avgcount: %v", out)
	}
	if out := post(t, s, "eres", 15); out["ok"] != true || fs.lastCall()[1] != 15 {
		t.Fatalf("eres: %v", out)
	}

	// Param verbs carry named fields.
	body, _ := json.Marshal(map[string]any{"control": "pulseparams", "lvl": 0.4, "min": 100.0, "max": 500.0, "cond": 3})
	req := httptest.NewRequest("POST", "/api/set", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	last := fs.lastCall()
	if last[0] != "pulse" {
		t.Fatalf("pulseparams not dispatched: %v", last)
	}
	args := last[1].([]any)
	if args[0] != 0.4 || args[1] != 100.0 || args[2] != 500.0 || args[3] != 3 {
		t.Fatalf("pulseparams args: %v", args)
	}
}

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

func TestStatusEndpoint(t *testing.T) {
	fs := &fakeScope{stats: engine.Stats{Frames: 5, TrigCode: 31434}}
	s := New(fs, nil, nil, nil)
	req := httptest.NewRequest("GET", "/api/status", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var rep map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep["frames"] != 5.0 {
		t.Fatalf("frames = %v", rep["frames"])
	}
	if rep["trig_volts"] != 0.0 {
		t.Fatalf("trig_volts for code 31434 = %v, want 0", rep["trig_volts"])
	}
	if len(rep["tdivs"].([]any)) != 33 {
		t.Fatalf("tdivs length = %d", len(rep["tdivs"].([]any)))
	}
}

func TestRootServesUI(t *testing.T) {
	s := New(&fakeScope{}, nil, nil, nil)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte("open-sds1000cml")) {
		t.Fatalf("root: code=%d", rec.Code)
	}
}
