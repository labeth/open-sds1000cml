// ENGMODEL-OWNER-UNIT: FU-WEB-REMOTE
package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel"
)

// get runs one GET (with extra headers) against s.
// TRLC-LINKS: REQ-SDS-220
func get(s *Server, path string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// The phone remote is its own page at /remote (/r redirects there) with the
// strict CSP, its own scripts and none of the main page's, the web app
// manifest and the home-screen icons, and the keep-awake videos served with
// byte ranges (iPhone Safari plays a video only so). Page files are not
// served by name.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-220
func TestRemotePageServed(t *testing.T) {
	s := New(&fakeScope{}, nil, nil, nil)
	rec := get(s, "/remote")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") || rec.Header().Get("Content-Security-Policy") != pageCSP {
		t.Fatalf("/remote: %d %q csp %q", rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Content-Security-Policy"))
	}
	for _, want := range []string{`<script src="remote_core.js">`, `<script src="remote_app.js">`, `rel="manifest" href="remote.webmanifest"`,
		`rel="apple-touch-icon" href="remote-icon-180.png"`, `name="apple-mobile-web-app-capable" content="yes"`, "viewport-fit=cover"} {
		if !strings.Contains(body, want) {
			t.Errorf("remote.html lacks %s", want)
		}
	}
	if strings.Contains(body, `src="app.js"`) || strings.Contains(body, `src="app_`) {
		t.Error("the remote loads the main page's scripts")
	}
	// every file the page names is served
	for _, m := range regexp.MustCompile(`(?:src|href)="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if r := get(s, "/"+m[1]); r.Code != 200 {
			t.Errorf("%s: %d", m[1], r.Code)
		}
	}
	if r := get(s, "/r"); r.Code != http.StatusFound || r.Header().Get("Location") != "/remote" {
		t.Fatalf("/r: %d -> %q", r.Code, r.Header().Get("Location"))
	}
	// the manifest: standalone from /remote, with icons that are served PNGs of their size
	r := get(s, "/remote.webmanifest")
	var man struct {
		StartURL string `json:"start_url"`
		Display  string `json:"display"`
		Icons    []struct{ Src, Sizes string }
	}
	if r.Code != 200 || r.Header().Get("Content-Type") != "application/manifest+json" || json.Unmarshal(r.Body.Bytes(), &man) != nil {
		t.Fatalf("manifest: %d %q", r.Code, r.Header().Get("Content-Type"))
	}
	if man.Display != "standalone" || man.StartURL != "/remote" || len(man.Icons) == 0 {
		t.Fatalf("manifest: %s", r.Body.String())
	}
	icons := append([]string{"remote-icon-180.png"}, func() (out []string) {
		for _, ic := range man.Icons {
			out = append(out, ic.Src)
		}
		return
	}()...)
	for _, src := range icons {
		ir := get(s, "/"+src)
		img, err := png.Decode(bytes.NewReader(ir.Body.Bytes()))
		if ir.Code != 200 || ir.Header().Get("Content-Type") != "image/png" || err != nil {
			t.Fatalf("%s: %d %q %v", src, ir.Code, ir.Header().Get("Content-Type"), err)
		}
		size := regexp.MustCompile(`(\d+)\.png$`).FindStringSubmatch(src)[1]
		if b := img.Bounds(); fmt.Sprint(b.Dx()) != size || b.Dx() != b.Dy() {
			t.Fatalf("%s is %v", src, b)
		}
	}
	// the keep-awake videos: small, with byte ranges
	for _, v := range []struct{ name, ct string }{{"remote-awake.mp4", "video/mp4"}, {"remote-awake.webm", "video/webm"}} {
		full := get(s, "/"+v.name)
		if full.Code != 200 || full.Header().Get("Content-Type") != v.ct || full.Header().Get("Accept-Ranges") != "bytes" || full.Body.Len() > 8<<10 {
			t.Fatalf("%s: %d %q ranges %q, %d bytes", v.name, full.Code, full.Header().Get("Content-Type"), full.Header().Get("Accept-Ranges"), full.Body.Len())
		}
		part := get(s, "/"+v.name, "Range", "bytes=0-1")
		if part.Code != http.StatusPartialContent || part.Body.Len() != 2 || !strings.HasPrefix(part.Header().Get("Content-Range"), "bytes 0-1/") {
			t.Fatalf("%s range: %d %d %q", v.name, part.Code, part.Body.Len(), part.Header().Get("Content-Range"))
		}
	}
	for _, p := range []string{"/remote.html", "/ui.html", "/remote/", "/nope.png", "/../remote.css"} {
		if r := get(s, p); r.Code == 200 {
			t.Errorf("%s served", p)
		}
	}
	// the main page's top bar points at the remote
	if !strings.Contains(get(s, "/").Body.String(), `id="remoteLink" class="tlink" href="remote"`) {
		t.Error("the main page has no Phone remote link")
	}
}

// The remote's files use no outside resource (the bench may have no
// internet): no external URLs, no inline script.
// TRLC-LINKS: REQ-SDS-220
func TestRemoteNoExternalResources(t *testing.T) {
	files, _ := filepath.Glob("remote*")
	ext := regexp.MustCompile(`(?i)(https?:)?//[a-z0-9.-]+\.[a-z]{2,}`)
	n := 0
	for _, f := range files {
		if !strings.HasSuffix(f, ".js") && !strings.HasSuffix(f, ".css") && !strings.HasSuffix(f, ".html") && !strings.HasSuffix(f, ".webmanifest") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		n++
		if m := ext.Find(b); m != nil && !strings.HasSuffix(f, "_test.go") {
			t.Errorf("%s names an outside resource: %s", f, m)
		}
		if strings.HasSuffix(f, ".html") {
			for _, seg := range strings.Split(string(b), "<script")[1:] {
				if !strings.Contains(seg[:strings.IndexByte(seg, '>')], "src=") {
					t.Errorf("%s has an inline script", f)
				}
			}
		}
	}
	if n < 6 {
		t.Fatalf("checked only %d remote files", n)
	}
}

// remoteFixture is the panel's remote surface with canned workspace answers;
// it records what the server asked for.
type remoteFixture struct {
	panelFixture
	ws    string
	again int
}

// TRLC-LINKS: REQ-SDS-220
func (p *remoteFixture) RemoteWorkspace(name string) panel.SetResult {
	p.ws = name
	if name == "nope" {
		return panel.SetResult{Row: "workspace", Error: `no workspace "nope": SCOPE, BUS, SPECTRUM, MEASURE or COMPARE`}
	}
	p.status.Workspace, p.status.WorkspaceID, p.status.View, p.status.AgainNext = strings.ToUpper(name), name, "Packets", "Eye diagram"
	if name == "measure" {
		p.status.Notice = "Measuring Vpp and Freq - the Measure key picks others"
	}
	return panel.SetResult{OK: true, Row: "workspace", Value: name, Text: strings.ToUpper(name)}
}

// TRLC-LINKS: REQ-SDS-220
func (p *remoteFixture) RemoteAgain() panel.SetResult {
	p.again++
	p.status.View, p.status.AgainNext = "Eye diagram", "Packets"
	return panel.SetResult{OK: true, Row: "again", Value: "Eye diagram", Text: "Eye diagram"}
}

// /api/panel takes the remote's workspace keys: {"workspace":name} and
// {"again":true}, answering with the workspace, its view, what again does
// next and the Seq; errors pass through; a panel without them says so.
// TRLC-LINKS: REQ-SDS-162, REQ-SDS-220
func TestPanelWorkspaceEndpoint(t *testing.T) {
	p := &remoteFixture{panelFixture: panelFixture{status: panel.Status{Seq: 4, Workspace: "SCOPE", WorkspaceID: "scope"}}}
	s := New(&fakeScope{}, nil, p, nil)
	_, out := httpCall(t, s, "POST", "/api/panel", map[string]any{"workspace": "bus"})
	if out["ok"] != true || p.ws != "bus" || out["workspace"] != "bus" || out["name"] != "BUS" || out["view"] != "Packets" || out["next"] != "Eye diagram" || out["seq"] != 4.0 {
		t.Fatalf("workspace: %v (asked %q)", out, p.ws)
	}
	_, out = httpCall(t, s, "POST", "/api/panel", map[string]any{"again": true})
	if out["ok"] != true || p.again != 1 || out["view"] != "Eye diagram" || out["next"] != "Packets" {
		t.Fatalf("again: %v", out)
	}
	// the notice the switch raised, and not one the LCD was still showing
	_, out = httpCall(t, s, "POST", "/api/panel", map[string]any{"workspace": "measure"})
	if out["notice"] != "Measuring Vpp and Freq - the Measure key picks others" {
		t.Fatalf("the switch's notice: %v", out)
	}
	_, out = httpCall(t, s, "POST", "/api/panel", map[string]any{"workspace": "bus"})
	if out["ok"] != true || out["notice"] != "" {
		t.Fatalf("a notice from before the switch was passed on: %v", out)
	}
	_, out = httpCall(t, s, "POST", "/api/panel", map[string]any{"workspace": "nope"})
	if out["ok"] != false || !strings.Contains(out["error"].(string), `no workspace "nope"`) {
		t.Fatalf("a refused switch: %v", out)
	}
	if p.writes != 0 {
		t.Fatal("the workspace keys pressed a button")
	}
	_, out = httpCall(t, New(&fakeScope{}, nil, &panelStatusOnly{}, nil), "POST", "/api/panel", map[string]any{"again": true})
	if out["ok"] != false || out["error"] != "no panel" {
		t.Fatalf("a panel without the workspace keys: %v", out)
	}
	_, out = httpCall(t, New(&fakeScope{}, nil, nil, nil), "POST", "/api/panel", map[string]any{"workspace": "bus"})
	if out["ok"] != false {
		t.Fatalf("no panel: %v", out)
	}
}

// End to end with the real controller: while the LCD has a sheet open (Trig
// Menu pressed on the scope), the remote switches the workspace and runs its
// again; the sheet stays open on its tab and row, where F3 would have picked
// one of its choices.
// TRLC-LINKS: REQ-SDS-162, REQ-SDS-211, REQ-SDS-220
func TestPanelWorkspaceWithAnLCDSheetOpen(t *testing.T) {
	ps := &panelScope{fakeScope: &fakeScope{stats: engine.Stats{Running: true, TdivS: 500e-6, TrigCode: 31437, TrigPosFrac: 0.5}}}
	pc := panel.New(ps, nil, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	stop := make(chan struct{})
	defer close(stop)
	go pc.Run(stop)
	s := New(ps, nil, pc, nil)
	if _, out := httpCall(t, s, "POST", "/api/panel", map[string]any{"button": "trigmenu"}); out["ok"] != true {
		t.Fatalf("Trig Menu: %v", out)
	}
	var before panel.Status
	for i := 0; i < 200; i++ {
		if before = pc.PanelStatus(); before.Mode == "sheet" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if before.Mode != "sheet" || before.Sheet.Tab != "trig" {
		t.Fatalf("the LCD: %s %+v", before.Mode, before.Sheet)
	}
	_, out := httpCall(t, s, "POST", "/api/panel", map[string]any{"workspace": "spectrum"})
	st := pc.PanelStatus()
	if out["ok"] != true || out["name"] != "SPECTRUM" || st.Workspace != "SPECTRUM" {
		t.Fatalf("switch: %v (%s)", out, st.Workspace)
	}
	if st.Mode != "sheet" || st.Sheet.Tab != "trig" || st.Sheet.Row.ID != before.Sheet.Row.ID || st.FKeys != before.FKeys {
		t.Fatalf("the LCD's sheet moved: %s %+v", st.Mode, st.Sheet)
	}
	_, out = httpCall(t, s, "POST", "/api/panel", map[string]any{"again": true})
	if out["ok"] != true || out["view"] != "Spectrogram" || out["next"] != "Bode" || pc.PanelStatus().Sheet.Tab != "trig" {
		t.Fatalf("again: %v", out)
	}
}
