// Package testenv gates tests that need an external toolchain (node,
// Playwright + Chromium). Normally a missing environment skips the test so
// `go test ./...` stays green on any host; on the CI browser lane
// (CI_REQUIRE_BROWSER=1) the same condition is a hard FAILURE, so the
// browser/node/parity suites can never be silently skipped where they are
// the whole point of the job.
package testenv

import (
	"os"
	"os/exec"
	"testing"
)

// required reports whether environment skips must be treated as failures
// (set CI_REQUIRE_BROWSER=1 on the lane that installs node + Chromium).
func required() bool { return os.Getenv("CI_REQUIRE_BROWSER") == "1" }

// NeedNode skips t when node is not on PATH — or fails it under
// CI_REQUIRE_BROWSER=1.
func NeedNode(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("node"); err == nil {
		return
	}
	if required() {
		t.Fatal("CI_REQUIRE_BROWSER=1 but node is not on PATH — this lane must run the node/browser tests, not skip them")
	}
	t.Skip("node not installed")
}

// SkipBrowser records a browser-environment skip (Playwright/Chromium not
// installed, or the browser failed to launch) — or fails the test under
// CI_REQUIRE_BROWSER=1.
func SkipBrowser(t testing.TB, format string, args ...any) {
	t.Helper()
	if required() {
		t.Fatalf("CI_REQUIRE_BROWSER=1 but the browser is unavailable: "+format, args...)
	}
	t.Skipf(format, args...)
}
