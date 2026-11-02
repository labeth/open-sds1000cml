// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/frames"
	"github.com/labeth/open-sds1000cml/app/internal/panel"
	"github.com/labeth/open-sds1000cml/app/internal/web"
)

// scopeSource wires the web layer: setters/stats from the engine, frames
// from the fan-out (the arena's read slot belongs to the fan-out alone).
// TRLC-LINKS: REQ-SDS-167
type scopeSource struct {
	*engine.Engine
	fo *frames.Fanout
}

// TRLC-LINKS: REQ-SDS-167
func (s scopeSource) WithFrame(fn func(*engine.Frame)) { s.fo.WithFrame(fn) }

// WaitNextFrame lets the web layer park a long-poll until the fan-out
// snapshots a frame newer than last (web.go type-asserts for this method, so
// test doubles without it degrade to a short poll).
// TRLC-LINKS: REQ-SDS-167
func (s scopeSource) WaitNextFrame(last uint64, timeout time.Duration) uint64 {
	return s.fo.WaitNext(last, timeout)
}

// webSetups is the panel's shared setup store as the web's Setups list it:
// what each slot holds, as the scope's FILE tab shows it.
// TRLC-LINKS: REQ-SDS-219
type webSetups struct{ *panel.SharedSetups }

// TRLC-LINKS: REQ-SDS-219
func (w webSetups) SetupSlots() []web.SetupSlot {
	var out []web.SetupSlot
	for _, s := range w.Slots() {
		out = append(out, web.SetupSlot{Slot: s.Slot, Used: s.Used, Summary: s.Summary})
	}
	return out
}

// matchText is a serial trigger's match as the BUS table writes its units:
// hex, "?" for a masked nibble, ERR for an error, SOF for a frame's start
// and CMD for a MIL-1553 command word.
// TRLC-LINKS: REQ-SDS-168
func matchText(p engine.SerialParams) string {
	var toks []string
	if len(p.Pattern) == 0 {
		for _, b := range p.Bytes {
			toks = append(toks, fmt.Sprintf("%02X", b))
		}
		return strings.Join(toks, " ")
	}
	for _, u := range p.Pattern {
		switch {
		case u.Kind == 4:
			toks = append(toks, "ERR")
			continue
		case u.Kind == 1: // a frame's start
			toks = append(toks, "SOF")
			continue
		case u.Mask&engine.MILCommand != 0 && u.Value&engine.MILCommand != 0:
			toks = append(toks, "CMD")
			continue
		}
		h := []byte(fmt.Sprintf("%02X", u.Value))
		for i := range h {
			if shift := 4 * (len(h) - 1 - i); (u.Mask>>shift)&15 != 15 {
				h[i] = '?'
			}
		}
		toks = append(toks, string(h))
	}
	return strings.Join(toks, " ")
}
