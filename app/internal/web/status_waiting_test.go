// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// sinceScope is a fake scope whose last frame came d ago.
type sinceScope struct {
	*fakeScope
	d time.Duration
}

// TRLC-LINKS: REQ-SDS-204
func (s sinceScope) SincePublish() time.Duration { return s.d }

// The status says the trigger waits, and how long, for every trigger the
// FPGA decides: NORMAL, and in AUTO an armed serial match, zones or a
// qualifier; a plain AUTO edge free-runs and never waits.
// TRLC-LINKS: REQ-SDS-204, REQ-SDS-206
func TestStatusWaitingForADecidedTrigger(t *testing.T) {
	status := func(st engine.Stats) (bool, float64) {
		s := New(sinceScope{&fakeScope{stats: st}, 6 * time.Second}, nil, nil, nil)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/status", nil))
		var rep struct {
			Waiting bool    `json:"waiting"`
			WaitS   float64 `json:"wait_s"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
			t.Fatal(err)
		}
		return rep.Waiting, rep.WaitS
	}
	base := engine.Stats{Running: true, TdivS: 1e-3}
	for name, st := range map[string]engine.Stats{
		"normal":       {Running: true, TdivS: 1e-3, Norm: true},
		"auto, serial": {Running: true, TdivS: 1e-3, SerialMode: 1},
		"auto, zones":  {Running: true, TdivS: 1e-3, ZoneMode: 1},
		"auto, pulse":  {Running: true, TdivS: 1e-3, TrigType: 1},
	} {
		if w, s := status(st); !w || s != 6 {
			t.Fatalf("%s: waiting %v %v s, want true 6", name, w, s)
		}
	}
	if w, s := status(base); w || s != 0 {
		t.Fatalf("auto edge: waiting %v %v s, want false", w, s)
	}
	stopped := engine.Stats{TdivS: 1e-3, Norm: true}
	if w, _ := status(stopped); w {
		t.Fatal("stopped: waiting")
	}
}
