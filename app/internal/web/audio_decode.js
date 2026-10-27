// ENGMODEL-OWNER-UNIT: FU-WEB-VISUAL
// audio_decode.js — digital audio out of a record: S/PDIF's subframes (from
// its decode), I2S read off its word select and data (the scope has two
// channels: the bit clock is recovered from the word select's period and the
// data's edges), and PDM (a MEMS microphone's clock and data, filtered down
// to PCM), as samples in -1..1 a channel, with the figures a fault shows in:
// the rate, the level, clipping, DC, the bits a word really uses. Pure;
// sliceChannel and logicAt come from decode.js. A module for the node tests.
"use strict";

// audioFromSPDIF reads a S/PDIF decode's samples: the preambles name the
// channel (B and M the first, W the second), each subframe's 24 bits from its
// least significant (the audio word with its auxiliary bits under it), from
// the first frame's first subframe. A subframe whose parity fails repeats the
// channel's last sample and counts.
// TRLC-LINKS: REQ-SDS-280
function audioFromSPDIF(r) {
  if (!r || !r.ok || !r.spans) return { ok: false, error: (r && r.error) || "no S/PDIF decode" };
  const ch = [[], []], raw = [[], []];
  let which = -1, errs = 0, blocks = 0, began = false;
  for (const sp of r.spans) {
    if (sp.kind === "sof" || (sp.kind === "rw" && (sp.text === "M" || sp.text === "W"))) {
      which = sp.text === "W" ? 1 : 0;
      if (sp.text === "B") blocks++;
      // a frame opens on the first channel's subframe: a record opening on a
      // second's is read from the next frame, the channels kept in step
      if (which === 0) began = true;
      if (!began) which = -1;
      continue;
    }
    if (which < 0) continue;
    if (sp.kind === "data" || sp.kind === "parity-error") {
      let v = sp.val & 0xffffff;
      if (v & 0x800000) v -= 0x1000000;
      if (sp.kind === "parity-error") { errs++; v = raw[which].length ? raw[which][raw[which].length - 1] : 0; }
      raw[which].push(v);
      ch[which].push(v / 8388608);
      which = -1;
    }
  }
  const n = Math.min(ch[0].length, ch[1].length || ch[0].length);
  if (n < 8) return { ok: false, error: "too few S/PDIF subframes" };
  const fs = r.meta && r.meta.baud > 0 ? r.meta.baud / 64 : 0;
  return { ok: true, kind: "S/PDIF", fs, bits: 24, ch: [Float32Array.from(ch[0].slice(0, n)), Float32Array.from((ch[1].length ? ch[1] : ch[0]).slice(0, n))],
    raw: [raw[0].slice(0, n), (raw[1].length ? raw[1] : raw[0]).slice(0, n)], errs, blocks };
}

// audioFromI2S reads I2S from its word select ws and data sd (codes, dt a
// sample's seconds). The frame is the word select's period; the slot width
// (16, 24 or 32 bits a channel) is the one whose bit grid the data's edges
// keep to (the coarsest of those that fit as well as the best). A word is
// read MSB first from one bit after the word select's edge (I2S; 0 for
// left-justified: opts.lj), the left channel while the word select is low
// (opts.swap the other way).
// TRLC-LINKS: REQ-SDS-280
function audioFromI2S(ws, sd, dt, opts) {
  opts = opts || {};
  const W = sliceChannel(ws, {}), D = sliceChannel(sd, {});
  if (!W.ok) return { ok: false, error: "word select: " + W.reason };
  if (!D.ok) return { ok: false, error: "data: " + D.reason };
  const E = W.edges;
  if (E.length < 8) return { ok: false, error: "too few word-select edges" };
  const half = [];
  for (let k = 1; k < E.length; k++) half.push(E[k].x - E[k - 1].x);
  const sorted = Float64Array.from(half).sort();
  const P = 2 * sorted[sorted.length >> 1]; // a frame: both channels
  if (!(P > 8)) return { ok: false, error: "word select too fast for the sample rate" };
  // the data's edges against each slot width's bit grid from the word select's edges
  // TRLC-LINKS: REQ-SDS-280
  const score = (slot) => {
    const bit = P / (2 * slot);
    if (bit < 2) return -1;
    let hit = 0, n = 0, j = 0;
    for (const e of D.edges) {
      while (j + 1 < E.length && E[j + 1].x <= e.x) j++;
      if (E[j].x > e.x) continue;
      const ph = (e.x - E[j].x) / bit;
      n++;
      if (Math.abs(ph - Math.round(ph)) < 0.25) hit++;
    }
    return n ? hit / n : -1;
  };
  const slots = [16, 24, 32].map((s) => ({ s, q: score(s) }));
  const best = Math.max(...slots.map((x) => x.q));
  if (!(best > 0.6)) return { ok: false, error: "the data does not keep to a bit grid of the word select (is it I2S?)" };
  const slot = slots.filter((x) => x.q >= best - 0.03).sort((a, b) => a.s - b.s)[0].s;
  const bit = P / (2 * slot), delay = opts.lj ? 0 : 1;
  const bits = opts.bits > 0 ? Math.min(opts.bits, slot) : slot;
  const ch = [[], []], raw = [[], []];
  for (let k = 0; k + 1 < E.length; k++) {
    const x0 = E[k].x;
    if (x0 + (delay + bits) * bit >= W.n) break;
    const lr = (E[k].dir < 0) !== !!opts.swap ? 0 : 1; // falling to low: the left word follows
    let v = 0;
    for (let b = 0; b < bits; b++) {
      const at = x0 + (delay + b + 0.5) * bit;
      v = v * 2 + (logicAt(D, at) === 1 ? 1 : 0);
    }
    if (v >= 2 ** (bits - 1)) v -= 2 ** bits;
    raw[lr].push(v);
    ch[lr].push(v / 2 ** (bits - 1));
  }
  const n = Math.min(ch[0].length, ch[1].length);
  if (n < 8) return { ok: false, error: "too few I2S frames" };
  return { ok: true, kind: opts.lj ? "Left-justified" : "I2S", fs: 1 / (P * dt), bits, slot,
    ch: [Float32Array.from(ch[0].slice(0, n)), Float32Array.from(ch[1].slice(0, n))], raw: [raw[0].slice(0, n), raw[1].slice(0, n)], errs: 0, gridFit: best };
}

