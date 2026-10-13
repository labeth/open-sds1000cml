// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"os/exec"
	"reflect"
	"testing"

	"open-sds/app/internal/testenv"
)

// A roll view's held samples reach the page as ranges, which it draws dimmed.
// TRLC-LINKS: REQ-SDS-071
func TestHeldRanges(t *testing.T) {
	held := []bool{false, true, true, false, false, true, true, true}
	if got := heldRanges(held, 8); !reflect.DeepEqual(got, [][2]int{{1, 3}, {5, 8}}) {
		t.Fatalf("ranges %v", got)
	}
	if got := heldRanges(held, 6); !reflect.DeepEqual(got, [][2]int{{1, 3}, {5, 6}}) {
		t.Fatalf("ranges of the first 6 %v", got)
	}
	if got := heldRanges(nil, 8); got != nil {
		t.Fatalf("no mask: %v", got)
	}
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "held_split.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("held split: %v\n%s", err, out)
	}
}
