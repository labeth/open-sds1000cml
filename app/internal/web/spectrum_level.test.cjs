// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// A peak's level in dBV comes from the transform's points, also when a
// decimated capture's band keeps fewer bins: a 40-code tone at 0.04 V a code
// is 1.13 V rms (1.07 dBV) - it read 11.4 dB high when the band kept 1103 of
// 4096 bins.
"use strict";
const assert = require('node:assert/strict'), fs = require('node:fs'), vm = require('node:vm');
const peaks = require('./peaks.js');
const src = fs.readFileSync('app_fft.js', 'utf8');
// TRLC-LINKS: REQ-SDS-216
const pick = (name) => { const i = src.indexOf('function ' + name + '('); return src.slice(i, src.indexOf('\n}\n', i) + 3); };
// TRLC-LINKS: REQ-SDS-216
const c = Object.assign({ specMemo: {}, frame: { vpc1: 0.04, vpc2: 0.04 }, peakSpanS: () => 0 }, peaks,
  // TRLC-LINKS: REQ-SDS-216
  { peakLvl: (p) => Number.isFinite(p.lvl) ? p.lvl : p.db });
vm.createContext(c);
vm.runInContext(pick('peakDBV') + pick('peakVolts'), c);
const dt = 128e-9, n = 15753, f = 500e3, g = new Float64Array(n);
for (let i = 0; i < n; i++) g[i] = 128 + 40 * Math.sin(2 * Math.PI * f * i * dt);
for (const band of [0, 1.05e6]) { // the whole Nyquist, and a capture filter's band
  const s = peaks.fftReduce(g, 0.5 / dt, band, 8192);
  c.specMemo[1] = { spec: s, src: null };
  const p = peaks.detectPeaks(s, { maxPeaks: 4 }).sort((a, b) => b.db - a.db)[0];
  assert.ok(p && Math.abs(p.freq - f) < s.binHz, 'the tone found');
  const dbv = c.peakDBV(1, p), want = 20 * Math.log10(40 * 0.04 / Math.SQRT2);
  assert.ok(Math.abs(dbv - want) < 0.1, `band ${band}: ${dbv.toFixed(2)} dBV, want ${want.toFixed(2)} (${s.half} of ${s.points / 2} bins)`);
  assert.ok(Math.abs(c.peakVolts(1, p) - 1.6) < 0.25, `band ${band}: ${c.peakVolts(1, p).toFixed(3)} V peak, want about 1.6`);
}
console.log('ALL PASS');
