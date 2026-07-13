package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// TestSigrokExportLogicE2E proves the whole point of the mixed analog+logic
// .sr export: a UART-shaped capture exported by sigrok_export.js is opened by
// a REAL sigrok-cli, which reports the expected channel set (D1/D2 logic +
// CH1/CH2 analog) and — the payoff — runs its own uart protocol decoder on
// the exported D1 logic channel and reads back the exact bytes that were
// encoded into the waveform. Needs node (to run the encoder) AND sigrok-cli;
// skips when either is missing. The browser CI lane installs both, and
// CI_REQUIRE_BROWSER=1 turns the node skip into a failure there.
func TestSigrokExportLogicE2E(t *testing.T) {
	testenv.NeedNode(t)
	if _, err := exec.LookPath("sigrok-cli"); err != nil {
		if os.Getenv("CI_REQUIRE_SIGROK") == "1" {
			t.Fatal("CI_REQUIRE_SIGROK=1 but sigrok-cli is not installed")
		}
		t.Skip("sigrok-cli not installed; skipping export e2e")
	}
	dir := t.TempDir()
	sr := filepath.Join(dir, "uart.sr")

	// 8N1 "Hi!" at 16 samples/bit, 1 MHz -> 62500 baud, rendered as two-level
	// ADC codes on CH1 (CH2 flat) and encoded via the real export module.
	script := `
const { sigrokSeries, sigrokSR } = require("./sigrok_export.js");
const spb = 16, bytes = [0x48, 0x69, 0x21];
const bits = [];
for (let i = 0; i < 4 * spb; i++) bits.push(1);
for (const b of bytes) {
  for (const bit of [0, ...Array.from({ length: 8 }, (_, i) => (b >> i) & 1), 1])
    for (let k = 0; k < spb; k++) bits.push(bit);
  for (let k = 0; k < 2 * spb; k++) bits.push(1);
}
for (let i = 0; i < 4 * spb; i++) bits.push(1);
const c1 = Int16Array.from(bits, (b) => (b ? 200 : 56));
const c2 = Int16Array.from(bits, () => 128);
const frame = { seq: 1, c1, c2, vpc1: 0.02, vpc2: 0.02, off1_v: 0, off2_v: 0, dt_s: 1e-6, col_span_s: c1.length * 1e-6 };
require("fs").writeFileSync(process.argv[process.argv.length - 1], sigrokSR(sigrokSeries(frame)));
`
	node, _ := exec.LookPath("node")
	if out, err := exec.Command(node, "-e", script, "--", sr).CombinedOutput(); err != nil {
		t.Fatalf("export encoder failed: %v\n%s", err, out)
	}

	show, err := exec.Command("sigrok-cli", "-i", sr, "--show").CombinedOutput()
	if err != nil {
		t.Fatalf("sigrok-cli --show failed: %v\n%s", err, show)
	}
	for _, want := range []string{"D1: logic", "D2: logic", "CH1: analog", "CH2: analog", "Logic unitsize: 1"} {
		if !strings.Contains(string(show), want) {
			t.Fatalf("sigrok-cli --show missing %q:\n%s", want, show)
		}
	}

	dec, err := exec.Command("sigrok-cli", "-i", sr,
		"-P", "uart:rx=D1:baudrate=62500", "-A", "uart=rx-data").CombinedOutput()
	if err != nil {
		t.Fatalf("sigrok-cli uart decode failed: %v\n%s", err, dec)
	}
	var got []string
	for _, line := range strings.Split(string(dec), "\n") {
		if i := strings.LastIndex(line, ": "); i >= 0 && strings.Contains(line, "uart-") {
			got = append(got, strings.TrimSpace(line[i+2:]))
		}
	}
	if want := "48,69,21"; strings.Join(got, ",") != want {
		t.Fatalf("sigrok decoded %v from the exported logic channel, want [%s]", got, want)
	}
}
