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
	RemoteAgain() panel.SetResult
}

// hPanelWorkspace switches the workspace ({"workspace":"bus"}) or runs the
// active one's "again" ({"again":true}) for the phone remote. Unlike F1..F5
// it works while the LCD has a sheet open and leaves that sheet as it is
// (REQ-SDS-220). The answer carries the workspace, its view, what again
// shows next and the notice the switch raised on the LCD (not one showing
// from before).
// TRLC-LINKS: REQ-SDS-162, REQ-SDS-220
func (s *Server) hPanelWorkspace(w http.ResponseWriter, name string, again bool) {
	pw, ok := s.panel.(panelWorkspaces)
	if !ok {
		writeJSON(w, map[string]any{"ok": false, "error": "no panel"})
		return
	}
	ps, hasStatus := s.panel.(interface{ PanelStatus() panel.Status })
	before := ""
	if hasStatus {
		before = ps.PanelStatus().Notice
	}
	var res panel.SetResult
	if name == "" && again {
		res = pw.RemoteAgain()
	} else {
		res = pw.RemoteWorkspace(name)
	}
	out := map[string]any{"ok": res.OK, "error": res.Error}
	if hasStatus {
		st := ps.PanelStatus()
		notice := ""
		if st.Notice != before {
			notice = st.Notice
		}
		out["seq"], out["workspace"], out["name"], out["view"], out["next"], out["notice"] =
			st.Seq, st.WorkspaceID, st.Workspace, st.View, st.AgainNext, notice
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
	seq, _ := strconv.ParseUint(r.URL.Query().Get("seq"), 10, 64)
	ms, err := strconv.Atoi(r.URL.Query().Get("timeout_ms"))
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
