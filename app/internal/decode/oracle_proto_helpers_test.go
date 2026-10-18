// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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

// skipSigrokDefect skips a cross-check when the installed sigrok's decoder
// cannot run it, told by symptom in its output on a short idle capture of
// the channels names: libsigrokdecode 0.5.3's swim decoder reads its sample
// rate before sigrok-cli has set it, and its sdcard_sd has no "commands"
// annotation. CI runs those cross-checks again against libsigrokdecode's own
// decoders, where a skip fails.
// TRLC-LINKS: REQ-SDS-018
func skipSigrokDefect(t *testing.T, names []string, pdSpec, annSpec, symptom string) {
	t.Helper()
	needSigrok(t)
	var sb strings.Builder
	sb.WriteString(strings.Join(names, ",") + "\n")
	for i := 0; i < 64; i++ {
		sb.WriteString(strings.Repeat("1,", len(names)-1) + "1\n")
	}
	csv := filepath.Join(t.TempDir(), "idle.csv")
	if err := os.WriteFile(csv, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("sigrok-cli", "-i", csv, "-I", "csv:column_formats="+strconv.Itoa(len(names))+"l:samplerate=1000000",
		"-P", pdSpec, "-A", annSpec).CombinedOutput()
	if strings.Contains(string(out), symptom) {
		t.Skipf("this sigrok's %s decoder cannot run the cross-check: %s", strings.SplitN(pdSpec, ":", 2)[0], symptom)
	}
}
