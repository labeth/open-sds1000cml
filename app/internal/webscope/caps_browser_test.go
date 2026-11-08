// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/profile"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/testenv"
	"github.com/labeth/open-dso-core/webclient"
	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
)

// cmlBoard is the scope's HAL board as the app composes it for the web: its
// profile and front end.
// TRLC-LINKS: REQ-SDS-164
func cmlBoard(t *testing.T, fe *analog.FrontEnd) hal.Board {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(pkgDir, "..", "..", "profile.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := profile.Load(data, hal.Identity{Model: "SDS1102CML+"})
	if err != nil {
		t.Fatal(err)
	}
	b := acq.New(acq.Config{})
	b.SetProfile(p)
	b.SetFrontEnd(fe)
	return b
}

// capsPage runs the core's caps_browser.mjs against a server over sc and
// returns its report as a JSON object.
// TRLC-LINKS: REQ-SDS-164
func capsPage(t *testing.T, sc webserver.Scope) map[string]any {
	t.Helper()
	srv := httptest.NewServer(webserver.New(sc).Handler())
	defer srv.Close()
	cmd := exec.CommandContext(t.Context(), "node", "caps_browser.mjs", srv.URL)
	cmd.Dir = webclient.Dir()
	out, err := cmd.CombinedOutput()
	if strings.HasPrefix(string(out), "SKIP") {
		testenv.SkipBrowser(t, "%s", out)
	}
	if err != nil {
		t.Fatalf("caps_browser.mjs: %v\n%s", err, out)
	}
	var r map[string]any
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if e, _ := r["errors"].([]any); len(e) > 0 {
		t.Fatalf("page errors: %v", e)
	}
	return r
}

// The page over this scope serving its capabilities is the page over it
// serving none: the same channels, lists, tables, trigger types and drawn
// frame, frames still in wire v1, and nothing asked besides the caps
// document.
// TRLC-LINKS: REQ-SDS-164
func TestCapsBrowserSamePage(t *testing.T) {
	testenv.NeedNode(t)
	scope := func(withCaps bool) webserver.Scope {
		g := newGoldenEngine(goldenNative(31))
		g.stats.WinCols = 1000 // the window the mask builder reads
		start := time.Now()
		g.frameGen = func() *sample.Frame { // a new frame every 50 ms, as a running scope publishes
			return goldenNative(31 + uint64(time.Since(start)/(50*time.Millisecond)))
		}
		fa := &fakeAnalog{idx: 8, set: true, probe: [2]float64{1, 1}}
		if !withCaps {
			return New(g, fa, levelsFor(g, fa), nil, nil)
		}
		fe := analog.New(remoteSPI{}, func(time.Duration) {}, nil)
		return New(g, fa, levelsFor(g, fa), nil, nil, WithBoard(cmlBoard(t, fe))).WithCaps()
	}
	plain := capsPage(t, scope(false))
	served := capsPage(t, scope(true))
	if plain["capsServed"] != false || served["capsServed"] != true || plain["wire"] != 1.0 || served["wire"] != 1.0 {
		t.Fatalf("caps served %v/%v, wire %v/%v; with caps the page asked %v", plain["capsServed"], served["capsServed"], plain["wire"], served["wire"], served["requests"])
	}
	// A request is its method and path: a body built from a live frame (the
	// mask) differs between the two pages because the frames move on.
	reqs := func(r map[string]any) []string {
		var out []string
		qs, _ := r["requests"].([]any)
		for _, q := range qs {
			if q, ok := q.(string); ok {
				f := strings.Fields(q)
				out = append(out, strings.Join(f[:min(2, len(f))], " "))
			}
		}
		return out
	}
	var extra []string
	for _, q := range reqs(served) {
		if !slices.Contains(reqs(plain), q) {
			extra = append(extra, q)
		}
	}
	if !reflect.DeepEqual(extra, []string{"GET /api/caps"}) {
		t.Fatalf("with caps the page asked besides: %v", extra)
	}
	// the title and the brand name the firmware and the unit from the caps'
	// identity: the things the caps change
	if t0, _ := served["title"].(string); !strings.Contains(t0, "open-sds1000cml") || !strings.Contains(t0, "SDS1102CML+") {
		t.Fatalf("with caps the title is %q, not the unit's", t0)
	}
	for _, k := range []string{"capsServed", "requests", "brand", "title", "brandName"} {
		delete(plain, k)
		delete(served, k)
	}
	if !reflect.DeepEqual(plain, served) {
		ja, _ := json.Marshal(plain)
		jb, _ := json.Marshal(served)
		t.Fatalf("the page differs with caps:\n without %s\n    with %s", ja, jb)
	}
}
