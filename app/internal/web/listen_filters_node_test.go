// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package web

import (
	"os/exec"
	"testing"

	"github.com/labeth/open-dso-core/testenv"
)

// The Listen chain's filters (listen_filters.test.cjs).
// TRLC-LINKS: REQ-SDS-265
func TestListenFiltersJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.CommandContext(t.Context(), "node", "listen_filters.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("listen filters: %v\n%s", err, out)
	}
}
