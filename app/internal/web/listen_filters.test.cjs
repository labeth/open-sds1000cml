// ENGMODEL-OWNER-UNIT: FU-WEB-SOUND
// Run by TestListenFiltersJS (listen_filters_node_test.go): the Listen chain
// (sound_client.js sndAudioFeed) blocks the inputs' DC without a thump, moves
// a cutoff without a click, notches a held tone, and lets a melody's notes
// through the notch without clicks.
const assert = require("node:assert/strict"), fs = require("node:fs"), vm = require("node:vm");
const c = { performance: { now: () => 0 }, Math, Float32Array, Float64Array, Array, Number, Infinity, NaN, console };
vm.createContext(c);
vm.runInContext(fs.readFileSync("sound.js", "utf8") + "\n" + fs.readFileSync("sound_client.js", "utf8"), c);
const fs_ = 500e6 / 8192;
function rig() {
  const cl = c.sndClient(() => false, "");
  cl.rate = fs_;
  const out = [];
  cl.audio.node = { port: { postMessage: (m) => out.push(...m.l) } };
  cl.audio.auto = false; cl.audio.gainDB = 0; cl.audio.vol = 1;
  return { cl, out };
}
let ph = 0;
const tone = (n, f, dc, amp) => { const x = new Float32Array(n); for (let i = 0; i < n; i++) { ph += 2 * Math.PI * f / fs_; x[i] = dc + amp * Math.sin(ph); } return x; };
const rms = (v) => Math.sqrt(v.reduce((s, x) => s + x * x, 0) / v.length);
const maxStep = (v) => { let m = 0; for (let i = 1; i < v.length; i++) m = Math.max(m, Math.abs(v[i] - v[i - 1])); return m; };
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
// the notch on a held tone takes it out once the pitch has held
{
  const r = rig();
  r.cl.audio.notch = true; r.cl.audio.hz = 440;
  for (let k = 0; k < 40; k++) feed(r, 1024, 440);
  const db = 20 * Math.log10(rms(r.out.slice(-8192)) / 0.212);
  assert.ok(db < -25, "the notch leaves " + db.toFixed(1) + " dB of the tone");
}
// a melody through the notch: each note retunes it, without a click between
{
  const r = rig();
  r.cl.audio.notch = true; r.cl.audio.lp = 3000;
  const notes = [440, 494, 523, 587, 659, 587, 523, 494];
  let worst = 0;
  for (const f of notes) for (let k = 0; k < 9; k++) {
    r.cl.audio.hz = f;
    const n0 = r.out.length;
    feed(r, 1024, f);
    worst = Math.max(worst, maxStep(r.out.slice(Math.max(0, n0 - 1))));
  }
  assert.ok(worst < 0.12, "a note change clicks: step " + worst);
}
console.log("listen filters ok");
