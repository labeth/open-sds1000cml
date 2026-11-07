// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/testenv"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// TestADCFaultBrowser drives adcfault_browser.mjs: with /api/status carrying
// adc_fault (converters left not converting after the recoveries), the web
// UI's status line names them, as the LCD's notice does.
// TRLC-LINKS: REQ-SDS-231
func TestADCFaultBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const N = 2048
	c1, c2 := make([]uint8, N), make([]uint8, N)
	for i := range c1 {
		c1[i], c2[i] = 150, 100
	}
	gen := func() *sample.Frame {
		return &sample.Frame{
			Seq:       3,
			WinPoints: N,
			TriggerAt: -1,
			SampleS:   800e-9,
			Snapshot:  sample.Settings{TdivS: 500e-6, DisplayedS: 500e-6},
			Traces:    []sample.Trace{{Codes8: c1, Valid: N}, {Ch: 1, Codes8: c2, Valid: N}},
		}
	}
	fs := &fakeScope{frameGen: gen, stats: engine.Stats{
		Running: true, TrigPosFrac: 0.5,
		ADCFault: "ADC fault: converter E2 not converting - restart the scope",
	}}
	srv := httptest.NewServer(newServer(fs, nil, nil, nil).Handler())
	defer srv.Close()
	out, err := exec.CommandContext(t.Context(), "node", "adcfault_browser.mjs", srv.URL).CombinedOutput()
	t.Logf("adcfault_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		testenv.SkipBrowser(t, "browser unavailable")
	}
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("ADC fault e2e failed: %v", err)
	}
}
