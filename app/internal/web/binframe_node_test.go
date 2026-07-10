package web

import (
	"os/exec"
	"strings"
	"testing"

	"open-sds/app/internal/testenv"
)

// TestBinframeJS runs the binary-transport decoder (binframe.js) under node
// against golden wire fixtures — the client half of the /api/frame.bin
// parity story (the server half is TestBinFrameParity). Skips if node is
// absent so `go test ./...` stays green everywhere.
func TestBinframeJS(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")
	out, err := exec.Command(node, "binframe.test.cjs").CombinedOutput()
	t.Logf("binframe.test.cjs output:\n%s", out)
	if err != nil {
		t.Fatalf("binframe.js test failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("binframe.js test did not report ALL PASS")
	}
}
