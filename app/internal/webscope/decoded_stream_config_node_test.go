// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/labeth/open-dso-core/testenv"
)

// TestDecodedStreamConfigJS: Stream + trigger runs on the decoder's auto
// settings (its threshold, the measured UART rate and SPI clock).
// TRLC-LINKS: REQ-SDS-206
func TestDecodedStreamConfigJS(t *testing.T) {
	testenv.NeedNode(t)
	out, err := exec.CommandContext(t.Context(), "node", "decoded_stream_config.test.cjs").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("decoded stream config: %v\n%s", err, out)
	}
}
