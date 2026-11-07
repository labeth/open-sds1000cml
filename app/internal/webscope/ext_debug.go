// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// Extensions are the SDS1000CML+'s own routes beside the shared API: the
// SRAM decoded capture and the FPGA stacking session where the engine has
// them, the FPGA panel scan, and the engine's live tuning knobs. Each is
// board-specific: the decoded capture's configurations, event layout and
// record are the 18 Mbit SRAM's and the CML images'; the stacking session
// is the CML's stacking image; the panel scan reads the CML's key chain
// through its FPGA; the tuning knobs are the CML engine's drain and settle
// timings.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-022, REQ-SDS-141, REQ-SDS-162
func (s *Scope) extensions() []webserver.Route {
	routes := s.decodedRoutes()
	routes = append(routes, s.fpgaStackRoutes()...)
	return append(routes,
		webserver.Route{Pattern: "/api/panel/scan", Feature: "panel_scan", Handler: http.HandlerFunc(s.hPanelScan)},
		webserver.Route{Pattern: "/api/debug/tune", Feature: "debug_tune", Handler: http.HandlerFunc(s.hTune)},
	)
}

// hTune applies the live frame-rate and CPU tuning knobs and
// returns the effective values. GET reports current values; POST {json} sets
// them. Fields omitted / negative are left unchanged (see engine.Tune).
// TRLC-LINKS: REQ-SDS-162
func (s *Scope) hTune(w http.ResponseWriter, r *http.Request) {
	cur := s.sc.TuneSnapshot()
	if r.Method != http.MethodPost {
		webserver.WriteJSON(w, cur)
		return
	}
	// Decode onto the current values so any omitted field (bools included) keeps
	// its present setting; only what the body carries is overridden.
	t := cur
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&t); err != nil {
		webserver.WriteBadJSON(w)
		return
	}
	webserver.WriteJSON(w, s.sc.Tune(t))
}

// panelScanner is the engine's front-panel scan (ADR-PANEL-SERIAL-SCAN).
type panelScanner interface {
	SetPanelScan(sramcapture.PanelConfig) error
	PanelScan() (sramcapture.PanelFrame, error)
}

// hPanelScan reads the panel scan's last frame (GET) or sets its config (POST).
// TRLC-LINKS: REQ-SDS-022
func (s *Scope) hPanelScan(w http.ResponseWriter, r *http.Request) {
	ps, ok := s.sc.(panelScanner)
	if !ok {
		webserver.WriteJSON(w, map[string]any{"ok": false, "err": "no panel scan"})
		return
	}
	if r.Method == http.MethodPost {
		var p sramcapture.PanelConfig
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&p); err != nil {
			webserver.WriteBadJSON(w)
			return
		}
		if err := ps.SetPanelScan(p); err != nil {
			webserver.WriteJSON(w, map[string]any{"ok": false, "err": err.Error()})
			return
		}
	}
	f, err := ps.PanelScan()
	if err != nil {
		webserver.WriteJSON(w, map[string]any{"ok": false, "err": err.Error()})
		return
	}
	webserver.WriteJSON(w, map[string]any{"ok": true, "frame": f})
}
