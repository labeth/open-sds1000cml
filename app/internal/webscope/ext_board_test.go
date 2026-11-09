package webscope

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// The scope's own routes are its board's extensions where it has a board,
// one extension a feature in their order, served once; without a board they
// are the scope's routes.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-022
func TestExtensionsOnTheBoard(t *testing.T) {
	g := newGoldenEngine(goldenNative(31))
	fe := &fakeAnalog{idx: 8, set: true}
	b := acq.New(acq.Config{})
	s := New(g, fe, levelsFor(g, fe), nil, nil, WithBoard(b))
	var features []string
	for _, x := range b.Extensions() {
		features = append(features, x.Feature)
	}
	if n := len(features); n < 2 || features[n-2] != "panel_scan" || features[n-1] != "debug_tune" {
		t.Fatalf("board extensions %v", features)
	}
	served := func(sc *Scope) []string {
		var out []string
		for _, r := range sc.Extensions() {
			out = append(out, r.Feature+" "+r.Pattern)
		}
		return out
	}
	var want []string
	for _, r := range routesOf(b.Extensions()) {
		want = append(want, r.Feature+" "+r.Pattern)
	}
	if got := served(s); !slices.Equal(got, want) {
		t.Fatalf("served %v, the board's %v", got, want)
	}
	if got := served(New(g, fe, levelsFor(g, fe), nil, nil)); !slices.Equal(got, want) {
		t.Fatalf("without a board %v, want %v", got, want)
	}
}

// stackSource records the stacking sessions it is given.
type stackSource struct{ reqs []engine.FPGAStackRequest }

func (s *stackSource) FPGAStack(_ context.Context, req engine.FPGAStackRequest) (engine.FPGAStackResult, error) {
	s.reqs = append(s.reqs, req)
	return engine.FPGAStackResult{Records: req.Records}, nil
}

// The tuning knobs a board carries are its own (the route reads and sets
// the board's), and the stacking session goes to the source the scope is
// given (the board's on the core's cycle) instead of the engine's.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-141
func TestExtensionSourcesOverTheBoard(t *testing.T) {
	g := newGoldenEngine(goldenNative(31))
	fe := &fakeAnalog{idx: 8, set: true}
	b := acq.New(acq.Config{})
	src := &stackSource{}
	s := New(g, fe, levelsFor(g, fe), nil, nil, WithBoard(b), WithStacker(src))
	srv := webserver.New(s)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	var got acq.TuneVals
	if err := json.Unmarshal(do(http.MethodGet, "/api/debug/tune", "").Body.Bytes(), &got); err != nil || got != b.TuneSnapshot() {
		t.Fatalf("tune GET %+v (%v), the board's %+v", got, err, b.TuneSnapshot())
	}
	do(http.MethodPost, "/api/debug/tune", `{"max_retry":5}`)
	if b.TuneSnapshot().MaxRetry != 5 {
		t.Fatalf("tune POST left the board's knobs at %+v", b.TuneSnapshot())
	}
	if rec := do(http.MethodPost, "/api/superres/fpga", `{"records":3}`); rec.Code != http.StatusOK || len(src.reqs) != 1 || src.reqs[0].Records != 3 {
		t.Fatalf("stack POST %d %q, source got %+v", rec.Code, rec.Body.String(), src.reqs)
	}
}
