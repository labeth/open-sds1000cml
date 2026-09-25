// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/bus"
	"open-sds/app/internal/diag"
	"open-sds/app/internal/iface"
)

// diagBus is a minimal fabric model for the handler tests: identity words,
// a register file, the DIAG window RAM, a snapshot RAM of 16 words.
// TRLC-LINKS: REQ-SDS-165
type diagBus struct {
	mu   sync.Mutex
	regs map[uint16]uint16
	win  map[uint16]uint16
	raw  []string
	snap int
}

// TRLC-LINKS: REQ-SDS-165
func newDiagBus() *diagBus {
	b := &diagBus{regs: map[uint16]uint16{}, win: map[uint16]uint16{}}
	b.regs[iface.SelBuildidLo] = iface.BuildIDLo
	b.regs[iface.SelBuildidHi] = iface.BuildIDHi
	b.regs[iface.SelVersion] = iface.VersionMagic
	b.regs[iface.SelFabricId] = iface.FabricID
	b.regs[iface.SelClkStat] = 3
	b.win[iface.DiagAdcHold] = 7
	return b
}

// TRLC-LINKS: REQ-SDS-165
func (b *diagBus) Read(plane uint8, sel uint16) (uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if plane == bus.PlaneCS3 {
		return 0xc0, nil
	}
	switch iface.MaskSel(sel) {
	case iface.SelDiagData:
		return b.win[b.regs[iface.SelDiagIdx]], nil
	case iface.SelSnapRemain:
		return iface.SnapRemainReadyMask | uint16(16-b.snap), nil
	case iface.SelSnapPop:
		v := uint16(0x100 + b.snap)
		b.snap++
		return v, nil
	case iface.SelBurstRemain:
		return iface.BurstRemainReadyMask | 64, nil
	case iface.SelStatusA:
		return iface.StatusAValidMask, nil
	}
	return b.regs[iface.MaskSel(sel)], nil
}

// TRLC-LINKS: REQ-SDS-165
func (b *diagBus) Write(plane uint8, sel, val uint16) error {
	if !bus.Writable(plane, sel) {
		return fmt.Errorf("not writable")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if plane != bus.PlaneCS1 {
		return nil
	}
	if iface.MaskSel(sel) == iface.SelDiagData {
		b.win[b.regs[iface.SelDiagIdx]] = val
		return nil
	}
	if iface.MaskSel(sel) == iface.SelDiagCtrl && val&iface.DiagCtrlSnapArmMask != 0 {
		b.snap = 0
	}
	b.regs[iface.MaskSel(sel)] = val
	return nil
}

// TRLC-LINKS: REQ-SDS-165
func (b *diagBus) RawWrite(sel, val uint16) error {
	b.mu.Lock()
	b.raw = append(b.raw, fmt.Sprintf("%02x=%04x", sel, val))
	b.mu.Unlock()
	return nil
}
// TRLC-LINKS: REQ-SDS-165
func (b *diagBus) BurstInto(c1, c2 []uint8, n int) {
	for i := 0; i < n; i++ {
		c1[i], c2[i] = uint8(i), 7
	}
}
// TRLC-LINKS: REQ-SDS-165
func (b *diagBus) PopWords(sel uint16, dst []uint16, n int) {
	for i := 0; i < n; i++ {
		dst[i], _ = b.Read(bus.PlaneCS1, sel)
	}
}
// TRLC-LINKS: REQ-SDS-165
func (b *diagBus) FastDrain() bool { return false }

// TRLC-LINKS: REQ-SDS-165
func diagServer(t *testing.T) (*Server, *diagBus) {
	t.Helper()
	fb := newDiagBus()
	s := New(&fakeScope{}, nil, nil, nil)
	d := diag.New(diag.RunnerFunc(func(fn func(bus.Bus) error, _ time.Duration) error { return fn(fb) }), t.Logf)
	d.SetStatusExtra(func() map[string]any { return map[string]any{"heartbeat": "diag"} })
	s.SetDiag(d)
	return s, fb
}

// TRLC-LINKS: REQ-SDS-165
func call(t *testing.T, h http.Handler, method, url, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, url, nil)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var m map[string]any
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		json.Unmarshal(rec.Body.Bytes(), &m)
	}
	return rec, m
}

