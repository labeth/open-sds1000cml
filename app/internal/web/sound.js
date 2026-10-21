// ENGMODEL-OWNER-UNIT: FU-WEB-SOUND
// sound.js — the SOUND analysis in the browser (ADR-SOUND): the twin of the Go
// package internal/sound (meters, tuner, ADSR, modulation, audio spectrum and
// harmonics, A/B, V/oct, cycle), held to it by TestSoundJSParity. Pure (no DOM,
// no globals beyond its own functions) so node runs the code the page runs.
"use strict";

const SND_LOG2 = 13;
const SND_DBU = 0.7745966692414834;
const SND_PRESETS = [
  { id: "dbu", name: "dBu", vrms: SND_DBU },
  { id: "dbv", name: "dBV", vrms: 1 },
  { id: "euro", name: "Eurorack 10 Vpp", vrms: 5 / Math.SQRT2 },
  { id: "pro", name: "+4 dBu line", vrms: SND_DBU * 1.5848931924611136 },
  { id: "consumer", name: "-10 dBV", vrms: 0.31622776601683794 },
  { id: "guitar", name: "guitar 100 mV", vrms: 0.1 },
];
const SND_NOTES = ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"];
const SND_FLOOR_CODES = 6e-5;
const sndTw = new Map();

// TRLC-LINKS: REQ-SDS-265
function sndRate(log2) { return 500e6 / Math.pow(2, log2); }

// ---- transforms ----
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-270
function sndTwiddle(n) {
  let t = sndTw.get(n);
  if (t) return t;
  const c = new Float64Array(n / 2);
  const s = new Float64Array(n / 2);
  for (let k = 0; k < n / 2; k++) { const a = -2 * Math.PI * k / n; c[k] = Math.cos(a); s[k] = Math.sin(a); }
  t = [c, s];
  sndTw.set(n, t);
  return t;
}

// TRLC-LINKS: REQ-SDS-267, REQ-SDS-270
function sndFFT(re, im, inverse) {
  const n = re.length;
  if (n < 2) return;
  for (let i = 1, j = 0; i < n; i++) {
    let bit = n >> 1;
    for (; j & bit; bit >>= 1) j ^= bit;
    j ^= bit;
    if (i < j) { let t = re[i]; re[i] = re[j]; re[j] = t; t = im[i]; im[i] = im[j]; im[j] = t; }
  }
  const [cs, sn] = sndTwiddle(n);
  for (let l = 2; l <= n; l <<= 1) {
    const h = l / 2, step = n / l;
    for (let i = 0; i < n; i += l) {
      for (let k = 0; k < h; k++) {
        const wr = cs[k * step];
        const wi = inverse ? -sn[k * step] : sn[k * step];
        const a = i + k, b = i + k + h;
        const vr = re[b] * wr - im[b] * wi;
        const vi = re[b] * wi + im[b] * wr;
        re[b] = re[a] - vr; im[b] = im[a] - vi;
        re[a] = re[a] + vr; im[a] = im[a] + vi;
      }
    }
  }
}

// TRLC-LINKS: REQ-SDS-267, REQ-SDS-270
function sndPow2(n) { let p = 1; while (p < n && p < 0x40000000) p <<= 1; return p; }

// TRLC-LINKS: REQ-SDS-267, REQ-SDS-270
function sndFloorPow2(n) { if (n < 1) return 0; let p = 1; while (p * 2 <= n && p < 0x40000000) p <<= 1; return p; }

// TRLC-LINKS: REQ-SDS-267, REQ-SDS-270
function sndHann(i, n) { return 0.5 - 0.5 * Math.cos(2 * Math.PI * i / (n - 1)); }

// TRLC-LINKS: REQ-SDS-267, REQ-SDS-270
function sndHannGain(d) { return d === 0 ? 1 : Math.sin(Math.PI * d) / (Math.PI * d * (1 - d * d)); }

// TRLC-LINKS: REQ-SDS-266
function sndMean(x) { if (!x.length) return 0; let s = 0; for (let i = 0; i < x.length; i++) s += x[i]; return s / x.length; }

// TRLC-LINKS: REQ-SDS-267, REQ-SDS-271
function sndToneAt(x, f, fs) {
  const n = x.length;
  if (n < 2) return [0, 0];
  const m = sndMean(x), w = 2 * Math.PI * f / fs, c = Math.cos(w), s = Math.sin(w);
  let ck = 1, sk = 0, re = 0, im = 0;
  for (let i = 0; i < n; i++) {
    const v = (x[i] - m) * sndHann(i, n);
    re += v * ck; im -= v * sk;
    const nc = ck * c - sk * s; sk = sk * c + ck * s; ck = nc;
    if ((i & 1023) === 1023) { const a = w * (i + 1); ck = Math.cos(a); sk = Math.sin(a); }
  }
  return [re, im];
}

// TRLC-LINKS: REQ-SDS-267
function sndWrapPi(a) {
  a = (a + Math.PI) % (2 * Math.PI);
  if (a <= 0) a += 2 * Math.PI;
  return a - Math.PI;
}

// TRLC-LINKS: REQ-SDS-268, REQ-SDS-269
function sndPercentile(x, p) {
  if (!x.length) return 0;
  const s = Float64Array.from(x).sort();
  const i = p * (s.length - 1), lo = Math.floor(i), hi = Math.min(lo + 1, s.length - 1), f = i - lo;
  return s[lo] * (1 - f) + s[hi] * f;
}

// TRLC-LINKS: REQ-SDS-267, REQ-SDS-268
function sndMedian(x) { return sndPercentile(x, 0.5); }

// ---- meters ----
// TRLC-LINKS: REQ-SDS-266
function sndPreset(id) { return SND_PRESETS.find((p) => p.id === id) || SND_PRESETS[0]; }

// TRLC-LINKS: REQ-SDS-266
function sndDB(v, ref) { return v > 0 && ref > 0 ? 20 * Math.log10(v / ref) : -Infinity; }

// TRLC-LINKS: REQ-SDS-266
function sndMeasure(x) {
  const l = { n: x.length, min: 0, max: 0, vpp: 0, dc: 0, rms: 0, total: 0, peak: 0, crestDB: 0, clip: false };
  if (!x.length) return l;
  let mn = x[0], mx = x[0], s = 0;
  for (let i = 0; i < x.length; i++) { const v = x[i]; s += v; if (v < mn) mn = v; if (v > mx) mx = v; }
  l.min = mn; l.max = mx; l.dc = s / x.length;
  let ss = 0;
  for (let i = 0; i < x.length; i++) { const d = x[i] - l.dc; ss += d * d; }
  l.rms = Math.sqrt(ss / x.length);
  l.total = Math.sqrt(l.rms * l.rms + l.dc * l.dc);
  l.vpp = mx - mn;
  l.peak = Math.max(mx - l.dc, l.dc - mn);
  if (l.rms > 0) l.crestDB = 20 * Math.log10(l.peak / l.rms);
  return l;
}

// TRLC-LINKS: REQ-SDS-266
function sndClipCodes(q) {
  if (!q.length) return false;
  let lo = 0, hi = 0;
  for (let i = 0; i < q.length; i++) { if (q[i] <= 512) lo++; if (q[i] >= 253 * 256) hi++; }
  return lo * 200 > q.length || hi * 200 > q.length;
}

// TRLC-LINKS: REQ-SDS-265, REQ-SDS-266
function sndVolts(q, vpc, off) {
  const out = new Float64Array(q.length), k = vpc / 256;
  for (let i = 0; i < q.length; i++) out[i] = (q[i] - 32768) * k - off;
  return out;
}

// ---- tuner ----
const SND_PITCH_DEFAULT = { fmin: 25, fmax: 5000, threshold: 0.15 };
const SND_VOICED = 0.45;

