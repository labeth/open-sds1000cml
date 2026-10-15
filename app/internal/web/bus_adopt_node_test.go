// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"os/exec"
	"testing"

	"open-sds/app/internal/testenv"
)

// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
func TestPageFollowsTheScopesDecodeJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "bus_adopt.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("the page follows the scope's decode: %v\n%s", err, out)
	}
}

// TRLC-LINKS: REQ-SDS-207, REQ-SDS-222
func TestBitOrderIsSPIsJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "bit_order.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("bit order on the page: %v\n%s", err, out)
	}
}

// TRLC-LINKS: REQ-SDS-214
func TestBusListFollowsTheSettingsJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "bus_list_key.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("the BUS list and the decoder settings: %v\n%s", err, out)
	}
}
