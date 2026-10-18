// ENGMODEL-OWNER-UNIT: FU-WEB-DECODE
// decode_wire1.js — STM8 SWIM, TI SDQ, PJDL with PJON, DSI and Qi, a
// classic script mirroring decode_wire1.go step for step. Uses
// sliceChannel, logicAt, hex2, fmtByte and fail from decode.js,
// levelRuns/pspan from decode_pulsewire.js, irMarks/irHalves from
// decode_ir.js, crc32IEEE/bmcBits from decode_biphase.js and quantile
// from decode_rfcodes.js.

// wireOut mirrors decode_wire1.go wireOut.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
function wireOut(n) {
  const o = { n, spans: [], toks: [], units: [] };
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
  o.add = (x0, x1, text, kind, val, unit) => {
    o.spans.push(pspan(x0, x1, n, text, kind, val));
    if (text !== "") o.toks.push(text);
    if (unit) o.units.push(val);
  };
  return o;
}

// fallsAndRises mirrors decode_wire1.go fallsAndRises.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
function fallsAndRises(S) {
  const E = S.edges, falls = [], rises = [];
  for (let k = 0; k < E.length; k++) {
    if (E[k].dir > 0) continue;
    falls.push(E[k].x);
    rises.push(k + 1 < E.length ? E[k + 1].x : -1);
  }
  return { falls, rises };
}

const SWIM_NAMES = { 0: "SRST", 1: "ROTF", 2: "WOTF" };

// decodeSWIM mirrors decode_wire1.go DecodeSWIM.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
function decodeSWIM(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("swim", S.reason);
  const { falls, rises } = fallsAndRises(S);
  const sp = [];
  for (let k = 1; k < falls.length; k++) sp.push(falls[k] - falls[k - 1]);
  if (sp.length < 4) return fail("swim", "too few bits");
  const lo = quantile(sp, 0);
  let sum = 0, cnt = 0;
  for (const v of sp) if (v <= 1.3 * lo) { sum += v; cnt++; }
  let T = sum / cnt;
  const baud = cfg.baud | 0;
  if (baud > 0 && colTimeS > 0) T = 1 / (baud * colTimeS);
  const o = wireOut(S.n);
  let state = 0, left = 0, good = false, fr = [];
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
  const frame = () => {
    if (!fr.length) return;
    const f = fr;
    fr = [];
    const nb = state === 0 ? 3 : 8;
    if (f.length !== nb + 3) { o.add(f[0].x0, f[f.length - 1].x1, "!", "frame-error", 0, false); return; }
    let v = 0, p = 0;
    for (let i = 1; i <= nb; i++) { v = (v << 1) | f[i].v; p ^= f[i].v; }
    const ok = p === f[nb + 1].v && f[nb + 2].v === 1;
    if (state === 0) o.add(f[0].x0, f[0].x0, "", "sof", 0, false);
    let txt;
    if (state === 0) txt = SWIM_NAMES[v] || "CMD" + v;
    else if (state === 1) txt = "N" + hex2(v);
    else if (state <= 4) txt = "@" + hex2(v);
    else txt = (f[0].v === 1 ? "R" : "W") + hex2(v);
    if (!ok) { o.add(f[1].x0, f[nb].x1, "!" + txt, "frame-error", v, true); return; }
    good = true;
    o.add(f[1].x0, f[nb].x1, txt, "data", v, true);
    if (state === 0 && (v === 1 || v === 2)) state = 1;
    else if (state === 1) { state = 2; left = v; }
    else if (state >= 2 && state < 5) state++;
    else if (state === 5) { left--; if (left <= 0) state = 0; }
  };
  for (let k = 0; k < falls.length; k++) {
    const f = falls[k], r = rises[k];
    if (r < 0) break;
    const low = r - f;
    if (colTimeS > 0) {
      const us = low * colTimeS * 1e6;
      if (us >= 64 / 8.8 && us <= 128 / 3.6 && low > 1.5 * T) {
        frame();
        o.add(f, r, "SYNC", "rw", 0, false);
        state = 0;
        continue;
      }
    }
    if (low > 1.5 * T) { frame(); continue; }
    let end = f + T;
    if (k + 1 < falls.length && falls[k + 1] - f <= 1.5 * T) end = falls[k + 1];
    const b = low >= end - r ? 0 : 1;
    if (fr.length > 0 && f - fr[fr.length - 1].x0 > 1.5 * T) frame();
    fr.push({ v: b, x0: f, x1: end });
    const nb = state === 0 ? 3 : 8;
    if (fr.length === nb + 3) frame();
  }
  frame();
  if (!o.spans.length) return fail("swim", "no SWIM frame");
  const res = { ok: good, proto: "swim", spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: S.threshold, spb: T } };
  if (colTimeS > 0) res.meta.baud = Math.round(1 / (T * colTimeS));
  if (!good) res.error = "no acknowledged SWIM frame";
  return res;
}

