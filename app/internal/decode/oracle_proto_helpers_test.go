// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// needSigrokPD skips a cross-check whose sigrok decoder the installed
// sigrok does not ship: Ubuntu's libsigrokdecode 0.5.3 has no ir_sirc or
// sae_j1850_vpw (added later), and CI runs that version. A missing
// sigrok-cli itself still fails under CI_REQUIRE_SIGROK=1 (needSigrok).
// TRLC-LINKS: REQ-SDS-018
func needSigrokPD(t *testing.T, pd string) {
	t.Helper()
	needSigrok(t)
	out, err := exec.Command("sigrok-cli", "-L").CombinedOutput()
	if err != nil {
		t.Fatalf("sigrok-cli -L: %v\n%s", err, out)
	}
	if !regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(pd) + `\s`).Match(out) {
		t.Skipf("this sigrok has no %s decoder", pd)
	}
}

// sigrokLogicDMX reports the 0.5 releases' DMX512 decoder (on logic, its
// channel "dmx"); later ones stack it on the UART decoder.
// TRLC-LINKS: REQ-SDS-018
func sigrokLogicDMX(t *testing.T) bool {
	t.Helper()
	out, err := exec.Command("sigrok-cli", "-P", "dmx512", "--show").CombinedOutput()
	if err != nil {
		return true // an older sigrok-cli: assume the 0.5 decoder
	}
	return !strings.Contains(string(out), "uart")
}