// TRLC-LINKS: REQ-SDS-165
func TestDiagRoutesUnwiredAre503(t *testing.T) {
	s := New(&fakeScope{}, nil, nil, nil)
	h := s.Handler()
	for _, u := range []string{"/api/diag/status", "/api/diag/reg?name=RUN", "/api/diag/census", "/api/diag/e2"} {
		rec, _ := call(t, h, http.MethodGet, u, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: %d, want 503 when diag is not wired", u, rec.Code)
		}
	}
	// The page and the map are static and always served.
	rec, _ := call(t, h, http.MethodGet, "/diag", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "diagnostic block") || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("/diag: %d", rec.Code)
	}
	rec, m := call(t, h, http.MethodGet, "/api/diag/map", "")
	if rec.Code != 200 || m["build_id"] != fmt.Sprintf("0x%08x", iface.BuildID) {
		t.Fatalf("/api/diag/map: %d %v", rec.Code, m["build_id"])
	}
}

// TRLC-LINKS: REQ-SDS-165
func TestDiagStatusRegWindow(t *testing.T) {
	s, fb := diagServer(t)
	h := s.Handler()
	rec, m := call(t, h, http.MethodGet, "/api/diag/status", "")
	if rec.Code != 200 || m["heartbeat"] != "diag" || m["pll_a_lock"] != true {
		t.Fatalf("status: %d %v", rec.Code, m)
	}
	if id := m["identity"].(map[string]any); id["ok"] != true {
		t.Fatalf("identity: %v", id)
	}
	rec, m = call(t, h, http.MethodPost, "/api/diag/reg", `{"name":"RUN","val":"0x0005"}`)
	if rec.Code != 200 || m["ok"] != true || fb.regs[iface.SelRun] != 5 {
		t.Fatalf("reg write: %d %v", rec.Code, m)
	}
	rec, m = call(t, h, http.MethodGet, "/api/diag/reg?name=run", "")
	if rec.Code != 200 || m["value"].(float64) != 5 || m["fields"].(map[string]any)["MODE"].(float64) != 1 {
		t.Fatalf("reg read: %d %v", rec.Code, m)
	}
	rec, m = call(t, h, http.MethodPost, "/api/diag/reg", `{"name":"FILL","val":"1"}`)
	if rec.Code != 400 || m["ok"] != false {
		t.Fatalf("read-only write must be refused: %d %v", rec.Code, m)
	}
	rec, _ = call(t, h, http.MethodPost, "/api/diag/reg", `{"name":"0x57","val":"1","raw":true}`)
	if rec.Code != 200 || len(fb.raw) != 1 {
		t.Fatalf("raw write: %d %v", rec.Code, fb.raw)
	}
	rec, _ = call(t, h, http.MethodGet, "/api/diag/reg", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"DIAG_CTRL"`) || strings.Contains(rec.Body.String(), `"name":"BURST"`) {
		t.Fatalf("read-all must list non-pop registers only: %s", rec.Body.String()[:80])
	}
	rec, m = call(t, h, http.MethodPost, "/api/diag/window", `{"name":"ADC_HOLD","val":"0x3"}`)
	if rec.Code != 200 || fb.win[iface.DiagAdcHold] != 3 {
		t.Fatalf("window write: %d %v", rec.Code, m)
	}
	rec, m = call(t, h, http.MethodGet, "/api/diag/window?name=LANEMAP.2", "")
	if rec.Code != 200 || m["idx"].(float64) != 0x12 {
		t.Fatalf("window read: %d %v", rec.Code, m)
	}
	rec, _ = call(t, h, http.MethodGet, "/api/diag/window", "")
	if rec.Code != 200 {
		t.Fatalf("window read-all: %d", rec.Code)
	}
	rec, m = call(t, h, http.MethodPost, "/api/diag/ctrl", `{"field":"D2","on":true}`)
	if rec.Code != 200 || fb.regs[iface.SelDiagCtrl]&iface.DiagCtrlD2Mask == 0 {
		t.Fatalf("ctrl: %d %v", rec.Code, m)
	}
	rec, _ = call(t, h, http.MethodPost, "/api/diag/ctrl", `{"field":"NOPE","on":true}`)
	if rec.Code != 400 {
		t.Fatalf("bad field: %d", rec.Code)
	}
}

// TRLC-LINKS: REQ-SDS-165
func TestDiagSnapshotBusExperiments(t *testing.T) {
	s, fb := diagServer(t)
	h := s.Handler()
	rec, m := call(t, h, http.MethodGet, "/api/diag/snapshot?mode=5&clk=1", "")
	if rec.Code != 200 || m["n"].(float64) != 16 {
		t.Fatalf("snapshot json: %d %v", rec.Code, m)
	}
	rec, _ = call(t, h, http.MethodGet, "/api/diag/snapshot?mode=5&clk=1&format=bin", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/octet-stream" || rec.Body.Len() != 32 {
		t.Fatalf("snapshot bin: %d %s %d", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	if w := binary.LittleEndian.Uint16(rec.Body.Bytes()[2:]); w != 0x101 {
		t.Fatalf("snapshot word 1 = %#x", w)
	}
	rec, _ = call(t, h, http.MethodGet, "/api/diag/snapshot?mode=9", "")
	if rec.Code != 400 {
		t.Fatalf("bad mode: %d", rec.Code)
	}
	rec, m = call(t, h, http.MethodPost, "/api/diag/bus", `{"ball":"R3","oe":true,"level":1}`)
	if rec.Code != 200 || m["balls"].(map[string]any)["R3"].(map[string]any)["oe"] != true {
		t.Fatalf("bus drive: %d %v", rec.Code, m)
	}
	rec, _ = call(t, h, http.MethodPost, "/api/diag/bus", `{"master":true}`)
	if rec.Code != 200 || fb.regs[iface.SelDiagCtrl]&iface.DiagCtrlBusDrvEnMask == 0 {
		t.Fatalf("master: %d", rec.Code)
	}
	rec, _ = call(t, h, http.MethodPost, "/api/diag/bus", `{"release_all":true}`)
	if rec.Code != 200 || fb.regs[iface.SelDiagCtrl]&iface.DiagCtrlBusDrvEnMask != 0 || fb.win[iface.DiagBusOeLo] != 0 {
		t.Fatalf("release: %d", rec.Code)
	}
	rec, _ = call(t, h, http.MethodPost, "/api/diag/bus", `{}`)
	if rec.Code != 400 {
		t.Fatalf("empty bus post: %d", rec.Code)
	}
	rec, m = call(t, h, http.MethodPost, "/api/diag/vendor", `{"dwell_ms":0}`)
	if rec.Code != 200 || len(m["steps"].([]any)) != 6 || len(fb.raw) != 6 {
		t.Fatalf("vendor: %d %v", rec.Code, m)
	}
	rec, m = call(t, h, http.MethodPost, "/api/diag/e2", `{"blocks":3,"repeats":1}`)
	if rec.Code != 200 || len(m["conditions"].([]any)) != 2 || m["restored"] != true {
		t.Fatalf("e2: %d %v", rec.Code, m["summary"])
	}
	rec, m = call(t, h, http.MethodPost, "/api/diag/capture", `{"words":64}`)
	if rec.Code != 200 || m["words"].(float64) != 64 || m["ramp_breaks_ch1"].(float64) != 0 || m["c1"] != nil {
		t.Fatalf("capture: %d %v", rec.Code, m)
	}
	rec, m = call(t, h, http.MethodGet, "/api/diag/census", "")
	if rec.Code != 200 || len(m["lanes"].([]any)) != 118 {
		t.Fatalf("census: %d", rec.Code)
	}
}
