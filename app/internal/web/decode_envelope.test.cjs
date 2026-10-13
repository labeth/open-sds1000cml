const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const source=fs.readFileSync('app_decode.js','utf8');
const compute=source.slice(source.indexOf('function computeDecode()'),source.indexOf('// watchReason'));
for(const flag of ['peak_detect','is_env']) {
 let leases=0, updates=0;
 const c={dcfg:{proto:'can',result:{text:'stale bytes'}},frame:{c1:[0,255,0,255],[flag]:true},renewDecodeLease(){leases++;},updateDecodeResults(){updates++;}};
 vm.createContext(c);vm.runInContext(compute,c);c.computeDecode();
 assert.equal(c.dcfg.result,null,'envelope must not retain a stale decode');
 assert.equal(leases,1,'request sampled data even while the current frame is an envelope');
 assert.equal(updates,1);
}

// A live envelope whose buckets span the bus's clock is refused: an I2C clock
// aliases there into starts and stops without bytes (bug hunt #3); a fine
// one decodes (framecalc.EnvelopeResolves).
{
  const guarded = source.slice(source.indexOf('// decodeFeatureSamples is'), source.indexOf('// watchReason'));
  const lo = 40, hi = 210, q = 25; // a quarter clock: 25 samples
  const scl = [], sda = [];
  const put = (c, d, n) => { for (let i = 0; i < n * q; i++) { scl.push(c); sda.push(d); } };
  const bit = (b) => { put(lo, b ? hi : lo, 2); put(hi, b ? hi : lo, 2); };
  while (scl.length < 60000) {
    put(hi, hi, 8); put(hi, lo, 2); put(lo, lo, 1);
    for (const v of [0x24 << 1, 0x55, 0xAA]) { for (let k = 7; k >= 0; k--) bit((v >> k) & 1); bit(0); }
    put(lo, lo, 2); put(hi, lo, 2); put(hi, hi, 2);
  }
  const env = (src, bucket) => { const out = []; let last = src[0];
    for (let b = 0; b + bucket <= src.length; b += bucket) {
      let mn = 255, mx = 0; for (let i = b; i < b + bucket; i++) { mn = Math.min(mn, src[i]); mx = Math.max(mx, src[i]); }
      if (Math.abs(last - mx) < Math.abs(last - mn)) { out.push(mx, mn); last = mn; } else { out.push(mn, mx); last = mx; }
    } return out; };
  const decodeAt = (bucket) => {
    const f = { c1: env(scl, bucket), c2: env(sda, bucket), peak_detect: true, ordered: true };
    const el = { value: '', textContent: '' };
    const c = { dcfg: { proto: 'i2c', auto: true, inverted: false, fmt: 'hex', scl: 1, sda: 2 }, frame: f, $: () => el,
      renewDecodeLease() {}, updateDecodeResults() {}, decCodes: (r) => (r === 2 ? f.c2 : f.c1), frameDtS: () => 1e-6 * bucket / 2 };
    vm.createContext(c);
    vm.runInContext(fs.readFileSync('decode.js', 'utf8'), c);
    vm.runInContext(guarded, c); c.computeDecode();
    return c.dcfg.result;
  };
  const fine = decodeAt(4); // two points per 4 samples: 25 points a half clock
  assert.ok(fine.ok && fine.bytes.slice(0, 2).join() === [0x55, 0xAA].join(), 'a fine envelope decodes: ' + JSON.stringify(fine.bytes.slice(0, 4)));
  const coarse = decodeAt(110); // a bucket over a clock period
  assert.ok(!coarse.ok && /I2C not resolved at this timebase/.test(coarse.error), 'a coarse envelope is refused: ' + JSON.stringify({ ok: coarse.ok, error: coarse.error, bytes: coarse.bytes }));
}
// The web's limit is the scope's (framecalc.MinEnvelopeFeature), and BUS steps
// the timebase as the scope's BUS does: a 115200 UART's bit is 3.6 points of
// 500 us/div's live readout (it read 0A as 02 on the bench) - 200 us/div reads
// it; 100 us/div keeps (bug hunt #3).
{
  const goLimit = +(/const MinEnvelopeFeature = ([0-9.]+)/.exec(fs.readFileSync('../panel/framecalc/framecalc.go', 'utf8'))[1]);
  const jsLimit = +(/const DECODE_MIN_ENVELOPE_FEATURE = ([0-9.]+)/.exec(source)[1]);
  assert.equal(jsLimit, goLimit, 'the web and the scope refuse envelopes alike');
  const bus = fs.readFileSync('app_bus.js', 'utf8');
  const fn = bus.slice(bus.indexOf('const BUS_LIVE_POINTS'), bus.indexOf('async function busDetect('));
  const step = (tdiv, serial) => {
    const sent = [];
    const c = { st: { tdivs: [1e-5, 2e-5, 5e-5, 1e-4, 2e-4, 5e-4, 1e-3], tdiv_s: tdiv, serial_mode: serial ? 1 : 0 },
      DECODE_MIN_ENVELOPE_FEATURE: jsLimit, send: (k, v) => sent.push([k, v]), fmtTdiv: (s) => Math.round(s * 1e6) + 'us' };
    vm.createContext(c); vm.runInContext(fn, c);
    return [c.busReadableTimebase(1 / 115200), sent];
  };
  let [note, sent] = step(5e-4, false);
  assert.deepEqual(sent, [['tdiv', 2e-4]], '500 us/div steps to 200 us/div: ' + JSON.stringify(sent));
  assert.equal(note, ', 200us/div to read it');
  [note, sent] = step(1e-4, false);
  assert.deepEqual(sent, [], '100 us/div reads it');
  [note, sent] = step(5e-4, true);
  assert.deepEqual(sent, [['tdiv', 2e-4]], 'with a serial trigger armed too');
}
console.log('ALL PASS');

// Detect on the live envelope (no sampled record came: Normal with nothing
// triggering) refuses a bus the envelope cannot resolve: the I2C source's
// data line read as Manchester at 102 kBd (bug hunt #3); a sampled record,
// or a bus the envelope resolves, stands.
{
  const guarded = source.slice(source.indexOf('// decodeFeatureSamples is'), source.indexOf('// watchReason'));
  const stands = source.slice(source.indexOf('// detectStands returns'), source.indexOf('// runAutodetect:'));
  const c = {}; vm.createContext(c); vm.runInContext(guarded + stands, c);
  const manch = { proto: 'manchester', result: { ok: true, proto: 'manchester', meta: { samplesPerBit: 4 } } };
  const env = { peak_detect: true, ordered: true };
  assert.equal(c.detectStands(manch, env, true).proto, 'off', 'an aliased bus on the envelope is refused');
  assert.match(c.detectStands(manch, env, true).reason, /waits for a trigger/);
  assert.match(c.detectStands(manch, env, false).reason, /faster time\/div/);
  assert.equal(c.detectStands(manch, { peak_detect: false }, true), manch, 'a sampled record stands');
  const uart = { proto: 'uart', result: { ok: true, proto: 'uart', meta: { samplesPerBit: 12 } } };
  assert.equal(c.detectStands(uart, env, true), uart, 'a bus the envelope resolves stands');
}
