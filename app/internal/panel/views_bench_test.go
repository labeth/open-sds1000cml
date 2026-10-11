// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import "testing"

// The LCD loop builds the view every tick and the panel loop publishes the
// remote snapshot every 40 ms; these measure both, so a change that makes
// them dear on the device's single core shows up.
// go test ./internal/panel -run - -bench View

// TRLC-LINKS: REQ-SDS-211
func BenchmarkViewScope(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Snapshot()
	}
}

// TRLC-LINKS: REQ-SDS-212
func BenchmarkViewSheet(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	c.button(btnTrigMenu)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Snapshot()
	}
}

// TRLC-LINKS: REQ-SDS-211
func BenchmarkViewPublish(b *testing.B) {
	c, _, _ := newC(&testing.T{})
	c.Sheets() // a remote is watching the sheets: the values are hashed too
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.publish()
	}
}