// TRLC-LINKS: REQ-SDS-267
function sndYinDiff(x, w, tauMax) {
  const n = x.length, m = sndMean(x), size = sndPow2(n + w);
  const ar = new Float64Array(size), ai = new Float64Array(size), br = new Float64Array(size), bi = new Float64Array(size);
  for (let i = 0; i < n; i++) { br[i] = x[i] - m; if (i < w) ar[i] = br[i]; }
  sndFFT(ar, ai, false);
  sndFFT(br, bi, false);
  for (let k = 0; k < size; k++) {
    const re = ar[k] * br[k] + ai[k] * bi[k], im = ar[k] * bi[k] - ai[k] * br[k];
    ar[k] = re; ai[k] = im;
  }
  sndFFT(ar, ai, true);
  const cum = new Float64Array(n + 1);
  for (let i = 0; i < n; i++) { const v = x[i] - m; cum[i + 1] = cum[i] + v * v; }
  const d = new Float64Array(tauMax), e0 = cum[w];
  for (let t = 0; t < tauMax; t++) d[t] = Math.max(0, e0 + (cum[t + w] - cum[t]) - 2 * ar[t] / size);
  return d;
}

// TRLC-LINKS: REQ-SDS-267
function sndRefine(x, fs, f) {
  const n = x.length, half = Math.floor(n / 2);
  if (half < 32 || !(f > 0)) return f;
  let bestH = 1, bestM = -1;
  for (let h = 1; h <= 4; h++) {
    const fh = h * f;
    if (fh >= fs * 0.45) break;
    const [re, im] = sndToneAt(x, fh, fs);
    const m = Math.hypot(re, im);
    if (m > bestM) { bestH = h; bestM = m; }
  }
  let fh = bestH * f;
  if (fh * half / fs < 4) return f;
  for (let it = 0; it < 2; it++) {
    const [r1, i1] = sndToneAt(x.subarray ? x.subarray(0, half) : x.slice(0, half), fh, fs);
    const [r2, i2] = sndToneAt(x.subarray ? x.subarray(half, 2 * half) : x.slice(half, 2 * half), fh, fs);
    if (Math.hypot(r1, i1) === 0 || Math.hypot(r2, i2) === 0) return f;
    const dphi = Math.atan2(i2, r2) - Math.atan2(i1, r1);
    const expect = 2 * Math.PI * fh * half / fs;
    fh += sndWrapPi(dphi - expect) * fs / (2 * Math.PI * half);
  }
  const g = fh / bestH;
  if (Math.abs(g - f) > 0.02 * f || !Number.isFinite(g)) return f;
  return g;
}

// TRLC-LINKS: REQ-SDS-267
function sndPitch(x, fs, o) {
  o = Object.assign({}, SND_PITCH_DEFAULT, o || {});
  const n = x.length;
  if (n < 64 || !(fs > 0)) return { hz: 0, clarity: 0, ok: false };
  let y = x, yfs = fs, ny = n;
  if (n > SND_YIN_MAX) {
    let dec = 1;
    while (Math.floor(n / dec) > SND_YIN_MAX) dec *= 2;
    y = sndBlockMeanN(x, dec); yfs = fs / dec; ny = y.length;
    o.fmax = Math.min(o.fmax, yfs / 4);
  }
  const tauMax = Math.min(Math.floor(ny / 2), Math.ceil(yfs / o.fmin) + 2);
  const tauMin = Math.max(2, Math.floor(yfs / o.fmax));
  if (tauMax <= tauMin + 2) return { hz: 0, clarity: 0, ok: false };
  const d = sndYinDiff(y, ny - tauMax, tauMax);
  const cm = new Float64Array(tauMax);
  cm[0] = 1;
  let run = 0;
  for (let t = 1; t < tauMax; t++) { run += d[t]; cm[t] = run > 0 ? d[t] * t / run : 1; }
  let tau = -1;
  for (let t = tauMin; t < tauMax - 1; t++) {
    if (cm[t] < o.threshold) {
      while (t + 1 < tauMax - 1 && cm[t + 1] < cm[t]) t++;
      tau = t;
      break;
    }
  }
  if (tau < 0) {
    let best = tauMin;
    for (let t = tauMin; t < tauMax - 1; t++) if (cm[t] < cm[best]) best = t;
    tau = best;
  }
  const dip = cm[tau];
  if (dip > SND_VOICED || (tau <= tauMin && cm[tau + 1] < dip)) return { hz: 0, clarity: Math.max(0, 1 - dip), ok: false };
  let ft = tau;
  if (tau > 0 && tau < tauMax - 1) {
    const a = cm[tau - 1], b = cm[tau], c = cm[tau + 1], den = a - 2 * b + c;
    if (den > 0) ft += 0.5 * (a - c) / den;
  }
  return { hz: sndRefine(x, fs, yfs / ft), clarity: Math.max(0, 1 - dip), ok: true };
}

const SND_YIN_MAX = 8192;

// TRLC-LINKS: REQ-SDS-267
function sndBlockMeanN(x, k) {
  const out = new Float64Array(Math.floor(x.length / k));
  for (let i = 0; i < out.length; i++) { let s = 0; for (let j = i * k; j < i * k + k; j++) s += x[j]; out[i] = s / k; }
  return out;
}

// TRLC-LINKS: REQ-SDS-267
function sndNote(f, a4) {
  if (!(a4 > 0)) a4 = 440;
  if (!(f > 0) || !Number.isFinite(f)) return { name: "", octave: 0, midi: 0, cents: 0, refHz: 0, text: "--" };
  const m = Math.round(12 * Math.log2(f / a4)) + 69;
  const ref = a4 * Math.pow(2, (m - 69) / 12);
  const name = SND_NOTES[((m % 12) + 12) % 12], octave = Math.floor(m / 12) - 1;
  return { name, octave, midi: m, cents: 1200 * Math.log2(f / ref), refHz: ref, text: name + octave };
}

// TRLC-LINKS: REQ-SDS-267
function sndCents(a, b) { return a > 0 && b > 0 ? 1200 * Math.log2(a / b) : NaN; }

// TRLC-LINKS: REQ-SDS-267
function sndTunerWindow(f, fs) {
  let n = 8192;
  if (f > 0) n = sndPow2(Math.floor(Math.min(16 * fs / f, 65536)));
  return Math.min(32768, Math.max(4096, n));
}

// TRLC-LINKS: REQ-SDS-267
function sndPitchRange(id) {
  if (id === "bass") return { fmin: 20, fmax: 600, threshold: 0.15 };
  if (id === "high") return { fmin: 200, fmax: 12000, threshold: 0.15 };
  return Object.assign({}, SND_PITCH_DEFAULT);
}

// TRLC-LINKS: REQ-SDS-267
function sndTuner(slow) { return { slow: !!slow, recent: [] }; }

// TRLC-LINKS: REQ-SDS-267
function sndTunerAdd(t, p) {
  if (!p.ok) { t.recent = []; return 0; }
  if (t.recent.length && Math.abs(sndCents(p.hz, t.recent[t.recent.length - 1])) > 50) t.recent = [];
  const k = t.slow ? 9 : 3;
  t.recent.push(p.hz);
  if (t.recent.length > k) t.recent = t.recent.slice(t.recent.length - k);
  return sndMedian(t.recent);
}

// ---- envelope / ADSR ----
// TRLC-LINKS: REQ-SDS-268
function sndEnvelope(x, fs, tau, block) {
  if (!x.length || !(fs > 0)) return { env: [], fs: 0 };
  const b = Math.max(1, Math.round(block * fs)), m = sndMean(x), k = tau > 0 ? Math.exp(-1 / (tau * fs)) : 0;
  const out = [];
  let hold = 0, top = 0;
  for (let i = 0; i < x.length; i++) {
    const a = Math.abs(x[i] - m);
    hold *= k;
    if (a > hold) hold = a;
    if (hold > top) top = hold;
    if ((i + 1) % b === 0) { out.push(top); top = 0; }
  }
  if (x.length % b !== 0) out.push(top);
  return { env: out, fs: fs / b };
}

