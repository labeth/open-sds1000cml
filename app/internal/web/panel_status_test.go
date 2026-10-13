// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"open-sds/app/internal/panel"
)

// panelFixture is the panel's remote surface with canned answers; it
// records what the server asked for. The panel package tests the behaviour
// behind it; these tests the HTTP plumbing.
// TRLC-LINKS: REQ-SDS-164
type panelFixture struct {
	status          panel.Status
	writes          int
	waitSeq         uint64
	waitFor         time.Duration
	setRow, setText string
	setValue        any
	actRow, actName string
	typed           string
}

// TRLC-LINKS: REQ-SDS-162
func (p *panelFixture) InjectButton(string) bool { p.writes++; return true }

// TRLC-LINKS: REQ-SDS-162
func (p *panelFixture) InjectKnob(string, int, int) bool { p.writes++; return true }

// TRLC-LINKS: REQ-SDS-164
func (p *panelFixture) PanelStatus() panel.Status { return p.status }

// TRLC-LINKS: REQ-SDS-164
func (p *panelFixture) WaitPanel(seq uint64, d time.Duration) panel.Status {
	p.waitSeq, p.waitFor = seq, d
	return p.status
}

// TRLC-LINKS: REQ-SDS-164
func (p *panelFixture) Sheets() panel.SheetsModel {
	return panel.SheetsModel{Seq: 7, Tabs: []panel.TabModel{{ID: "trig", Name: "TRIG",
		Rows: []panel.RowModel{{ID: "trig.level", Label: "Level", Kind: "number", Value: 0.2, Text: "200mV", Unit: "V"}}}}}
}

// TRLC-LINKS: REQ-SDS-162
func (p *panelFixture) SheetSet(row string, value any, text string) panel.SetResult {
	p.setRow, p.setValue, p.setText = row, value, text
	if text == "50V" {
		return panel.SetResult{Row: row, Error: "level 50V is outside -6.4V .. 6.4V"}
	}
	return panel.SetResult{OK: true, Row: row, Value: 1.25, Text: "1.25V"}
}

// TRLC-LINKS: REQ-SDS-162
func (p *panelFixture) SheetAction(row, action string) panel.SetResult {
	p.actRow, p.actName = row, action
	return panel.SetResult{OK: true, Row: row, Text: "Stack now"}
}

// TRLC-LINKS: REQ-SDS-162
func (p *panelFixture) TypeText(text string) panel.SetResult {
	p.typed = text
	return panel.SetResult{OK: true, Row: "bus.match", Value: "55 AA", Text: "55 AA"}
}

// httpCall runs one request against s and decodes the JSON reply.
// TRLC-LINKS: REQ-SDS-162
func httpCall(t *testing.T, s *Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	switch b := body.(type) {
	case nil:
		rd = bytes.NewReader(nil)
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, rd))
	var out map[string]any
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: bad json %q", method, path, rec.Body.String())
		}
	}
	return rec.Code, out
}

// The status carries the panel snapshot the LCD shows (workspace, F-key
// labels, tool line, lamps, Seq) with AutosetBusy kept where the page reads
// it, and reading it writes nothing.
// TRLC-LINKS: REQ-SDS-164, REQ-SDS-211
func TestStatusReportsPanelSnapshotWithoutControlWrites(t *testing.T) {
	p := &panelFixture{status: panel.Status{Seq: 3, AutosetBusy: true, Workspace: "BUS", View: "Packets", Mode: "workspace",
		FKeys: [5]panel.KeyStatus{{Label: "SCOP"}, {Label: "BUS", Sel: true}}, Tool: []panel.ToolSeg{{Knob: "Adjust", Job: "packet"}},
		Again: "F2 again: Eye diagram", Lamps: 0x2050, LampsLit: []string{"CH1", "CH2", "RUN"}}}
	s := New(&fakeScope{}, nil, p, nil)
	got := getStatus(t, s)["panel"].(map[string]any)
	if got["Workspace"] != "BUS" || got["AutosetBusy"] != true || got["Seq"] != 3.0 ||
		got["FKeys"].([]any)[1].(map[string]any)["Label"] != "BUS" || got["Tool"].([]any)[0].(map[string]any)["Job"] != "packet" {
		t.Fatalf("missing LCD state: %v", got)
	}
	p.status.Workspace = "SPECTRUM"
	if getStatus(t, s)["panel"].(map[string]any)["Workspace"] != "SPECTRUM" {
		t.Fatal("panel state was cached")
	}
	if p.writes != 0 {
		t.Fatal("status read injected a control event")
	}
	if _, exists := getStatus(t, New(&fakeScope{}, nil, nil, nil))["panel"]; exists {
		t.Fatal("absent panel should be omitted")
	}
}

