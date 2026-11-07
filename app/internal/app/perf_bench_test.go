// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"testing"

	"github.com/labeth/open-dso-core/render"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/uimodel/wsview"
)

// benchHUD is a HUD as the LCD loop builds it for SCOPE with a menu open.
// TRLC-LINKS: REQ-SDS-168
func benchHUD() render.HUD {
	var hud render.HUD
	hud.UI = wsview.View{}
	hud.TdivS, hud.Ch[0].VdivV, hud.Running, hud.Notice = 1e-6, 0.5, true, "AUTO: square 1 kHz"
	hud.AutosetMsg, hud.MeasItems = "", []string{"Vpp", "Freq", "Rise"}
	return hud
}

// TRLC-LINKS: REQ-SDS-168
func BenchmarkRenderSigOf(b *testing.B) {
	hud := benchHUD()
	f := &sample.Frame{Seq: 9, Traces: []sample.Trace{{}, {Ch: 1}}}
	for b.Loop() {
		renderSigOf(f, hud, true)
	}
}

// TRLC-LINKS: REQ-SDS-168
func BenchmarkUISig(b *testing.B) {
	hud := benchHUD()
	for b.Loop() {
		uiSig(hud.UI)
	}
}
