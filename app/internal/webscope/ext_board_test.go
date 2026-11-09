package webscope

import (
	"slices"
	"testing"

	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
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
		for _, r := range sc.Scope.Extensions() {
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
