package web

import (
	"net/http"
	"testing"
)

// The v3 rung endpoints answer over the handler-test fabric (which has no
// drain monitors: the verdicts are "not exact", the routes and shapes are
// what is checked here; the rung logic is tested in internal/diag).
func TestDiagV3Routes(t *testing.T) {
	s, _ := diagServer(t)
	h := s.Handler()
	rec, m := call(t, h, http.MethodGet, "/api/diag/gpmc", "")
	if rec.Code != 200 || m["port"] != false || m["err"] == nil {
		t.Fatalf("gpmc status without a timing port: %d %v", rec.Code, m)
	}
	rec, m = call(t, h, http.MethodPost, "/api/diag/gpmc", `{"action":"sweep"}`)
	if rec.Code != 400 || m["ok"] != false {
		t.Fatalf("sweep without a timing port must refuse: %d %v", rec.Code, m)
	}
	rec, m = call(t, h, http.MethodPost, "/api/diag/gpmc", `{"action":"nope"}`)
	if rec.Code != 400 {
		t.Fatalf("bad action: %d %v", rec.Code, m)
	}
	rec, m = call(t, h, http.MethodGet, "/api/diag/schema?writes=2", "")
	if rec.Code != 200 || m["registers"] == nil || m["restored"] != true {
		t.Fatalf("schema: %d %v", rec.Code, m["err"])
	}
	rec, m = call(t, h, http.MethodGet, "/api/diag/redrain?words=64&windows=2&passes=1", "")
	if rec.Code != 200 || m["rec_len"].(float64) != 64 || len(m["windows"].([]any)) != 2 || m["restored"] != true {
		t.Fatalf("redrain: %d %v", rec.Code, m["err"])
	}
	rec, m = call(t, h, http.MethodGet, "/api/diag/capture?words=64&tsrc=1&chmode=2&start=8&len=16", "")
	if rec.Code != 200 || m["tsrc"].(float64) != 1 || m["chmode"].(float64) != 2 || m["window"].(map[string]any)["start"].(float64) != 8 {
		t.Fatalf("capture options: %d %v", rec.Code, m)
	}
	if _, ok := m["exact"].(map[string]any); !ok {
		t.Fatalf("capture carries no exactness verdict: %v", m)
	}
}
