// ENGMODEL-OWNER-UNIT: FU-WEB-DECODE
// decode_rfcodes.js — remote-control encoders, Oregon Scientific OOK, Miller
// coding, Morse code and DCF77, a classic script mirroring decode_rfcodes.go
// step for step. Uses sliceChannel, hex2 and fail from decode.js and
// levelRuns/pspan from decode_pulsewire.js.

// activeMarks mirrors decode_rfcodes.go activeMarks.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function activeMarks(S) {
  const runs = levelRuns(S);
  let longest = -1, idle = 0;
  for (const r of runs) { const d = r.x1 - r.x0; if (d > longest) { longest = d; idle = r.lvl; } }
  const out = [];
  for (const r of runs) {
    if (r.lvl === idle || r.open) continue;
    const k = out.length;
    if (k > 0) out[k - 1].gap = r.x0 - out[k - 1].x1;
    out.push({ x0: r.x0, x1: r.x1, gap: Infinity });
  }
  return { marks: out, high: idle === 0 };
}

// markLead mirrors decode_rfcodes.go lead.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function markLead(m, i) { return i === 0 ? m[0].x0 : m[i - 1].gap; }

// quantile mirrors decode_rfcodes.go quantile.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function quantile(v, q) {
  if (!v.length) return 0;
  const c = Float64Array.from(v).sort(); // numeric without a comparator call
  return c[Math.trunc(q * (c.length - 1))];
}

// rcTrit mirrors decode_rfcodes.go rcTrit.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function rcTrit(e) {
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
  const long = (l, s) => l >= 2 * s && l <= 5 * s;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
  const eq = (a, b) => a >= 0.5 * b && a <= 1.5 * b;
  if (long(e[1], e[0]) && eq(e[2], e[0]) && long(e[3], e[0])) return 0;
  if (long(e[0], e[1]) && eq(e[0], e[2]) && long(e[0], e[3])) return 1;
  if (long(e[1], e[0]) && long(e[2], e[0]) && eq(e[3], e[0])) return 2;
  return -1;
}

// rcBit mirrors decode_rfcodes.go rcBit.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function rcBit(h, l) {
  if (l >= 1.5 * h && l <= 5 * h) return 0;
  if (h >= 1.5 * l && h <= 5 * l) return 1;
  return -1;
}

// decodeRCEncode mirrors decode_rfcodes.go DecodeRCEncode.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function decodeRCEncode(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("rcencode", S.reason);
  const { marks: m, high } = activeMarks(S);
  if (m.length < 25) return fail("rcencode", "too few pulses");
  const short = quantile(m.map((k) => k.x1 - k.x0), 0.25);
  const n = S.n, spans = [], toks = [], units = [];
  const variant = cfg.variant | 0;
  let good = false, frameBits = 0;
  for (let i = 0; i + 25 <= m.length; i++) {
    if (markLead(m, i) < 10 * short) continue;
    const f = m.slice(i, i + 25);
    if (f[24].gap < 10 * short) continue;
    let inner = true;
    for (let k = 0; k < 24; k++) if (f[k].gap >= 10 * short) inner = false;
    if (!inner) continue;
    spans.push(pspan(f[0].x0, f[0].x0, n, "", "sof", 0));
    const trits = [];
    let allTrits = true;
    for (let t = 0; t < 12; t++) {
      const a = f[2 * t], b = f[2 * t + 1];
      trits.push(rcTrit([a.x1 - a.x0, a.gap, b.x1 - b.x0, b.gap]));
      allTrits = allTrits && trits[t] >= 0;
    }
    let bad = false;
    if (variant === 12 || (variant !== 24 && allTrits)) {
      for (let t = 0; t < 12; t++) {
        let txt = "", kind = "data";
        if (trits[t] === 0 || trits[t] === 1) txt = String(trits[t]);
        else if (trits[t] === 2) txt = "F";
        else { txt = "!"; kind = "frame-error"; bad = true; }
        const v = Math.max(0, trits[t]);
        spans.push(pspan(f[2 * t].x0, f[2 * t + 1].x1 + f[2 * t + 1].gap, n, txt, kind, v));
        toks.push(txt); units.push(v);
      }
      if (frameBits === 0) frameBits = 12;
    } else {
      for (let by = 0; by < 3; by++) {
        let v = 0, ok = true;
        for (let k = 0; k < 8; k++) {
          const p = f[8 * by + k];
          let b = rcBit(p.x1 - p.x0, p.gap);
          if (b < 0) { ok = false; b = 0; }
          v |= b << k;
        }
        let txt = hex2(v), kind = "data";
        if (!ok) { txt = "!" + txt; kind = "frame-error"; bad = true; }
        const last = f[8 * by + 7];
        spans.push(pspan(f[8 * by].x0, last.x1 + last.gap, n, txt, kind, v));
        toks.push(txt); units.push(v);
      }
      if (frameBits === 0) frameBits = 24;
    }
    spans.push(pspan(f[24].x0, f[24].x1, n, "SYNC", "stop", 0));
    toks.push("SYNC");
    good = good || !bad;
    i += 24;
  }
  if (!spans.length) return fail("rcencode", "no encoder frame");
  const res = { ok: good, proto: "rcencode", spans, text: toks.join(" "), bytes: units,
    meta: { threshold: S.threshold, inverted: !high, spb: short, frameBits } };
  if (colTimeS > 0 && short > 0) res.meta.baud = Math.round(1 / (short * colTimeS));
  if (!good) res.error = "no whole encoder frame";
  return res;
}

