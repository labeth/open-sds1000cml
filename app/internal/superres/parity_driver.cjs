// Parity driver: run the reference-locked super-res path in JS (../web/superres.js)
// on the SAME frames the Go package sees, so the golden-vector test can assert the
// two engines converge to the same stack. Reads a JSON payload file (argv[2]),
// prints the JS result as JSON. Run by superres_parity_test.go.
"use strict";
const fs = require("fs");
const SR = require("../web/superres.js");
const inp = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const { N, K, align, frames } = inp;
const st = SR.srNew(N, K);
st.align = align;
st.c[0].vpc = st.c[1].vpc = 1 / 32;
const arr = a => Int16Array.from(a);
const f0 = frames[0];
const seedOk = SR.srSeedRef(st, arr(f0.c1), arr(f0.c2), f0.edgeX);
const disp = [], hitsAfter = [];
for (let i = 1; i < frames.length; i++) {
  const f = frames[i];
  disp.push(SR.srFeed(st, arr(f.c1), arr(f.c2), { edgeX: f.edgeX }));
  hitsAfter.push(st.hits);
}
const res = SR.srResult(st, { stride: 1 });
let meanSum = 0, meanCount = 0;
for (let b = 0; b < res.mean.length; b++) if (res.mean[b] !== -1) { meanSum += res.mean[b]; meanCount++; }
process.stdout.write(JSON.stringify({
  seedOk, disp, hitsAfter, gridL: st.gridL, gateLo: st.gateLo, gateHi: st.gateHi,
  frames: st.frames, hits: st.hits, rejected: st.rejected,
  bitsGained: res.bitsGained, sigmaSingle: res.sigmaSingle, sigmaStack: res.sigmaStack,
  meanSum, meanCount,
}));
