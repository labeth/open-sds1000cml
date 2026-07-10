package web

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"open-sds/app/internal/engine"
)

// TestStatusInvertSource pins the server half of the display-level INVS
// plumbing: /api/status carries inv1/inv2 (exact wire names the page reads)
// from the wired invert source — the SCPI handler's shadow in production —
// and reports false/false when no source is wired (tests, no SCPI).
func TestStatusInvertSource(t *testing.T) {
	fs := &fakeScope{stats: engine.Stats{Running: true}}
	s := New(fs, nil, nil, nil)

	get := func() (bool, bool) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.hStatus(rec, httptest.NewRequest("GET", "/api/status", nil))
		// Decode with the page's wire names, so a JSON-tag change fails here.
		var rep struct {
			Inv1 bool `json:"inv1"`
			Inv2 bool `json:"inv2"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatalf("status decode: %v", err)
		}
		return rep.Inv1, rep.Inv2
	}

	if i1, i2 := get(); i1 || i2 {
		t.Fatalf("no invert source wired: got inv1=%v inv2=%v, want false/false", i1, i2)
	}

	inv := [2]bool{}
	s.SetInvertSource(func() [2]bool { return inv })
	if i1, i2 := get(); i1 || i2 {
		t.Fatalf("source OFF/OFF: got inv1=%v inv2=%v", i1, i2)
	}
	inv = [2]bool{true, false}
	if i1, i2 := get(); !i1 || i2 {
		t.Fatalf("source ON/OFF: got inv1=%v inv2=%v", i1, i2)
	}
	inv = [2]bool{false, true}
	if i1, i2 := get(); i1 || !i2 {
		t.Fatalf("source OFF/ON: got inv1=%v inv2=%v", i1, i2)
	}
}