// TRLC-LINKS: REQ-SDS-268
function sndBlockMeans(x, fs, block) {
  if (!x.length || !(fs > 0)) return { env: [], fs: 0 };
  const b = Math.max(1, Math.round(block * fs)), out = [];
  for (let i = 0; i < x.length; i += b) {
    const j = Math.min(x.length, i + b);
    let s = 0;
    for (let k = i; k < j; k++) s += x[k];
    out.push(s / (j - i));
  }
  return { env: out, fs: fs / b };
}

// TRLC-LINKS: REQ-SDS-268
function sndCrossUp(e, i0, i1, level) {
  for (let i = Math.max(1, i0); i < i1 && i < e.length; i++) {
    if (e[i] >= level && e[i - 1] < level) return i - 1 + (level - e[i - 1]) / (e[i] - e[i - 1]);
    if (i === i0 && e[i] >= level) return i;
  }
  return -1;
}

// TRLC-LINKS: REQ-SDS-268
function sndCrossDown(e, i0, i1, level) {
  if (i0 >= 0 && i0 < e.length && e[i0] <= level) return i0;
  for (let i = Math.max(1, i0 + 1); i < i1 && i < e.length; i++) {
    if (e[i] <= level && e[i - 1] > level) return i - 1 + (e[i - 1] - level) / (e[i - 1] - e[i]);
  }
  return -1;
}

// TRLC-LINKS: REQ-SDS-268
function sndExpTau(e, i0, i1, s, lo, rate) {
  let n = 0, sx = 0, sy = 0, sxx = 0, sxy = 0;
  for (let i = Math.max(0, i0); i < i1 && i < e.length; i++) {
    const d = e[i] - s;
    if (d <= lo) continue;
    const t = i / rate, y = Math.log(d);
    n++; sx += t; sy += y; sxx += t * t; sxy += t * y;
  }
  if (n < 3) return NaN;
  const den = n * sxx - sx * sx;
  if (den === 0) return NaN;
  const slope = (n * sxy - sx * sy) / den;
  return slope >= 0 ? NaN : -1 / slope;
}

// TRLC-LINKS: REQ-SDS-268
function sndFlatStretch(e, from, fs) {
  let bs = 0, be = 0;
  const w = Math.max(4, Math.floor(0.02 * fs));
  for (let s = from; s + w <= e.length; s += w) {
    const lvl = sndMedian(e.slice(s, s + w));
    if (lvl < 0.03) continue;
    const band = Math.max(0.05, 0.2 * lvl);
    let end = s;
    while (end < e.length && Math.abs(e[end] - lvl) <= band) end++;
    if (end - s > be - bs) { bs = s; be = end; }
  }
  return [bs, be];
}

// TRLC-LINKS: REQ-SDS-268
function sndADSR(env, fs, gateOn, gateOff) {
  const r = { ok: false, why: "", start: NaN, peakAt: NaN, decayEnd: NaN, releaseAt: NaN, end: NaN, floor: 0, peak: 0,
    attack1090: NaN, attack0100: NaN, decay: NaN, decayTau: NaN, sustain: NaN, sustainPct: NaN, sustainDB: NaN,
    hasSustain: false, release: NaN, releaseTau: NaN };
  const n = env.length;
  if (n < 8 || !(fs > 0)) { r.why = "too short"; return r; }
  const low = sndPercentile(env, 0.05);
  let pk = env[0], pi = 0;
  for (let i = 0; i < n; i++) if (env[i] > pk) { pk = env[i]; pi = i; }
  r.floor = low; r.peak = pk;
  if (!(pk - low > 1e-12) || pk - low < 0.05 * Math.abs(pk)) { r.why = "no envelope: the level hardly moves"; return r; }
  const e = new Float64Array(n);
  for (let i = 0; i < n; i++) e[i] = (env[i] - low) / (pk - low);
  let start = -1;
  if (gateOn >= 0) { start = gateOn * fs; if (start >= pi) start = -1; }
  if (start < 0) {
    let c10 = sndCrossUp(e, 0, pi + 1, 0.1);
    if (c10 < 0) c10 = pi;
    start = 0;
    for (let i = Math.floor(c10); i >= 0; i--) if (e[i] <= 0.02) { start = i; break; }
  }
  for (let i = Math.trunc(start); i < pi; i++) if (e[i] >= 0.995) { pi = i; break; }
  r.start = start / fs; r.peakAt = pi / fs; r.attack0100 = (pi - start) / fs;
  const a10 = sndCrossUp(e, Math.trunc(start), pi + 1, 0.1), a90 = sndCrossUp(e, Math.trunc(start), pi + 1, 0.9);
  if (a10 >= 0 && a90 >= a10) r.attack1090 = (a90 - a10) / fs;
  let rel = -1, sus = 0;
  if (gateOff >= 0 && gateOff * fs > pi) {
    rel = Math.min(n - 1, Math.round(gateOff * fs));
    const lo = pi + Math.trunc((rel - pi) * 3 / 4);
    sus = sndMedian(Array.from(e.subarray(lo, rel + 1)));
    r.hasSustain = sus > 0.03;
  } else {
    const [bs, be] = sndFlatStretch(e, pi, fs);
    if (be > bs && be - bs >= 0.1 * fs) {
      sus = sndMedian(Array.from(e.subarray(bs, be)));
      r.hasSustain = sus > 0.03;
      if (be < n) rel = be; // a sustain held to the capture's end has no release in it
    }
  }
  if (!r.hasSustain) sus = 0;
  r.sustain = low + sus * (pk - low);
  r.sustainPct = 100 * sus;
  r.sustainDB = 20 * Math.log10(Math.max(sus, 1e-6));
  if (r.hasSustain) {
    let dEnd = sndCrossDown(e, pi, n, sus + 0.1 * (1 - sus));
    if (rel >= 0) { dEnd = Math.min(dEnd, rel); if (dEnd < 0) dEnd = rel; }
    if (dEnd >= 0) {
      r.decayEnd = dEnd / fs; r.decay = (dEnd - pi) / fs;
      r.decayTau = sndExpTau(e, pi, Math.trunc(dEnd) + 1, sus, 0.05 * (1 - sus), fs);
    }
    if (rel < 0) { r.ok = true; return r; }
    r.releaseAt = rel / fs;
    const lr = gateOff < 0 ? sus : e[rel];
    const end = sndCrossDown(e, rel, n, 0.1 * lr);
    if (end >= 0) {
      r.end = end / fs; r.release = (end - rel) / fs;
      r.releaseTau = sndExpTau(e, rel, Math.trunc(end) + 1, 0, 0.05 * lr, fs);
    }
    r.ok = true;
    return r;
  }
  const end = sndCrossDown(e, pi, n, 0.1);
  if (end >= 0) {
    r.decayEnd = end / fs; r.end = end / fs; r.decay = (end - pi) / fs;
    r.decayTau = sndExpTau(e, pi, Math.trunc(end) + 1, 0, 0.05, fs);
  }
  if (rel >= 0) r.releaseAt = rel / fs;
  r.ok = true;
  return r;
}

