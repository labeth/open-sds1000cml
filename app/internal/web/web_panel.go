// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"open-sds/app/internal/panel"
)

// The phone remote's surface (REQ-SDS-211, REQ-SDS-212): the panel snapshot
// with a long-poll, the whole settings-sheet model, and setting a row or
// running an action by id. A remote change never opens, moves or closes
// anything on the LCD; the scope shows a short REMOTE note instead.

// panelRemote is the panel controller's remote surface (type-asserted, so a
// test double without it answers 404).
type panelRemote interface {
	PanelStatus() panel.Status
	WaitPanel(seq uint64, timeout time.Duration) panel.Status
	Sheets() panel.SheetsModel
	SheetSet(row string, value any, text string) panel.SetResult
	SheetAction(row, action string) panel.SetResult
	TypeText(text string) panel.SetResult
}

// panelWorkspaces is the remote's workspace switch (type-asserted, so a
// test double without it answers "no panel").
type panelWorkspaces interface {
	RemoteWorkspace(name string) panel.SetResult
	RemoteAgain(want string) panel.SetResult
}

// panelAutoFor presses AUTO for a named workspace (type-asserted; without it
// AUTO frames for the LCD's workspace).
type panelAutoFor interface {
	InjectAutoFor(ws string) bool
}

// panelKeys is the remote's keys and knobs: answered once they have run,
// with what they did (type-asserted; without it a key is only queued).
// panelRefs is the panel's saved references (GET /api/refs).
type panelRefs interface {
	RefsExport() [2]panel.RefExport
}

// panelRefActions stores, shows, hides or deletes a reference slot.
type panelRefActions interface {
	RefAction(slot int, op string) panel.SetResult
}

// hRefs serves the scope's REF A and REF B (the master is REF A): the web
// app shows the scope's references, not its own. POST {"slot":"A",
// "op":"store"} (store, show, hide, clear) changes one on the scope, as the
// phone's COMPARE rows do, and answers with both slots after it.
// TRLC-LINKS: REQ-SDS-138, REQ-SDS-218, REQ-SDS-227
func (s *Server) hRefs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		return
	}
	pr, ok := s.panel.(panelRefs)
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "error": "no panel on this server"})
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, map[string]any{"ok": true, "refs": pr.RefsExport()})
		return
	}
	pa, ok := s.panel.(panelRefActions)
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "error": "this scope's references cannot be changed from here"})
		return
	}
	var req struct {
		Slot string `json:"slot"`
		Op   string `json:"op"`
	}
	if err := decodeSmall(r.Body, &req); err != nil || (req.Slot != "A" && req.Slot != "B") {
		writeJSON(w, map[string]any{"ok": false, "error": "bad json: want {\"slot\":\"A\"|\"B\",\"op\":store|show|hide|clear}"})
		return
	}
	res := pa.RefAction(int(req.Slot[0]-'A'), req.Op)
	writeJSON(w, map[string]any{"ok": res.OK, "error": res.Error, "notice": res.Notice, "refs": pr.RefsExport()})
}

type panelKeys interface {
	RemoteKey(name string, nomenu bool) panel.SetResult
	RemoteKnob(name string, dir, steps int, nomenu bool) panel.SetResult
}

// hPanelWorkspace switches the workspace ({"workspace":"bus"}) or runs the
// active one's "again" ({"again":true}; with "workspace" it is refused
// unless that one is active) for the phone remote. Unlike F1..F5
// it works while the LCD has a sheet open and leaves that sheet as it is
// (REQ-SDS-220). The answer carries the workspace, its view, what again
// shows next, the notice the switch itself raised on the LCD (not one
// showing from before), the rows it changed and the sheet values' count.
// TRLC-LINKS: REQ-SDS-162, REQ-SDS-220
func (s *Server) hPanelWorkspace(w http.ResponseWriter, name string, again bool) {
	pw, ok := s.panel.(panelWorkspaces)
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "error": "no panel"})
		return
	}
	var res panel.SetResult
	if again {
		res = pw.RemoteAgain(name)
	} else {
		res = pw.RemoteWorkspace(name)
	}
	out := map[string]any{"ok": res.OK, "error": res.Error, "notice": res.Notice, "changed": res.Changed, "sheetsSeq": res.SheetsSeq}
	if ps, ok := s.panel.(interface{ PanelStatus() panel.Status }); ok {
		st := ps.PanelStatus()
		out["seq"], out["workspace"], out["name"], out["view"], out["next"] = st.Seq, st.WorkspaceID, st.Workspace, st.View, st.AgainNext
	}
	writeJSON(w, out)
}

// remote returns the panel's remote surface or answers 404.
// TRLC-LINKS: REQ-SDS-162
func (s *Server) remote(w http.ResponseWriter) (panelRemote, bool) {
	pr, ok := s.panel.(panelRemote)
	if !ok {
		http.Error(w, "no panel", http.StatusNotFound)
	}
	return pr, ok
}

// hPanelWait parks until the panel snapshot changes: ?seq=N&timeout_ms=T
// (at most 10 s) returns it as soon as its Seq differs from N, else at the
// timeout.
// TRLC-LINKS: REQ-SDS-162
func (s *Server) hPanelWait(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.remote(w)
	if !ok {
		return
	}
	q := r.URL.Query() // parsed once for both values
	seq, _ := strconv.ParseUint(q.Get("seq"), 10, 64)
	ms, err := strconv.Atoi(q.Get("timeout_ms"))
	if err != nil || ms < 0 {
		ms = 5000
	}
	if ms > 10000 {
		ms = 10000
	}
	writeJSON(w, pr.WaitPanel(seq, time.Duration(ms)*time.Millisecond))
}

// hSheets returns the whole sheet model: every tab and row with its id,
// kind, options or range, value and whether it is in use, and the Seq.
// TRLC-LINKS: REQ-SDS-162
func (s *Server) hSheets(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.remote(w)
	if !ok {
		return
	}
	writeJSON(w, pr.Sheets())
}

// hSheetsSet sets one row: {"row":"trig.level","value":1.25} or
// {"row":"trig.level","text":"1.25V"}.
// TRLC-LINKS: REQ-SDS-162
func (s *Server) hSheetsSet(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.remote(w)
	if !ok {
		return
	}
	var req struct {
		Row   string `json:"row"`
		Value any    `json:"value"`
		Text  string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil || req.Row == "" {
		writeJSON(w, panel.SetResult{Error: "bad json: want {\"row\":id,\"value\":v} or {\"row\":id,\"text\":t}"})
		return
	}
	writeJSON(w, pr.SheetSet(req.Row, req.Value, req.Text))
}

// hSheetsAction runs an action row: {"row":"file.setup3","action":"save"}
// (the action may be left out on a row with one).
// TRLC-LINKS: REQ-SDS-162
func (s *Server) hSheetsAction(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.remote(w)
	if !ok {
		return
	}
	var req struct {
		Row    string `json:"row"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil || req.Row == "" {
		writeJSON(w, panel.SetResult{Error: "bad json: want {\"row\":id,\"action\":name}"})
		return
	}
	writeJSON(w, pr.SheetAction(req.Row, req.Action))
}
