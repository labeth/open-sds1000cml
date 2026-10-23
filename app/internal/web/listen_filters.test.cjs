// ENGMODEL-OWNER-UNIT: FU-WEB-SOUND
// Run by TestListenFiltersJS (listen_filters_node_test.go): the Listen chain
// (sound_client.js sndAudioFeed) blocks the inputs' DC without a thump, moves
// a cutoff without a click, and its 24 dB low-pass cuts.
const assert = require("node:assert/strict"), fs = require("node:fs"), vm = require("node:vm");
// TRLC-LINKS: REQ-SDS-265
const c = { performance: { now: () => 0 }, Math, Float32Array, Float64Array, Array, Number, Infinity, NaN, console };
vm.createContext(c);
vm.runInContext(fs.readFileSync("sound.js", "utf8") + "\n" + fs.readFileSync("sound_client.js", "utf8"), c);
const fs_ = 500e6 / 8192;
// TRLC-LINKS: REQ-SDS-265
function rig() {
  const cl = c.sndClient(() => false, "");
  cl.rate = fs_;
  const out = [];
  // TRLC-LINKS: REQ-SDS-265
  cl.audio.node = { port: { postMessage: (m) => out.push(...m.l) } };
  cl.audio.auto = false; cl.audio.gainDB = 0; cl.audio.vol = 1;
  return { cl, out };
}
let ph = 0;
// TRLC-LINKS: REQ-SDS-265
const tone = (n, f, dc, amp) => { const x = new Float32Array(n); for (let i = 0; i < n; i++) { ph += 2 * Math.PI * f / fs_; x[i] = dc + amp * Math.sin(ph); } return x; };
// TRLC-LINKS: REQ-SDS-265
const rms = (v) => Math.sqrt(v.reduce((s, x) => s + x * x, 0) / v.length);
// TRLC-LINKS: REQ-SDS-265
const maxStep = (v) => { let m = 0; for (let i = 1; i < v.length; i++) m = Math.max(m, Math.abs(v[i] - v[i - 1])); return m; };
// TRLC-LINKS: REQ-SDS-265
const feed = (r, n, f, dc = 1.6, amp = 0.3) => { const x = tone(n, f, dc, amp); c.sndAudioFeed(r.cl, x, x, false); };
// DC blocked from the first sample: no 1.6 V step, no DC after it settles
{
  const r = rig();
  for (let k = 0; k < 40; k++) feed(r, 1024, 440);
  assert.ok(Math.abs(r.out[0]) < 0.05, "the first sample steps " + r.out[0]);
  const tail = r.out.slice(-8192), mean = tail.reduce((s, x) => s + x, 0) / tail.length;
  assert.ok(Math.abs(mean) < 0.01, "DC left " + mean);
  assert.ok(maxStep(r.out) < 0.06, "a step of " + maxStep(r.out));
}
// a cutoff moved mid-stream (the slider) keeps the filters' state: no click
{
  const r = rig();
  r.cl.audio.lp = 2000;
  for (let k = 0; k < 20; k++) feed(r, 1024, 440);
  const before = maxStep(r.out.slice(-4096));
  for (const lp of [1500, 1000, 700, 1200, 3000]) { r.cl.audio.lp = lp; feed(r, 1024, 440); }
  assert.ok(maxStep(r.out.slice(-6000)) < 1.5 * before + 1e-3, `a cutoff change clicks: ${maxStep(r.out.slice(-6000))} vs ${before}`);
}
// a low-pass at 400 Hz takes a 4 kHz tone down by more than 30 dB (24 dB an octave)
{
  const r = rig(), x = rig();
  r.cl.audio.lp = 400;
  for (let k = 0; k < 20; k++) { feed(r, 1024, 4000, 0, 0.3); feed(x, 1024, 4000, 0, 0.3); }
  const db = 20 * Math.log10(rms(r.out.slice(-8192)) / rms(x.out.slice(-8192)));
  assert.ok(db < -30, "LP 400 Hz on 4 kHz: " + db.toFixed(1) + " dB");
}
console.log("listen filters ok");
