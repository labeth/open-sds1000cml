package web

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestSerialTriggerEndpoints(t *testing.T) {
	fs := &fakeScope{}
	s := New(fs, nil, nil, nil)

	// config upload → SetSerialParams (with clamping)
	body, _ := json.Marshal(map[string]any{
		"proto": 2, "chA": 0, "chB": 1, "addr": 0x50, "rw": 0,
		"bytes": []int{0xDE, 0x1FF /*→clamped 255*/, -3 /*→0*/},
	})
	req := httptest.NewRequest("POST", "/api/serial", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("/api/serial code=%d", rec.Code)
	}
	p := fs.serialParams
	if p.Proto != 2 || p.ChA != 0 || p.ChB != 1 || p.Addr != 0x50 || p.RW != 0 {
		t.Fatalf("serial params not applied: %+v", p)
	}
	if !reflect.DeepEqual(p.Bytes, []int{0xDE, 255, 0}) {
		t.Fatalf("bytes not clamped: %v", p.Bytes)
	}

	// arm via /api/set
	post(t, s, "serialmode", 1)
	if fs.serialMode != 1 {
		t.Fatalf("serialmode not armed: %d", fs.serialMode)
	}
	post(t, s, "serialmode", 0)
	if fs.serialMode != 0 {
		t.Fatalf("serialmode not disarmed: %d", fs.serialMode)
	}

	// bad json → ok:false, no panic
	req = httptest.NewRequest("POST", "/api/serial", bytes.NewReader([]byte("{garbage")))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["ok"] != false {
		t.Fatalf("bad json should return ok:false, got %v", out)
	}

	// GET on the POST-only endpoint → 405
	req = httptest.NewRequest("GET", "/api/serial", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Fatalf("GET /api/serial code=%d, want 405", rec.Code)
	}
}
