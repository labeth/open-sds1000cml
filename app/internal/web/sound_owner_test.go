// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel"
	"open-sds/app/internal/testenv"
)

// ownerScope is a scope with an audio stream behind the real panel: the
// panel's engine and the server's, its lease kept as an expiry (0 ends it)
// and every lease counted.
// TRLC-LINKS: REQ-SDS-277
type ownerScope struct {
	*panelScope
	snd    *soundScope
	lmu    sync.Mutex
	until  time.Time
	leases int
}

// TRLC-LINKS: REQ-SDS-277
func (o *ownerScope) LeaseSound(d time.Duration) {
	o.lmu.Lock()
	defer o.lmu.Unlock()
	if d <= 0 {
		o.until = time.Time{}
		return
	}
	o.until = time.Now().Add(d)
	o.leases++
}

// leased reports the lease running and the leases taken so far.
// TRLC-LINKS: REQ-SDS-277
func (o *ownerScope) leased() (bool, int) {
	o.lmu.Lock()
	defer o.lmu.Unlock()
	return time.Now().Before(o.until), o.leases
}

// TRLC-LINKS: REQ-SDS-277
func (o *ownerScope) SoundInfo() engine.SoundInfo {
	on, _ := o.leased()
	return engine.SoundInfo{State: "streaming", Active: on, RateHz: o.snd.fs, Next: o.snd.nextOrd(), Mode: "live"}
}

// TRLC-LINKS: REQ-SDS-277
func (o *ownerScope) SoundRead(from uint64, n int, q1, q2 []uint16) engine.SoundBlock {
	return o.snd.SoundRead(from, n, q1, q2)
}

// TRLC-LINKS: REQ-SDS-277
func (o *ownerScope) SoundWait(after uint64, timeout time.Duration) uint64 {
	return o.snd.SoundWait(after, timeout)
}

// newOwnerScope is the scope, the running panel and the server.
// TRLC-LINKS: REQ-SDS-277
func newOwnerScope(t *testing.T) (*ownerScope, *panel.Controller, *Server, func()) {
	gen := func() *engine.Frame {
		const N = 1024
		c1 := make([]uint8, N)
		c2 := make([]uint8, N)
		for i := range c1 {
			c1[i] = uint8(128 + 60*math.Sin(2*math.Pi*8*float64(i)/N))
			c2[i] = 128
		}
		return &engine.Frame{C1: c1, C2: c2, Seq: 1, Valid: N, WinCols: N, EdgeX: -1, TdivS: 1e-3, DisplayedS: 1e-3, SampleS: 1e-5, Trigd: true}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{Running: true, TdivS: 500e-6, TrigCode: 31437, TrigPosFrac: 0.5}}
	o := &ownerScope{panelScope: &panelScope{fakeScope: fs}, snd: &soundScope{fakeScope: fs, t0: time.Now(), fs: 500e6 / 8192}}
	pc := panel.New(o, nil, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	stop := make(chan struct{})
	go pc.Run(stop)
	return o, pc, New(o, nil, pc, nil), func() { close(stop) }
}

// soundBinHead reads a /api/sound.bin reply's header.
// TRLC-LINKS: REQ-SDS-277
func soundBinHead(t *testing.T, h http.Handler, url string) soundBinHeader {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
	b := rec.Body.Bytes()
	if len(b) < 8 || string(b[:2]) != "SB" {
		t.Fatalf("%s: %d %q", url, rec.Code, b)
	}
	n := binary.LittleEndian.Uint32(b[4:8])
	var hd soundBinHeader
	if err := json.Unmarshal(b[8:8+n], &hd); err != nil {
		t.Fatal(err)
	}
	return hd
}

// soundJSON runs /api/sound and returns its report.
// TRLC-LINKS: REQ-SDS-277
func soundJSON(t *testing.T, h http.Handler, method, url, body string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, url, strings.NewReader(body)))
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s %s: %v %q", method, url, err, rec.Body.String())
	}
	rep, _ := out["report"].(map[string]any)
	if rep == nil {
		t.Fatalf("%s %s: no report: %v", method, url, out)
	}
	return rep
}

