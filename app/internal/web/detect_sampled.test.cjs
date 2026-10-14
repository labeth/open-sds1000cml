// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-207
// Detect takes a decimated record first (lease "samples", given back at once)
// and keeps the bus found there when its shortest feature spans
// DETECT_DECIMATED_FEATURE of its samples; else one full-rate record, as the
// scope's BUS search does (panel detectOn) - none when the sampled record was
// at the full rate already; CRC-checked buses stand on fewer samples a bit,
// an odd-rate UART on none. At the slowest full-rate timebase a fast bus is
// looked for on a short full-rate record before the whole one (panel findBus).
"use strict";
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const src = fs.readFileSync('app_decode.js', 'utf8');
const code = src.slice(src.indexOf('// DETECT_DECIMATED_FEATURE is'), src.indexOf('// runAutodetect:'));
const goSrc = fs.readFileSync('../panel/ws_bus.go', 'utf8');
// TRLC-LINKS: REQ-SDS-207
const goNum = (name) => +(new RegExp(name + '\\s*= ([0-9.]+)').exec(goSrc)[1]);
const goLimit = goNum('decimatedFeature'), goChecked = goNum('decimatedCheckedFeature');
const goWindow = +(/const windowDivs = ([0-9.]+)/.exec(goSrc)[1]);
// TRLC-LINKS: REQ-SDS-207
async function run(decFS, decFrame, fullS, rawMaxTdiv, rawSampleS) {
  const calls = [], fetched = [];
  // c1, c2: the edges sliceChannel finds (a run of 4 samples: not resolved)
  const dec = decFrame === undefined ? { name: 'dec', sample_s: 256e-9, c1: [0, 4, 8], res: { proto: 'uart', result: { fs: decFS, meta: { baud: 115200 } } } } : decFrame;
  const raw = { name: 'raw', sample_s: rawSampleS, res: { proto: 'uart', result: { fs: 4340 } } };
  const c = { dcfg: { fmt: 'hex' }, STANDARD_BAUDS: [9600, 115200, 1500000],
    // TRLC-LINKS: REQ-SDS-207
    sliceChannel: (edges) => ({ ok: true, edges: edges.map((i) => ({ i })) }),
    // TRLC-LINKS: REQ-SDS-207
    autodetect: (f) => f.res,
    // TRLC-LINKS: REQ-SDS-207
    decodeFeatureSamples: (r) => r.fs,
    // TRLC-LINKS: REQ-SDS-207
    fetch: (u) => { fetched.push(u); return Promise.resolve(); },
    // TRLC-LINKS: REQ-SDS-207
    fetchSampledFrame: async (wait, accept, lease) => {
      calls.push(lease || 'raw');
      const f = (lease || 'raw') === 'samples' ? dec : raw;
      return f && (!accept || accept(f)) ? f : null;
    } };
  vm.createContext(c); vm.runInContext(code, c);
  const got = await c.detectSampled(null, 4000, fullS, rawMaxTdiv);
  return { got: got && got.src.name, calls: calls.join(','), ended: fetched.some((u) => /lease=samples&end=1/.test(u)),
    rawEnded: fetched.some((u) => /lease=raw&end=1/.test(u)) };
}
(async () => {
  const jsLimit = /const DETECT_DECIMATED_FEATURE = ([0-9.]+), DETECT_CHECKED_FEATURE = ([0-9.]+)/.exec(src);
  assert.deepEqual([+jsLimit[1], +jsLimit[2]], [goLimit, goChecked], 'the web and the scope stand on the same resolution');
  assert.equal(+/const DETECT_SCANT_BYTES = ([0-9]+)/.exec(src)[1], goNum('scantBytes'), 'the web and the scope look on past the same scant UART');
  assert.deepEqual(await run(35), { got: 'dec', calls: 'samples', ended: true, rawEnded: false }, 'a UART at 35 samples a bit stands on the decimated record');
  assert.deepEqual(await run(4.4), { got: 'raw', calls: 'samples,raw', ended: true, rawEnded: true }, 'a bus too fast for it: the full rate');
  assert.deepEqual(await run(0, null), { got: 'raw', calls: 'samples,raw', ended: true, rawEnded: true }, 'no decimated record: the full rate');
  assert.deepEqual(await run(1.2, { name: 'short', sample_s: 2e-9, c1: [0, 4, 8], res: { proto: 'off' } }, 2e-9), { got: 'short', calls: 'samples', ended: true, rawEnded: false },
    'a short screen read undecimated: no second, full-rate read of it');
  assert.deepEqual(await run(4.4, undefined, 2e-9), { got: 'raw', calls: 'samples,raw', ended: true, rawEnded: true }, 'a decimated record: the full rate');
  assert.deepEqual(await run(4.4, undefined, 2e-9, 0, 256e-9), { got: null, calls: 'samples,raw', ended: true, rawEnded: true },
    'a decimated record still in flight is no full-rate one');
  assert.deepEqual(await run(0, { name: 'resolved', sample_s: 32e-9, tdiv_s: 10e-6, c1: [0, 300, 600], res: { proto: 'off' } }, 2e-9, 50e-6),
    { got: 'resolved', calls: 'samples', ended: true, rawEnded: false }, 'a record that resolves its every edge (a UART at 10 us/div): no full-rate read of it');
  assert.deepEqual(await run(4.4, { name: 'slow', sample_s: 256e-9, tdiv_s: 200e-6, c1: [0, 4, 8], res: { proto: 'manchester', result: { fs: 4.4 } } }, 2e-9, 50e-6),
    { got: 'slow', calls: 'samples', ended: true, rawEnded: false }, 'a timebase whose full-rate record is long: no full-rate read of it here');

  assert.equal(+/const DETECT_WINDOW_DIVS = ([0-9.]+)/.exec(src)[1], goWindow, 'the web and the scope look on the same short record');
  const ladder = [1e-6, 2e-6, 5e-6, 10e-6, 20e-6, 50e-6, 100e-6, 200e-6, 500e-6];
  const ctx = vm.createContext({ dcfg: { fmt: 'hex' } });
  vm.runInContext(code, ctx);
  assert.equal(ctx.detectWindowTdiv(ladder, 200e-6), 50e-6, 'a quarter of the full-rate timebase');

  // detectAtFullRate: which timebases it sets and which records it reads
  // TRLC-LINKS: REQ-SDS-207
  async function full(found, prev = 2e-6, first = null) {
    const sent = [], leases = [];
    const c = { dcfg: { fmt: 'hex' },
      // TRLC-LINKS: REQ-SDS-207
      sliceChannel: () => ({ ok: false, edges: [] }),
      // TRLC-LINKS: REQ-SDS-207
      autodetect: (f) => f.res,
      // TRLC-LINKS: REQ-SDS-207
      decodeFeatureSamples: (r) => r.fs,
      // TRLC-LINKS: REQ-SDS-207
      fetch: () => Promise.resolve(),
      // TRLC-LINKS: REQ-SDS-207
      setDetectMsg: () => {},
      // TRLC-LINKS: REQ-SDS-207
      fmtTdiv: (t) => String(t),
      // TRLC-LINKS: REQ-SDS-207
      send: async (k, v) => { sent.push(v); },
      // TRLC-LINKS: REQ-SDS-207
      fetchSampledFrame: async (wait, accept, lease) => {
        const t = sent[sent.length - 1], kind = (lease || 'raw') + '@' + t;
        leases.push(kind);
        const res = found[kind] ? { proto: 'manchester', result: { fs: found[kind] } } : { proto: 'off' };
        return { tdiv_s: t, sample_s: lease === 'samples' ? 256e-9 : 2e-9, c1: [], res };
      } };
    vm.createContext(c); vm.runInContext(code, c);
    const r = await c.detectAtFullRate(200e-6, prev, ladder, first, 2e-9);
    return { sent: sent.join(','), leases: leases.join(','), stepped: r && r.stepped, within: r && r.within };
  }
  assert.deepEqual(await full({ 'samples@0.0002': 35 }), { sent: '0.0002', leases: 'samples@0.0002', stepped: 200e-6, within: 0 },
    'a slow bus: the decimated record of the whole full-rate timebase');
  assert.deepEqual(await full({ 'samples@0.0002': 1, 'raw@0.00005': 125 }), { sent: '0.0002,0.00005', leases: 'samples@0.0002,raw@0.00005', stepped: 50e-6, within: 200e-6 },
    'a fast bus: the short full-rate record, not the whole one');
  assert.deepEqual(await full({ 'raw@0.0002': 125 }), { sent: '0.0002,0.00005,0.0002', leases: 'samples@0.0002,raw@0.00005,raw@0.0002', stepped: 200e-6, within: 0 },
    'a sparse bus: the whole full-rate record');
  assert.deepEqual(await full({ 'raw@0.00005': 125 }, 200e-6, { d: { proto: 'off' } }), { sent: '0.00005', leases: 'raw@0.00005', stepped: 50e-6, within: 200e-6 },
    'on the full-rate timebase already: its decimated record is not read again');
  assert.deepEqual(await full({ 'raw@0.0002': 125 }, 200e-6, { d: { proto: 'off' } }), { sent: '0.00005,0.0002', leases: 'raw@0.00005,raw@0.0002', stepped: 0, within: 0 },
    'found on the whole record of the timebase on screen: no step');
  assert.deepEqual(await full({}), { sent: '0.0002,0.00005,0.0002,0.000002', leases: 'samples@0.0002,raw@0.00005,raw@0.0002', stepped: null, within: null },
    'no bus: the timebase goes back');
  // what stands on a decimated record, and a UART that ends no search
  const stands = vm.createContext({ STANDARD_BAUDS: [9600, 28800, 38400, 115200, 500000, 1500000],
    // TRLC-LINKS: REQ-SDS-207
    decodeFeatureSamples: (r) => r.fs });
  vm.runInContext(code, stands);
  assert.equal(stands.detectStandsDecimated({ proto: 'canfd', result: { fs: 7.8 } }), true, 'CAN at 500 kbit/s and 200 us/div: its CRCs stand');
  assert.equal(stands.detectStandsDecimated({ proto: 'canfd', result: { fs: 5 } }), false);
  assert.equal(stands.detectStandsDecimated({ proto: 'uart', result: { fs: 7.8, meta: { baud: 115200 } } }), false, 'no CRC: the full rate');
  assert.equal(stands.detectStandsDecimated({ proto: 'uart', result: { fs: 35, meta: { baud: 115200 } } }), true);
  assert.equal(stands.detectStandsDecimated({ proto: 'uart', result: { fs: 29.7, meta: { baud: 32917 } } }), false, 'an odd rate: an alias of a faster bus');
  assert.equal(stands.detectStandsDecimated({ proto: 'i2c', result: { fs: 9.5 } }), true);
  assert.equal(stands.detectStandsDecimated({ proto: 'off' }), false);
  assert.equal(stands.detectScantUART({ proto: 'uart', result: { bytes: [1, 2, 3], meta: { baud: 1500000 } } }), true, 'three bytes');
  assert.equal(stands.detectScantUART({ proto: 'uart', result: { bytes: [1, 2, 3, 4], meta: { baud: 115200 } } }), false);
  assert.equal(stands.detectScantUART({ proto: 'uart', result: { bytes: new Array(12).fill(0), meta: { baud: 10002156 } } }), true, 'an odd rate');
  assert.equal(stands.detectScantUART({ proto: 'manchester', result: { bytes: [1] } }), false);
  console.log('ALL PASS');
})().catch((e) => { console.error(e); process.exit(1); });
