// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"fmt"
	"sync"
	"time"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/uimodel/panel"
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
func (s *fakeRefStore) act(frames func(func(*sample.Frame)), slot int, op string) panel.SetResult {
	if slot < 0 || slot > 1 {
		return panel.SetResult{Error: "no slot"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := &s.refs[slot]
	switch op {
	case "store":
		var e panel.RefExport
		frames(func(f *sample.Frame) {
			if f == nil || len(f.Codes8(0)) == 0 || f.Flags.Has(sample.Envelope) {
				return
			}
			valid := min(max(f.Len(), 1), len(f.Codes8(0)))
			win := f.WinPoints
			if win <= 0 || win > valid {
				win = valid
			}
			xc := f.TriggerAt
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
			e = panel.RefExport{
				Has: true, Show: true, Cols: panel.RefCols, C1: cols(f.Codes8(0)), C2: cols(f.Codes8(1)), Vdiv: panel.ChanValues{1, 1},
				Probe: panel.ChanValues{1, 1}, TdivS: f.Snapshot.TdivS, PosFrac: .5, At: time.Now().UnixMilli(),
			}
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
	frames func(func(*sample.Frame))
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
