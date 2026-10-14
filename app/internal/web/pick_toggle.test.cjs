// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-070
// A click on a spectrum peak picks it, or unpicks the pick that holds it; a
// pick shown lost (its tone gone) near that peak is not what the click meant.
"use strict";
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const src = fs.readFileSync('app_fft.js', 'utf8');
const code = src.slice(src.indexOf('// TRLC-LINKS: REQ-SDS-070\nfunction togglePeakCh'), src.indexOf('// TRLC-LINKS: REQ-SDS-070\nfunction clearPeaksCh'));
const { nearestPeak, trackPicks } = require('./peaks.js');
// TRLC-LINKS: REQ-SDS-070
const run = (sel, clickHz) => {
  const peaks = [{ freq: 1000 }, { freq: 5000 }];
  const S = { peaks, sel, lostSince: new Map() };
  const t = trackPicks(S.sel, S.peaks, 10, S.lostSince, 0);
  S.sel = t.sel; S.selIdx = t.idxs; S.lostSince = t.lostSince;
  // TRLC-LINKS: REQ-SDS-070
  const c = { fftCh: { 1: S }, nearestPeak, peakListLastT: 1, redraw() {} };
  vm.createContext(c); vm.runInContext(code, c);
  c.togglePeakCh(1, clickHz);
  return S.sel.slice().sort((a, b) => a - b);
};
assert.deepEqual(run([1000], 1000), [], 'a click on a picked peak unpicks it');
assert.deepEqual(run([], 1000), [1000], 'a click on a peak picks it');
assert.deepEqual(run([2000], 1000), [1000, 2000], 'a click on the peak next to a lost pick picks it (the lost pick stays until it lapses)');
assert.deepEqual(run([1000, 2000], 1000), [2000], 'unpicking a peak leaves the lost pick');
console.log('ALL PASS');
