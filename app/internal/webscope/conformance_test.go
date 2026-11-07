// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"testing"

	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-dso-core/webserver/webtest"
)

// The SDS1000CML+ adapter passes the shared server's conformance suite: with
// the engine's every optional surface, the panel and the front end (as the
// app runs it), and with a bare engine and no panel or front end.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
func TestConformance(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		webtest.Run(t, func() webserver.Scope {
			g := newGoldenEngine(goldenNative(31))
			fa := &fakeAnalog{idx: 8, set: true, probe: [2]float64{10, 1}}
			sc := New(g, fa, levelsFor(g, fa), newGoldenPanel(), func() []byte { return []byte("\x89PNG") })
			sc.SetSetupMemory(&goldenSetups{})
			return sc
		}, webserver.WithPprof())
	})
	t.Run("bare", func(t *testing.T) {
		webtest.Run(t, func() webserver.Scope {
			fs := &fakeScope{frame: goldenNative(31)}
			return New(fs, nil, levelsFor(fs, nil), nil, nil)
		})
	})
}
