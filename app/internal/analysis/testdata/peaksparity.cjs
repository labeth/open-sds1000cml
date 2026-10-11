// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
// peaksparity.cjs - runs the web's FFT peak search (internal/web/peaks.js:
// spectrum + detectPeaks) on the vectors of TestPeaksJSParity in
// peaks_node_test.go. Usage: node peaksparity.cjs <vectors.json> <web dir>.
// Prints, per vector, the spectrum length and every peak's bin, frequency and
// level.
"use strict";
const fs = require("node:fs");
const path = require("node:path");

const [vecPath, webDir] = process.argv.slice(2);
if (!vecPath || !webDir) {
  console.error("usage: node peaksparity.cjs <vectors.json> <web dir>");
  process.exit(2);
}
const P = require(path.resolve(webDir, "peaks.js"));
const vecs = JSON.parse(fs.readFileSync(vecPath, "utf8"));

// TRLC-LINKS: REQ-SDS-216
function run(v) {
  const sp = P.spectrum(v.samples, v.nyq);
  const peaks = P.detectPeaks(sp, { floorDb: -50, maxPeaks: v.max });
  return { name: v.name, half: sp ? sp.half : 0, peaks: peaks.map((p) => ({ k: p.k, freq: p.freq, db: p.db })) };
}

process.stdout.write(JSON.stringify(vecs.map(run)));
