// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-019, REQ-SDS-067
func TestAnalyzerRawTransitionJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "analyzer_raw.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("analyzer transition: %v\n%s", err, out)
	}
}