// decodeSDQ mirrors decode_wire1.go DecodeSDQ.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
function decodeSDQ(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("sdq", S.reason);
  if (!(colTimeS > 0)) return fail("sdq", "no sample time");
  const baud = (cfg.baud | 0) > 0 ? cfg.baud | 0 : 98425;
  const T = 1 / (baud * colTimeS);
  const { falls, rises } = fallsAndRises(S);
  const o = wireOut(S.n);
  let v = 0, nb = 0, x0 = 0, prev = -1, bytesAfter = -1, good = false;
  for (let k = 0; k < falls.length; k++) {
    const f = falls[k], r = rises[k];
    if (r < 0) break;
    const d = r - f;
    if (d > 1.2 * T) { o.add(f, r, "", "sof", 0, false); v = 0; nb = 0; bytesAfter = 0; prev = -1; continue; }
    if (nb > 0 && f - prev < 0.7 * T) {
      o.add(prev, f, "!", "frame-error", 0, false);
      v = 0; nb = 0; bytesAfter = -1;
    }
    prev = f;
    if (nb === 0) x0 = f;
    if (d <= T / 2) v |= 1 << nb;
    nb++;
    if (nb === 8) {
      o.add(x0, f + T, fmtByte(v, cfg.format), "data", v, true);
      v = 0; nb = 0;
      if (bytesAfter >= 0) { bytesAfter++; good = good || bytesAfter >= 2; }
    }
  }
  if (!o.spans.length) return fail("sdq", "no SDQ bits");
  const res = { ok: good, proto: "sdq", spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: S.threshold, spb: T, baud: Math.round(baud) } };
  if (!good) res.error = "no SDQ bytes after a break";
  return res;
}

const PJDL_MODES = [[0, 0], [44, 116], [40, 92], [28, 88], [26, 60]];

// pjdlIn mirrors decode_wire1.go pjdlIn.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
function pjdlIn(us, want) {
  const lo = Math.min(want * 0.9, want - 1.5);
  const hi = Math.max(want * 1.1, want + 1.5);
  return us >= lo && us <= hi;
}

// crc8PJON mirrors decode_wire1.go crc8PJON.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
function crc8PJON(b) {
  let c = 0;
  for (const v of b) {
    c ^= v & 0xff;
    for (let i = 0; i < 8; i++) c = (c & 1) ? (c >>> 1) ^ 0x97 : c >>> 1;
  }
  return c;
}

// pjonFields mirrors decode_wire1.go pjonFields.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
function pjonFields(o, b, fx, dt) {
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
  const put = (i, txt, bad) => {
    let kind = "data";
    if (bad) { txt = "!" + txt; kind = "frame-error"; }
    o.add(fx[i], fx[i] + 9 * dt, txt, kind, b[i], true);
  };
  if (b.length < 4) {
    for (let i = 0; i < b.length; i++) put(i, hex2(b[i]), i === b.length - 1);
    return false;
  }
  const cfgB = b[1];
  let head = 4, length = b[2];
  if (cfgB & 0x40) {
    head = 5;
    if (b.length >= 5) length = (b[2] << 8) | b[3];
  }
  const metaOK = crc8PJON(b.slice(0, head - 1)) === b[head - 1];
  const crc32 = (cfgB & 0x20) !== 0;
  const endLen = crc32 ? 4 : 1;
  const complete = length === b.length && length > head + endLen;
  let endOK = false;
  if (complete) {
    if (crc32) {
      let v = 0;
      for (const x of b.slice(length - 4)) v = v * 256 + x;
      endOK = crc32IEEE(b.slice(0, length - 4)) === v;
    } else endOK = crc8PJON(b.slice(0, length - 1)) === b[length - 1];
  }
  for (let i = 0; i < b.length; i++) {
    let bad = false;
    if (i === head - 1) bad = !metaOK;
    else if (i === b.length - 1) bad = !complete || !endOK;
    put(i, (i === 0 ? "RX" : "") + hex2(b[i]), bad);
  }
  return metaOK;
}

