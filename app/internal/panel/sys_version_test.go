// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import (
	"strings"
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/buildinfo"
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
	c.OnFrame(&sample.Frame{Seq: 1, WinPoints: 2049, SampleS: 2e-9, Snapshot: sample.Settings{TdivS: 1e-6}, Traces: []sample.Trace{{Codes8: sig, Valid: 4096}, {Ch: 1, Codes8: sig, Valid: 4096}}})
	press(t, c, "help")
	var text []string
	for _, sp := range c.View().Sheet.Status {
		text = append(text, sp.Text)
	}
	if got := strings.Join(text, " | "); !strings.Contains(got, "open-sds1000cml v1.0.0-rc") {
		t.Fatalf("the SYS tab's status: %q", got)
	}
}