// PDM_DECIM is PDM's decimation to PCM: 64, a 3.072 MHz clock to 48 kHz.
const PDM_DECIM = 64;

// audioFromPDM reads PDM from its clock and data (codes, dt a sample's
// seconds): the data taken a quarter period after each rising edge (the
// first microphone of a pair) and each falling one (the second), +1 or -1, a
// third-order CIC filter decimating by PDM_DECIM to PCM, its DC taken out.
// TRLC-LINKS: REQ-SDS-280
function audioFromPDM(clk, data, dt) {
  const C = sliceChannel(clk, {}), D = sliceChannel(data, {});
  if (!C.ok) return { ok: false, error: "clock: " + C.reason };
  if (!D.ok) return { ok: false, error: "data: " + D.reason };
  const E = C.edges;
  if (E.length < 4 * PDM_DECIM) return { ok: false, error: "too few clock edges for PDM" };
  const gaps = [];
  for (let k = 2; k < E.length; k++) gaps.push(E[k].x - E[k - 2].x);
  const period = Float64Array.from(gaps).sort()[gaps.length >> 1];
  if (!(period >= 4)) return { ok: false, error: "the clock is too fast for the sample rate" };
  const streams = [[], []];
  for (const e of E) {
    const at = e.x + period / 4;
    if (at >= D.n) break;
    streams[e.dir > 0 ? 0 : 1].push(logicAt(D, at) === 1 ? 1 : -1);
  }
  const ch = streams.map((s) => pdmCIC(s, PDM_DECIM));
  const n = Math.min(ch[0].length, ch[1].length);
  if (n < 8) return { ok: false, error: "too short for PDM" };
  return { ok: true, kind: "PDM", fs: 1 / (period * dt * PDM_DECIM), bits: 0, ch: [ch[0].slice(0, n), ch[1].slice(0, n)], raw: null, errs: 0 };
}

// pdmCIC is a third-order CIC decimator of a ±1 stream by R, scaled to -1..1
// and its mean (the DC) taken out.
// TRLC-LINKS: REQ-SDS-280
function pdmCIC(x, R) {
  const out = [];
  let i1 = 0, i2 = 0, i3 = 0, c1 = 0, c2 = 0, c3 = 0;
  for (let k = 0; k < x.length; k++) {
    i1 += x[k]; i2 += i1; i3 += i2;
    if ((k + 1) % R !== 0) continue;
    const d1 = i3 - c1; c1 = i3;
    const d2 = d1 - c2; c2 = d1;
    const d3 = d2 - c3; c3 = d2;
    out.push(d3 / (R * R * R));
  }
  const y = Float32Array.from(out.slice(3)); // the filter's first outputs are its start
  let m = 0;
  for (const v of y) m += v;
  m /= y.length || 1;
  for (let k = 0; k < y.length; k++) y[k] -= m;
  return y;
}

// audioStats are a channel's figures: RMS and peak in dBFS, the samples at
// full scale (clipped), the DC as a share of full scale and, from the raw
// words, the bits they really use (the low bits never set do not count).
// TRLC-LINKS: REQ-SDS-280
function audioStats(x, raw, bits) {
  let s2 = 0, pk = 0, sum = 0, clip = 0;
  for (const v of x) { s2 += v * v; sum += v; const a = Math.abs(v); if (a > pk) pk = a; if (a >= 0.999) clip++; }
  const n = x.length || 1;
  // TRLC-LINKS: REQ-SDS-280
  const db = (v) => v > 0 ? 20 * Math.log10(v) : -Infinity;
  let used = NaN;
  if (raw && bits > 0) {
    let or = 0;
    for (const v of raw) or |= v & ((2 ** Math.min(bits, 31)) - 1);
    let tz = 0;
    while (tz < bits && !((or >> tz) & 1)) tz++;
    used = or ? bits - tz : 0;
  }
  return { rmsDb: db(Math.sqrt(s2 / n)), peakDb: db(pk), clip, dc: sum / n, bitsUsed: used };
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { audioFromSPDIF, audioFromI2S, audioFromPDM, pdmCIC, audioStats, PDM_DECIM };
