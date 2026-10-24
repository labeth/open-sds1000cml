// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"io"
	"net/http"
)

// SetupMemory stores and recalls whole instrument setups in numbered slots.
// main wires settings.Slots, the same store as the front panel's SAVE/RECALL
// key and SCPI *SAV/*RCL, so a setup saved in the browser recalls on the scope.
type SetupMemory interface {
	SaveSetup(slot int) error
	RecallSetup(slot int) error
}

// SetupSlot is one setup slot as the scope's FILE tab shows it.
type SetupSlot struct {
	Slot    int    `json:"slot"`
	Used    bool   `json:"used"`
	Summary string `json:"summary,omitempty"`
}

// setupLister is a store that says what its slots hold (the panel's).
type setupLister interface {
	SetupSlots() []SetupSlot
}

// setupSlotCount is the slot range the web offers (the SCPI *SAV range).
const setupSlotCount = 20

// SetSetupMemory wires the setup store behind /api/setup. Call before serving;
// without it the endpoint answers that setup memory is unavailable.
// TRLC-LINKS: REQ-SDS-219
func (s *Server) SetSetupMemory(m SetupMemory) { s.setups = m }

// hSetup saves or recalls a setup slot (the web top bar's Setups):
// POST {"op":"save"|"recall","slot":1..20} → {"ok":bool,"err":string}.
// GET lists what the slots hold, as the scope's FILE tab shows them:
// {"ok":true,"slots":[{"slot":1,"used":true,"summary":"BUS  500us/div ..."}]}
// (slots is null when the store cannot say).
// TRLC-LINKS: REQ-SDS-219
func (s *Server) hSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if s.setups == nil {
			writeJSON(w, map[string]any{"ok": false, "err": "setup memory unavailable"})
			return
		}
		var slots []SetupSlot
		if l, ok := s.setups.(setupLister); ok {
			slots = l.SetupSlots()
		}
		writeJSON(w, map[string]any{"ok": true, "slots": slots})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Op   string `json:"op"`
		Slot int    `json:"slot"`
	}
	if err := decodeSmall(io.LimitReader(r.Body, 1<<10), &req); err != nil {
		writeJSON(w, map[string]any{"ok": false, "err": "bad json"})
		return
	}
	if req.Slot < 1 || req.Slot > setupSlotCount {
		writeJSON(w, map[string]any{"ok": false, "err": "slot must be 1-20"})
		return
	}
	if s.setups == nil {
		writeJSON(w, map[string]any{"ok": false, "err": "setup memory unavailable"})
		return
	}
	var err error
	switch req.Op {
	case "save":
		err = s.setups.SaveSetup(req.Slot)
	case "recall":
		err = s.setups.RecallSetup(req.Slot)
	default:
		writeJSON(w, map[string]any{"ok": false, "err": "op must be save or recall"})
		return
	}
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "err": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
