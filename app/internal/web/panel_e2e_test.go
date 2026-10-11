// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel"
)

// panelScope is the web test's engine fake as both the server's scope and
// the panel controller's engine (the matrix idle, the lamps unlatched),
// with the state the two goroutines share behind a lock.
type panelScope struct {
	*fakeScope
	mu sync.Mutex
}

// TRLC-LINKS: REQ-SDS-135
func (p *panelScope) ReadMatrix() ([5]uint16, bool) {
	return [5]uint16{0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF, 0}, true
}

// TRLC-LINKS: REQ-SDS-135
func (p *panelScope) SetLEDs(uint16) {}

// TRLC-LINKS: REQ-SDS-164
func (p *panelScope) Snapshot() engine.Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fakeScope.Snapshot()
}

// TRLC-LINKS: REQ-SDS-162
func (p *panelScope) SetNorm(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fakeScope.SetNorm(on)
}

// TRLC-LINKS: REQ-SDS-162
func (p *panelScope) SetTdiv(s float64) (engine.Band, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fakeScope.SetTdiv(s)
}

// TRLC-LINKS: REQ-SDS-162
func (p *panelScope) SetRunning(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fakeScope.SetRunning(on)
}

// The phone remote end to end: the real panel controller, running its own
// goroutine, behind the real handlers. The status labels what the LCD
// shows, a key wakes the long-poll, a row set through /api/sheets changes
// the instrument and not the LCD, an action runs through
// /api/sheets/action, typed entry answers with a readable error.
// TRLC-LINKS: REQ-SDS-162, REQ-SDS-164, REQ-SDS-211, REQ-SDS-212
func TestPanelRemoteEndToEnd(t *testing.T) {
	ps := &panelScope{fakeScope: &fakeScope{stats: engine.Stats{Running: true, TdivS: 500e-6, TrigCode: 31437, TrigPosFrac: 0.5}}}
	pc := panel.New(ps, nil, -1, engine.SupportedTdivs(), 500e-6, t.Logf)
	stop := make(chan struct{})
	defer close(stop)
	go pc.Run(stop)
	s := New(ps, nil, pc, nil)

	status := func() map[string]any { return getStatus(t, s)["panel"].(map[string]any) }
	st := status()
	if st["Workspace"] != "SCOPE" || st["Mode"] != "workspace" || st["FKeys"].([]any)[2].(map[string]any)["Label"] != "SPEC" {
		t.Fatalf("status: %v", st)
	}
	seq := st["Seq"].(float64)

	// A key wakes the long-poll.
	got := make(chan map[string]any, 1)
	go func() {
		_, out := httpCall(t, s, "GET", fmt.Sprintf("/api/panel/wait?seq=%d&timeout_ms=5000", int(seq)), nil)
		got <- out
	}()
	time.Sleep(30 * time.Millisecond)
	if _, out := httpCall(t, s, "POST", "/api/panel", map[string]any{"button": "f3"}); out["ok"] != true {
		t.Fatalf("F3: %v", out)
	}
	select {
	case out := <-got:
		if out["Workspace"] != "SPECTRUM" || out["Seq"].(float64) == seq {
			t.Fatalf("long-poll woke with %v", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the long-poll did not wake on F3")
	}

	// The model lists the rows; a set changes the instrument, not the LCD.
	_, model := httpCall(t, s, "GET", "/api/sheets", nil)
	found := false
	for _, tab := range model["Tabs"].([]any) {
		for _, row := range tab.(map[string]any)["Rows"].([]any) {
			found = found || row.(map[string]any)["ID"] == "trig.mode"
		}
	}
	if !found {
		t.Fatal("trig.mode missing from /api/sheets")
	}
	_, out := httpCall(t, s, "POST", "/api/sheets/set", map[string]any{"row": "trig.mode", "text": "Normal"})
	if out["ok"] != true || out["text"] != "Normal" || !ps.Snapshot().Norm {
		t.Fatalf("set trig.mode: %v (norm %v)", out, ps.Snapshot().Norm)
	}
	if st := status(); st["Mode"] != "workspace" || st["Workspace"] != "SPECTRUM" || !strings.HasPrefix(st["Remote"].(string), "REMOTE  TRIG mode Normal") {
		t.Fatalf("the LCD after a remote set: %v", st)
	}
	_, out = httpCall(t, s, "POST", "/api/sheets/set", map[string]any{"row": "trig.mode", "value": 7})
	if out["ok"] == true || !strings.Contains(out["error"].(string), "has no value 7") {
		t.Fatalf("bad value: %v", out)
	}
	_, out = httpCall(t, s, "POST", "/api/sheets/set", map[string]any{"row": "time.tdiv", "text": "2us"})
	if out["ok"] != true || ps.Snapshot().TdivS != 2e-6 {
		t.Fatalf("set time.tdiv: %v (tdiv %g)", out, ps.Snapshot().TdivS)
	}
	_, out = httpCall(t, s, "POST", "/api/sheets/action", map[string]any{"row": "trig.force"})
	if out["ok"] != true {
		t.Fatalf("force: %v", out)
	}
	_, out = httpCall(t, s, "POST", "/api/panel", map[string]any{"type": map[string]any{"text": "1V"}})
	if out["ok"] != false || !strings.Contains(out["error"].(string), "nothing to type into") {
		t.Fatalf("typed entry with no context: %v", out)
	}
	raw, _ := json.Marshal(status())
	if !strings.Contains(string(raw), `"AutosetBusy":false`) {
		t.Fatalf("AutosetBusy missing from the status: %s", raw)
	}
}
