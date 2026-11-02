// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package web

import (
	"os/exec"
	"testing"

	"github.com/labeth/open-sds1000cml/app/internal/testenv"
)

// TRLC-LINKS: REQ-SDS-203
func TestDecodeEnvelopeJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "decode_envelope.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out)
	}
}
