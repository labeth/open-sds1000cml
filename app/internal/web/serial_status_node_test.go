// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-203
func TestSerialStatusJS(t *testing.T) {
	testenv.NeedNode(t)
	// The race checks run after the first ALL PASS; a promise left pending
	// ends node with no error and no PASS line, so the line is required.
	out, err := exec.Command("node", "serial_status.test.cjs").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "serial mode race PASS") {
		t.Fatalf("serial status: %v\n%s", err, out)
	}
}

// TestSerialFollowJS: with a serial trigger this page armed, a clock rate or
// match set on the scope is followed and never sent back; an edit made on
// the page reaches the scope once.
// TRLC-LINKS: REQ-SDS-206
func TestSerialFollowJS(t *testing.T) {
	testenv.NeedNode(t)
	out, err := exec.Command("node", "serial_follow.test.cjs").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "serial follow PASS") {
		t.Fatalf("serial follow: %v\n%s", err, out)
	}
}

// TestRefsFollowJS: after the page hides a reference itself, the scope
// showing it again is followed, whatever reads were on their way.
// TRLC-LINKS: REQ-SDS-227
func TestRefsFollowJS(t *testing.T) {
	testenv.NeedNode(t)
	out, err := exec.Command("node", "refs_follow.test.cjs").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "refs follow PASS") {
		t.Fatalf("refs follow: %v\n%s", err, out)
	}
}
