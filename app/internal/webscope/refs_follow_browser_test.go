// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"os"
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/testenv"
	"github.com/labeth/open-dso-core/uimodel/panel"
)

// framedRig is the remote rig whose engine serves a square on C1 and a
// slower one on C2 at any timebase, the panel's frame source too, so the
// panel stores references and runs AUTO on them.
// TRLC-LINKS: REQ-SDS-227
func framedRig(t *testing.T) *remoteRig {
	t.Helper()
	const n = 2048
	var seq uint64
	// wired before the panel's loop runs: it reads the frame source
	return newRemoteRig(t, func(pc *panel.Controller, eng *remoteEngine) {
		eng.lock(func() {
			f := eng.fakeScope
			f.mu.Lock()
			// called under f.mu (fakeScope.WithFrame)
			f.frameGen = func() *sample.Frame {
				seq++
				c1, c2 := make([]uint8, n), make([]uint8, n)
				for i := range c1 {
					c1[i], c2[i] = 68, 98
					if i%128 < 64 {
						c1[i] = 188
					}
					if i%512 < 256 {
						c2[i] = 158
					}
				}
				tdiv := f.stats.TdivS
				return &sample.Frame{
					Seq:       seq,
					WinPoints: n,
					TriggerAt: n / 2,
					SampleS:   tdiv * 10 / n,
					Snapshot:  sample.Settings{TdivS: tdiv, DisplayedS: tdiv},
					Status:    sample.Status{Triggered: true, Coherent: true},
					Traces:    []sample.Trace{{Codes8: c1, Valid: n}, {Ch: 1, Codes8: c2, Valid: n}},
				}
			}
			f.mu.Unlock()
		})
		pc.SetFrameSource(eng.WithFrame)
	})
}

// TestWebRefsAndAutoFollowTheScope: the page's REF A/B are the scope's (a
// reference stored, shown or cleared on the scope or the phone shows on the
// page; one saved, hidden or cleared on the page is the scope's), the
// difference readout compares against them, and AUTO on the page frames for
// the page's workspace, not the LCD's.
// TRLC-LINKS: REQ-SDS-227, REQ-SDS-226
func TestWebRefsAndAutoFollowTheScope(t *testing.T) {
	testenv.NeedNode(t)
	for _, f := range []string{"refs_follow_browser.mjs", "scope_po.mjs"} {
		if _, err := os.ReadFile(f); err != nil {
			t.Fatal(err)
		}
	}
	r := framedRig(t)
	wsDriver(t, "refs_follow_browser.mjs", r.app.URL, r.ctrl.URL)
}