// decodePJDL mirrors decode_wire1.go DecodePJDL.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
function decodePJDL(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("pjdl", S.reason);
  if (!(colTimeS > 0)) return fail("pjdl", "no sample time");
  const runs = levelRuns(S);
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
  const us = (d) => d * colTimeS * 1e6;
  let mode = cfg.variant | 0;
  if (mode < 1 || mode > 4) {
    let best = 0;
    for (let m = 1; m <= 4; m++) {
      let c = 0;
      for (const r of runs) if (r.lvl === 1 && !r.open && pjdlIn(us(r.x1 - r.x0), PJDL_MODES[m][1])) c++;
      if (c > best) { best = c; mode = m; }
    }
    if (best === 0) return fail("pjdl", "no PJDL sync pads");
  }
  const data = PJDL_MODES[mode][0], pad = PJDL_MODES[mode][1];
  const dt = data / us(1);
  const o = wireOut(S.n);
  let good = false, frame = [], fx = [], inFrame = false;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
  const flush = () => {
    if (inFrame && frame.length) good = pjonFields(o, frame, fx, dt) || good;
    inFrame = false; frame = []; fx = [];
  };
  let lastEnd = 0;
  const idle = (pad + 9 * data + 4) / us(1);
  let ri = 0;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
  const highFrom = (x) => {
    while (ri < runs.length && (runs[ri].x1 <= x || runs[ri].lvl !== 1 || (runs[ri].open && runs[ri].x0 === 0))) ri++;
    if (ri >= runs.length || runs[ri].open) return null;
    return { a: Math.max(runs[ri].x0, x), b: runs[ri].x1 };
  };
  for (let x = 0; ;) {
    const h = highFrom(x);
    if (!h) break;
    if (!pjdlIn(us(h.b - h.a), pad)) { x = h.b; continue; }
    if (inFrame && h.a - lastEnd > idle) flush();
    let pads = 1, e = h.b;
    while (pads < 3) {
      const n2 = highFrom(e);
      if (!n2 || !pjdlIn(us(n2.a - e), data) || !pjdlIn(us(n2.b - n2.a), pad)) break;
      pads++; e = n2.b;
    }
    if (pads === 3) {
      flush();
      inFrame = true;
      o.add(h.a, e + dt, "", "sof", 0, false);
      x = e; lastEnd = e + dt;
      continue;
    }
    let v = 0;
    for (let k = 0; k < 8; k++) if (logicAt(S, e + dt * (1.5 + k)) === 1) v |= 1 << k;
    if (inFrame) { frame.push(v); fx.push(e + dt); }
    else o.add(e + dt, e + 9 * dt, hex2(v), "data", v, true);
    x = e + 9 * dt; lastEnd = e + 9 * dt;
  }
  flush();
  if (!o.spans.length) return fail("pjdl", "no PJDL frame");
  const res = { ok: good, proto: "pjdl", spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: S.threshold, spb: dt, baud: Math.round(1e6 / data), frameBits: mode } };
  if (!good) res.error = "no PJON frame whose CRC checks";
  return res;
}