// manchesterHalves mirrors decode_rfcodes.go manchesterHalves.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function manchesterHalves(runs, k, hp) {
  const lv = [], xs = [];
  for (; k < runs.length; k++) {
    const r = runs[k];
    let q = Math.round((r.x1 - r.x0) / hp);
    if (r.open && q > 2) q = 2;
    if (q < 1 || q > 2) break;
    for (let j = 0; j < q; j++) { lv.push(r.lvl); xs.push(r.x0 + j * (r.x1 - r.x0) / q); }
  }
  return { lv, xs };
}

// shortestRunCluster mirrors decode_rfcodes.go shortestRunCluster.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function shortestRunCluster(runs) {
  let minR = Infinity;
  for (const r of runs) if (!r.open && r.x1 - r.x0 < minR) minR = r.x1 - r.x0;
  let sum = 0, cnt = 0;
  for (const r of runs) if (!r.open && r.x1 - r.x0 <= 1.5 * minR) { sum += r.x1 - r.x0; cnt++; }
  return cnt === 0 ? 0 : sum / cnt;
}

// oregonChecksum mirrors decode_rfcodes.go oregonChecksum.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function oregonChecksum(nib, ver) {
  for (const at of [nib.length - 2, nib.length - 4]) {
    if (at < 8) continue;
    let sum = 0;
    for (let i = 0; i < at; i++) sum += nib[i];
    const got = nib[at] | (nib[at + 1] << 4);
    if ((sum & 0xff) === got || (ver === 21 && ((sum - 10) & 0xff) === got)) return at;
  }
  return -1;
}