// waitFor polls cond for up to two seconds.
// TRLC-LINKS: REQ-SDS-277
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); !cond(); {
		if time.Now().After(end) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The real panel behind the real handlers: a page's SOUND holds the stream
// and the LCD names it; a key pressed on the LCD (/api/panel) ends the
// lease, the page's stream replies come paused without leasing, its report
// says paused; "take_scope" streams again. The phone is named as the phone.
// TRLC-LINKS: REQ-SDS-277
func TestSoundLCDKeyPausesThePage(t *testing.T) {
	o, pc, s, done := newOwnerScope(t)
	defer done()
	h := s.Handler()
	if hd := soundBinHead(t, h, "/api/sound.bin?max=64"); hd.Paused {
		t.Fatal("a page's first request came paused")
	}
	if on, _ := o.leased(); !on {
		t.Fatal("the page did not lease the stream")
	}
	if n := pc.View().Notice; !strings.Contains(n, "web page is on SOUND") || !strings.Contains(n, "Press AUTO") {
		t.Fatalf("the LCD's notice: %q", n)
	}
	soundBinHead(t, h, "/api/sound.bin?max=64&who=phone")
	if n := pc.View().Notice; !strings.Contains(n, "a phone are on SOUND") {
		t.Fatalf("the LCD's notice for the web page and the phone: %q", n)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/panel", strings.NewReader(`{"button":"set50"}`)))
	waitFor(t, "the LCD's key took the scope", pc.SoundPaused)
	_, before := o.leased()
	hd := soundBinHead(t, h, "/api/sound.bin?max=64&wait=50")
	if on, after := o.leased(); on || after != before || !hd.Paused || hd.N != 0 || hd.State != panel.SoundPausedText {
		t.Fatalf("paused page: leased %v (%d -> %d leases), header %+v", on, before, after, hd)
	}
	if rep := soundJSON(t, h, "GET", "/api/sound", ""); rep["paused"] != true || rep["paused_why"] != panel.SoundPausedText {
		t.Fatalf("the report while paused: %v %v", rep["paused"], rep["paused_why"])
	}
	if n := pc.View().Notice; strings.Contains(n, "is on SOUND") {
		t.Fatalf("the LCD still names a page: %q", n)
	}

	if rep := soundJSON(t, h, "POST", "/api/sound", `{"action":"take_scope"}`); rep["paused"] != false {
		t.Fatalf("take_scope: paused %v", rep["paused"])
	}
	if on, _ := o.leased(); !on {
		t.Fatal("take_scope did not lease the stream")
	}
	if hd := soundBinHead(t, h, "/api/sound.bin?max=64"); hd.Paused {
		t.Fatal("the stream came paused after take_scope")
	}
}

// TestSoundOwnerBrowser drives the pause in headless Chromium: the web
// page's SOUND streams, a key on the scope pauses it ("paused: the scope is
// in use" and Take the scope), the button streams again; the page's other
// tabs never lease the stream; the phone pauses and takes it back the same.
// Skips without node/browser (fails under CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-277
func TestSoundOwnerBrowser(t *testing.T) {
	testenv.NeedNode(t)
	o, _, s, done := newOwnerScope(t)
	defer done()
	mux := http.NewServeMux()
	// TRLC-LINKS: REQ-SDS-277
	mux.HandleFunc("/test/leases", func(w http.ResponseWriter, r *http.Request) {
		on, n := o.leased()
		_, _ = io.WriteString(w, fmt.Sprintf(`{"on":%v,"n":%d}`, on, n))
	})
	mux.Handle("/", s.Handler())
	srv := httptest.NewServer(mux)
	defer srv.Close()
	out, err := exec.Command("node", "sound_owner_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("sound_owner_browser.mjs:\n%s", out)
	if strings.Contains(string(out), "SKIP:") {
		testenv.SkipBrowser(t, "browser driver skipped: %s", firstLine(out))
	}
	if err != nil {
		t.Fatalf("browser e2e failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("browser e2e did not report ALL PASS")
	}
}
