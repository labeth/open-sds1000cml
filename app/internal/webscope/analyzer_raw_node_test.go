// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"os/exec"
	"testing"

	"github.com/labeth/open-dso-core/testenv"
)

// TRLC-LINKS: REQ-SDS-019, REQ-SDS-067
func TestAnalyzerRawTransitionJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "analyzer_raw.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("analyzer transition: %v\n%s", err, out)
	}
}