// decodeOOK mirrors decode_rfcodes.go DecodeOOK.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function decodeOOK(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("ook", S.reason);
  const runs = levelRuns(S);
  const hpg = shortestRunCluster(runs);
  if (!(hpg > 0)) return fail("ook", "no bursts");
  const n = S.n, spans = [], toks = [], units = [];
  let good = false, ver = 0, cut = false, hp = hpg, hpRead = hpg;
  for (let k = 0; k < runs.length; k++) {
    if (!runs[k].open && runs[k].x1 - runs[k].x0 < 8 * hpg) continue;
    let e = k + 1;
    while (e < runs.length && !runs[e].open && runs[e].x1 - runs[e].x0 < 8 * hpg) e++;
    const burst = runs.slice(k + 1, e);
    hp = shortestRunCluster(burst);
    if (!(hp > 0)) continue;
    const { lv, xs } = manchesterHalves(burst, 0, hp);
    let bits = [], bx = [];
    for (let off = 0; off < 2; off++) {
      const b = [], x = [];
      for (let j = off; j + 1 < lv.length; j += 2) {
        if (lv[j] === lv[j + 1]) break;
        b.push(lv[j]); x.push(xs[j]);
      }
      if (b.length > bits.length) { bits = b; bx = x; }
    }
    if (bits.length < 16) continue;
    let v = 0, from = -1, ones = 0;
    for (let j = 0; j + 4 <= bits.length; j++) {
      if (ones >= 12 && bits[j] === 0 && bits[j + 1] === 1 && bits[j + 2] === 0 && bits[j + 3] === 1) { v = 3; from = j + 4; break; }
      ones = (ones + 1) * bits[j];
    }
    if (from < 0) {
      for (let j = 17; j + 8 <= bits.length; j++) {
        const s = bits.slice(j, j + 8);
        if (s[0] === 1 && s[1] === 0 && s[2] === 0 && s[3] === 1 && s[4] === 1 && s[5] === 0 && s[6] === 0 && s[7] === 1 &&
          bits[j - 1] === 1 && bits[j - 2] === 0) { v = 21; from = j + 8; break; }
      }
    }
    if (from < 0) continue;
    let data = bits.slice(from), dx = bx.slice(from);
    if (v === 21) {
      const d = [], x = [];
      for (let j = 0; j + 1 < data.length; j += 2) {
        if (data[j] === data[j + 1]) break;
        d.push(data[j + 1]); x.push(dx[j]);
      }
      data = d; dx = x;
    }
    if (data.length < 32) continue;
    if (ver === 0) ver = v;
    hpRead = hp;
    const bitLen = v === 21 ? 4 * hp : 2 * hp;
    spans.push(pspan(bx[0], dx[0], n, "", "sof", 0));
    const nib = [];
    for (let j = 0; j + 4 <= data.length; j += 4) nib.push(data[j] | (data[j + 1] << 1) | (data[j + 2] << 2) | (data[j + 3] << 3));
    const ck = oregonChecksum(nib, v);
    // the record's end cuts the burst: it runs to the record's last run
    // (open, the line still within a bit when the record stopped) - no
    // checksum to judge
    const ends = e === runs.length || (e === runs.length - 1 && runs[e].x1 - runs[e].x0 < 8 * hpg);
    for (let j = 0; j < nib.length; j++) {
      let txt = nib[j].toString(16).toUpperCase(), kind = "data";
      if (ck < 0 && !ends && j === nib.length - 1) { txt = "!" + txt; kind = "frame-error"; }
      spans.push(pspan(dx[4 * j], dx[4 * j + 3] + bitLen, n, txt, kind, nib[j]));
      toks.push(txt); units.push(nib[j]);
    }
    if (ck >= 0) good = true;
    else if (ends) cut = true;
    k = e - 1;
  }
  if (!spans.length) return fail("ook", "no Oregon burst");
  const res = { ok: good, proto: "ook", spans, text: toks.join(" "), bytes: units,
    meta: { threshold: S.threshold, spb: 2 * hpRead, frameBits: ver } };
  if (colTimeS > 0) res.meta.baud = Math.round(1 / (2 * hpRead * colTimeS));
  if (cut) res.meta.cover = 1;
  if (!good) res.error = "no Oregon burst whose checksum checks";
  return res;
}

