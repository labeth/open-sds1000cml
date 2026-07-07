package web

import (
	"fmt"
	"math"
	"net/http/httptest"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// API fuzz: every endpoint is fed malformed, extreme, and hostile inputs.
// The server contract under attack is narrow — no panic (each panic is a
// remotely-triggerable crash of the scope), no unbounded allocation, and a
// well-formed JSON error for garbage. A fakeScope keeps verdicts local; the
// engine-level effects are separately covered by the engine suites.
func TestAPIFuzz(t *testing.T) {
	n := uint64(0)
	gen := func() *engine.Frame {
		n++
		c1 := make([]uint8, 2048)
		for i := range c1 {
			c1[i] = uint8(128 + 60*math.Sin(float64(i)/32))
		}
		return &engine.Frame{C1: c1, C2: c1, Seq: n, Valid: 2048, WinCols: 2048,
			EdgeX: 1024, TdivS: 500e-6, SampleS: 800e-9, Trigd: true, Ptp: 120}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TrigPosFrac: 0.5, WinCols: 2048}}
	srv := httptest.NewServer(New(fs, nil, nil, nil).Handler())
	defer srv.Close()
	client := srv.Client()

	post := func(path, body string) int {
		r, err := client.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST %s: transport error: %v (server crashed?)", path, err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	get := func(path string) int {
		r, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: transport error: %v (server crashed?)", path, err)
		}
		r.Body.Close()
		return r.StatusCode
	}

	// ---- /api/set: garbage bodies + extreme values on every verb ----
	bodies := []string{
		``, `{`, `null`, `[]`, `"x"`, `{"control":null}`, `{"control":123,"value":"y"}`,
		`{"control":"tdiv"}`, `{"control":"tdiv","value":1e308}`, `{"control":"tdiv","value":-1e308}`,
		`{"control":"tdiv","value":0}`, `{"control":"memdepth","value":-1e12}`,
		`{"control":"avgcount","value":0}`, `{"control":"avgcount","value":1e9}`,
		`{"control":"eres","value":-5}`, `{"control":"holdoff","value":1e300}`,
		`{"control":"trigpos","value":-99}`, `{"control":"trigpos","value":99}`,
		`{"control":"zonemode","value":9e9}`, `{"control":"maskmode","value":-3}`,
		`{"control":"framePeriod","value":-1}`, `{"control":"zoom","value":1e9}`,
		`{"control":"triglevelcode","value":1e9}`, `{"control":"vdiv1","value":0}`,
		`{"control":"offset1","value":-1e308}`, `{"control":"decbaud","value":0}`,
		`{"control":"` + strings.Repeat("A", 3000) + `","value":1}`,
		`{"control":"tdiv","value":` + strings.Repeat("9", 3500) + `}`,
	}
	for i, b := range bodies {
		if code := post("/api/set", b); code >= 500 {
			t.Fatalf("set body %d (%.60q): status %d", i, b, code)
		}
	}
	// oversized body must be rejected without buffering it all
	if code := post("/api/set", `{"control":"pad","value":1,"x":"`+strings.Repeat("A", 1<<20)+`"}`); code >= 500 {
		t.Fatalf("oversized set body: status %d", code)
	}

	// ---- /api/zones: hostile arrays ----
	zbodies := []string{
		``, `{}`, `[{}]`, `[null]`, `[{"dt_lo_s":1e308,"dt_hi_s":-1e308,"code_lo":-9999999,"code_hi":99999999}]`,
		`[` + strings.Repeat(`{"dt_lo_s":0,"dt_hi_s":1,"code_lo":0,"code_hi":255},`, 200) + `{}]`,
		`[{"dt_lo_s":"x"}]`,
	}
	for i, b := range zbodies {
		if code := post("/api/zones", b); code >= 500 {
			t.Fatalf("zones body %d: status %d", i, code)
		}
	}
	// zone count must stay capped no matter what was sent
	if len(fs.zones) > 4 {
		t.Fatalf("zone cap breached: %d installed", len(fs.zones))
	}

	// ---- /api/mask: shape lies + memory bombs ----
	mbodies := []string{
		``, `{}`, `{"win":-1,"lo":[],"hi":[]}`,
		`{"win":3,"lo":[1,2],"hi":[3,4,5]}`,
		`{"win":2,"lo":[999,-4],"hi":[1,2]}`,
		`{"win":1000000000,"lo":[1],"hi":[2]}`,
		`{"win":2,"lo":[1,2],"hi":[3,4],"ch":99}`,
		fmt.Sprintf(`{"win":%d,"lo":[%s1],"hi":[%s2]}`, 60000, strings.Repeat("1,", 59999), strings.Repeat("2,", 59999)),
	}
	for i, b := range mbodies {
		if code := post("/api/mask", b); code >= 500 {
			t.Fatalf("mask body %d: status %d", i, code)
		}
	}
	if fs.mask != nil && (fs.mask.WinCols < 0 || fs.mask.WinCols > 1<<20 || len(fs.mask.Lo) != fs.mask.WinCols) {
		t.Fatalf("hostile mask installed: win=%d len=%d", fs.mask.WinCols, len(fs.mask.Lo))
	}

	// ---- GET endpoints with hostile queries ----
	gets := []string{
		"/api/frame.bin?since=-1&waitms=-5&raw=2",
		"/api/frame.bin?since=99999999999999999999&waitms=999999999",
		"/api/frame.bin?since=NaN&waitms=abc",
		"/api/frame?raw=1",
		"/api/maskfail?i=-1", "/api/maskfail?i=999999999999999999", "/api/maskfail?i=x",
		"/api/status", "/api/frame.bin", "/api/measure?ch=99",
	}
	for _, g := range gets {
		if code := get(g); code >= 500 {
			t.Fatalf("GET %s: status %d", g, code)
		}
	}

	// ---- /api/panel without a panel wired ----
	for _, b := range []string{`{"button":"` + strings.Repeat("Z", 3000) + `"}`, `{"knob":"adjust","dir":9,"steps":-9}`, `{`} {
		if code := post("/api/panel", b); code >= 500 {
			t.Fatalf("panel fuzz: status %d", code)
		}
	}

	// the server must still be alive and serving real frames after all of it
	r, err := client.Get(srv.URL + "/api/status")
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("server unhealthy after fuzz: %v %v", err, r)
	}
	r.Body.Close()
}
