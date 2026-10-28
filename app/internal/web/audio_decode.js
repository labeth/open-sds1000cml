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
  const W = sliceChannel(ws, {});
  if (!W.ok) return { ok: false, error: "word select: " + W.reason };
  // the data at the word select's levels; one holding still (digital
  // silence) read as its level against the word select's threshold
  let D = sliceChannel(sd, {});
  if (!D.ok) D = sliceChannel(sd, { threshold: W.threshold });
  if (!D.ok) {
    if (!sd || sd.length !== ws.length) return { ok: false, error: "data: " + D.reason };
    D = { ok: true, n: sd.length, codes: sd, threshold: W.threshold, edges: [] };
  }
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
  // the slot width from the data's edges; data with too few edges to tell
  // (silence, a constant) takes the width asked, else 32
  const still = D.edges.length < 8;
  let slot = opts.slot > 0 ? opts.slot : 32, best = NaN;
  if (!still) {
    const slots = [16, 24, 32].map((s) => ({ s, q: score(s) }));
    best = Math.max(...slots.map((x) => x.q));
    if (!(best > 0.6)) return { ok: false, error: "the data does not keep to a bit grid of the word select (is it I2S?)" };
    slot = slots.filter((x) => x.q >= best - 0.03).sort((a, b) => a.s - b.s)[0].s;
  }
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
    ch: [Float32Array.from(ch[0].slice(0, n)), Float32Array.from(ch[1].slice(0, n))], raw: [raw[0].slice(0, n), raw[1].slice(0, n)], errs: 0, gridFit: best,
    still: still ? "the data line holds still: digital silence (its slot width cannot be told)" : "" };
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

// audioFFT is an in-place radix-2 FFT of re/im (their length a power of two).
// TRLC-LINKS: REQ-SDS-280
function audioFFT(re, im) {
  const n = re.length;
  for (let i = 1, j = 0; i < n; i++) {
    let bit = n >> 1;
    for (; j & bit; bit >>= 1) j ^= bit;
    j ^= bit;
    if (i < j) { let t = re[i]; re[i] = re[j]; re[j] = t; t = im[i]; im[i] = im[j]; im[j] = t; }
  }
  for (let len = 2; len <= n; len <<= 1) {
    const a = -2 * Math.PI / len, wr = Math.cos(a), wi = Math.sin(a);
    for (let i = 0; i < n; i += len) {
      let cr = 1, ci = 0;
      for (let k = 0; k < len / 2; k++) {
        const ur = re[i + k], ui = im[i + k], j = i + k + len / 2;
        const vr = re[j] * cr - im[j] * ci, vi = re[j] * ci + im[j] * cr;
        re[i + k] = ur + vr; im[i + k] = ui + vi; re[j] = ur - vr; im[j] = ui - vi;
        const nr = cr * wr - ci * wi; ci = cr * wi + ci * wr; cr = nr;
      }
    }
  }
}

// SPEC_SINAD_MAX is the most SINAD (and THD's floor) a spectrum states: past
// it is the arithmetic's, not the signal's (a 32-bit word is 194 dB).
const SPEC_SINAD_MAX = 200;

// audioSpectrum analyses a channel (samples in -1..1 at fs): the largest
// power of two of its samples, windowed (four-term Blackman-Harris: its
// sidelobes 92 dB down, so a tone off a bin's centre does not leak into the
// noise measured), as dBFS a bin (a full-scale
// sine 0 dBFS); its tone (the strongest bin past DC, its frequency
// interpolated) and the tone's distortion (THD: harmonics 2 to 5 against
// it), its SINAD (it against all else past DC) and the bits that make (ENOB).
// TRLC-LINKS: REQ-SDS-280
function audioSpectrum(x, fs) {
  let n = 1;
  while (n * 2 <= x.length && n < 65536) n *= 2;
  if (n < 64) return { ok: false, error: "too few samples for a spectrum" };
  const re = new Float64Array(n), im = new Float64Array(n);
  let wsum = 0;
  for (let i = 0; i < n; i++) {
    const p = 2 * Math.PI * i / n, w = 0.35875 - 0.48829 * Math.cos(p) + 0.14128 * Math.cos(2 * p) - 0.01168 * Math.cos(3 * p);
    re[i] = x[i] * w; wsum += w;
  }
  audioFFT(re, im);
  const half = n / 2, pw = new Float64Array(half), db = new Float32Array(half);
  for (let k = 0; k < half; k++) {
    const a = 2 * Math.hypot(re[k], im[k]) / wsum; // a sine's amplitude
    pw[k] = a * a / 2; db[k] = a > 0 ? 20 * Math.log10(a) : -200;
  }
  const bin = fs / n, guard = 4; // the window spreads a tone over four bins each side: the tone's power is the bins about it
  let pk = guard + 1;
  for (let k = guard + 1; k < half; k++) if (pw[k] > pw[pk]) pk = k;
  // TRLC-LINKS: REQ-SDS-280
  const around = (k0) => { let s = 0; for (let k = Math.max(1, k0 - guard); k <= Math.min(half - 1, k0 + guard); k++) s += pw[k]; return s; };
  const a0 = db[pk - 1], a1 = db[pk], a2 = db[pk + 1] !== undefined ? db[pk + 1] : a1;
  const off = a0 - 2 * a1 + a2 !== 0 ? 0.5 * (a0 - a2) / (a0 - 2 * a1 + a2) : 0;
  const f0 = (pk + off) * bin, tone = around(pk);
  // the distortion only where a harmonic's bins lie clear of the tone's (a
  // short record's wide bins hold the tone and its harmonics together)
  let harm = 0, harmN = 0;
  if (pk > 2 * guard + 1) for (let h = 2; h <= 5; h++) { const k = Math.round(h * (pk + off)); if (k + guard < half) { harm += around(k); harmN++; } }
  // the noise and distortion: the bins past DC (its guard) and past the
  // tone's, summed as they are; none when the tone is near DC or too few are left
  let rest = 0, noiseBins = 0;
  for (let k = guard + 1; k < half; k++) if (Math.abs(k - pk) > guard) { rest += pw[k]; noiseBins++; }
  const sinad = noiseBins >= 32 && pk > 2 * guard ? Math.min(SPEC_SINAD_MAX, 10 * Math.log10(tone / Math.max(rest, tone * 1e-20))) : NaN;
  // the bins' sum is a tone's power times the window's noise bandwidth (2.0044 bins)
  return { ok: true, n, bin, db, toneHz: f0, toneDb: 10 * Math.log10(Math.max(tone, 1e-30) * 2 / 2.0044),
    thdDb: harmN ? (harm > 0 ? 10 * Math.log10(harm / tone) : -SPEC_SINAD_MAX) : NaN, sinadDb: sinad, enob: (sinad - 1.76) / 6.02,
    why: harmN ? "" : "the record is too short to part the tone from its harmonics (" + Math.round(bin) + " Hz a bin)" };
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { audioFromSPDIF, audioFromI2S, audioFromPDM, pdmCIC, audioStats, audioSpectrum, audioFFT, PDM_DECIM };