// decodeMiller mirrors decode_rfcodes.go DecodeMiller.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function decodeMiller(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("miller", S.reason);
  const runs = levelRuns(S);
  const lows = [], highs = [];
  for (const r of runs) {
    if (r.open) continue;
    if (r.lvl === 0) lows.push(r.x1 - r.x0); else highs.push(r.x1 - r.x0);
  }
  let xs = S.edges.map((e) => e.x);
  let iv = [];
  for (let k = 1; k < xs.length; k++) iv.push(xs[k] - xs[k - 1]);
  if (iv.length < 8) return fail("miller", "too few edges");
  const pauses = quantile(lows, 1) < 0.4 * quantile(highs, 0.5);
  if (pauses) {
    xs = S.edges.filter((e) => e.dir < 0).map((e) => e.x);
    iv = [];
    for (let k = 1; k < xs.length; k++) iv.push(xs[k] - xs[k - 1]);
  }
  let T = 0;
  const baud = cfg.baud | 0;
  if (baud > 0 && colTimeS > 0) T = 1 / (baud * colTimeS);
  else {
    const lo = quantile(iv, 0);
    const hi = quantile(iv.filter((v) => v < 2.6 * lo), 1);
    let sum = 0, cnt = 0;
    for (const v of iv) if (v <= 1.1 * lo) { sum += v; cnt++; }
    T = sum / cnt;
    const r = hi / lo;
    if (r > 1.2 && r < 1.45) T /= 1.5;
  }
  if (!(T > 0)) return fail("miller", "no bit time");
  const n = S.n, spans = [], toks = [], units = [];
  let good = false, inMsg = 0, halfMore = 0;
  for (let k = 0; k < xs.length;) {
    if (k > 0 && xs[k] - xs[k - 1] < 2 * T) { k++; continue; }
    const bits = [{ v: 0, x0: xs[k], x1: xs[k] + T }];
    let prev = 0, start = xs[k] + T, bad = false, j = k + 1;
    for (; j < xs.length; j++) {
      const q = (xs[j] - xs[j - 1]) / T * 2;
      const d = Math.round(q) / 2;
      if (d < 2.5 && Math.abs(q - 2 * d) > 0.3) { bad = true; break; }
      if (d < 2.5) { inMsg++; if (d === 1.5) halfMore++; }
      if (prev === 0) {
        if (d >= 2) break;
        if (d === 1) { bits.push({ v: 0, x0: xs[j], x1: xs[j] + T }); start = xs[j] + T; }
        else if (d === 1.5) { bits.push({ v: 1, x0: start, x1: xs[j] + T / 2 }); prev = 1; start = xs[j] + T / 2; }
        else bad = true;
      } else {
        if (d >= 2.5) { bits.push({ v: 0, x0: start, x1: start + T }); break; }
        if (d === 1) { bits.push({ v: 1, x0: start, x1: xs[j] + T / 2 }); start = xs[j] + T / 2; }
        else if (d === 1.5) { bits.push({ v: 0, x0: start, x1: xs[j] }, { v: 0, x0: xs[j], x1: xs[j] + T }); prev = 0; start = xs[j] + T; }
        else if (d === 2) { bits.push({ v: 0, x0: start, x1: start + T }, { v: 1, x0: xs[j] - T / 2, x1: xs[j] + T / 2 }); start = xs[j] + T / 2; }
        else bad = true;
      }
      if (bad) break;
    }
    if (!bad && j === xs.length && prev === 1 && n - xs[j - 1] >= 2.5 * T) bits.push({ v: 0, x0: start, x1: start + T });
    spans.push(pspan(bits[0].x0, bits[0].x0, n, "", "sof", 0));
    for (let b = 0; b < bits.length; b += 8) {
      let v = 0;
      const e = Math.min(b + 8, bits.length);
      for (let q = b; q < e; q++) v |= bits[q].v << (q - b);
      const txt = hex2(v);
      spans.push(pspan(bits[b].x0, bits[e - 1].x1, n, txt, "data", v));
      toks.push(txt); units.push(v);
    }
    if (bad) {
      spans.push(pspan(xs[j], xs[j], n, "!", "frame-error", 0));
      toks.push("!");
      k = j + 1;
      continue;
    }
    if (bits.length >= 8) good = true;
    k = j;
  }
  const res = { ok: good, proto: "miller", spans, text: toks.join(" "), bytes: units,
    meta: { threshold: S.threshold, spb: T, inverted: pauses } };
  if (inMsg > 0) res.meta.cover = halfMore / inMsg;
  if (colTimeS > 0) res.meta.baud = Math.round(1 / (T * colTimeS));
  if (!good) res.error = "no Miller message";
  return res;
}

const MORSE_CODE = { ".-": "A", "-...": "B", "-.-.": "C", "-..": "D", ".": "E", "..-.": "F", "--.": "G", "....": "H",
  "..": "I", ".---": "J", "-.-": "K", ".-..": "L", "--": "M", "-.": "N", "---": "O", ".--.": "P", "--.-": "Q", ".-.": "R",
  "...": "S", "-": "T", "..-": "U", "...-": "V", ".--": "W", "-..-": "X", "-.--": "Y", "--..": "Z", ".----": "1",
  "..---": "2", "...--": "3", "....-": "4", ".....": "5", "-....": "6", "--...": "7", "---..": "8", "----.": "9",
  "-----": "0", ".-.-.-": ".", "--..--": ",", "---...": ":", "..--..": "?", "-....-": "-", "-..-.": "/", "-.--.": "(",
  "-.--.-": ")", "-...-": "=", ".-.-.": "+", ".--.-.": "@" };

// decodeMorse mirrors decode_rfcodes.go DecodeMorse.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function decodeMorse(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("morse", S.reason);
  const { marks: m, high } = activeMarks(S);
  if (m.length < 2) return fail("morse", "too few marks");
  let minW = Infinity;
  for (const k of m) minW = Math.min(minW, k.x1 - k.x0);
  let sum = 0, cnt = 0;
  for (const k of m) if (k.x1 - k.x0 <= 1.5 * minW) { sum += k.x1 - k.x0; cnt++; }
  let u = sum / cnt;
  const baud = cfg.baud | 0;
  if (baud > 0 && colTimeS > 0) u = 1 / (baud * colTimeS);
  const n = S.n, spans = [], toks = [], units = [];
  let letters = 0, code = "", x0 = 0, timed = 0;
  for (const k of m) {
    const w = (k.x1 - k.x0) / u;
    if ((w >= 0.75 && w <= 1.25) || (w >= 2.25 && w <= 3.75)) timed++;
  }
  for (let i = 0; i < m.length; i++) {
    const k = m[i];
    if (code === "") {
      x0 = k.x0;
      if (i === 0 || markLead(m, i) >= 5 * u) spans.push(pspan(k.x0, k.x0, n, "", "sof", 0));
    }
    code += k.x1 - k.x0 < 2 * u ? "." : "-";
    if (k.gap < 2 * u) continue;
    const ch = MORSE_CODE[code];
    let txt = ch, kind = "data", c = ch ? ch.charCodeAt(0) : 0;
    if (!ch) { txt = "?"; kind = "frame-error"; c = 63; }
    spans.push(pspan(x0, k.x1, n, txt, kind, c));
    toks.push(txt); units.push(c);
    if (ch) letters++;
    code = "";
  }
  const good = letters >= 2;
  const res = { ok: good, proto: "morse", spans, text: toks.join(" "), bytes: units,
    meta: { threshold: S.threshold, inverted: !high, spb: u, cover: timed / m.length } };
  if (colTimeS > 0) res.meta.baud = Math.round(1 / (u * colTimeS));
  if (!good) res.error = "too few Morse letters";
  return res;
}

