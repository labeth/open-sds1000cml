// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"

	"open-sds/app/internal/buildinfo"
	"open-sds/app/internal/engine"
)

// The SYS tab (Help key) names the firmware and its version, so an owner can
// read on the scope itself which release it runs.
// TRLC-LINKS: REQ-SDS-212, REQ-SDS-174
func TestSysTabNamesTheVersion(t *testing.T) {
	old := buildinfo.Version
	buildinfo.Version = "v1.0.0-rc"
	defer func() { buildinfo.Version = old }()
	c, _, _ := newC(t)
	sig := make([]uint8, 4096)
	c.OnFrame(&engine.Frame{Seq: 1, C1: sig, C2: sig, Valid: 4096, WinCols: 2049, SampleS: 2e-9, TdivS: 1e-6})
	press(t, c, "help")
	var text []string
	for _, sp := range c.View().Sheet.Status {
		text = append(text, sp.Text)
	}
	if got := strings.Join(text, " | "); !strings.Contains(got, "open-sds1000cml v1.0.0-rc") {
		t.Fatalf("the SYS tab's status: %q", got)
	}
}
