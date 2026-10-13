// spectrumparity.cjs: peaks.js fftReduce on the records of vectors.json, for
// TestSpectrumJSParity. Usage: node spectrumparity.cjs vectors.json webdir
"use strict";
const fs = require("fs"), path = require("path");
const P = require(path.resolve(process.argv[3], "peaks.js"));
const vs = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const out = vs.map((v) => {
  const s = P.fftReduce(Float64Array.from(v.samples), v.nyq, v.band, v.maxN);
  return s ? { name: v.name, half: s.half, nyq: s.nyq, fs: s.fs, points: s.points, peak: s.peak, mags: Array.from(s.mags) } : { name: v.name, half: 0 };
});
process.stdout.write(JSON.stringify(out));
