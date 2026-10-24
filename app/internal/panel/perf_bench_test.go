// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"testing"

	"open-sds/app/internal/panel/wsview"
)

// Each workspace's view, built every LCD tick on the device's single core.
// go test ./internal/panel -run - -bench WSView

// TRLC-LINKS: REQ-SDS-211
func BenchmarkWSView(b *testing.B) {
	for _, ws := range []struct {
		name string
		ws   wsview.WS
	}{{"scope", wsview.Scope}, {"bus", wsview.Bus}, {"spectrum", wsview.Spectrum}, {"measure", wsview.Measure}, {"compare", wsview.Compare}} {
		b.Run(ws.name, func(b *testing.B) {
			c, _, _ := newC(&testing.T{})
			c.switchWS(ws.ws)
			b.ReportAllocs()
			for b.Loop() {
				c.Snapshot()
			}
		})
	}
}
