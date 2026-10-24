// ENGMODEL-OWNER-UNIT: FU-WEB-DECODE
// decode_ir.js — the infrared remote protocols (NEC, Sony SIRC, RC-5, RC-6),
// a classic script mirroring decode_ir.go step for step. Uses sliceChannel,
// hex2 and fail from decode.js, uiClamp from decode_uartproto.js and
// levelRuns/pspan from decode_pulsewire.js.

const IR_MERGE_US = 150;
const IR_NAMES = { irnec: "NEC", irsirc: "SIRC", irrc5: "RC-5", irrc6: "RC-6" };

// irMarks finds the line's marks and whether they are its high level, read
// straight off the edges: the runs levelRuns would list (a run from each
// edge to the next, the first from 0, the last open to n), without making
// an object a run.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
function irMarks(S, colTimeS) {
  const E = S.edges, n = S.n;
  let longest = -1, idle = 1;
  if (!E.length) {
    longest = n; idle = logicAt(S, 0);
  } else {
    longest = E[0].x; idle = E[0].dir > 0 ? 0 : 1;
    for (let k = 0; k < E.length; k++) {
      const d = (k + 1 < E.length ? E[k + 1].x : n) - E[k].x;
      if (d > longest) { longest = d; idle = E[k].dir > 0 ? 1 : 0; }
    }
  }
  const active = 1 - idle;
  const marks = [];
  const merge = IR_MERGE_US * 1e-6 / colTimeS;
  for (let k = 0; k + 1 < E.length; k++) { // the closed runs: edge k to edge k+1
    if ((E[k].dir > 0 ? 1 : 0) !== active) continue;
    const x0 = E[k].x, x1 = E[k + 1].x;
    const j = marks.length;
    if (j > 0 && x0 - marks[j - 1].x1 < merge) { marks[j - 1].x1 = x1; continue; }
    marks.push({ x0, x1 });
  }
  return { marks, high: active === 1 };
}

// irDecode runs a line through irMarks and a protocol's frame reader.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
function irDecode(proto, codes, colTimeS, cfg, read) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail(proto, S.reason);
  if (!(colTimeS > 0)) return fail(proto, "no sample time");
  const { marks, high } = irMarks(S, colTimeS);
  const o = { spans: [], toks: [], units: [], good: false, format: cfg.format, unitUs: 0 };
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
  o.add = (x0, x1, n, text, kind, val, unit) => {
    o.spans.push(pspan(x0, x1, n, text, kind, val));
    if (text !== "") o.toks.push(text);
    if (unit) o.units.push(val);
  };
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
  const us = (d) => d * colTimeS * 1e6;
  read(marks, us, S.n, o);
  if (!o.spans.length) return fail(proto, "no " + IR_NAMES[proto] + " frame");
  const res = { ok: o.good, proto, spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: S.threshold, inverted: high, spb: o.unitUs * 1e-6 / colTimeS } };
  if (o.unitUs > 0) res.meta.baud = Math.round(1e6 / o.unitUs);
  if (!o.good) res.error = "no complete " + IR_NAMES[proto] + " frame";
  return res;
}

// decodeIRNEC mirrors decode_ir.go DecodeIRNEC.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
function decodeIRNEC(codes, colTimeS, cfg) {
  return irDecode("irnec", codes, colTimeS, cfg, (m, us, n, o) => {
    o.unitUs = 1125;
    for (let i = 0; i + 1 < m.length; i++) {
      const lead = us(m[i].x1 - m[i].x0);
      if (lead < 8000 || lead > 10000) continue;
      const space = us(m[i + 1].x0 - m[i].x1);
      if (space >= 1800 && space <= 2800) {
        const stop = us(m[i + 1].x1 - m[i + 1].x0);
        if (stop >= 300 && stop <= 800) { o.add(m[i].x0, m[i + 1].x1, n, "REP", "sof", 0, false); i++; }
        continue;
      }
      if (space < 3500 || space > 5500) continue;
      o.add(m[i].x0, m[i + 1].x0, n, "LDR", "sof", 0, false);
      const vals = [0, 0, 0, 0];
      let ok = true, j = i + 1;
      for (let b = 0; b < 32; b++) {
        if (j + 1 >= m.length) { ok = false; break; }
        const mk = us(m[j].x1 - m[j].x0), per = us(m[j + 1].x0 - m[j].x0);
        let bit = -1;
        if (mk < 300 || mk > 800) bit = -1;
        else if (per >= 800 && per <= 1500) bit = 0;
        else if (per >= 1700 && per <= 2800) bit = 1;
        if (bit < 0) { ok = false; break; }
        vals[b >> 3] |= bit << (b % 8);
        j++;
      }
      if (!ok) { o.add(m[j].x0, m[j].x0, n, "!", "frame-error", 0, false); i = j - 1; continue; }
      for (let k = 0; k < 4; k++) {
        const x0 = m[i + 1 + 8 * k].x0, x1 = m[i + 9 + 8 * k].x0;
        let txt = hex2(vals[k]), kind = "data";
        if (k === 0) txt = "A" + txt;
        else if (k === 2) txt = "C" + txt;
        else if (k === 3 && (vals[2] ^ vals[3]) !== 0xff) { txt = "!" + txt; kind = "frame-error"; }
        o.add(x0, x1, n, txt, kind, vals[k], true);
      }
      if ((vals[2] ^ vals[3]) === 0xff) o.good = true;
      i = j - 1;
    }
  });
}

