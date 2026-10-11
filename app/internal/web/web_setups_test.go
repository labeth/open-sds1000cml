// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
)

// fakeSetups records the slots saved and recalled; slot 7 is empty.
type fakeSetups struct{ saved, recalled []int }

// TRLC-LINKS: REQ-SDS-219
func (f *fakeSetups) SaveSetup(n int) error { f.saved = append(f.saved, n); return nil }

// TRLC-LINKS: REQ-SDS-219
func (f *fakeSetups) RecallSetup(n int) error {
	if n == 7 {
		return errors.New("setup slot 7 is empty")
	}
	f.recalled = append(f.recalled, n)
	return nil
}

// TRLC-LINKS: REQ-SDS-219
func setupCall(t *testing.T, s *Server, body string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/setup", bytes.NewReader([]byte(body))))
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json %q: %v", rec.Body.String(), err)
	}
	return out
}

// TestSetupEndpoint: the web's Setups save and recall through the wired setup
// store, report an empty slot and refuse bad input; without a store it says so.
// TRLC-LINKS: REQ-SDS-219
func TestSetupEndpoint(t *testing.T) {
	s := New(&fakeScope{}, nil, nil, nil)
	if r := setupCall(t, s, `{"op":"save","slot":3}`); r["ok"] != false || r["err"] != "setup memory unavailable" {
		t.Fatalf("no store: %v", r)
	}
	mem := &fakeSetups{}
	s.SetSetupMemory(mem)
	if r := setupCall(t, s, `{"op":"save","slot":3}`); r["ok"] != true {
		t.Fatalf("save: %v", r)
	}
	if r := setupCall(t, s, `{"op":"recall","slot":3}`); r["ok"] != true {
		t.Fatalf("recall: %v", r)
	}
	if r := setupCall(t, s, `{"op":"recall","slot":7}`); r["ok"] != false || r["err"] != "setup slot 7 is empty" {
		t.Fatalf("empty slot: %v", r)
	}
	for _, bad := range []string{`{"op":"save","slot":0}`, `{"op":"save","slot":21}`, `{"op":"erase","slot":2}`, `nope`} {
		if r := setupCall(t, s, bad); r["ok"] != false {
			t.Fatalf("%s accepted: %v", bad, r)
		}
	}
	if len(mem.saved) != 1 || mem.saved[0] != 3 || len(mem.recalled) != 1 || mem.recalled[0] != 3 {
		t.Fatalf("store calls: saved %v recalled %v", mem.saved, mem.recalled)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/setup", nil))
	if rec.Code != 405 {
		t.Fatalf("GET: %d", rec.Code)
	}
}