// ---- modulation ----
// TRLC-LINKS: REQ-SDS-269
function sndDemod(x, fs, fc, outRate) {
  const d = { fs: 0, fc, amp: [], freq: [] };
  const n = x.length;
  if (n < 16 || !(fs > 0) || !(fc > 0) || fc >= fs / 2) return d;
  const p1 = Math.max(1, Math.floor(fs / fc)), p2 = Math.max(1, Math.ceil(fs / fc));
  const dec = Math.max(1, Math.round(fs / Math.max(outRate, 1)));
  d.fs = fs / dec;
  const m = sndMean(x), w = 2 * Math.PI * fc / fs, c = Math.cos(w), s = Math.sin(w);
  let ck = 1, sk = 0;
  const cr = new Float64Array(n + 1), ci = new Float64Array(n + 1);
  for (let i = 0; i < n; i++) {
    const v = x[i] - m;
    cr[i + 1] = cr[i] + v * ck; ci[i + 1] = ci[i] - v * sk;
    const nc = ck * c - sk * s; sk = sk * c + ck * s; ck = nc;
    if ((i & 1023) === 1023) { const a = w * (i + 1); ck = Math.cos(a); sk = Math.sin(a); }
  }
  const b1r = new Float64Array(n + 1), b1i = new Float64Array(n + 1);
  for (let i = p1 - 1; i < n; i++) {
    b1r[i + 1] = b1r[i] + (cr[i + 1] - cr[i + 1 - p1]) / p1;
    b1i[i + 1] = b1i[i] + (ci[i + 1] - ci[i + 1 - p1]) / p1;
  }
  let prev = 0, have = false;
  for (let i = p1 - 1 + p2 - 1; i < n; i += dec) {
    const zr = (b1r[i + 1] - b1r[i + 1 - p2]) / p2, zi = (b1i[i + 1] - b1i[i + 1 - p2]) / p2;
    d.amp.push(2 * Math.hypot(zr, zi));
    const ph = Math.atan2(zi, zr);
    d.freq.push(have ? fc + sndWrapPi(ph - prev) * d.fs / (2 * Math.PI) : fc);
    prev = ph; have = true;
  }
  if (d.freq.length > 1) d.freq[0] = d.freq[1];
  return d;
}

// TRLC-LINKS: REQ-SDS-269
function sndSmooth(x, h) {
  const out = new Float64Array(x.length), cum = new Float64Array(x.length + 1);
  for (let i = 0; i < x.length; i++) cum[i + 1] = cum[i] + x[i];
  for (let i = 0; i < x.length; i++) {
    const a = Math.max(0, i - h), b = Math.min(x.length, i + h + 1);
    out[i] = (cum[b] - cum[a]) / (b - a);
  }
  return out;
}

// TRLC-LINKS: REQ-SDS-269
function sndModRate(x, fs) {
  if (x.length < 4 || !(fs > 0)) return [0, 0];
  const m = sndMean(x), h = 0.1 * (sndPercentile(x, 0.95) - sndPercentile(x, 0.05)) / 2;
  if (!(h > 0)) return [0, 0];
  let armed = false, first = -1, last = -1, n = 0;
  for (let i = 1; i < x.length; i++) {
    if (x[i] < m - h) armed = true;
    if (armed && x[i - 1] < m && x[i] >= m) {
      const t = i - 1 + (m - x[i - 1]) / (x[i] - x[i - 1]);
      if (first < 0) first = t;
      last = t; n++; armed = false;
    }
  }
  if (n < 2 || !(last > first)) return [0, 0];
  return [(n - 1) * fs / (last - first), n - 1];
}

// TRLC-LINKS: REQ-SDS-269
function sndShape(x, fs, rate) {
  if (!(rate > 0) || x.length < 8) return "";
  const h = [0, 0, 0, 0];
  for (let k = 1; k <= 3; k++) {
    if (k * rate >= fs / 2) break;
    const [re, im] = sndToneAt(x, k * rate, fs);
    h[k] = Math.hypot(re, im);
  }
  if (!(h[1] > 0)) return "";
  const r2 = h[2] / h[1], r3 = h[3] / h[1];
  if (r2 > 0.3) return "saw";
  if (r3 > 0.22) return "square";
  if (r3 > 0.06) return "triangle";
  return "sine";
}

// TRLC-LINKS: REQ-SDS-269
function sndRms(x) { if (!x || !x.length) return 0; let s = 0; for (let i = 0; i < x.length; i++) s += x[i] * x[i]; return Math.sqrt(s / x.length); }

// TRLC-LINKS: REQ-SDS-269
function sndRbj(kind, f0, q, fs) {
  const w = 2 * Math.PI * f0 / fs, cw = Math.cos(w), sw = Math.sin(w), al = sw / (2 * q), a0 = 1 + al;
  let b;
  if (kind === "hp") b = [(1 + cw) / 2, -(1 + cw), (1 + cw) / 2, -2 * cw, 1 - al];
  else if (kind === "lp") b = [(1 - cw) / 2, 1 - cw, (1 - cw) / 2, -2 * cw, 1 - al];
  else b = [al, 0, -al, -2 * cw, 1 - al];
  return b.map((v) => v / a0);
}

// TRLC-LINKS: REQ-SDS-269
function sndBiquad(b, x) {
  const out = new Float64Array(x.length);
  let x1 = 0, x2 = 0, y1 = 0, y2 = 0;
  for (let i = 0; i < x.length; i++) {
    const v = x[i], y = b[0] * v + b[1] * x1 + b[2] * x2 - b[3] * y1 - b[4] * y2;
    x2 = x1; x1 = v; y2 = y1; y1 = y;
    out[i] = y;
  }
  return out;
}

// TRLC-LINKS: REQ-SDS-269
function sndBandPass(x, fs, lo, hi) {
  if (!(hi > lo) || hi >= fs / 2) return null;
  const y = sndBiquad(sndRbj("lp", hi, Math.SQRT1_2, fs), sndBiquad(sndRbj("hp", lo, Math.SQRT1_2, fs), x));
  return y.subarray(Math.min(Math.floor(y.length / 2), Math.trunc(fs / lo)));
}

// TRLC-LINKS: REQ-SDS-269
function sndWeightWF(x, fs) {
  if (fs < 20) return null;
  const y = sndBiquad(sndRbj("bp", 4, 0.6, fs), x);
  return y.subarray(Math.min(Math.floor(y.length / 2), Math.trunc(fs / 2)));
}

// TRLC-LINKS: REQ-SDS-269
function sndModulation(d) {
  const m = { ok: false, why: "", fc: 0, hasAM: false, amPct: 0, amDB: 0, amRate: 0, amShape: "", amMin: 0, amMax: 0,
    hasFM: false, fmHz: 0, fmCents: 0, fmRate: 0, fmShape: "", wowPct: 0, flutterPct: 0, wfPct: 0, seconds: 0,
    amCycles: 0, fmCycles: 0, freqMin: 0, freqMax: 0 };
  const n = d.amp.length;
  if (n < 32 || !(d.fs > 0)) { m.why = "too short"; return m; }
  const trim = Math.trunc(0.03 * n);
  const a = sndSmooth(d.amp.slice(trim, n - trim), 2), f = sndSmooth(d.freq.slice(trim, n - trim), 2);
  m.seconds = a.length / d.fs;
  const lo = sndPercentile(a, 0.01), hi = sndPercentile(a, 0.99);
  if (!(hi > 0)) { m.why = "no tone"; return m; }
  m.amMin = lo; m.amMax = hi;
  m.amPct = 100 * (hi - lo) / (hi + lo);
  m.amDB = 20 * Math.log10(hi / Math.max(lo, 1e-12));
  m.fc = sndMean(f);
  const flo = sndPercentile(f, 0.01), fhi = sndPercentile(f, 0.99);
  m.freqMin = flo; m.freqMax = fhi;
  m.fmHz = (fhi - flo) / 2;
  if (flo > 0) m.fmCents = 600 * Math.log2(fhi / flo);
  m.hasAM = m.amPct >= 2;
  m.hasFM = m.fmCents >= 2;
  if (m.hasAM) { [m.amRate, m.amCycles] = sndModRate(a, d.fs); m.amShape = sndShape(a, d.fs, m.amRate); }
  if (m.hasFM) { [m.fmRate, m.fmCycles] = sndModRate(f, d.fs); m.fmShape = sndShape(f, d.fs, m.fmRate); }
  if (m.fc > 0) {
    const rel = new Float64Array(f.length);
    for (let i = 0; i < f.length; i++) rel[i] = (f[i] - m.fc) / m.fc;
    m.wowPct = 100 * sndRms(sndBandPass(rel, d.fs, 0.5, 6));
    m.flutterPct = 100 * sndRms(sndBandPass(rel, d.fs, 6, Math.min(100, 0.45 * d.fs)));
    m.wfPct = 200 * sndRms(sndWeightWF(rel, d.fs));
  }
  m.ok = true;
  return m;
}