const DCF77_FIELDS = [{ at: 21, w: 7, name: "MIN" }, { at: 29, w: 6, name: "HR" }, { at: 36, w: 6, name: "DAY" },
  { at: 42, w: 3, name: "DOW" }, { at: 45, w: 5, name: "MON" }, { at: 50, w: 8, name: "YR" }];

// dcf77Second mirrors decode_rfcodes.go dcf77Second.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function dcf77Second(m, colTimeS) {
  const sp = [];
  for (let i = 1; i < m.length; i++) sp.push((m[i].x0 - m[i - 1].x0) * colTimeS);
  const s = medianOf(sp);
  if (s < 1e-3 || s > 2 || (s >= 0.9 && s <= 1.1)) return 0;
  return s;
}

// decodeDCF77 mirrors decode_rfcodes.go DecodeDCF77.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
function decodeDCF77(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("dcf77", S.reason);
  if (!(colTimeS > 0)) return fail("dcf77", "no sample time");
  const { marks: m, high } = activeMarks(S);
  const sec = (cfg.baud | 0) > 0 ? 1 / (cfg.baud | 0) : dcf77Second(m, colTimeS) || 1;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
  const ms = (d) => d * colTimeS * 1e3 / sec;
  const n = S.n, spans = [], toks = [], units = [];
  let good = false, c = -1;
  const bits = new Array(59).fill(0), bx = new Array(60).fill(0);
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
  const unit = (f, par) => {
    const d = DCF77_FIELDS[f];
    let v = 0;
    for (let q = 0; q < d.w; q++) v |= bits[d.at + q] << q;
    let txt = d.name + v.toString(16).toUpperCase(), kind = "data";
    if (par) { txt = "!" + txt; kind = "parity-error"; } else good = true;
    spans.push(pspan(bx[d.at], bx[d.at + d.w], n, txt, kind, v));
    toks.push(txt); units.push(v);
  };
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-251
  const odd = (from, to) => {
    let p = 0;
    for (let q = from; q <= to; q++) p ^= bits[q];
    return p !== 0;
  };
  for (let i = 0; i < m.length; i++) {
    const k = m[i];
    if (i > 0) {
      const d = ms(k.x0 - m[i - 1].x0);
      if (d >= 1600 && d <= 2400) { c = 0; spans.push(pspan(k.x0, k.x0, n, "", "sof", 0)); }
    }
    if (c < 0 || c > 58) continue;
    const w = ms(k.x1 - k.x0);
    let b = -1;
    if (w >= 40 && w <= 160) b = 0;
    else if (w > 160 && w <= 260) b = 1;
    if (b < 0) { spans.push(pspan(k.x0, k.x1, n, "!", "frame-error", 0)); toks.push("!"); c = -1; continue; }
    bits[c] = b; bx[c] = k.x0; bx[c + 1] = k.x0 + sec / colTimeS;
    if (c === 28) unit(0, odd(21, 28));
    else if (c === 35) unit(1, odd(29, 35));
    else if (c === 58) {
      const bad = odd(36, 58);
      for (let f = 2; f < DCF77_FIELDS.length; f++) unit(f, bad);
    }
    c++;
  }
  if (!spans.length) return fail("dcf77", "no DCF77 minute");
  const res = { ok: good, proto: "dcf77", spans, text: toks.join(" "), bytes: units,
    meta: { threshold: S.threshold, inverted: !high, spb: sec / colTimeS, baud: Math.round(1 / sec) } };
  if (!good) res.error = "no DCF77 field whose parity checks";
  return res;
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { decodeRCEncode, decodeOOK, decodeMiller, decodeMorse, decodeDCF77 };
