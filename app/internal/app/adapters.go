// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"time"

	"github.com/labeth/open-dso-core/engine/fanout"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/uimodel/panel"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// scopeSource wires the web layer: setters/stats from the engine's controls, frames
// from the fan-out (the arena's read slot belongs to the fan-out alone).
// TRLC-LINKS: REQ-SDS-167
type scopeSource struct {
	cmlControls
	fo *fanout.Fanout[sample.Frame]
}

// TRLC-LINKS: REQ-SDS-167
func (s scopeSource) WithFrame(fn func(*sample.Frame)) { s.fo.WithFrame(fn) }

// WaitNextFrame lets the web layer park a long-poll until the fan-out
// snapshots a frame newer than last (webscope type-asserts for this method, so
// test doubles without it degrade to a short poll).
// TRLC-LINKS: REQ-SDS-167
func (s scopeSource) WaitNextFrame(last uint64, timeout time.Duration) uint64 {
	return s.fo.WaitNext(last, timeout)
}

// matchText is a serial trigger's match as the BUS table writes its units
// (panel.SerialMatchText).
// TRLC-LINKS: REQ-SDS-168
func matchText(p engine.SerialParams) string { return panel.SerialMatchText(p) }
