package web

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http/httptest"
	"open-sds/app/internal/engine"
	"testing"
)

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

func TestProbeAttenuation(t *testing.T) {
	// code 30496 ≈ +1 V trigger level on source C1.
	fs := &fakeScope{stats: engine.Stats{TrigCode: 30496, TrigSource: 0}}
	fa := &fakeAnalog{}
	s := New(fs, fa, nil, nil)

	// only 1/10/100 are valid factors.
	if out := post(t, s, "probe1", 3); out["ok"] != false {
		t.Fatalf("probe 3× accepted: %v", out)
	}
	_, base := s.vertScales() // both channels ×1
	tv1 := getStatus(t, s)["trig_volts"].(float64)

	if out := post(t, s, "probe1", 10); out["ok"] != true || fa.probe[0] != 10 {
		t.Fatalf("probe1 10×: %v fa=%+v", out, fa)
	}
	if out := post(t, s, "probe2", 100); out["ok"] != true || fa.probe[1] != 100 {
		t.Fatalf("probe2 100×: %v fa=%+v", out, fa)
	}

	// vertScales multiplies each channel's volts-per-code by its probe factor.
	_, vpc := s.vertScales()
	if math.Abs(vpc[0]-base[0]*10) > 1e-12 {
		t.Fatalf("vpc[0] not ×10: base %v got %v", base[0], vpc[0])
	}
	if math.Abs(vpc[1]-base[1]*100) > 1e-12 {
		t.Fatalf("vpc[1] not ×100: base %v got %v", base[1], vpc[1])
	}

	// Status echoes the factors and scales the trigger-level readout by the
	// trigger source's (C1) probe.
	rep := getStatus(t, s)
	if rep["probe1"] != 10.0 || rep["probe2"] != 100.0 {
		t.Fatalf("status probes = %v / %v", rep["probe1"], rep["probe2"])
	}
	if tv10 := rep["trig_volts"].(float64); math.Abs(tv10-tv1*10) > 1e-9 {
		t.Fatalf("trig_volts not ×10: base %v got %v", tv1, tv10)
	}
}

func TestCouplingVerbs(t *testing.T) {
	fs := &fakeScope{}
	fa := &fakeAnalog{}
	s := New(fs, fa, nil, nil)

	if out := post(t, s, "coupling1", 2); out["ok"] != true || fa.cpl[0] != 2 {
		t.Fatalf("coupling1 GND: %v fa=%+v", out, fa)
	}
	if out := post(t, s, "coupling2", 1); out["ok"] != true || fa.cpl[1] != 1 {
		t.Fatalf("coupling2 AC: %v fa=%+v", out, fa)
	}
	if out := post(t, s, "coupling1", 5); out["ok"] != false {
		t.Fatalf("bad coupling accepted: %v", out)
	}
	rep := getStatus(t, s)
	if rep["cpl1"] != 2.0 || rep["cpl2"] != 1.0 {
		t.Fatalf("status couplings = %v / %v", rep["cpl1"], rep["cpl2"])
	}
}

func TestACCouplingRemovesDC(t *testing.T) {
	const N = 512
	// A DC-offset square-ish signal: mean well above centre.
	c1 := make([]uint8, N)
	for i := range c1 {
		if i%2 == 0 {
			c1[i] = 210
		} else {
			c1[i] = 190
		}
	}
	fs := &fakeScope{frameGen: func() *engine.Frame {
		return &engine.Frame{C1: c1, C2: c1, Seq: 3, Valid: N, WinCols: N, EdgeX: N / 2,
			TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9}
	}}
	fa := &fakeAnalog{}
	s := New(fs, fa, nil, nil)

	frameV := func() map[string]any {
		req := httptest.NewRequest("GET", "/api/frame", nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("frame json: %v", err)
		}
		return m
	}

	// DC-coupled: mean sits ~72 codes above centre → nonzero Vmean.
	dc := frameV()["m1"].(map[string]any)
	if math.Abs(dc["vmean"].(float64)) < 1e-6 {
		t.Fatalf("DC-coupled vmean unexpectedly ~0: %v", dc["vmean"])
	}
	// AC-coupled: software DC-block centres the record → Vmean ≈ 0, Vpp intact.
	post(t, s, "coupling1", 1) // AC
	ac := frameV()["m1"].(map[string]any)
	if math.Abs(ac["vmean"].(float64)) > math.Abs(dc["vmean"].(float64))*0.1 {
		t.Fatalf("AC vmean not removed: dc=%v ac=%v", dc["vmean"], ac["vmean"])
	}
	if math.Abs(ac["vpp"].(float64)-dc["vpp"].(float64)) > dc["vpp"].(float64)*0.2 {
		t.Fatalf("AC changed vpp too much: dc=%v ac=%v", dc["vpp"], ac["vpp"])
	}
}

func TestHoldoffVerb(t *testing.T) {
	fs := &fakeScope{}
	s := New(fs, nil, nil, nil)
	if out := post(t, s, "holdoff", 0.25); out["ok"] != true || out["applied"] != 0.25 {
		t.Fatalf("holdoff: %v", out)
	}
	if fs.holdoff != 0.25 {
		t.Fatalf("scope holdoff = %v, want 0.25", fs.holdoff)
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

// The /api/bode endpoint (hBode) JSON contract had no Go coverage.
func TestBodeEndpoint(t *testing.T) {
	fs := &fakeScope{bodePts: []engine.BodePoint{
		{FreqHz: 1e6, GainDB: -6, PhaseDeg: -45},
		{FreqHz: 2e6, GainDB: -12, PhaseDeg: -90},
	}}
	s := New(fs, nil, nil, nil)
	req := httptest.NewRequest("GET", "/api/bode", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var rep struct {
		OK    bool      `json:"ok"`
		N     int       `json:"n"`
		Freq  []float64 `json:"freq"`
		Gain  []float64 `json:"gain_db"`
		Phase []float64 `json:"phase_deg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, rec.Body.Bytes())
	}
	if !rep.OK || rep.N != 2 {
		t.Fatalf("ok=%v n=%d, want true/2", rep.OK, rep.N)
	}
	if len(rep.Freq) != 2 || rep.Freq[0] != 1e6 || rep.Freq[1] != 2e6 {
		t.Errorf("freq = %v", rep.Freq)
	}
	if rep.Gain[1] != -12 || rep.Phase[0] != -45 {
		t.Errorf("gain/phase mismatch: %v %v", rep.Gain, rep.Phase)
	}
	// empty curve → n:0, valid arrays (not null-crash)
	s2 := New(&fakeScope{}, nil, nil, nil)
	rec2 := httptest.NewRecorder()
	s2.Handler().ServeHTTP(rec2, httptest.NewRequest("GET", "/api/bode", nil))
	var rep2 map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &rep2); err != nil {
		t.Fatalf("empty bode bad JSON: %v", err)
	}
	if rep2["n"].(float64) != 0 {
		t.Errorf("empty bode n = %v, want 0", rep2["n"])
	}
}
