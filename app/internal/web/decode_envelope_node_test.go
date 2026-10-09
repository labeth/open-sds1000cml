// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-203
func TestDecodeEnvelopeJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "decode_envelope.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out)
	}
}
