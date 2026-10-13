// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// The TIME tab names the converters' sample interval: on a peak-detect
// readout a min/max point spans 244 samples, which is not the interval (2 ns
// sampling, not 488 ns).
// TRLC-LINKS: REQ-SDS-212
func TestTimeTabNamesTheRealSampling(t *testing.T) {
	c, _, _ := newC(t)
	sig := make([]uint8, 4096)
	c.OnFrame(&engine.Frame{Seq: 1, C1: sig, C2: sig, Valid: 4096, WinCols: 2049, SampleS: 488e-9, CaptureSampleS: 2e-9, PeakDetect: true, TdivS: 100e-6})
	press(t, c, "horizmenu")
	var text []string
	for _, sp := range c.View().Sheet.Status {
		text = append(text, sp.Text)
	}
	if got := strings.Join(text, " | "); !strings.Contains(got, "sampled every 2ns, shown peak-detected") {
		t.Fatalf("the TIME tab's status: %q", got)
	}
}
