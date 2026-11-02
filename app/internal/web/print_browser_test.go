// ENGMODEL-OWNER-UNIT: FU-WEB-APP-CONTROLS

package web

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/labeth/open-sds1000cml/app/internal/testenv"
)

// The web's Device screenshot (PRINT) says what the scope saved, as its LCD
// does, not just "saving" (print_browser.mjs).
// TRLC-LINKS: REQ-SDS-069
func TestDevicePrintNamesTheFileBrowser(t *testing.T) {
	testenv.NeedNode(t)
	r := newRemoteRig(t)
	r.pc.SetHardcopy(func() (string, error) { return "SDS0042.png", nil })
	out, err := exec.CommandContext(t.Context(), "node", "print_browser.mjs", r.app.URL).CombinedOutput()
	t.Logf("print_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		testenv.SkipBrowser(t, "browser unavailable")
	}
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("device print e2e failed: %v", err)
	}
}
