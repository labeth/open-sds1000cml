// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-207
// Detect takes a decimated record first (lease "samples", given back at once)
// and keeps the bus found there when its shortest feature spans
// DETECT_DECIMATED_FEATURE of its samples; else one full-rate record, as the
// scope's BUS search does (panel detectOn).
"use strict";
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const src = fs.readFileSync('app_decode.js', 'utf8');
const code = src.slice(src.indexOf('// DETECT_DECIMATED_FEATURE is'), src.indexOf('// runAutodetect:'));
const goLimit = +(/const decimatedFeature = ([0-9.]+)/.exec(fs.readFileSync('../panel/ws_bus.go', 'utf8'))[1]);
// TRLC-LINKS: REQ-SDS-207
async function run(decFS, decFrame) {
  const calls = [], fetched = [];
  const dec = decFrame === undefined ? { name: 'dec', res: { proto: 'uart', result: { fs: decFS } } } : decFrame;
  const raw = { name: 'raw', res: { proto: 'uart', result: { fs: 4340 } } };
  const c = { dcfg: { fmt: 'hex' },
    // TRLC-LINKS: REQ-SDS-207
    autodetect: (f) => f.res,
    // TRLC-LINKS: REQ-SDS-207
    decodeFeatureSamples: (r) => r.fs,
    // TRLC-LINKS: REQ-SDS-207
    fetch: (u) => { fetched.push(u); return Promise.resolve(); },
    // TRLC-LINKS: REQ-SDS-207
    fetchSampledFrame: async (wait, accept, lease) => { calls.push(lease || 'raw'); return (lease || 'raw') === 'samples' ? dec : raw; } };
  vm.createContext(c); vm.runInContext(code, c);
  const got = await c.detectSampled(null, 4000);
  return { got: got && got.src.name, calls: calls.join(','), ended: fetched.some((u) => /lease=samples&end=1/.test(u)),
    rawEnded: fetched.some((u) => /lease=raw&end=1/.test(u)) };
}
(async () => {
  const jsLimit = +(/const DETECT_DECIMATED_FEATURE = ([0-9.]+)/.exec(src)[1]);
  assert.equal(jsLimit, goLimit, 'the web and the scope stand on the same resolution');
  assert.deepEqual(await run(35), { got: 'dec', calls: 'samples', ended: true, rawEnded: false }, 'a UART at 35 samples a bit stands on the decimated record');
  assert.deepEqual(await run(4.4), { got: 'raw', calls: 'samples,raw', ended: true, rawEnded: true }, 'a bus too fast for it: the full rate');
  assert.deepEqual(await run(0, null), { got: 'raw', calls: 'samples,raw', ended: true, rawEnded: true }, 'no decimated record: the full rate');
  console.log('ALL PASS');
})().catch((e) => { console.error(e); process.exit(1); });
