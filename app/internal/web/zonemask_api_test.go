package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

func TestZoneMaskAPI(t *testing.T) {
	fs := &fakeScope{}
	s := New(fs, nil, nil, nil)
	mux := s.Handler()

	// zones install (clamped, capped at 4)
	body := `[{"dt_lo_s":-1e-6,"dt_hi_s":2e-6,"code_lo":-5,"code_hi":300,"avoid":true,"ch":3},
	          {"dt_lo_s":0,"dt_hi_s":1e-6,"code_lo":10,"code_hi":20}]`
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("POST", "/api/zones", strings.NewReader(body)))
	var rep map[string]any
	json.Unmarshal(rr.Body.Bytes(), &rep)
	if rep["ok"] != true || rep["zones"].(float64) != 2 {
		t.Fatalf("zones reply: %v", rep)
	}
	if len(fs.zones) != 2 || fs.zones[0].CodeLo != 0 || fs.zones[0].CodeHi != 255 || fs.zones[0].Ch != 1 || !fs.zones[0].Avoid {
		t.Fatalf("zones clamped wrong: %+v", fs.zones)
	}

	// mask upload + clear
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("POST", "/api/mask", strings.NewReader(`{"lo":[10,10],"hi":[200,200],"win":2,"ch":0}`)))
	json.Unmarshal(rr.Body.Bytes(), &rep)
	if rep["ok"] != true || fs.mask == nil || fs.mask.WinCols != 2 || fs.mask.Hi[1] != 200 {
		t.Fatalf("mask upload: %v %+v", rep, fs.mask)
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("POST", "/api/mask", strings.NewReader(`{"lo":[],"hi":[],"win":0}`)))
	if fs.mask != nil {
		t.Fatal("empty mask must clear")
	}
	// length mismatch rejected
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("POST", "/api/mask", strings.NewReader(`{"lo":[1,2,3],"hi":[4],"win":3}`)))
	json.Unmarshal(rr.Body.Bytes(), &rep)
	if rep["ok"] != false {
		t.Fatal("length mismatch must be rejected")
	}

	// maskfail gallery
	fs.maskRing = []engine.MaskFail{{C1: []uint8{1, 2, 3}, C2: []uint8{4, 5, 6}, Valid: 3, Seq: 42, FailCol: 7, FailCode: 99, AtNs: 1500000}}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/maskfail?i=0", nil))
	json.Unmarshal(rr.Body.Bytes(), &rep)
	if rep["ok"] != true || rep["seq"].(float64) != 42 || rep["fail_code"].(float64) != 99 {
		t.Fatalf("maskfail: %v", rep)
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/maskfail?i=5", nil))
	json.Unmarshal(rr.Body.Bytes(), &rep)
	if rep["ok"] != false {
		t.Fatal("out-of-range index must report no such failure")
	}

	// set verbs
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("POST", "/api/set", strings.NewReader(`{"control":"zonemode","value":1}`)))
	if fs.zoneMode != 1 {
		t.Fatal("zonemode not applied")
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("POST", "/api/set", strings.NewReader(`{"control":"maskmode","value":2}`)))
	if fs.maskMode != 2 {
		t.Fatal("maskmode not applied")
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("POST", "/api/set", strings.NewReader(`{"control":"maskclear","value":0}`)))
	if !fs.maskCleared {
		t.Fatal("maskclear not applied")
	}
}
