// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/labeth/open-dso-core/testenv"
)

// TRLC-LINKS: REQ-SDS-203
func TestDecodeEnvelopeJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "decode_envelope.test.cjs", filepath.Join(pkgDir, "..", "panel", "framecalc", "framecalc.go")).CombinedOutput(); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out)
	}
}
