package web

import (
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// TestDecodeJS runs the protocol-decoder logic (decode.js) under node against
// synthetic UART/I2C/SPI waveforms — the closest thing to an end-to-end test of
// the browser decode code without a headless browser. Skips if node is absent
// (a hard failure under CI_REQUIRE_BROWSER=1).
func TestDecodeJS(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")
	out, err := exec.Command(node, "decode.test.cjs").CombinedOutput()
	t.Logf("decode.test.cjs output:\n%s", out)
	if err != nil {
		t.Fatalf("decode.js e2e test failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("decode.js e2e test did not report ALL PASS")
	}
}
