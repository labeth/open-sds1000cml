// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"os"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel"
	"open-sds/app/internal/testenv"
)

// framedRig is the remote rig whose engine serves a square on C1 and a
// slower one on C2 at any timebase, the panel's frame source too, so the
// panel stores references and runs AUTO on them.
// TRLC-LINKS: REQ-SDS-227
func framedRig(t *testing.T) *remoteRig {
	const n = 2048
	var seq uint64
	// wired before the panel's loop runs: it reads the frame source
	return newRemoteRig(t, func(pc *panel.Controller, eng *remoteEngine) {
		eng.lock(func() {
			f := eng.fakeScope
			f.mu.Lock()
			// called under f.mu (fakeScope.WithFrame)
			f.frameGen = func() *engine.Frame {
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
				return &engine.Frame{C1: c1, C2: c2, Seq: seq, Valid: n, WinCols: n, EdgeX: n / 2, TdivS: tdiv, DisplayedS: tdiv,
					SampleS: tdiv * 10 / n, Trigd: true, Coherent: true}
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
