// Package testenv gates tests that need an external toolchain (node,
// Playwright + Chromium). Normally a missing environment skips the test so
// `go test ./...` stays green on any host; on the CI browser lane
// (CI_REQUIRE_BROWSER=1) the same condition is a hard FAILURE, so the
// browser/node/parity suites can never be silently skipped where they are
// the whole point of the job.
// ENGMODEL-OWNER-UNIT: FU-APP-TESTENV
package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// required reports whether environment skips must be treated as failures
// (set CI_REQUIRE_BROWSER=1 on the lane that installs node + Chromium).
// TRLC-LINKS: REQ-SDS-179
func required() bool { return os.Getenv("CI_REQUIRE_BROWSER") == "1" }

// NeedNode skips t when node is not on PATH — or fails it under
// CI_REQUIRE_BROWSER=1.
// TRLC-LINKS: REQ-SDS-179
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
// TRLC-LINKS: REQ-SDS-179
func SkipBrowser(t testing.TB, format string, args ...any) {
	t.Helper()
	if required() {
		t.Fatalf("CI_REQUIRE_BROWSER=1 but the browser is unavailable: "+format, args...)
	}
	t.Skipf(format, args...)
}

// RTLDir returns a directory holding every FPGA source file flat, with the
// testbenches under sim/, as symlinks into fpga/ (common, trigger, images/*).
// RTL tests name files the way they sit in an image project ("top.v",
// "sim/tb_x.v") whichever group of the source tree holds them.
// TRLC-LINKS: REQ-SDS-179
func RTLDir(tb testing.TB) string {
	tb.Helper()
	fpga := ""
	for d, _ := filepath.Abs("."); ; d = filepath.Dir(d) {
		if st, err := os.Stat(filepath.Join(d, "fpga", "common")); err == nil && st.IsDir() {
			fpga = filepath.Join(d, "fpga")
			break
		}
		if filepath.Dir(d) == d {
			tb.Fatal("testenv: fpga/common not found above the working directory")
		}
	}
	dir := tb.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sim"), 0o755); err != nil {
		tb.Fatal(err)
	}
	groups, _ := filepath.Glob(filepath.Join(fpga, "images", "*"))
	groups = append([]string{filepath.Join(fpga, "common"), filepath.Join(fpga, "trigger")}, groups...)
	for _, g := range groups {
		for _, sub := range []string{"", "sim"} {
			files, _ := filepath.Glob(filepath.Join(g, sub, "*.v*"))
			for _, f := range files {
				if err := os.Symlink(f, filepath.Join(dir, sub, filepath.Base(f))); err != nil {
					tb.Fatal(err)
				}
			}
		}
	}
	return dir
}
