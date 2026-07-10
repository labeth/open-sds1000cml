// comp_parity_driver.cjs — evaluate the falloff-compensation reference
// implementation (../web/superres_comp.js) on the payload the Go test builds
// (frequency-grid curves, auto-sizing cases, full srCompensate cases) and
// print the results as JSON. Run by comp_jsparity_test.go.
"use strict";
const fs = require("fs");
const C = require("../web/superres_comp.js");
const inp = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const out = { curves: [], autos: [], comps: [] };

// mkOpts mirrors the JS call sites: a partial opts object completed from
// SRCOMP_DEFAULT (srCompGain itself is always handed a completed object).
const mkOpts = (o) => Object.assign({}, C.SRCOMP_DEFAULT, o || {});

for (const c of inp.curves || []) {
  const o = mkOpts(c.opts);
  const gain = [], cal = [], target = [];
  for (const f of inp.freqs) {
    gain.push(C.srCompGain(f, o));
    cal.push(C.srCompCalH(f));
    target.push(C.srCompTargetH(f, o.fbw, o.order));
  }
  const info = C.srCompInfo(c.opts || {});
  out.curves.push({ gain, cal, target, peakBoostDb: info.peakBoostDb, recoveredF3: info.recoveredF3 });
}

for (const a of inp.autos || []) {
  const o = C.srCompAuto(a.bitsGained, a.rawNyqHz, a.spend);
  const info = C.srCompInfo(o);
  out.autos.push({
    fbw: o.fbw, eps: o.eps, gmax: o.gmax, budgetDb: o.budgetDb,
    peakBoostDb: info.peakBoostDb, recoveredF3: info.recoveredF3,
  });
}

for (const cc of inp.comps || []) {
  const mean = Float32Array.from(cc.mean);
  const opts = cc.auto ? C.srCompAuto(cc.auto.bitsGained, cc.auto.rawNyqHz, cc.auto.spend) : (cc.opts || {});
  const r = C.srCompensate(mean, cc.dtFine, opts);
  out.comps.push({ comp: Array.from(r.comp) });
}

process.stdout.write(JSON.stringify(out));
