// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
)

// The LCD draws 20 frames a second on the device's single core; these
// measure a whole frame for the plain trace and the workspace screens, so a
// change that makes one of them much dearer than the trace shows up.
// go test ./internal/lcd -run - -bench Render

// benchRender renders f with h b.N times.
// TRLC-LINKS: REQ-SDS-021
func benchRender(b *testing.B, f *engine.Frame, h HUD) {
	sf := NewMemSurface()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Render(sf, f, h, true)
	}
}

// TRLC-LINKS: REQ-SDS-021
func BenchmarkRenderTrace(b *testing.B) { benchRender(b, goldenYTFrame(), goldenYTHUD()) }

// TRLC-LINKS: REQ-SDS-211
func BenchmarkRenderScopeRail(b *testing.B) {
	v := wsview.View{WS: wsview.Scope, Layout: paneLayout(0), Rail: true, Scope: &wsview.ScopePane{AdjustTag: "Adjust: Level 200mV"}}
	benchRender(b, goldenYTFrame(), goldenUIHUD(v))
}

// TRLC-LINKS: REQ-SDS-212
func BenchmarkRenderSheet(b *testing.B) {
	keys := &wsview.Keys{Title: "LEVEL", Top: 30, Items: [5]wsview.Key{{Label: "Down"}, {Label: "Up"}}}
	v := wsview.View{WS: wsview.Scope, Layout: wsview.Layout{MenuW: 116, Sheet: true, HideTraces: true, HideBottom: true}, Sheet: goldenSheet(), Keys: keys}
	benchRender(b, goldenYTFrame(), goldenUIHUD(v))
}

// TRLC-LINKS: REQ-SDS-215
func BenchmarkRenderBusEye(b *testing.B) {
	const cols, rows = 384, 166
	grid, mx := goldenEyeGrid(cols, rows)
	e := &wsview.EyePane{Pane: wsview.Pane{Title: "BUS"}, Grid: grid, Cols: cols, Rows: rows, Max: mx, HasBox: true,
		Box: [4]int{110, 20, 270, 145}, SampleCol: cols / 2, ThreshRow: rows / 2, TimeLabel: "TIME"}
	v := wsview.View{WS: wsview.Bus, Layout: wsview.Layout{MenuW: 30, PlotTop: 136, HideTraces: true, HideBottom: true}, Rail: true, Eye: e}
	h := goldenUIHUD(v)
	h.TdivS = 100e-6
	benchRender(b, goldenBusFrame(), h)
}
