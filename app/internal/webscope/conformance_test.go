// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"testing"
	"time"

	"github.com/labeth/open-dso-core/uimodel/panel"
	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-dso-core/webserver/webtest"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/board"
	cmlpanel "github.com/labeth/open-sds1000cml/app/internal/panel"
	engine "github.com/labeth/open-sds1000cml/app/internal/scopeapi"
)

// The SDS1000CML+ adapter passes the shared server's conformance suite: with
// the panel controller and the front end as the app runs them (the sheets
// list every row the page drives), with them serving the capabilities
// document from the board as the app does, with the engine's every optional surface
// and the front end, and with a bare engine and no panel or front end.
// TRLC-LINKS: REQ-SDS-161, REQ-SDS-162, REQ-SDS-163, REQ-SDS-164
func TestConformance(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		webtest.Run(t, func() webserver.Scope {
			eng := newRemoteEngine()
			fe := analog.New(remoteSPI{}, func(time.Duration) {}, nil)
			fe.OnOffset(eng.SetOffsetDAC)
			lv := board.New(eng, fe)
			pc := panel.New(eng, fe, lv, cmlpanel.Hardware{}, engine.SupportedTdivs(), 500e-6, t.Logf)
			sc := New(eng, fe, lv, pc, func() []byte { return []byte("\x89PNG") })
			sc.SetSetupMemory(&goldenSetups{})
			return sc
		}, webserver.WithPprof())
	})
	t.Run("caps", func(t *testing.T) {
		webtest.Run(t, func() webserver.Scope {
			eng := newRemoteEngine()
			fe := analog.New(remoteSPI{}, func(time.Duration) {}, nil)
			fe.OnOffset(eng.SetOffsetDAC)
			lv := board.New(eng, fe)
			pc := panel.New(eng, fe, lv, cmlpanel.Hardware{}, engine.SupportedTdivs(), 500e-6, t.Logf)
			return New(eng, fe, lv, pc, nil, WithBoard(cmlBoard(t, fe))).WithCaps()
		})
	})
	t.Run("parts", func(t *testing.T) {
		webtest.Run(t, func() webserver.Scope {
			g := newGoldenEngine(goldenNative(31))
			fa := &fakeAnalog{idx: 8, set: true, probe: [2]float64{10, 1}}
			sc := New(g, fa, levelsFor(g, fa), nil, func() []byte { return []byte("\x89PNG") })
			sc.SetSetupMemory(&goldenSetups{})
			return sc
		})
	})
	t.Run("bare", func(t *testing.T) {
		webtest.Run(t, func() webserver.Scope {
			fs := &fakeScope{frame: goldenNative(31)}
			return New(fs, nil, levelsFor(fs, nil), nil, nil)
		})
	})
}
