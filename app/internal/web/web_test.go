package web

import (
	"bytes"
	"encoding/json"
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
	holdoff  float64
	calls    [][2]any

	zones        []engine.Zone
	zoneMode     int
	mask         *engine.Mask
	maskMode     int
	maskCleared  bool
	maskRing     []engine.MaskFail
	serialParams engine.SerialParams
	serialMode   int
	bodeOn       bool
	bodeRef      int
	bodeDut      int
	bodeCleared  bool
	bodePts      []engine.BodePoint

	acqLog   []engine.AcqSample
	halfRate float64
	cmdLog   []engine.CmdNote
}

func (f *fakeScope) SetOffsetDAC(ch int, code uint16)       { f.offCh, f.offCode = ch, &code }
func (f *fakeScope) SetETS(on bool)                         { f.ets = &on }
func (f *fakeScope) SetSingle()                             { f.single = true }
func (f *fakeScope) QuietRLock()                            {}
func (f *fakeScope) QuietRUnlock()                          {}
func (f *fakeScope) Tune(t engine.TuneVals) engine.TuneVals { return t }
func (f *fakeScope) TuneSnapshot() engine.TuneVals          { return engine.TuneVals{} }
func (f *fakeScope) SetTrigPosFrac(frac float64)            { f.trigPos = frac }
func (f *fakeScope) SetMemDepth(n int) int                  { f.memDepth = n; return n }
func (f *fakeScope) SetFramePeriod(ms int) int              { return ms }
func (f *fakeScope) SetStreamMode(on bool) bool             { return on }
func (f *fakeScope) SetHoldoff(sec float64) float64         { f.holdoff = sec; return sec }
func (f *fakeScope) SetZones(z []engine.Zone)               { f.zones = z }
func (f *fakeScope) SetZoneMode(m int)                      { f.zoneMode = m }
func (f *fakeScope) SetMask(m *engine.Mask)                 { f.mask = m }
func (f *fakeScope) SetMaskMode(m int)                      { f.maskMode = m }
func (f *fakeScope) ClearMaskFails()                        { f.maskCleared = true }
func (f *fakeScope) MaskFails() []engine.MaskFail           { return f.maskRing }
func (f *fakeScope) SetSerialParams(p engine.SerialParams)  { f.serialParams = p }
func (f *fakeScope) SetSerialMode(m int)                    { f.serialMode = m }
func (f *fakeScope) SetBodeMode(on bool, ref, dut int)      { f.bodeOn, f.bodeRef, f.bodeDut = on, ref, dut }
func (f *fakeScope) ClearBode()                             { f.bodeCleared = true }
func (f *fakeScope) BodePoints() []engine.BodePoint         { return f.bodePts }

func (f *fakeScope) AcqLog(n int) ([]engine.AcqSample, float64) { return f.acqLog, f.halfRate }
func (f *fakeScope) CmdLog(n int) []engine.CmdNote              { return f.cmdLog }
func (f *fakeScope) NoteCmd(name string, val float64) {
	f.cmdLog = append(f.cmdLog, engine.CmdNote{Name: name, Val: val})
}

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
	probe    [2]float64
	cpl      [2]int
}

func (f *fakeAnalog) SetVdiv(ch, idx int) error {
	f.ch, f.idx, f.set = ch, idx, true
	return nil
}
func (f *fakeAnalog) Snapshot() ([2]int, bool) { return [2]int{f.idx, f.idx}, f.set }
func (f *fakeAnalog) SetOffset(ch int, volts float64) uint16 {
	c := uint16(10223 - int(volts*100))
	f.offCh, f.offVolts = ch, volts
	return c
}
func (f *fakeAnalog) OffsetVolts(ch int, code uint16) float64 {
	return float64(10223-int(code)) / 100
}
func (f *fakeAnalog) CalSource() string                    { return "defaults" }
func (f *fakeAnalog) DCVolts(ch int, mean float64) float64 { return 0 }
func (f *fakeAnalog) SetProbe(ch int, x float64)           { f.probe[ch&1] = x }
func (f *fakeAnalog) ProbeFactor(ch int) float64 {
	if p := f.probe[ch&1]; p >= 1 {
		return p
	}
	return 1
}
func (f *fakeAnalog) SetCoupling(ch, mode int) error { f.cpl[ch&1] = mode; return nil }
func (f *fakeAnalog) Coupling(ch int) int            { return f.cpl[ch&1] }

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

func getStatus(t *testing.T, s *Server) map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/status", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var rep map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("bad status json: %v", err)
	}
	return rep
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
