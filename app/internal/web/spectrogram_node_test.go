package web

import (
	"os/exec"
	"strings"
	"testing"
)

// Locks the JS spectrogram helpers under node: the colormap endpoints/segments
// match the Go heat() (parity), the ramp is monotone in brightness, and a
// pushed tone row lights the correct frequency column.
func TestSpectrogramJS(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	script := `
const s = require("./spectrogram.js");
let fail = 0;
const ok = (c, m) => { if (!c) { console.log("FAIL " + m); fail++; } };
// endpoints
ok(JSON.stringify(s.sgHeat(0)) === "[0,0,0]", "heat(0) black");
ok(JSON.stringify(s.sgHeat(1)) === "[255,255,255]", "heat(1) white");
// monotone in a rough luminance
const lum = ([r,g,b]) => 2*r + 4*g + b;
let prev = -1, mono = true;
for (let i = 0; i <= 20; i++) { const l = lum(s.sgHeat(i/20)); if (l < prev - 6) mono = false; prev = l; }
ok(mono, "heat monotone in brightness");
// a tone row lights the right column: synth mags with a single peak
const sg = s.sgNew(200, 40);
const half = 512, mags = new Float64Array(half);
const peakBin = 128; // 25% of Nyquist
for (let k = 0; k < half; k++) mags[k] = (k === peakBin) ? 1000 : 1;
s.sgPushRow(sg, mags, half, 1000, 1e8);
// brightest column in the top row (row 0)
let bx = 0, bl = -1;
for (let x = 0; x < sg.w; x++) { const o = x*4; const l = lum([sg.data[o],sg.data[o+1],sg.data[o+2]]); if (l > bl) { bl = l; bx = x; } }
const expCol = Math.floor(peakBin / half * sg.w);
ok(Math.abs(bx - expCol) <= 1, "tone lights the right column: got " + bx + " want " + expCol);
console.log(fail ? fail + " FAILED" : "ALL PASS");
process.exit(fail ? 1 : 0);
`
	cmd := exec.Command("node", "-e", script)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	t.Logf("spectrogram.js:\n%s", out)
	if err != nil || !strings.Contains(string(out), "ALL PASS") {
		t.Fatalf("spectrogram.js tests failed: %v", err)
	}
}