// decodeDSI mirrors decode_wire1.go DecodeDSI.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
function decodeDSI(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("dsi", S.reason);
  if (!(colTimeS > 0)) return fail("dsi", "no sample time");
  const { marks, high } = irMarks(S, colTimeS);
  const n = S.n;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
  const us = (d) => d * colTimeS * 1e6;
  const half = 833.33;
  const hx = half / us(1);
  const o = wireOut(n);
  let good = false;
  for (let i = 0; i < marks.length; i++) {
    if ((i > 0 && us(marks[i].x0 - marks[i - 1].x1) < 3200) || (i === 0 && us(marks[0].x0) < 1700)) continue;
    let { lv, xs } = irHalves(marks, i, half, 2, us);
    if (!lv.length) continue;
    lv = [0].concat(lv);
    xs = [marks[i].x0 - hx].concat(xs);
    if (lv.length % 2 !== 0) { lv.push(0); xs.push(xs[xs.length - 1] + hx); }
    const bits = [];
    let ok = true;
    for (let b = 0; 2 * b + 1 < lv.length; b++) {
      if (lv[2 * b] === lv[2 * b + 1]) { ok = false; break; }
      bits.push(lv[2 * b]);
    }
    o.add(xs[0], xs[0] + 2 * hx, "START", "sof", 0, false);
    if (!ok || bits.length !== 9 || bits[0] !== 0) {
      o.add(xs[0], xs[xs.length - 1] + hx, "!" + bits.length + "bits", "frame-error", 0, false);
      continue;
    }
    let v = 0;
    for (let k = 1; k <= 8; k++) v = (v << 1) | bits[k];
    o.add(xs[2], xs[2] + 16 * hx, fmtByte(v, cfg.format), "data", v, true);
    good = true;
  }
  if (!o.spans.length) return fail("dsi", "no DSI frame");
  const res = { ok: good, proto: "dsi", spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: S.threshold, inverted: high, spb: 2 * hx, baud: 600 } };
  if (!good) res.error = "no whole DSI frame";
  return res;
}

// qiLen mirrors decode_wire1.go qiLen.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
function qiLen(h) {
  if (h <= 0x1f) return 1 + Math.trunc(h / 32);
  if (h <= 0x7f) return 2 + Math.trunc((h - 32) / 16);
  if (h <= 0xdf) return 8 + Math.trunc((h - 128) / 8);
  return 20 + Math.trunc((h - 224) / 4);
}

// decodeQi mirrors decode_wire1.go DecodeQi.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
function decodeQi(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("qi", S.reason);
  if (!(colTimeS > 0)) return fail("qi", "no sample time");
  const bit = 500e-6 / colTimeS;
  const o = wireOut(S.n);
  let good = false;
  for (const burst of bmcBits(S, bit)) {
    let ones = 0, k = 0;
    for (; k < burst.length; k++) {
      if (burst[k].v === 1) { ones++; continue; }
      if (ones >= 4) break;
      ones = 0;
    }
    const r = burst.length - k;
    if (r % 11 === 10 && r > 0) burst.push({ v: 1, x0: burst[burst.length - 1].x0 + bit });
    let pkt = [], px = [], bad = false;
    for (; k + 11 <= burst.length; k += 11) {
      const c = burst.slice(k, k + 11);
      if (c[0].v !== 0) break;
      let v = 0, p = 1;
      for (let i = 0; i < 8; i++) { v |= c[1 + i].v << i; p ^= c[1 + i].v; }
      if (c[9].v !== p || c[10].v !== 1) {
        bad = true;
        o.add(c[0].x0, c[10].x0 + bit, "!" + hex2(v), "parity-error", v, true);
        break;
      }
      if (!pkt.length) o.add(c[0].x0, c[0].x0, "", "sof", 0, false);
      pkt.push(v); px.push(c[0].x0);
      if (pkt.length === qiLen(pkt[0]) + 2) {
        let x = 0;
        for (let i = 0; i + 1 < pkt.length; i++) x ^= pkt[i];
        for (let i = 0; i < pkt.length; i++) {
          let txt = hex2(pkt[i]), kind = "data";
          if (i === pkt.length - 1 && x !== pkt[i]) { txt = "!" + txt; kind = "frame-error"; }
          o.add(px[i], px[i] + 11 * bit, txt, kind, pkt[i], true);
        }
        good = good || x === pkt[pkt.length - 1];
        pkt = []; px = [];
      }
    }
    if (pkt.length && !bad) {
      for (let i = 0; i < pkt.length; i++) o.add(px[i], px[i] + 11 * bit, hex2(pkt[i]), "data", pkt[i], true);
      o.add(px[px.length - 1] + 11 * bit, px[px.length - 1] + 11 * bit, "!", "frame-error", 0, false);
    }
  }
  if (!o.spans.length) return fail("qi", "no Qi packet");
  const res = { ok: good, proto: "qi", spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: S.threshold, spb: bit, baud: 2000 } };
  if (!good) res.error = "no Qi packet whose checksum checks";
  return res;
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { decodeSWIM, decodeSDQ, decodePJDL, decodeDSI, decodeQi };