// TRLC-LINKS: REQ-SDS-269, REQ-SDS-270
function sndFindLines(db, below) {
  let top = -Infinity;
  for (let k = 1; k < db.length; k++) top = Math.max(top, db[k]);
  const out = [];
  for (let k = 2; k < db.length - 2; k++) {
    const v = db[k];
    if (v < top - below || v < db[k - 1] || v < db[k + 1] || v < db[k - 2] || v < db[k + 2] || v === db[k - 1]) continue;
    let off = 0;
    const den = db[k - 1] - 2 * v + db[k + 1];
    if (den < 0) off = 0.5 * (db[k - 1] - db[k + 1]) / den;
    out.push({ bin: k + off, amp: Math.pow(10, v / 20) / sndHannGain(off) });
  }
  return out;
}

// TRLC-LINKS: REQ-SDS-269
function sndBesselJ(nmax, x) {
  const out = new Float64Array(nmax + 1);
  if (!(x > 0)) { out[0] = 1; return out; }
  const top = Math.max(nmax, Math.trunc(x));
  const m = 2 * Math.trunc((top + 16 + Math.trunc(Math.sqrt(40 * top))) / 2);
  let jp1 = 0, j = 1e-30, sum = 0;
  for (let k = m; k >= 1; k--) {
    const jm1 = 2 * k / x * j - jp1;
    jp1 = j; j = jm1;
    if (k - 1 <= nmax) out[k - 1] = j;
    if ((k - 1) % 2 === 0 && k - 1 > 0) sum += 2 * j;
    if (Math.abs(j) > 1e10) {
      j *= 1e-10; jp1 *= 1e-10; sum *= 1e-10;
      for (let i = k - 1; i <= nmax; i++) out[i] *= 1e-10;
    }
  }
  sum += j;
  for (let i = 0; i <= nmax; i++) out[i] /= sum;
  return out;
}

// TRLC-LINKS: REQ-SDS-269
function sndRatioText(r) {
  if (!(r > 0) || !Number.isFinite(r)) return "";
  for (let q = 1; q <= 12; q++) {
    const p = Math.round(r * q);
    if (p >= 1 && Math.abs(p / q - r) <= 0.001 * r) return p + ":" + q;
  }
  return String(Number(r.toPrecision(3)));
}

// TRLC-LINKS: REQ-SDS-269
function sndAmpAt(lines, bin) {
  let a = 0;
  for (const l of lines) if (Math.abs(l.bin - bin) <= Math.max(1.5, 0.01 * bin) && l.amp > a) a = l.amp;
  return a;
}

// TRLC-LINKS: REQ-SDS-269
function sndBesselErr(beta, ks, amps, norm) {
  const j = sndBesselJ(10, beta);
  let jn = 0;
  for (const k of ks) { const v = j[Math.abs(k)]; jn += v * v; }
  jn = Math.sqrt(jn);
  let e = 0;
  for (let i = 0; i < ks.length; i++) { const d = amps[i] / norm - Math.abs(j[Math.abs(ks[i])]) / Math.max(jn, 1e-12); e += d * d; }
  return e;
}

// SND_SB_MAX_LINES bounds the lines the sideband search weighs (Go's
// sbMaxLines: the search is quadratic in them).
const SND_SB_MAX_LINES = 64;

// sndStrongestLines keeps the n strongest lines, in bin order.
// TRLC-LINKS: REQ-SDS-269
function sndStrongestLines(l, n) {
  if (l.length <= n) return l;
  const c = l.slice().sort((a, b) => b.amp - a.amp).slice(0, n);
  return c.sort((a, b) => a.bin - b.bin);
}

// TRLC-LINKS: REQ-SDS-269
function sndSidebands(db, binHz) {
  const sb = { ok: false, why: "", fc: 0, fm: 0, ratio: "", beta: 0, fit: 0, lines: 0 };
  const lines = sndStrongestLines(sndFindLines(db, 40), SND_SB_MAX_LINES);
  if (lines.length < 3) { sb.why = "fewer than three lines"; return sb; }
  const gaps = [];
  for (let i = 1; i < lines.length; i++) gaps.push(lines[i].bin - lines[i - 1].bin);
  gaps.sort((p, q) => p - q);
  let fmBins = gaps[0], best = -1;
  for (const g of gaps) {
    if (g < 1.5) continue;
    let cnt = 0;
    for (const h of gaps) { const k = Math.round(h / g); if (k >= 1 && Math.abs(h - k * g) <= Math.max(1.5, 0.03 * h)) cnt++; }
    if (cnt > best) { best = cnt; fmBins = g; }
  }
  if (best < 2) { sb.why = "no common line spacing"; return sb; }
  let carrier = -1, bestScore = Infinity;
  for (let i = 0; i < lines.length; i++) {
    const l = lines[i];
    let num = 0, den = 0, pairs = 0;
    for (let k = 1; k <= 10; k++) {
      const lo = l.bin - k * fmBins, hi = l.bin + k * fmBins;
      if (lo < fmBins / 2 || hi >= db.length - 2) break;
      const a = sndAmpAt(lines, lo), b = sndAmpAt(lines, hi);
      if (a === 0 && b === 0) continue;
      num += Math.abs(a - b); den += a + b; pairs++;
    }
    if (pairs === 0 || den === 0) continue;
    const score = num / den;
    if (score < bestScore - 1e-9 || (Math.abs(score - bestScore) <= 1e-9 && l.amp > lines[carrier].amp)) { carrier = i; bestScore = score; }
  }
  if (carrier < 0) { sb.why = "no line with sidebands either side"; return sb; }
  const fc = lines[carrier].bin, amps = [], ks = [];
  let norm = 0;
  for (let k = -10; k <= 10; k++) {
    const b = fc + k * fmBins;
    if (b < fmBins / 2 || b >= db.length - 2) continue;
    const a = sndAmpAt(lines, b);
    amps.push(a); ks.push(k); norm += a * a;
  }
  norm = Math.sqrt(norm);
  if (!(norm > 0)) { sb.why = "no lines"; return sb; }
  let bb = 0, be = Infinity;
  for (let i = 0; i <= 240; i++) {
    const b = i * 0.05;
    const e = sndBesselErr(b, ks, amps, norm);
    if (e < be) { bb = b; be = e; }
  }
  let lo = Math.max(0, bb - 0.05), hi = bb + 0.05;
  for (let it = 0; it < 40; it++) {
    const m1 = hi - (hi - lo) * 0.618, m2 = lo + (hi - lo) * 0.618;
    if (sndBesselErr(m1, ks, amps, norm) < sndBesselErr(m2, ks, amps, norm)) hi = m2; else lo = m1;
  }
  sb.beta = (lo + hi) / 2;
  sb.fit = Math.max(0, 1 - Math.sqrt(sndBesselErr(sb.beta, ks, amps, norm)));
  sb.fc = fc * binHz; sb.fm = fmBins * binHz;
  sb.ratio = sndRatioText(sb.fc / sb.fm);
  sb.lines = amps.filter((a) => a > 0).length;
  sb.ok = true;
  return sb;
}