// The long-poll hands the client's Seq and a timeout (at most 10 s) to the
// panel.
// TRLC-LINKS: REQ-SDS-164
func TestPanelWaitParams(t *testing.T) {
	p := &panelFixture{status: panel.Status{Seq: 9, Workspace: "SCOPE"}}
	s := New(&fakeScope{}, nil, p, nil)
	code, out := httpCall(t, s, "GET", "/api/panel/wait?seq=8&timeout_ms=250", nil)
	if code != 200 || out["Seq"] != 9.0 || p.waitSeq != 8 || p.waitFor != 250*time.Millisecond {
		t.Fatalf("wait: %d %v seq %d timeout %v", code, out, p.waitSeq, p.waitFor)
	}
	httpCall(t, s, "GET", "/api/panel/wait?seq=9&timeout_ms=999999", nil)
	if p.waitFor != 10*time.Second {
		t.Fatalf("timeout not capped: %v", p.waitFor)
	}
	httpCall(t, s, "GET", "/api/panel/wait?seq=9", nil)
	if p.waitFor != 5*time.Second {
		t.Fatalf("default timeout %v", p.waitFor)
	}
	if code, _ := httpCall(t, New(&fakeScope{}, nil, nil, nil), "GET", "/api/panel/wait", nil); code != http.StatusNotFound {
		t.Fatalf("no panel: %d", code)
	}
}

// /api/sheets returns the model; set and action pass the row, the raw
// value or the typed text and the action through, and the answer carries
// ok, value, text and the error.
// TRLC-LINKS: REQ-SDS-162, REQ-SDS-212
func TestSheetsEndpoints(t *testing.T) {
	p := &panelFixture{}
	s := New(&fakeScope{}, nil, p, nil)
	code, out := httpCall(t, s, "GET", "/api/sheets", nil)
	if code != 200 || out["Seq"] != 7.0 || out["Tabs"].([]any)[0].(map[string]any)["Rows"].([]any)[0].(map[string]any)["ID"] != "trig.level" {
		t.Fatalf("sheets: %d %v", code, out)
	}
	_, out = httpCall(t, s, "POST", "/api/sheets/set", map[string]any{"row": "trig.level", "text": "1.25V"})
	if out["ok"] != true || out["text"] != "1.25V" || out["value"] != 1.25 || p.setRow != "trig.level" || p.setText != "1.25V" {
		t.Fatalf("set text: %v (row %q text %q)", out, p.setRow, p.setText)
	}
	httpCall(t, s, "POST", "/api/sheets/set", map[string]any{"row": "ch2.coupling", "value": 1})
	if p.setRow != "ch2.coupling" || p.setValue != 1.0 || p.setText != "" {
		t.Fatalf("set value: row %q value %v text %q", p.setRow, p.setValue, p.setText)
	}
	_, out = httpCall(t, s, "POST", "/api/sheets/set", map[string]any{"row": "trig.level", "text": "50V"})
	if out["ok"] == true || out["error"] != "level 50V is outside -6.4V .. 6.4V" {
		t.Fatalf("error not passed through: %v", out)
	}
	_, out = httpCall(t, s, "POST", "/api/sheets/set", "{nope")
	if out["ok"] == true || out["error"] == nil {
		t.Fatalf("bad json accepted: %v", out)
	}
	_, out = httpCall(t, s, "POST", "/api/sheets/action", map[string]any{"row": "cmp.stack_master"})
	if out["ok"] != true || p.actRow != "cmp.stack_master" || p.actName != "" {
		t.Fatalf("action: %v (row %q action %q)", out, p.actRow, p.actName)
	}
	if code, _ := httpCall(t, s, "GET", "/api/sheets/set", nil); code == 200 {
		t.Fatal("set answered a GET")
	}
	if code, _ := httpCall(t, New(&fakeScope{}, nil, &panelStatusOnly{}, nil), "GET", "/api/sheets", nil); code != http.StatusNotFound {
		t.Fatalf("a panel without the remote surface: %d", code)
	}
}

// panelStatusOnly is a panel with no remote surface.
type panelStatusOnly struct{}

// TRLC-LINKS: REQ-SDS-162
func (panelStatusOnly) InjectButton(string) bool { return false }

// TRLC-LINKS: REQ-SDS-162
func (panelStatusOnly) InjectKnob(string, int, int) bool { return false }

// /api/panel types into the LCD's current context or sets a row by id; the
// button and knob injection by physical name is unchanged.
// TRLC-LINKS: REQ-SDS-162, REQ-SDS-212
func TestPanelTypedEntry(t *testing.T) {
	p := &panelFixture{}
	s := New(&fakeScope{}, nil, p, nil)
	_, out := httpCall(t, s, "POST", "/api/panel", map[string]any{"type": map[string]any{"text": "55 AA"}})
	if out["ok"] != true || out["applied"] != "55 AA" || out["row"] != "bus.match" || p.typed != "55 AA" {
		t.Fatalf("type: %v (typed %q)", out, p.typed)
	}
	_, out = httpCall(t, s, "POST", "/api/panel", map[string]any{"set": map[string]any{"row": "trig.level", "text": "50V"}})
	if out["ok"] != false || out["error"] != "level 50V is outside -6.4V .. 6.4V" || p.setRow != "trig.level" {
		t.Fatalf("set: %v", out)
	}
	_, out = httpCall(t, s, "POST", "/api/panel", map[string]any{"button": "f2"})
	if out["ok"] != true || p.writes != 1 {
		t.Fatalf("button: %v writes %d", out, p.writes)
	}
}
