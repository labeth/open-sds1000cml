package web

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// siggenPanel is a minimal Panel that also implements siggenControl, recording the
// last SetSiggen call so the web hook can be asserted end-to-end.
type siggenPanel struct {
	called   bool
	on, ramp bool
	failInj  bool // simulate a full inject queue → SetSiggen returns false
}

func (p *siggenPanel) InjectButton(string) bool         { return true }
func (p *siggenPanel) InjectKnob(string, int, int) bool { return true }
func (p *siggenPanel) SetSiggen(on, ramp bool) bool {
	if p.failInj {
		return false
	}
	p.called, p.on, p.ramp = true, on, ramp
	return true
}

// postSiggen posts /api/set {control:"siggen", value, shape} and returns the reply.
func postSiggen(t *testing.T, s *Server, value float64, shape bool) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"control": "siggen", "value": value, "shape": shape})
	req := httptest.NewRequest("POST", "/api/set", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json reply: %v", err)
	}
	return out
}

// TestSetSiggen proves the /api/set siggen verb delegates to panel.SetSiggen with the
// right (on, ramp) — enable + shape — and surfaces failure cleanly.
func TestSetSiggen(t *testing.T) {
	fs := &fakeScope{}
	p := &siggenPanel{}
	s := New(fs, nil, p, nil)

	// enable, triangle (shape=false).
	if out := postSiggen(t, s, 1, false); out["ok"] != true {
		t.Fatalf("siggen on/triangle: %v", out)
	}
	if !p.called || !p.on || p.ramp {
		t.Fatalf("SetSiggen(on=true,ramp=false) not delegated: %+v", p)
	}

	// enable, ramp (shape=true).
	p.called = false
	if out := postSiggen(t, s, 1, true); out["ok"] != true {
		t.Fatalf("siggen on/ramp: %v", out)
	}
	if !p.called || !p.on || !p.ramp {
		t.Fatalf("SetSiggen(on=true,ramp=true) not delegated: %+v", p)
	}

	// disable.
	p.called = false
	if out := postSiggen(t, s, 0, false); out["ok"] != true {
		t.Fatalf("siggen off: %v", out)
	}
	if !p.called || p.on {
		t.Fatalf("SetSiggen(off) not delegated: %+v", p)
	}
}

// TestSetSiggenUnavailable proves the verb reports cleanly when the panel is absent
// (no siggenControl) or its inject queue is full — never a silent success.
func TestSetSiggenUnavailable(t *testing.T) {
	// No panel at all → "siggen control unavailable".
	s := New(&fakeScope{}, nil, nil, nil)
	if out := postSiggen(t, s, 1, false); out["ok"] != false {
		t.Fatalf("siggen with nil panel should fail: %v", out)
	}

	// Panel present but inject queue full → SetSiggen returns false → ok=false.
	p := &siggenPanel{failInj: true}
	s2 := New(&fakeScope{}, nil, p, nil)
	if out := postSiggen(t, s2, 1, false); out["ok"] != false {
		t.Fatalf("siggen with full inject queue should fail: %v", out)
	}
}
