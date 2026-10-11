// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
// ejparity.cjs - replays the eye/jitter parity vectors through the web engine
// (internal/web/eyejitter.js) for TestEyeJitterJSParity in eyejitter_node_test.go.
// Usage: node ejparity.cjs <vectors.json> <web dir>. Prints one JSON result per
// vector: the per-record ejFeed answers, the ejResult figures and a digest of
// the eye density map.
"use strict";
const fs = require("node:fs");
const path = require("node:path");

const [vecPath, webDir] = process.argv.slice(2);
if (!vecPath || !webDir) {
  console.error("usage: node ejparity.cjs <vectors.json> <web dir>");
  process.exit(2);
}
const EJ = require(path.resolve(webDir, "eyejitter.js"));
const vecs = JSON.parse(fs.readFileSync(vecPath, "utf8"));

// TRLC-LINKS: REQ-SDS-215
const num = (v) => (v === undefined || !Number.isFinite(v) ? null : v);

// TRLC-LINKS: REQ-SDS-215
function run(v) {
  const st = EJ.ejNew(v.opts || {});
  const status = v.records.map((r) => EJ.ejFeed(st, Float64Array.from(r), r.length, v.sampleS));
  const r = EJ.ejResult(st);
  let sum = 0, weighted = 0, cells = 0;
  for (let i = 0; i < st.eye.length; i++) {
    if (st.eye[i] !== 0) { sum += st.eye[i]; weighted += st.eye[i] * (i + 1); cells++; }
  }
  const em = r.eyeMetrics;
  return {
    name: v.name, status,
    records: r.records, rejected: r.rejected, edges: r.edges, lastErr: r.lastErr,
    ui: num(r.ui), uiSeconds: num(r.uiSeconds), bitRate: num(r.bitRate), tieHpHz: num(r.tieHpHz),
    tieRms: num(r.tieRms), tiePp: num(r.tiePp), rj: num(r.rj), dj: num(r.dj),
    periodJRms: num(r.periodJRms), c2cJRms: num(r.c2cJRms), tieWindow: num(r.tieWindow),
    spectrum: r.spectrum ? Array.from(r.spectrum) : null, specDf: num(r.specDf),
    specPeakHz: num(r.specPeakHz), specPeakAmp: num(r.specPeakAmp),
    eye: em ? { height: em.eyeHeightCodes, width: em.eyeWidthUI, cross: em.crossTotal } : null,
    digest: { sum, weighted, cells },
  };
}

process.stdout.write(JSON.stringify(vecs.map(run)));
