// ENGMODEL-OWNER-UNIT: FU-APP-APP
package app

import (
	"open-sds/app/internal/engine"
	"open-sds/app/internal/lcd"
	"open-sds/app/internal/panel/wsview"
	"testing"
)

// benchHUD is a HUD as the LCD loop builds it for SCOPE with a menu open.
// TRLC-LINKS: REQ-SDS-168
func benchHUD() lcd.HUD {
	var hud lcd.HUD
	hud.UI = wsview.View{}
	hud.TdivS, hud.C1VdivV, hud.Running, hud.Notice = 1e-6, 0.5, true, "AUTO: square 1 kHz"
	hud.AutosetMsg, hud.MeasItems = "", []string{"Vpp", "Freq", "Rise"}
	return hud
}

// TRLC-LINKS: REQ-SDS-168
func BenchmarkRenderSigOf(b *testing.B) {
	hud := benchHUD()
	f := &engine.Frame{Seq: 9}
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
