// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// The web eye folds full-rate raw records only: a frame feed interleaving them
// with the live envelope, a decimated record and an averaged one folds the raw
// ones, and the eye (its density, records and status) never changes on another
// frame; without a raw record for a while it says it is held.
"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");
const EJ = require("./eyejitter.js");

// TRLC-LINKS: REQ-SDS-215
function nop() {}
// TRLC-LINKS: REQ-SDS-215
function yes() { return true; }
// TRLC-LINKS: REQ-SDS-215
function el() { return { textContent: "", value: "1", classList: { add: nop, remove: nop, contains: yes }, onclick: null, style: {} }; }
const els = {};
let now = 1e6;
let reply = null;
let renders = 0;
// TRLC-LINKS: REQ-SDS-215
function clock() { return now; }
// TRLC-LINKS: REQ-SDS-215
function byId(id) { return (els[id] ||= el()); }
// TRLC-LINKS: REQ-SDS-215
function status(m) { byId("ejStats").textContent = m; }
// TRLC-LINKS: REQ-SDS-215
function engStub(v, u) { return v + " " + u; }
// TRLC-LINKS: REQ-SDS-215
function body() { return Promise.resolve(reply); }
// TRLC-LINKS: REQ-SDS-215
function fetchStub() { return Promise.resolve({ ok: true, arrayBuffer: body }); }
// TRLC-LINKS: REQ-SDS-215
function same(f) { return f; }
// TRLC-LINKS: REQ-SDS-215
function render() { renders++; }
const context = {
  ...EJ, console, Math, JSON, Float32Array, Float64Array, Int16Array, Uint8Array, Array,
  Date: { now: clock }, performance: { now: clock },
  $: byId, ejStatus: status, eng: engStub, setTimeout: nop, fetch: fetchStub, decodeBinFrame: same,
  st: { running: true, acq_mode: 0, tdiv_s: 1e-6, full_rate_tdiv_s: 200e-6, zoom1: 1, inv1: false },
  sr: { armed: false },
  ej: { st: null, armed: false, gen: 0, lastSeq: 0, fails: 0, lastUi: 0, vpc: 1 / 25 },
  ejLastUi: 0,
};
vm.createContext(context);
vm.runInContext(fs.readFileSync("app_eye.js", "utf8"), context);
context.ejRender = render;

// PRBS7 NRZ, spb samples a bit, from phase: n samples
// TRLC-LINKS: REQ-SDS-215
function prbs(n, spb, phase) {
  const sig = new Int16Array(n);
  let s = 0x7f, bit = 0, k = -1;
  for (let i = 0; i < n; i++) {
    const b = Math.floor((i - phase) / spb);
    while (k < b) { bit = (s >> 6) & 1; s = ((s << 1) | (((s >> 6) ^ (s >> 5)) & 1)) & 0x7f; k++; }
    sig[i] = bit ? 200 : 60;
  }
  return sig;
}
let seq = 0;
// TRLC-LINKS: REQ-SDS-215
const raw = (k) => ({ seq: ++seq, c1: prbs(8000, 20, 3 + 0.37 * k), cols: 8000, sample_s: 2e-9, full_rate: true, vpc1: 0.04 });
const others = [
  (k) => ({ seq: ++seq, c1: prbs(1000, 2.5, 1 + 0.21 * k), cols: 1000, sample_s: 16e-9, peak_detect: true, ordered: true, vpc1: 0.04 }), // envelope
  (k) => ({ seq: ++seq, c1: prbs(4000, 10, 2 + 0.29 * k), cols: 4000, sample_s: 4e-9, decimation: 2, vpc1: 0.04 }), // decimated record
  (k) => ({ seq: ++seq, c1: prbs(8000, 20, 5 + 0.13 * k), cols: 8000, sample_s: 2e-9, filter: "; average 16", vpc1: 0.04 }), // averaged
];
const ej = context.ej;
// TRLC-LINKS: REQ-SDS-215
const look = () => JSON.stringify({ records: ej.st.records, rejected: ej.st.rejected, uis: ej.st.uis, eye: Array.from(ej.st.eye).reduce((a, v, i) => a + v * (i % 97 + 1), 0), armed: ej.armed });

(async () => {
  els.ejCh = el(); els.ejCh.value = "1";
  ej.st = EJ.ejNew({ eyeW: 384, eyeH: 166 }); ej.armed = true; ej.gen = 1; ej.foldAt = 0;
  // entered on a frame it may not fold, the eye waits for one it may
  reply = others[0](0);
  await context.ejLoop(1);
  assert.equal(ej.st.records, 0, "an envelope frame folded");
  assert.match(els.ejStats.textContent, /waiting for a full-rate capture/);
  for (let k = 1; k <= 6; k++) {
    reply = raw(k); now += 300;
    await context.ejLoop(1);
    const folded = look();
    assert.equal(ej.st.records, k, "raw record " + k + " folded");
    for (const [i, other] of others.entries()) {
      reply = other(k); now += 300;
      await context.ejLoop(1);
      assert.equal(look(), folded, "frame kind " + i + " changed the eye after raw record " + k);
      assert.ok(ej.armed, "frame kind " + i + " stopped the eye");
    }
  }
  // the time-scale slower than the full rate: the status says what to set
  context.st.tdiv_s = 500e-6;
  now += 7000;
  reply = others[1](7);
  await context.ejLoop(1);
  assert.match(els.ejStats.textContent, /^held: no full-rate capture for \d+ s — T\/div 0.0002 s or faster · 6 records$/);
  context.st.tdiv_s = 1e-6;
  reply = raw(8); now += 300;
  await context.ejLoop(1);
  assert.equal(ej.st.records, 7, "a raw record after the hold folds");
  assert.ok(renders > 0);
  console.log("ALL PASS");
})().catch((e) => { console.error(e); process.exit(1); });
