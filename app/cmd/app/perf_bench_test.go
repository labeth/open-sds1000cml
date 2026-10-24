// ENGMODEL-OWNER-UNIT: FU-APP-APP
package main

import (
	"fmt"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/lcd"
	"open-sds/app/internal/panel/wsview"
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

// TRLC-LINKS: REQ-SDS-169
func BenchmarkSRAMHealthToken(b *testing.B) {
	for b.Loop() {
		sramHealthToken(123456, 1700000000123456789)
	}
}

// TRLC-LINKS: REQ-SDS-169
func BenchmarkSRAMHealthTokenFmt(b *testing.B) {
	for b.Loop() {
		_ = []byte(fmt.Sprintf("mode=diag backend=sram beats=%d ts=%d\n", 123456, int64(1700000000123456789)))
	}
}

// The token reads as the fmt one did.
// TRLC-LINKS: REQ-SDS-169
func TestSRAMHealthTokenText(t *testing.T) {
	if got, want := string(sramHealthToken(7, -3)), fmt.Sprintf("mode=diag backend=sram beats=%d ts=%d\n", 7, -3); got != want {
		t.Fatalf("%q, want %q", got, want)
	}
}
