// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"strings"
	"testing"
)

// TestDecodedStreamConfigJS: Stream + trigger runs on the decoder's auto
// settings (its threshold, the measured UART rate and SPI clock).
// TRLC-LINKS: REQ-SDS-206
func TestDecodedStreamConfigJS(t *testing.T) {
	testenv.NeedNode(t)
	out, err := exec.Command("node", "decoded_stream_config.test.cjs").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("decoded stream config: %v\n%s", err, out)
	}
}
