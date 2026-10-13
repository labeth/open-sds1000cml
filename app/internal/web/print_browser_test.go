// ENGMODEL-OWNER-UNIT: FU-WEB-APP-CONTROLS
package web

import (
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// The web's Device screenshot (PRINT) says what the scope saved, as its LCD
// does (print_browser.mjs; it only ever said "saving", bug hunt #3).
// TRLC-LINKS: REQ-SDS-069
func TestDevicePrintNamesTheFileBrowser(t *testing.T) {
	testenv.NeedNode(t)
	r := newRemoteRig(t)
	r.pc.SetHardcopy(func() (string, error) { return "SDS0042.png", nil })
	out, err := exec.Command("node", "print_browser.mjs", r.app.URL).CombinedOutput()
	t.Logf("print_browser.mjs:\n%s", out)
	if strings.HasPrefix(strings.TrimSpace(string(out)), "SKIP:") {
		testenv.SkipBrowser(t, "browser unavailable")
	}
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("device print e2e failed: %v", err)
	}
}