// TRLC-LINKS: REQ-SDS-269
function sndLFO(x, fs) {
  const l = { ok: false, why: "", rateHz: 0, cycles: 0, min: 0, max: 0, vpp: 0, mean: 0, risePct: 0, shape: "" };
  if (x.length < 16 || !(fs > 0)) { l.why = "too short"; return l; }
  if (fs > 2000) { const b = sndBlockMeans(x, fs, 1 / 1000); x = b.env; fs = b.fs; }
  l.min = sndPercentile(x, 0.005); l.max = sndPercentile(x, 0.995);
  l.vpp = l.max - l.min; l.mean = sndMean(x);
  if (!(l.vpp > 0)) { l.why = "flat"; return l; }
  [l.rateHz, l.cycles] = sndModRate(x, fs);
  if (l.cycles < 1) { l.why = "less than a cycle"; return l; }
  l.shape = sndShape(x, fs, l.rateHz);
  const per = fs / l.rateHz, shares = [];
  for (let s = 0; s + per <= x.length; s += per) {
    const a = Math.trunc(s), b = Math.min(x.length, Math.trunc(s + per));
    let lo = a, hi = a;
    for (let i = a; i < b; i++) { if (x[i] < x[lo]) lo = i; if (x[i] > x[hi]) hi = i; }
    let d = hi - lo;
    if (d < 0) d += per;
    shares.push(100 * d / per);
  }
  if (shares.length) l.risePct = sndMedian(shares);
  l.ok = true;
  return l;
}

// ---- audio spectrum ----
// TRLC-LINKS: REQ-SDS-270
function sndSpectrum(x, fs, n) {
  n = sndFloorPow2(Math.min(n, x.length));
  if (n < 64 || !(fs > 0)) return { n: 0, binHz: 0, dbv: [] };
  const off = x.length - n, re = new Float64Array(n), im = new Float64Array(n);
  let m = 0;
  for (let i = 0; i < n; i++) m += x[off + i];
  m /= n;
  for (let i = 0; i < n; i++) re[i] = (x[off + i] - m) * sndHann(i, n);
  sndFFT(re, im, false);
  const dbv = new Float64Array(n / 2), k = 4 / n / Math.SQRT2;
  for (let i = 0; i < n / 2; i++) dbv[i] = 20 * Math.log10(Math.hypot(re[i], im[i]) * k + 1e-12);
  return { n, binHz: fs / n, dbv };
}

// TRLC-LINKS: REQ-SDS-270
function sndLineAt(sp, f, tol) {
  const c = f / sp.binHz, lo = Math.max(1, Math.floor(c - tol)), hi = Math.min(sp.dbv.length - 2, Math.ceil(c + tol));
  if (hi < lo) return [0, -Infinity, -1];
  let k = lo;
  for (let i = lo; i <= hi; i++) if (sp.dbv[i] > sp.dbv[k]) k = i;
  let off = 0;
  if (k > 0 && k < sp.dbv.length - 1) {
    const a = sp.dbv[k - 1], b = sp.dbv[k], cc = sp.dbv[k + 1], den = a - 2 * b + cc;
    if (den < 0) off = Math.max(-0.5, Math.min(0.5, 0.5 * (a - cc) / den));
  }
  return [(k + off) * sp.binHz, sp.dbv[k] - 20 * Math.log10(sndHannGain(off)), k];
}

// TRLC-LINKS: REQ-SDS-270
function sndLocalFloor(sp, k) {
  const lo = Math.max(1, Math.trunc(k * 0.8)), hi = Math.min(sp.dbv.length - 1, Math.trunc(k * 1.25) + 1), v = [];
  for (let i = lo; i <= hi; i++) if (Math.abs(i - k) > 3) v.push(sp.dbv[i]);
  return v.length ? sndMedian(v) : -Infinity;
}

// TRLC-LINKS: REQ-SDS-270
function sndHum(sp) {
  let best = { found: false, mainHz: 0, dbv: -Infinity, sumDBV: -Infinity };
  if (sp.dbv.length < 16 || sp.binHz > 10) return best;
  for (const mains of [50, 60]) {
    const h = { found: false, mainHz: mains, dbv: -Infinity, sumDBV: -Infinity };
    let sum = 0;
    for (let n = 1; n <= 3; n++) {
      const [, db, k] = sndLineAt(sp, n * mains, Math.max(1, 1.5 / sp.binHz));
      if (k < 0 || db <= sndLocalFloor(sp, k) + 6) continue;
      h.found = true;
      if (n === 1) h.dbv = db;
      sum += Math.pow(10, db / 10);
    }
    if (sum > 0) h.sumDBV = 10 * Math.log10(sum);
    if (h.found && h.sumDBV > best.sumDBV) best = h;
  }
  return best;
}

// TRLC-LINKS: REQ-SDS-270
function sndHarmonics(sp, f0) {
  const r = { ok: false, f0: 0, h: [], thdPct: NaN, thdDB: NaN, evenDB: -Infinity, oddDB: -Infinity, evenOdd: NaN,
    hum: { found: false, mainHz: 0, dbv: -Infinity, sumDBV: -Infinity }, floorDBV: 0, character: "" };
  if (sp.dbv.length < 16) return r;
  r.floorDBV = sndMedian(Array.from(sp.dbv).slice(1));
  if (!(f0 > 0)) {
    const k0 = Math.max(1, Math.ceil(15 / sp.binHz));
    let best = k0;
    for (let k = k0; k < sp.dbv.length - 1; k++) if (sp.dbv[k] > sp.dbv[best]) best = k;
    f0 = sndLineAt(sp, best * sp.binHz, 1)[0];
  }
  const nyq = sp.binHz * sp.dbv.length;
  if (!(f0 > 0) || f0 >= nyq) return r;
  r.f0 = f0;
  let p1 = 0, even = 0, odd = 0, all = 0;
  for (let n = 1; n <= 10; n++) {
    const h = { n, hz: n * f0, dbv: -Infinity, relDB: -Infinity, found: false };
    if (h.hz < nyq * 0.98) {
      const [hz, db, k] = sndLineAt(sp, h.hz, Math.max(2, 0.005 * h.hz / sp.binHz));
      if (k >= 0) { h.hz = hz; h.dbv = db; h.found = db > sndLocalFloor(sp, k) + 6; }
    }
    if (n > 1 && h.dbv < r.h[0].dbv - 90) h.found = false;
    r.h.push(h);
    if (n === 1) {
      if (!h.found) return r;
      p1 = Math.pow(10, h.dbv / 10);
      continue;
    }
    h.relDB = h.dbv - r.h[0].dbv;
    if (!h.found) continue;
    const p = Math.pow(10, h.dbv / 10) / p1;
    all += p;
    if (n % 2 === 0) even += p; else odd += p;
  }
  r.h[0].relDB = 0;
  r.f0 = r.h[0].hz;
  r.ok = true;
  if (all > 0) { r.thdPct = 100 * Math.sqrt(all); r.thdDB = 10 * Math.log10(all); }
  if (even > 0) r.evenDB = 10 * Math.log10(even);
  if (odd > 0) r.oddDB = 10 * Math.log10(odd);
  if (even > 0 || odd > 0) {
    r.evenOdd = r.evenDB - r.oddDB;
    if (even > 0 && (odd === 0 || r.evenOdd > 6)) r.character = "even (asymmetric)";
    else if (odd > 0 && (even === 0 || r.evenOdd < -6)) r.character = "odd (symmetric)";
    else r.character = "mixed";
  }
  r.hum = sndHum(sp);
  return r;
}

// TRLC-LINKS: REQ-SDS-270
function sndFloorDBV(binHz, vpc) { return binHz > 0 && vpc > 0 ? 20 * Math.log10(SND_FLOOR_CODES * vpc * Math.sqrt(1.5 * binHz)) : -Infinity; }

// TRLC-LINKS: REQ-SDS-270
function sndLogX(f, f0, f1) { return f > 0 ? Math.log(f / f0) / Math.log(f1 / f0) : -Infinity; }

// TRLC-LINKS: REQ-SDS-270
function sndNoteGrid(f0, f1, a4) {
  if (!(a4 > 0)) a4 = 440;
  const out = [];
  for (let oct = -1; oct <= 10; oct++) {
    const f = a4 * Math.pow(2, (12 * (oct + 1) - 69) / 12);
    if (f >= f0 && f <= f1) out.push({ f, name: "C" + oct });
  }
  return out;
}

