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

// Adversarial breaker: bodeDraw and the range/tick helpers must survive any
// point set — empty, single, NaN/Inf gains, non-positive/unsorted frequencies,
// a huge point count — without throwing or feeding a NaN coordinate into a
// canvas draw call. A mock 2D context records every coordinate it is handed.
func TestBodeJSBreaker(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	script := `
const b = require("./bode.js");
let fail = 0;
const ok = (c, m) => { if (!c) { console.log("FAIL " + m); fail++; } };

// mock 2D context: records NaN coordinates handed to geometry/text ops
function mockCtx() {
  let nanCoord = 0;
  const chk = (...xs) => { for (const v of xs) if (typeof v === "number" && !Number.isFinite(v)) nanCoord++; };
  return {
    _nan: () => nanCoord,
    clearRect: (...a) => chk(...a), fillRect: (...a) => chk(...a),
    beginPath(){}, closePath(){}, stroke(){}, fill(){},
    moveTo: (x,y) => chk(x,y), lineTo: (x,y) => chk(x,y),
    arc: (x,y,r) => chk(x,y,r),
    fillText: (s,x,y) => chk(x,y),
    set fillStyle(v){}, set strokeStyle(v){}, set lineWidth(v){},
    set font(v){}, set textAlign(v){}, set textBaseline(v){},
  };
}
const mk = (freq, gain, phase) => ({ freq, gain_db: gain, phase_deg: phase });
const cases = {
  empty:       mk([], [], []),
  single:      mk([1e6], [0], [0]),
  two:         mk([1e5, 1e6], [0, -6], [0, -45]),
  nanGain:     mk([1e5, 1e6], [NaN, -6], [0, -45]),
  infGain:     mk([1e5, 1e6], [Infinity, -6], [0, -45]),
  nanPhase:    mk([1e5, 1e6], [0, -6], [NaN, -45]),
  zeroFreq:    mk([0, 1e6], [0, -6], [0, -45]),
  negFreq:     mk([-1e5, 1e6], [0, -6], [0, -45]),
  sameFreq:    mk([1e6, 1e6, 1e6], [0, -6, -12], [0, -45, -90]),
  unsorted:    mk([1e6, 1e5, 5e5], [0, -6, -3], [0, -45, -20]),
  allZeroGain: mk([1e5, 1e6], [0, 0], [0, 0]),
  huge:        (() => { const f=[],g=[],p=[]; for(let i=0;i<5000;i++){f.push(1e3*Math.pow(10,i/1000));g.push(-i*0.01);p.push(-i*0.02);} return mk(f,g,p); })(),
};
for (const [name, pts] of Object.entries(cases)) {
  const g = mockCtx();
  try { b.bodeDraw(g, 460, 300, pts, {}); }
  catch (e) { ok(false, "bodeDraw("+name+") threw: " + e.message); continue; }
  // empty/single/two + clean cases must not push NaN coordinates to the canvas
  if (["empty","single","two","sameFreq","unsorted","allZeroGain","huge"].includes(name))
    ok(g._nan() === 0, "bodeDraw("+name+") emitted "+g._nan()+" NaN coords");
}
// helper robustness on hostile inputs
for (const vals of [[], [NaN], [Infinity, -Infinity], [NaN, 1, 2], [1e300, -1e300]]) {
  try { const [lo, hi] = b.bodeNiceRange(vals, -20, 20, 10); ok(true, "range ok"); }
  catch (e) { ok(false, "bodeNiceRange threw: " + e.message); }
}
for (const [f0, f1] of [[0, 1e6], [-1, 1e6], [1e6, 1e6], [1e6, 1e5], [NaN, 1e6], [1, 1e12]]) {
  try { const t = b.bodeLogTicks(f0, f1); ok(Array.isArray(t), "ticks array"); }
  catch (e) { ok(false, "bodeLogTicks("+f0+","+f1+") threw: " + e.message); }
}
for (const f of [0, -1, NaN, Infinity, 1e-9, 999, 1e6, 2.5e6]) {
  try { const s = b.bodeFmtHz(f); ok(typeof s === "string", "fmt string for " + f); }
  catch (e) { ok(false, "bodeFmtHz("+f+") threw: " + e.message); }
}
console.log(fail ? fail + " FAILED" : "ALL PASS");
process.exit(fail ? 1 : 0);
`
	cmd := exec.Command("node", "-e", script)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	t.Logf("bode.js breaker:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("bode.js breaker failed: %v", err)
	}
}
