const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const source=fs.readFileSync('app_decode.js','utf8');
const compute=source.slice(source.indexOf('function computeDecode()'),source.indexOf('// watchReason'));
for(const flag of ['peak_detect','is_env']) {
 let updates=0;
 const c={dcfg:{proto:'can',result:{text:'stale bytes'}},frame:{c1:[0,255,0,255],[flag]:true},
  // TRLC-LINKS: REQ-SDS-207
  updateDecodeResults(){updates++;}};
 vm.createContext(c);vm.runInContext(compute,c);c.computeDecode();
 assert.equal(c.dcfg.result,null,'envelope must not retain a stale decode');
 assert.equal(updates,1);
}

// A live envelope whose buckets span the bus's clock is refused: an I2C clock
// aliases there into starts and stops without bytes; a fine one decodes
// (framecalc.EnvelopeResolves).
{
  const guarded = source.slice(source.indexOf('// decodeFeatureSamples is'), source.indexOf('// watchReason'));
  const lo = 40, hi = 210, q = 25; // a quarter clock: 25 samples
  const scl = [], sda = [];
  // TRLC-LINKS: REQ-SDS-207
  const put = (c, d, n) => { for (let i = 0; i < n * q; i++) { scl.push(c); sda.push(d); } };
  // TRLC-LINKS: REQ-SDS-207
  const bit = (b) => { put(lo, b ? hi : lo, 2); put(hi, b ? hi : lo, 2); };
  while (scl.length < 60000) {
    put(hi, hi, 8); put(hi, lo, 2); put(lo, lo, 1);
    for (const v of [0x24 << 1, 0x55, 0xAA]) { for (let k = 7; k >= 0; k--) bit((v >> k) & 1); bit(0); }
    put(lo, lo, 2); put(hi, lo, 2); put(hi, hi, 2);
  }
  // TRLC-LINKS: REQ-SDS-207
  const env = (src, bucket) => { const out = []; let last = src[0];
    for (let b = 0; b + bucket <= src.length; b += bucket) {
      let mn = 255, mx = 0; for (let i = b; i < b + bucket; i++) { mn = Math.min(mn, src[i]); mx = Math.max(mx, src[i]); }
      if (Math.abs(last - mx) < Math.abs(last - mn)) { out.push(mx, mn); last = mn; } else { out.push(mn, mx); last = mx; }
    } return out; };
  // TRLC-LINKS: REQ-SDS-207
  const decodeAt = (bucket) => {
    const f = { c1: env(scl, bucket), c2: env(sda, bucket), peak_detect: true, ordered: true };
    const el = { value: '', textContent: '' };
    // TRLC-LINKS: REQ-SDS-207
    const c = { dcfg: { proto: 'i2c', auto: true, inverted: false, fmt: 'hex', scl: 1, sda: 2 }, frame: f, $: () => el,
      // TRLC-LINKS: REQ-SDS-207
      // TRLC-LINKS: REQ-SDS-207
      updateDecodeResults() {},
      // TRLC-LINKS: REQ-SDS-207
      decCodes: (r) => (r === 2 ? f.c2 : f.c1),
      // TRLC-LINKS: REQ-SDS-207
      frameDtS: () => 1e-6 * bucket / 2 };
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
// The web's limit is the scope's (framecalc.MinEnvelopeFeature).
{
  const goLimit = +(/const MinEnvelopeFeature = ([0-9.]+)/.exec(fs.readFileSync('../panel/framecalc/framecalc.go', 'utf8'))[1]);
  const jsLimit = +(/const DECODE_MIN_ENVELOPE_FEATURE = ([0-9.]+)/.exec(source)[1]);
  assert.equal(jsLimit, goLimit, 'the web and the scope refuse envelopes alike');
}
console.log('ALL PASS');
