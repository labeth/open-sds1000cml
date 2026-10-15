// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"fmt"
	"sync"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel"
)

// fakeRefStore keeps REF A/B as the panel does for the web's fake scopes: a
// store takes the screen window of the latest frame (the trigger at mid
// screen, one point per column, nearest sample) at 1 V/div.
type fakeRefStore struct {
	mu   sync.Mutex
	refs [2]panel.RefExport
	gen  uint64
}

// TRLC-LINKS: REQ-SDS-227
func (s *fakeRefStore) export() [2]panel.RefExport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refs
}

// TRLC-LINKS: REQ-SDS-227
func (s *fakeRefStore) act(frames func(func(*engine.Frame)), slot int, op string) panel.SetResult {
	if slot < 0 || slot > 1 {
		return panel.SetResult{Error: "no slot"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := &s.refs[slot]
	switch op {
	case "store":
		var e panel.RefExport
		frames(func(f *engine.Frame) {
			if f == nil || len(f.C1) == 0 || f.IsEnv {
				return
			}
			valid := min(max(f.Valid, 1), len(f.C1))
			win := f.WinCols
			if win <= 0 || win > valid {
				win = valid
			}
			xc := f.EdgeX
			if xc < 0 {
				xc = float64(valid) / 2
			}
			cols := func(sig []uint8) []int {
				if len(sig) < valid {
					return nil
				}
				out := make([]int, panel.RefCols)
				for x := range out {
					pos := xc - float64(win)/2 + float64(x)*float64(win)/panel.RefCols
					out[x] = int(sig[int(min(max(pos, 0), float64(valid-1)))])
				}
				return out
			}
			e = panel.RefExport{Has: true, Show: true, Cols: panel.RefCols, C1: cols(f.C1), C2: cols(f.C2), Vdiv: [2]float64{1, 1},
				Probe: [2]float64{1, 1}, TdivS: f.TdivS, PosFrac: .5, At: time.Now().UnixMilli()}
		})
		if !e.Has {
			return panel.SetResult{Error: "no frame to store"}
		}
		s.gen++
		e.Gen = s.gen
		*r = e
	case "show", "hide":
		if !r.Has {
			return panel.SetResult{Error: fmt.Sprintf("reference %c is empty", 'A'+slot)}
		}
		r.Show = op == "show"
	case "clear":
		if !r.Has {
			return panel.SetResult{Error: fmt.Sprintf("reference %c is empty", 'A'+slot)}
		}
		*r = panel.RefExport{}
	default:
		return panel.SetResult{Error: "no action " + op}
	}
	return panel.SetResult{OK: true}
}

// refsOnlyPanel is a scope with references and no keys (the page's AUTO is
// refused, as with no panel).
type refsOnlyPanel struct {
	frames func(func(*engine.Frame))
	refs   fakeRefStore
}

// TRLC-LINKS: REQ-SDS-227
func (p *refsOnlyPanel) InjectButton(string) bool { return false }

// TRLC-LINKS: REQ-SDS-227
func (p *refsOnlyPanel) InjectKnob(string, int, int) bool { return false }

// TRLC-LINKS: REQ-SDS-227
func (p *refsOnlyPanel) RefsExport() [2]panel.RefExport { return p.refs.export() }

// TRLC-LINKS: REQ-SDS-227
func (p *refsOnlyPanel) RefAction(slot int, op string) panel.SetResult {
	return p.refs.act(p.frames, slot, op)
}