// decodeIRSIRC mirrors decode_ir.go DecodeIRSIRC.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
function decodeIRSIRC(codes, colTimeS, cfg) {
  return irDecode("irsirc", codes, colTimeS, cfg, (m, us, n, o) => {
    o.unitUs = 600;
    for (let i = 0; i < m.length; i++) {
      const w = us(m[i].x1 - m[i].x0);
      if (w < 2100 || w > 2620) continue;
      const bits = [];
      let j = i + 1;
      for (; j < m.length && bits.length < 20; j++) {
        const sp = us(m[j].x0 - m[j - 1].x1);
        if (sp < 450 || sp > 800) break;
        const mk = us(m[j].x1 - m[j].x0);
        if (mk >= 350 && mk < 900) bits.push(0);
        else if (mk >= 900 && mk <= 1500) bits.push(1);
        else j = -1;
        if (j < 0) break;
      }
      if (j < 0) {
        o.add(m[i].x0, m[i].x1, n, "SIRC", "sof", 0, false);
        o.add(m[i].x1, m[i].x1, n, "!", "frame-error", 0, false);
        continue;
      }
      o.add(m[i].x0, m[i].x1, n, "SIRC", "sof", 0, false);
      const nb = bits.length;
      if (nb !== 12 && nb !== 15 && nb !== 20) {
        o.add(m[i].x1, m[j - 1].x1, n, "!" + nb + "bits", "frame-error", 0, false);
        i = j - 1;
        continue;
      }
      // TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
      const field = (from, cnt, pfx) => {
        let v = 0;
        for (let k = 0; k < cnt; k++) v |= bits[from + k] << k;
        o.add(m[i + 1 + from].x0, m[i + from + cnt].x1, n, pfx + hex2(v), "data", v, true);
      };
      field(0, 7, "C");
      if (nb === 12) field(7, 5, "A");
      else if (nb === 15) field(7, 8, "A");
      else { field(7, 5, "A"); field(12, 8, "E"); }
      o.good = true;
      i = j - 1;
    }
  });
}

// irHalves mirrors decode_ir.go irHalves.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
function irHalves(m, from, unit, maxUnits, us) {
  const lv = [], xs = [];
  for (let j = from; j < m.length; j++) {
    let k = Math.round(us(m[j].x1 - m[j].x0) / unit);
    if (k < 1 || k > maxUnits) break;
    let step = (m[j].x1 - m[j].x0) / k;
    for (let q = 0; q < k; q++) { lv.push(1); xs.push(m[j].x0 + q * step); }
    if (j + 1 >= m.length) break;
    k = Math.round(us(m[j + 1].x0 - m[j].x1) / unit);
    if (k < 1 || k > maxUnits) break;
    step = (m[j + 1].x0 - m[j].x1) / k;
    for (let q = 0; q < k; q++) { lv.push(0); xs.push(m[j].x1 + q * step); }
  }
  return { lv, xs };
}

