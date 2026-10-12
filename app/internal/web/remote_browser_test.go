// ENGMODEL-OWNER-UNIT: FU-WEB-REMOTE
package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel"
	"open-sds/app/internal/settings"
	"open-sds/app/internal/testenv"
)

// ---------------------------------------------------------------------------
// The phone remote in a real browser (phone emulation), against the real
// panel controller and analog front end behind the real handlers. A control
// server beside the app plays the person at the scope: it presses the scope's
// own keys and knobs, reads what the LCD shows, and drops or restores the app
// server's connections.
// ---------------------------------------------------------------------------

// remoteEngine is the web tests' fake scope as the panel controller's engine:
// an idle key matrix, unlatched lamps and a serial trigger, every call behind
// one lock (the HTTP handlers and the panel loop share it).
// TRLC-LINKS: REQ-SDS-220
type remoteEngine struct {
	mu sync.Mutex
	*fakeScope
	serial engine.SerialParams
}

// TRLC-LINKS: REQ-SDS-220
func newRemoteEngine() *remoteEngine {
	return &remoteEngine{fakeScope: &fakeScope{stats: engine.Stats{Running: true, TdivS: 500e-6, TrigCode: 31437, TrigPosFrac: 0.5,
		TrigRising: true, AvgCount: 16, EresLen: 1, MemDepth: 20480}}}
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) ReadMatrix() ([5]uint16, bool) {
	return [5]uint16{0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF, 0}, true
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetLEDs(uint16) {}

// lock runs fn under the engine's lock.
// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) lock(fn func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn()
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) Snapshot() (s engine.Stats) {
	e.lock(func() { s = e.fakeScope.Snapshot() })
	return
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetTdiv(v float64) (b engine.Band, ok bool) {
	e.lock(func() { b, ok = e.fakeScope.SetTdiv(v) })
	return
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetRunning(on bool) { e.lock(func() { e.fakeScope.SetRunning(on) }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetNorm(on bool) { e.lock(func() { e.fakeScope.SetNorm(on) }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetSingle() {
	e.lock(func() { e.fakeScope.SetSingle(); e.fakeScope.SetRunning(true) })
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetTrigLevelCode(c uint16) (out uint16) {
	e.lock(func() { out = e.fakeScope.SetTrigLevelCode(c) })
	return
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetTrigSlope(r bool) { e.lock(func() { e.fakeScope.SetTrigSlope(r) }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetTrigSource(ch int) { e.lock(func() { e.fakeScope.SetTrigSource(ch) }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetTrigType(t int) { e.lock(func() { e.fakeScope.SetTrigType(t) }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetAcqMode(m int) { e.lock(func() { e.fakeScope.SetAcqMode(m) }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetAvgCount(n int) { e.lock(func() { e.fakeScope.SetAvgCount(n) }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetEresLen(l int) { e.lock(func() { e.fakeScope.SetEresLen(l) }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetETS(on bool) { e.lock(func() { e.fakeScope.SetETS(on) }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetTrigPosFrac(f float64) { e.lock(func() { e.fakeScope.SetTrigPosFrac(f) }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetHoldoff(s float64) (out float64) {
	e.lock(func() { out = e.fakeScope.SetHoldoff(s) })
	return
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetMemDepth(n int) (out int) {
	e.lock(func() { out = e.fakeScope.SetMemDepth(n) })
	return
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetOffsetDAC(ch int, code uint16) {
	e.lock(func() { e.fakeScope.SetOffsetDAC(ch, code) })
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetPulseParams(l, a, b float64, c int) {
	e.lock(func() { e.fakeScope.SetPulseParams(l, a, b, c) })
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetSlopeParams(lo, hi, a, b float64, c int) {
	e.lock(func() { e.fakeScope.SetSlopeParams(lo, hi, a, b, c) })
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetVideoParams(std, line int, neg bool) {
	e.lock(func() { e.fakeScope.SetVideoParams(std, line, neg) })
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetMask(m *engine.Mask) { e.lock(func() { e.fakeScope.SetMask(m) }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetMaskMode(m int) { e.lock(func() { e.fakeScope.SetMaskMode(m) }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) ClearMaskFails() { e.lock(func() { e.fakeScope.ClearMaskFails() }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) AcqLog(n int) (s []engine.AcqSample, h float64) {
	e.lock(func() { s, h = e.fakeScope.AcqLog(n) })
	return
}

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetSerialMode(m int) { e.lock(func() { e.fakeScope.SetSerialMode(m) }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SetSerialParams(p engine.SerialParams) { e.lock(func() { e.serial = p }) }

// TRLC-LINKS: REQ-SDS-220
func (e *remoteEngine) SerialConfig() (p engine.SerialParams) {
	e.lock(func() { p = e.serial })
	return
}

// remoteSPI is the front end's SPI with nothing on it.
type remoteSPI struct{}

// TRLC-LINKS: REQ-SDS-220
func (remoteSPI) WriteRelay(uint32) error { return nil }

// TRLC-LINKS: REQ-SDS-220
func (remoteSPI) WriteGain(uint8, uint8) error { return nil }

// remoteSetups is the setup slots in memory: a slot keeps the timebase and
// the workspace, enough for its FILE row's summary and a recall.
type remoteSetups struct {
	mu    sync.Mutex
	eng   *remoteEngine
	ws    func() int
	slots map[int]settings.Settings
}

// TRLC-LINKS: REQ-SDS-220
func (m *remoteSetups) SaveSetup(n int) error {
	st := m.eng.Snapshot()
	s := settings.Settings{TdivS: st.TdivS, UI: settings.UI{Set: true, Workspace: m.ws()},
		Trigger: settings.Trigger{LevelCode: int(st.TrigCode), Rising: st.TrigRising, Source: st.TrigSource}}
	s.Ch[0].VdivV, s.Ch[1].VdivV = 1, 1
	m.mu.Lock()
	m.slots[n] = s
	m.mu.Unlock()
	return nil
}

// TRLC-LINKS: REQ-SDS-220
func (m *remoteSetups) RecallSetup(n int) error {
	m.mu.Lock()
	s, ok := m.slots[n]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("slot %d is empty", n)
	}
	m.eng.SetTdiv(s.TdivS)
	return nil
}

// TRLC-LINKS: REQ-SDS-220
func (m *remoteSetups) PeekSetup(n int) (settings.Settings, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.slots[n]
	return s, ok
}

// remoteGate drops every connection while the scope is "down", and counts
// the phones' requests by path (not the driver's own, which say
// X-Test-Driver).
type remoteGate struct {
	down atomic.Bool
	h    http.Handler
	mu   sync.Mutex
	hits map[string]int
}

// TRLC-LINKS: REQ-SDS-220
func (g *remoteGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Test-Driver") == "" {
		g.mu.Lock()
		if g.hits == nil {
			g.hits = map[string]int{}
		}
		g.hits[r.URL.Path]++
		g.mu.Unlock()
	}
	if g.down.Load() {
		if hj, ok := w.(http.Hijacker); ok {
			if c, _, err := hj.Hijack(); err == nil {
				c.Close()
				return
			}
		}
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	g.h.ServeHTTP(w, r)
}

// remoteRig is the app server (the remote's scope) and its control server.
type remoteRig struct {
	app, ctrl *httptest.Server
	pc        *panel.Controller
	eng       *remoteEngine
	gate      *remoteGate
}

// newRemoteRig builds the scope the remote talks to: the real panel
// controller (its loop running) and front end over the locked fake engine,
// behind the real handlers. The control server answers /down and /up (drop
// the app server's connections or let them through), /button?name=,
// /knob?name=&dir= (the scope's own keys and knobs), /status (what the LCD
// shows), /engine (the engine's state), /reset (factory settings, SCOPE,
// no menu on the LCD), /mask?pass= (a mask test's count) and /hits (the
// phones' requests by path since the last /hits).
// TRLC-LINKS: REQ-SDS-220
func newRemoteRig(t testing.TB) *remoteRig {
	eng := newRemoteEngine()
	fe := analog.New(remoteSPI{}, func(time.Duration) {}, nil)
	fe.OnOffset(eng.SetOffsetDAC)
	pc := panel.New(eng, fe, -1, engine.SupportedTdivs(), 500e-6, func(string, ...any) {})
	pc.SetSetupMemory(&remoteSetups{eng: eng, slots: map[int]settings.Settings{},
		ws: func() int {
			return map[string]int{"bus": 1, "spectrum": 2, "measure": 3, "compare": 4}[pc.PanelStatus().WorkspaceID]
		}})
	stop := make(chan struct{})
	go pc.Run(stop)
	gate := &remoteGate{h: New(eng, nil, pc, nil).Handler()}
	r := &remoteRig{pc: pc, eng: eng, gate: gate}
	r.app = httptest.NewServer(gate)
	mux := http.NewServeMux()
	mux.HandleFunc("/reset", func(w http.ResponseWriter, _ *http.Request) {
		ok := pc.InjectButtonWait("defaultsetup", 3*time.Second)
		if pc.PanelStatus().Mode != "workspace" {
			ok = ok && pc.InjectButtonWait("menu", 3*time.Second)
		}
		ok = ok && pc.RemoteWorkspace("scope").OK
		writeJSON(w, map[string]bool{"ok": ok})
	})
	mux.HandleFunc("/down", func(w http.ResponseWriter, _ *http.Request) {
		gate.down.Store(true)
		r.app.CloseClientConnections()
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/up", func(w http.ResponseWriter, _ *http.Request) {
		gate.down.Store(false)
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/button", func(w http.ResponseWriter, q *http.Request) {
		writeJSON(w, map[string]bool{"ok": pc.InjectButtonWait(q.URL.Query().Get("name"), 3*time.Second)})
	})
	mux.HandleFunc("/knob", func(w http.ResponseWriter, q *http.Request) {
		dir, _ := strconv.Atoi(q.URL.Query().Get("dir"))
		ok := pc.InjectKnob(q.URL.Query().Get("name"), dir, 1)
		time.Sleep(50 * time.Millisecond) // the knob runs on the panel loop
		writeJSON(w, map[string]bool{"ok": ok})
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, pc.PanelStatus()) })
	mux.HandleFunc("/mask", func(w http.ResponseWriter, q *http.Request) {
		n, _ := strconv.ParseInt(q.URL.Query().Get("pass"), 10, 64)
		eng.lock(func() {
			eng.fakeScope.mu.Lock()
			eng.fakeScope.stats.MaskPass = n
			eng.fakeScope.mu.Unlock()
		})
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/hits", func(w http.ResponseWriter, _ *http.Request) {
		gate.mu.Lock()
		hits := gate.hits
		gate.hits = nil
		gate.mu.Unlock()
		writeJSON(w, hits)
	})
	mux.HandleFunc("/engine", func(w http.ResponseWriter, _ *http.Request) {
		st := eng.Snapshot()
		ser := eng.SerialConfig()
		writeJSON(w, map[string]any{"tdiv": st.TdivS, "trigCode": st.TrigCode, "norm": st.Norm, "running": st.Running,
			"serialMode": st.SerialMode, "serialBytes": ser.Bytes, "holdoff": st.HoldoffS, "acqMode": st.AcqMode, "avg": st.AvgCount})
	})
	r.ctrl = httptest.NewServer(mux)
	t.Cleanup(func() {
		r.ctrl.Close()
		gate.down.Store(false)
		r.app.Close()
		close(stop)
	})
	return r
}

// TestRemoteBrowser drives the phone remote in phone emulation (Pixel 7 and
// iPhone 14 viewports, touch): every tab, a tile change, typed numbers with
// units and a refused one, match bytes in hex and text, a workspace switch
// while the LCD has a sheet open (the LCD's sheet stays as it was), undo in
// History (and a scope knob listed as the scope's), Default Setup refusing a
// tap and running on a 1 s hold, the reconnect after the server drops, keep
// awake and fullscreen states, and the layout rules (44 px targets, 13 px
// text, no sideways scroll) at three phone sizes.
// TRLC-LINKS: REQ-SDS-220
func TestRemoteBrowser(t *testing.T) {
	testenv.NeedNode(t)
	// read here so Go's test cache sees a change to the driver (node reads it)
	for _, f := range []string{"remote_browser.mjs", "scope_po.mjs"} {
		if _, err := os.ReadFile(f); err != nil {
			t.Fatal(err)
		}
	}
	r := newRemoteRig(t)
	wsDriver(t, "remote_browser.mjs", r.app.URL, r.ctrl.URL)
	// the LCD stayed as the person at the scope left it, and no sheet was
	// ever opened by the remote (the driver checked it at every step)
	var st panel.Status
	raw, _ := json.Marshal(r.pc.PanelStatus())
	if err := json.Unmarshal(raw, &st); err != nil || st.Mode != "workspace" {
		t.Fatalf("the LCD after the remote: %s", raw)
	}
}
