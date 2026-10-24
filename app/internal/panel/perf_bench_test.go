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

// TRLC-LINKS: REQ-SDS-212
func BenchmarkWrapText(b *testing.B) {
	s := "The chip or protocol the bus carries: the packet list, the strip and the web table name each transaction."
	for b.Loop() {
		wrapText(s, 38)
	}
}

// TRLC-LINKS: REQ-SDS-212
func BenchmarkRowValues(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	s := c.snap()
	for b.Loop() {
		for _, t := range sheetTabs {
			for _, r := range t.rows {
				r.valueText(s)
				if r.kind == wsview.Number {
					r.gauge(s)
				}
			}
		}
	}
}