// decodeIRRC5 mirrors decode_ir.go DecodeIRRC5.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
function decodeIRRC5(codes, colTimeS, cfg) {
  return irDecode("irrc5", codes, colTimeS, cfg, (m, us, n, o) => {
    const half = 889;
    o.unitUs = 2 * half;
    for (let i = 0; i < m.length; i++) {
      if ((i > 0 && us(m[i].x0 - m[i - 1].x1) < 5000) || (i === 0 && us(m[0].x0) < 2000)) continue;
      let { lv, xs } = irHalves(m, i, half, 2, us);
      const hx = half / us(1);
      lv = [0].concat(lv);
      xs = [m[i].x0 - hx].concat(xs);
      while (lv.length < 28) { lv.push(0); xs.push(xs[xs.length - 1] + hx); }
      const bits = new Array(14).fill(0);
      let ok = true;
      for (let b = 0; b < 14; b++) {
        if (lv[2 * b] === 0 && lv[2 * b + 1] === 1) bits[b] = 1;
        else if (lv[2 * b] === 1 && lv[2 * b + 1] === 0) bits[b] = 0;
        else ok = false;
      }
      o.add(xs[0], xs[0] + 2 * hx, n, "RC5", "sof", 0, false);
      if (!ok || bits[0] !== 1) { o.add(xs[0], xs[0], n, "!", "frame-error", 0, false); continue; }
      // TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
      const bx = (b) => xs[2 * b];
      o.add(bx(2), bx(3), n, "T" + bits[2], "rw", bits[2], false);
      let a = 0, c = 0;
      for (let k = 0; k < 5; k++) a = (a << 1) | bits[3 + k];
      for (let k = 0; k < 6; k++) c = (c << 1) | bits[8 + k];
      c |= (1 - bits[1]) << 6;
      o.add(bx(3), bx(8), n, "A" + hex2(a), "data", a, true);
      o.add(bx(8), bx(13) + 2 * hx, n, "C" + hex2(c), "data", c, true);
      o.good = true;
    }
  });
}

// decodeIRRC6 mirrors decode_ir.go DecodeIRRC6.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
function decodeIRRC6(codes, colTimeS, cfg) {
  return irDecode("irrc6", codes, colTimeS, cfg, (m, us, n, o) => {
    const t = 444;
    o.unitUs = 2 * t;
    for (let i = 0; i + 1 < m.length; i++) {
      const lead = us(m[i].x1 - m[i].x0), sp = us(m[i + 1].x0 - m[i].x1);
      if (lead < 2200 || lead > 3100 || sp < 650 || sp > 1150) continue;
      o.add(m[i].x0, m[i + 1].x0, n, "RC6", "sof", 0, false);
      const { lv, xs } = irHalves(m, i + 1, t, 3, us);
      const ux = t / us(1);
      // TRLC-LINKS: REQ-SDS-018, REQ-SDS-234
      const pair = (u) => {
        if (u + 1 >= lv.length) return -1;
        if (lv[u] === 1 && lv[u + 1] === 0) return 1;
        if (lv[u] === 0 && lv[u + 1] === 1) return 0;
        return -1;
      };
      while (lv.length % 2 !== 0 || lv.length < 12) {
        lv.push(0);
        xs.push(xs.length ? xs[xs.length - 1] + ux : m[i + 1].x0);
      }
      const start = pair(0);
      let mode = 0, ok = start === 1;
      for (let b = 0; b < 3 && ok; b++) { const v = pair(2 + 2 * b); ok = v >= 0; mode = (mode << 1) | v; }
      let toggle = -1;
      if (ok) {
        if (lv[8] === 1 && lv[9] === 1 && lv[10] === 0 && lv[11] === 0) toggle = 1;
        else if (lv[8] === 0 && lv[9] === 0 && lv[10] === 1 && lv[11] === 1) toggle = 0;
        ok = toggle >= 0;
      }
      if (!ok) { o.add(m[i + 1].x0, m[i + 1].x0, n, "!", "frame-error", 0, false); continue; }
      o.add(xs[2], xs[8], n, "M" + mode, "rw", mode, false);
      o.add(xs[8], xs[11] + ux, n, "T" + toggle, "rw", toggle, false);
      let bits = [];
      for (let u = 12; ; u += 2) {
        const v = pair(u);
        if (v < 0) {
          if (u + 1 < lv.length || (u < lv.length && lv[u] === 1)) bits = [];
          break;
        }
        bits.push(v);
      }
      if ((mode === 0 && bits.length !== 16) || bits.length === 0 || bits.length % 8 !== 0) {
        o.add(xs[11] + ux, xs[11] + ux, n, "!", "frame-error", 0, false);
        continue;
      }
      for (let b = 0; b < bits.length / 8; b++) {
        let v = 0;
        for (let k = 0; k < 8; k++) v = (v << 1) | bits[8 * b + k];
        let txt = hex2(v);
        if (mode === 0) txt = ["A", "C"][b] + txt;
        o.add(xs[12 + 16 * b], xs[12 + 16 * b + 15] + ux, n, txt, "data", v, true);
      }
      o.good = true;
    }
  });
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { decodeIRNEC, decodeIRSIRC, decodeIRRC5, decodeIRRC6 };
