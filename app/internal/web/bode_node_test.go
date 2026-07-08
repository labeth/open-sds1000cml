package web

import (
	"os/exec"
	"strings"
	"testing"
)

// Runs the bode.js pure helpers under node (log ticks, nice range, hz format)
// to lock the web renderer's math. Self-skips without node.
func TestBodeJSHelpers(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	script := `
const b = require("./bode.js");
let fail = 0;
const ok = (c, m) => { if (!c) { console.log("FAIL " + m); fail++; } };
// nice range brackets the data on round steps
let [lo, hi] = b.bodeNiceRange([-6, -6.1, -5.9], -20, 20, 10);
ok(lo <= -6 && hi >= -6 && lo % 10 === 0, "nice range brackets -6 on 10s: " + lo + ".." + hi);
// log ticks include the decade majors within range
const t = b.bodeLogTicks(1e6, 1e7);
ok(t.some(x => x.f === 1e6 && x.major) && t.some(x => x.f === 1e7 && x.major), "decade majors present");
ok(t.some(x => x.f === 2e6 && !x.major) && t.some(x => x.f === 5e6 && !x.major), "1-2-5 minors present");
// hz formatting
ok(b.bodeFmtHz(2.5e6) === "2.5M", "2.5M: " + b.bodeFmtHz(2.5e6));
ok(b.bodeFmtHz(1e3) === "1k", "1k: " + b.bodeFmtHz(1e3));
console.log(fail ? fail + " FAILED" : "ALL PASS");
process.exit(fail ? 1 : 0);
`
	cmd := exec.Command("node", "-e", script)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	t.Logf("bode.js helpers:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("bode.js helper tests failed: %v", err)
	}
}