// ---- A/B ----
// TRLC-LINKS: REQ-SDS-271
function sndDelay(a, b, fs, maxS) {
  const n = Math.min(a.length, b.length, 65536);
  if (n < 64) return [0, false];
  const oa = a.length - n, ob = b.length - n, size = sndPow2(2 * n);
  const ar = new Float64Array(size), ai = new Float64Array(size), br = new Float64Array(size), bi = new Float64Array(size);
  let ma = 0, mb = 0;
  for (let i = 0; i < n; i++) { ma += a[oa + i]; mb += b[ob + i]; }
  ma /= n; mb /= n;
  for (let i = 0; i < n; i++) { const w = sndHann(i, n); ar[i] = (a[oa + i] - ma) * w; br[i] = (b[ob + i] - mb) * w; }
  sndFFT(ar, ai, false);
  sndFFT(br, bi, false);
  for (let k = 0; k < size; k++) {
    const re = ar[k] * br[k] + ai[k] * bi[k], im = ar[k] * bi[k] - ai[k] * br[k];
    ar[k] = re; ai[k] = im;
  }
  sndFFT(ar, ai, true);
  const lim = Math.min(n - 1, Math.trunc(maxS * fs));
  let best = 0, bv = -Infinity;
  for (let l = -lim; l <= lim; l++) { const v = ar[l < 0 ? l + size : l]; if (v > bv) { best = l; bv = v; } }
  if (!(bv > 0)) return [0, false];
  let off = 0;
  if (best > -lim && best < lim) {
    const p = ar[best - 1 < 0 ? best - 1 + size : best - 1], q = ar[best + 1 < 0 ? best + 1 + size : best + 1], den = p - 2 * bv + q;
    if (den < 0) off = 0.5 * (p - q) / den;
  }
  return [(best + off) / fs, true];
}

// TRLC-LINKS: REQ-SDS-271
function sndCompareAB(a, b, fs, f0) {
  const r = { ok: false, why: "", f0: 0, gainDB: 0, phaseDeg: 0, rmsGainDB: 0, delayS: 0, delayOK: false,
    thdA: NaN, thdB: NaN, addedPct: NaN, addedDB: NaN, match: 0 };
  const n = Math.min(a.length, b.length);
  if (n < 256 || !(fs > 0)) { r.why = "too short"; return r; }
  a = a.slice(a.length - n); b = b.slice(b.length - n);
  if (!(f0 > 0)) {
    const p = sndPitch(a.slice(n - Math.min(n, 8192)), fs);
    if (!p.ok) { r.why = "no pitch on the input"; return r; }
    f0 = p.hz;
  }
  r.f0 = f0;
  const [ar, ai] = sndToneAt(a, f0, fs), [br, bi] = sndToneAt(b, f0, fs);
  const ma = Math.hypot(ar, ai), mb = Math.hypot(br, bi);
  if (!(ma > 0)) { r.why = "no input at the fundamental"; return r; }
  r.match = mb / ma;
  r.gainDB = 20 * Math.log10(Math.max(mb, 1e-15) / ma);
  r.phaseDeg = Math.atan2(bi * ar - br * ai, br * ar + bi * ai) * 180 / Math.PI;
  const la = sndMeasure(a), lb = sndMeasure(b);
  if (la.rms > 0 && lb.rms > 0) r.rmsGainDB = 20 * Math.log10(lb.rms / la.rms);
  [r.delayS, r.delayOK] = sndDelay(a, b, fs, 0.02);
  const nn = Math.min(n, 16384);
  const ha = sndHarmonics(sndSpectrum(a, fs, nn), f0), hb = sndHarmonics(sndSpectrum(b, fs, nn), f0);
  r.thdA = ha.thdPct; r.thdB = hb.thdPct;
  let ta = ha.thdPct, tb = hb.thdPct;
  if (Number.isNaN(ta) && hb.ok) ta = 0;
  if (Number.isNaN(tb) && hb.ok) tb = 0;
  r.addedPct = tb - ta;
  if (ta > 0 && tb > 0) r.addedDB = 20 * Math.log10(tb / ta);
  r.ok = true;
  return r;
}

// ---- V/oct and drift ----
// TRLC-LINKS: REQ-SDS-267
function sndLineFit(ps) {
  let n = 0, sx = 0, sy = 0, sxx = 0, sxy = 0;
  for (const p of ps) { const y = Math.log2(p.hz); n++; sx += p.v; sy += y; sxx += p.v * p.v; sxy += p.v * y; }
  const den = n * sxx - sx * sx;
  if (den === 0) return [0, sy / n];
  const slope = (n * sxy - sx * sy) / den;
  return [slope, (sy - slope * sx) / n];
}

// TRLC-LINKS: REQ-SDS-267
function sndFitVOct(pts, a4) {
  const f = { ok: false, rows: [], octPerV: 0, scaleCents: 0, trimPct: 0, hfDevCents: 0, spanOctaves: 0 };
  const ps = pts.filter((p) => p.hz > 0 && Number.isFinite(p.v) && Number.isFinite(p.hz)).sort((p, q) => p.v - q.v);
  if (!ps.length) return f;
  const v0 = ps[0].v, h0 = ps[0].hz;
  for (const p of ps) {
    const ideal = h0 * Math.pow(2, p.v - v0);
    f.rows.push({ v: p.v, hz: p.hz, note: sndNote(p.hz, a4).text, errCents: sndCents(p.hz, ideal), residCents: 0 });
  }
  if (ps.length < 2 || !(ps[ps.length - 1].v - ps[0].v > 0.05)) return f;
  const [slope, icpt] = sndLineFit(ps);
  f.octPerV = slope;
  f.scaleCents = 1200 * (slope - 1);
  if (slope > 0) f.trimPct = 100 * (1 / slope - 1);
  for (let i = 0; i < ps.length; i++) f.rows[i].residCents = 1200 * (Math.log2(ps[i].hz) - (icpt + slope * ps[i].v));
  f.spanOctaves = Math.log2(ps[ps.length - 1].hz / ps[0].hz);
  if (ps.length >= 4) {
    const [s2, i2] = sndLineFit(ps.slice(0, Math.trunc((ps.length + 1) / 2)));
    const top = ps[ps.length - 1];
    f.hfDevCents = 1200 * (Math.log2(top.hz) - (i2 + s2 * top.v));
  }
  f.ok = true;
  return f;
}

// TRLC-LINKS: REQ-SDS-267
function sndVOctAuto(holdS, stepV) { return { holdS: holdS || 0.5, stepV: stepV || 0.05, hist: [], lastV: 0, have: false }; }

// TRLC-LINKS: REQ-SDS-267
function sndVOctAutoAdd(a, t, cv, hz) {
  if (!(hz > 0)) { a.hist = []; return null; }
  a.hist.push({ t, v: cv, hz });
  while (a.hist.length > 1 && t - a.hist[0].t > a.holdS) a.hist.shift();
  if (t - a.hist[0].t < a.holdS * 0.8 || a.hist.length < 3) return null;
  let vlo = a.hist[0].v, vhi = vlo, flo = a.hist[0].hz, fhi = flo, sv = 0, sf = 0;
  for (const s of a.hist) { vlo = Math.min(vlo, s.v); vhi = Math.max(vhi, s.v); flo = Math.min(flo, s.hz); fhi = Math.max(fhi, s.hz); sv += s.v; sf += s.hz; }
  if (vhi - vlo > 0.006 || sndCents(fhi, flo) > 4) return null;
  const p = { v: sv / a.hist.length, hz: sf / a.hist.length };
  if (a.have && Math.abs(p.v - a.lastV) < a.stepV) return null;
  a.lastV = p.v; a.have = true; a.hist = [];
  return p;
}

// TRLC-LINKS: REQ-SDS-267
function sndDrift() { return { t0: 0, points: [], cur: [], curSec: 0, base: 0 }; }

