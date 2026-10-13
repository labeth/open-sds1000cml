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
// GET lists what the slots hold when the store can say.
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
	// GET: what the slots hold, when the store can say (null when it cannot)
	get := func() map[string]any {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/setup", nil))
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("GET: bad json %q: %v", rec.Body.String(), err)
		}
		return out
	}
	if r := get(); r["ok"] != true || r["slots"] != nil {
		t.Fatalf("GET without a lister: %v", r)
	}
	s.SetSetupMemory(&wsSetups{saved: []int{4}})
	r := get()
	slots, _ := r["slots"].([]any)
	if len(slots) != 20 {
		t.Fatalf("GET: %v", r)
	}
	if s4 := slots[3].(map[string]any); s4["slot"] != 4.0 || s4["used"] != true || s4["summary"] != "SCOPE  1ms/div  C1^ 0V  C1 1V C2 1V" {
		t.Fatalf("slot 4: %v", s4)
	}
	if s5 := slots[4].(map[string]any); s5["used"] != false || s5["summary"] != nil {
		t.Fatalf("slot 5: %v", s5)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("PUT", "/api/setup", nil))
	if rec.Code != 405 {
		t.Fatalf("PUT: %d", rec.Code)
	}
}