// TRLC-LINKS: REQ-SDS-267
function sndDriftAdd(d, t, hz) {
  if (!d.points.length && !d.cur.length) { d.t0 = t; d.curSec = 0; }
  const sec = Math.floor(t - d.t0);
  if (sec !== d.curSec && d.cur.length) {
    const m = sndMedian(d.cur);
    if (d.base === 0) d.base = m;
    d.points.push({ t: d.curSec, hz: m, cents: sndCents(m, d.base) });
    if (d.points.length > 7200) d.points.splice(0, d.points.length - 7200);
    d.cur = [];
  }
  d.curSec = sec;
  if (hz > 0) d.cur.push(hz);
}

// ---- cycle ----
// TRLC-LINKS: REQ-SDS-272
function sndPlaceMark(x, mark, level, rising) {
  let best = -1, bd = Infinity;
  for (let i = Math.max(1, mark - 6); i <= Math.min(x.length - 1, mark + 6); i++) {
    const a = x[i - 1], b = x[i];
    const hit = rising ? a < level && b >= level : a > level && b <= level;
    if (!hit || a === b) continue;
    const t = i - 1 + (level - a) / (b - a), d = Math.abs(t - mark);
    if (d < bd) { best = t; bd = d; }
  }
  return [best, best >= 0];
}

// TRLC-LINKS: REQ-SDS-272
function sndCatmull(x, t) {
  const i = Math.floor(t), f = t - i, L = x.length - 1;
  const p0 = x[Math.max(0, Math.min(L, i - 1))], p1 = x[Math.max(0, Math.min(L, i))];
  const p2 = x[Math.max(0, Math.min(L, i + 1))], p3 = x[Math.max(0, Math.min(L, i + 2))];
  return p1 + 0.5 * f * (p2 - p0 + f * (2 * p0 - 5 * p1 + 4 * p2 - p3 + f * (3 * (p1 - p2) + p3 - p0)));
}

// TRLC-LINKS: REQ-SDS-272
function sndEdgeTime(x, lo, hi, rising) {
  let start = -1;
  for (let i = 1; i < x.length; i++) {
    const a = x[i - 1], b = x[i];
    if (rising) {
      if (a < lo && b >= lo) start = i - 1 + (lo - a) / (b - a);
      if (start >= 0 && a < hi && b >= hi) return i - 1 + (hi - a) / (b - a) - start;
    } else {
      if (a > hi && b <= hi) start = i - 1 + (a - hi) / (a - b);
      if (start >= 0 && a > lo && b <= lo) return i - 1 + (a - lo) / (a - b) - start;
    }
  }
  return NaN;
}

// TRLC-LINKS: REQ-SDS-272
function sndCycles(x, fs, at, period, cycles, points) {
  const c = { ok: false, trace: [], cycles, periodS: 0, min: 0, max: 0, vpp: 0, dutyPct: 0, riseS: NaN, fallS: NaN, slewVus: 0 };
  if (!(period > 1) || cycles < 1 || points < 8 || at < 0 || at + period * cycles > x.length - 2) return c;
  const span = period * cycles;
  c.trace = new Float64Array(points);
  for (let i = 0; i < points; i++) c.trace[i] = sndCatmull(x, at + span * i / (points - 1));
  const seg = x.slice(Math.trunc(at), Math.ceil(at + span) + 1);
  const l = sndMeasure(seg);
  c.min = l.min; c.max = l.max; c.vpp = l.vpp; c.periodS = period / fs;
  const mid = (l.min + l.max) / 2, one = x.slice(Math.trunc(at), Math.ceil(at + period));
  let above = 0;
  for (let i = 0; i < one.length; i++) if (one[i] > mid) above++;
  if (one.length) c.dutyPct = 100 * above / one.length;
  const lo = l.min + 0.1 * l.vpp, hi = l.min + 0.9 * l.vpp;
  c.riseS = sndEdgeTime(seg, lo, hi, true) / fs;
  c.fallS = sndEdgeTime(seg, lo, hi, false) / fs;
  let steep = 0;
  for (let i = 1; i < seg.length; i++) steep = Math.max(steep, Math.abs(seg[i] - seg[i - 1]));
  c.slewVus = steep * fs / 1e6;
  c.ok = true;
  return c;
}

// sndMinMaxColumns reduces x to cols columns of their lowest and highest
// values (Go's MinMaxColumns).
// TRLC-LINKS: REQ-SDS-275
function sndMinMaxColumns(x, cols) {
  const n = x.length;
  if (!n || cols <= 0) return { lo: [], hi: [] };
  const lo = new Float64Array(cols);
  const hi = new Float64Array(cols);
  for (let i = 0; i < cols; i++) {
    const a = Math.min(Math.floor(i * n / cols), n - 1);
    const b = Math.min(Math.max(a + 1, Math.floor((i + 1) * n / cols)), n);
    let l = x[a], h = x[a];
    for (let k = a + 1; k < b; k++) { l = Math.min(l, x[k]); h = Math.max(h, x[k]); }
    lo[i] = l; hi[i] = h;
  }
  return { lo, hi };
}

// sndSpectrogram is cols Hann spectra (dBV, n points each) of x centred at
// even steps across [from, from+span): a take's spectrogram. Each column is
// the half spectrum's bins (binHz apart).
// TRLC-LINKS: REQ-SDS-275
function sndSpectrogram(x, fs, from, span, cols, n) {
  const out = [];
  const re = new Float64Array(n);
  const im = new Float64Array(n);
  const k = 4 / n / Math.SQRT2;
  for (let c = 0; c < cols; c++) {
    const mid = Math.round(from + (c + 0.5) * span / cols), a = mid - n / 2;
    let m = 0, have = 0;
    for (let i = 0; i < n; i++) { const j = a + i; if (j >= 0 && j < x.length) { m += x[j]; have++; } }
    m = have ? m / have : 0;
    for (let i = 0; i < n; i++) { const j = a + i; re[i] = j >= 0 && j < x.length ? (x[j] - m) * sndHann(i, n) : 0; im[i] = 0; }
    sndFFT(re, im, false);
    const db = new Float32Array(n / 2);
    for (let i = 0; i < n / 2; i++) db[i] = 20 * Math.log10(Math.hypot(re[i], im[i]) * k + 1e-12);
    out.push(db);
  }
  return { binHz: fs / n, cols: out };
}

// sndPitchTrack is the pitch at cols points across [from, from+span) of x
// (the tuner's YIN on a window each), 0 where none.
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-267
function sndPitchTrack(x, fs, from, span, cols, range) {
  const n = sndTunerWindow(0, fs) / 2;
  const hz = new Float64Array(cols);
  for (let c = 0; c < cols; c++) {
    const mid = Math.round(from + (c + 0.5) * span / cols), a = Math.max(0, Math.min(x.length - n, mid - n / 2));
    if (a < 0 || x.length < n) continue;
    const p = sndPitch(x.subarray ? x.subarray(a, a + n) : x.slice(a, a + n), fs, sndPitchRange(range || ""));
    hz[c] = p.ok ? p.hz : 0;
  }
  return hz;
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { SND_LOG2, SND_PRESETS, sndRate, sndFFT, sndMeasure, sndClipCodes, sndVolts, sndDB, sndPreset, sndMinMaxColumns, sndSpectrogram, sndPitchTrack,
    sndPitch, sndNote, sndCents, sndTunerWindow, sndPitchRange, sndTuner, sndTunerAdd, sndEnvelope, sndBlockMeans, sndADSR,
    sndDemod, sndModulation, sndModRate, sndShape, sndSidebands, sndBesselJ, sndRatioText, sndLFO, sndSpectrum, sndHarmonics,
    sndHum, sndFloorDBV, sndLogX, sndNoteGrid, sndDelay, sndCompareAB, sndFitVOct, sndVOctAuto, sndVOctAutoAdd, sndDrift,
    sndDriftAdd, sndPlaceMark, sndCycles };
}
