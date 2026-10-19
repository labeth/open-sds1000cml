// ENGMODEL-OWNER-UNIT: FU-WEB-DECODE
// decode_clocked.js — PS/2, SWD, MDIO and Wiegand, a classic script
// mirroring decode_clocked.go step for step. Uses sliceChannel, logicAt,
// hex2, fmtByte and fail from decode.js, pspan and levelRuns from
// decode_pulsewire.js. cfg.threshold slices the first line (clock, D0),
// cfg.thresholdB the second (data, D1).

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
function clockedOpts(thr) { return thr != null ? { threshold: thr } : {}; }

// clockedLines mirrors decode_clocked.go clockedLines.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
function clockedLines(a, b, cfg) {
  if (!b) return { why: "needs both lines (clock and data)" };
  const C = sliceChannel(a, clockedOpts(cfg.threshold));
  if (!C.ok) return { why: C.reason };
  const D = sliceChannel(b, clockedOpts(cfg.thresholdB));
  if (!D.ok) return { why: D.reason };
  const out = [], gaps = [];
  let lastFall = -1;
  for (const e of C.edges) {
    out.push({ x: e.x, dir: e.dir, d: logicAt(D, e.x) });
    if (e.dir < 0) {
      if (lastFall >= 0) gaps.push(e.x - lastFall);
      lastFall = e.x;
    }
  }
  if (gaps.length < 4) return { why: "too few clock edges" };
  gaps.sort((p, q) => p - q);
  return { E: out, per: gaps[gaps.length >> 1], C, why: "" };
}

// decodePS2 mirrors decode_clocked.go DecodePS2.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
function decodePS2(clk, data, colTimeS, cfg) {
  cfg = cfg || {};
  const L = clockedLines(clk, data, cfg);
  if (L.why) return fail("ps2", L.why);
  const { E, per, C } = L;
  const F = E.filter((e) => e.dir < 0);
  const n = C.n, spans = [], toks = [], units = [];
  let good = false, free = false;
  for (let i = 0; i < F.length; i++) {
    const quiet = (i === 0 && F[0].x >= 1.4 * per) || (i > 0 && F[i].x - F[i - 1].x >= 1.4 * per);
    if (F[i].d !== 0 || !(quiet || free) || i + 10 >= F.length) { free = false; continue; }
    let ok = true;
    for (let k = 1; k <= 10; k++) if (F[i + k].x - F[i + k - 1].x > 1.4 * per) ok = false;
    if (!ok) { free = false; continue; }
    let v = 0, ones = 0;
    for (let k = 0; k < 8; k++) { v |= F[i + 1 + k].d << k; ones += F[i + 1 + k].d; }
    const par = F[i + 9].d, stop = F[i + 10].d;
    const host = i + 11 < F.length && F[i + 11].x - F[i + 10].x <= 1.4 * per && F[i + 11].d === 0;
    spans.push(pspan(F[i].x, F[i].x, n, "", "sof", 0));
    let txt = host ? "H" + hex2(v) : fmtByte(v, cfg.format);
    let kind = "data";
    if ((ones + par) % 2 !== 1) kind = "parity-error";
    else if (stop !== 1) kind = "frame-error";
    if (kind !== "data") txt = "!" + txt; else good = true;
    spans.push(pspan(F[i + 1].x, F[i + 9].x, n, txt, kind, v));
    toks.push(txt); units.push(v);
    let last = i + 10;
    if (host) { spans.push(pspan(F[i + 11].x, F[i + 11].x + per, n, "ACK", "ack", 0)); toks.push("ACK"); last++; }
    i = last;
    free = true;
  }
  if (!spans.length) return fail("ps2", "no PS/2 frame");
  const res = { ok: good, proto: "ps2", spans, text: toks.join(" "), bytes: units, meta: { threshold: C.threshold, spb: per } };
  if (colTimeS > 0) res.meta.baud = Math.floor(1 / (per * colTimeS) + 0.5);
  if (!good) res.error = "no PS/2 frame whose parity checks";
  return res;
}

// swdTargetAtRise: whether more requests on the clock (start 1, stop 0,
// park 1, parity checking) read an OK acknowledge at the rises than at the
// falls (decode.go swdTargetAtRise).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
function swdTargetAtRise(cy) {
  let fall = 0;
  let rise = 0;
  for (let c = 0; c + 12 <= cy.length; c++) {
    const r0 = cy[c].dr;
    const r1 = cy[c + 1].dr;
    const r2 = cy[c + 2].dr;
    const r3 = cy[c + 3].dr;
    const r4 = cy[c + 4].dr;
    const r5 = cy[c + 5].dr;
    if (r0 !== 1 || cy[c + 6].dr !== 0 || cy[c + 7].dr !== 1 || (r1 + r2 + r3 + r4) % 2 !== r5) continue;
    if (cy[c + 9].df === 1 && cy[c + 10].df === 0 && cy[c + 11].df === 0) fall++;
    if (cy[c + 9].dr === 1 && cy[c + 10].dr === 0 && cy[c + 11].dr === 0) rise++;
  }
  return rise > fall;
}

// decodeSWD mirrors decode_clocked.go DecodeSWD.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
function decodeSWD(clk, dio, colTimeS, cfg) {
  cfg = cfg || {};
  const L = clockedLines(clk, dio, cfg);
  if (L.why) return fail("swd", L.why);
  const { E, per, C } = L;
  const cy = [];
  for (let k = 0; k + 1 < E.length; k++)
    if (E[k].dir < 0 && E[k + 1].dir > 0) cy.push({ xf: E[k].x, xr: E[k + 1].x, df: E[k].d, dr: E[k + 1].d });
  // the target's bits at the falls, or at the rises when that reads more OK
  // acknowledges (decode.go swdTargetAtRise)
  const atRise = swdTargetAtRise(cy);
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
  const tb = (k) => (atRise ? cy[k].dr : cy[k].df);
  const n = C.n, spans = [], toks = [], units = [];
  let good = false;
  const resets = [];
  let ones = 0;
  for (let c = 0; c < cy.length; c++) {
    if (cy[c].dr === 1) { ones++; continue; }
    if (ones >= 50) resets.push([c - ones, c]);
    ones = 0;
  }
  let explained = 0;
  for (const rs of resets) explained += rs[1] - rs[0];
  for (const y of cy) if (y.dr === 0 && y.df === 0) explained++;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
  const unexplained = (from, to) => { let n2 = 0; for (let k = from; k < to; k++) if (cy[k].dr !== 0 || cy[k].df !== 0) n2++; return n2; };
  let ri = 0;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
  const flushResets = (upTo) => {
    for (; ri < resets.length && resets[ri][1] <= upTo; ri++) {
      spans.push(pspan(cy[resets[ri][0]].xr, cy[resets[ri][1]].xr, n, "LINERESET", "rw", 0));
      toks.push("LINERESET");
    }
  };
  let next = -1;
  let firstC = cy.length;
  let lastC = 0;
  for (const rs of resets) { firstC = Math.min(firstC, rs[0]); lastC = Math.max(lastC, rs[1]); }
  for (let c = 0; c + 12 <= cy.length; c++) {
    flushResets(c);
    // TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
    const r = (k) => cy[c + k].dr;
    if (r(0) !== 1 || r(6) !== 0 || r(7) !== 1 || (c > 0 && cy[c - 1].dr !== 0 && c !== next)) continue;
    const apdp = r(1), rnw = r(2), a2 = r(3), a3 = r(4), par = r(5);
    const req = 1 | (apdp << 1) | (rnw << 2) | (a2 << 3) | (a3 << 4) | (par << 5) | (1 << 7);
    const parOK = (apdp + rnw + a2 + a3) % 2 === par;
    const ack = tb(c + 9) | (tb(c + 10) << 1) | (tb(c + 11) << 2);
    if (!parOK || (ack !== 1 && ack !== 2 && ack !== 4)) continue;
    spans.push(pspan(cy[c].xr, cy[c].xr, n, "", "sof", 0));
    const txt = (rnw ? "R" : "W") + (apdp ? "AP" : "DP") + ((a2 | (a3 << 1)) << 2).toString(16).toUpperCase();
    spans.push(pspan(cy[c].xf, cy[c + 7].xr + per / 2, n, txt, "data", req));
    toks.push(txt); units.push(req);
    const ackTxt = { 1: "OK", 2: "WAIT", 4: "FAULT" }[ack];
    spans.push(pspan(cy[c + 9].xf, cy[c + 11].xr + per / 2, n, ackTxt, "data", ack));
    toks.push(ackTxt); units.push(ack);
    firstC = Math.min(firstC, c);
    if (ack !== 1) { explained += unexplained(c, c + 12); c += 11; next = c + 1; lastC = Math.max(lastC, next); continue; }
    const first = rnw === 0 ? c + 13 : c + 12;
    if (first + 33 > cy.length) break;
    let v = 0, cnt = 0;
    for (let k = 0; k < 32; k++) {
      const b = rnw === 0 ? cy[first + k].dr : tb(first + k);
      v = (v | (b << k)) >>> 0;
      cnt += b;
    }
    const p = rnw === 0 ? cy[first + 32].dr : tb(first + 32);
    let dtxt = v.toString(16).toUpperCase().padStart(8, "0"), kind = "data";
    if (cnt % 2 !== p) { dtxt = "!" + dtxt; kind = "parity-error"; } else good = true;
    spans.push(pspan(cy[first].xf, cy[first + 31].xr + per / 2, n, dtxt, kind, v));
    toks.push(dtxt); units.push(v);
    explained += unexplained(c, first + 33);
    c = first + 32;
    next = c + 1;
    lastC = Math.max(lastC, next);
  }
  flushResets(cy.length);
  // the record's ends cut a transfer: the busy cycles before the first and
  // after the last, up to a transfer's 46 each, are its rest (decode_clocked.go)
  const edges = firstC < cy.length ? Math.min(unexplained(0, firstC), 46) + Math.min(unexplained(Math.min(lastC, cy.length), cy.length), 46) : 0;
  const cover = Math.min(1, explained / Math.max(1, cy.length - edges));
  let run = 0, even = 0;
  for (let k = 1; k < cy.length; k++) {
    const g = cy[k].xf - cy[k - 1].xf;
    if (g > 4 * per) continue;
    run++;
    if (g >= 0.75 * per && g <= 1.25 * per) even++;
  }
  good = good && cover >= 0.5 && run > 0 && even >= 0.8 * run;
  if (!spans.length) return fail("swd", "no SWD request");
  const res = { ok: good, proto: "swd", spans, text: toks.join(" "), bytes: units, meta: { threshold: C.threshold, spb: per } };
  if (colTimeS > 0) res.meta.baud = Math.floor(1 / (per * colTimeS) + 0.5);
  if (!good) res.error = "no SWD transfer acknowledged OK whose parity checks";
  return res;
}

const MDIO_C22 = { 1: "WR", 2: "RD" };
const MDIO_C45 = { 0: "ADDR", 1: "WR45", 2: "RDI45", 3: "RD45" };

// decodeMDIO mirrors decode_clocked.go DecodeMDIO.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
function decodeMDIO(mdc, mdio, colTimeS, cfg) {
  cfg = cfg || {};
  const L = clockedLines(mdc, mdio, cfg);
  if (L.why) return fail("mdio", L.why);
  const { E, per, C } = L;
  const R = E.filter((e) => e.dir > 0);
  const n = C.n, spans = [], toks = [], units = [];
  let good = false, ones = 0;
  for (let i = 0; i < R.length; i++) {
    if (R[i].d === 1) { ones++; continue; }
    const pre = ones;
    ones = 0;
    if (pre < 16 || i + 31 >= R.length) continue;
    // TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
    const bits = (from, cnt) => { let v = 0; for (let k = 0; k < cnt; k++) v = (v << 1) | R[i + from + k].d; return v; };
    const st = bits(0, 2);
    if (st !== 1 && st !== 0) continue;
    const op = bits(2, 2), phy = bits(4, 5), reg = bits(9, 5), ta = bits(14, 2), data = bits(16, 16);
    const name = st === 1 ? MDIO_C22[op] : MDIO_C45[op];
    if (!name) continue;
    const head = (st << 12) | (op << 10) | (phy << 5) | reg;
    spans.push(pspan(R[i].x, R[i].x, n, "", "sof", 0));
    const htxt = name + " " + hex2(phy) + "." + hex2(reg);
    spans.push(pspan(R[i].x, R[i + 14].x, n, htxt, "data", head));
    toks.push(htxt); units.push(head);
    let dtxt = data.toString(16).toUpperCase().padStart(4, "0"), kind = "data";
    if (ta & 1) { dtxt = "!" + dtxt; kind = "frame-error"; } else good = true;
    spans.push(pspan(R[i + 16].x, R[i + 31].x + per, n, dtxt, kind, data));
    toks.push(dtxt); units.push(data);
    i += 31;
  }
  if (!spans.length) return fail("mdio", "no MDIO frame");
  const res = { ok: good, proto: "mdio", spans, text: toks.join(" "), bytes: units, meta: { threshold: C.threshold, spb: per } };
  if (colTimeS > 0) res.meta.baud = Math.floor(1 / (per * colTimeS) + 0.5);
  if (!good) res.error = "no MDIO frame a PHY answered";
  return res;
}

// decodeWiegand mirrors decode_clocked.go DecodeWiegand.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
function decodeWiegand(d0, d1, colTimeS, cfg) {
  cfg = cfg || {};
  if (!d1) return fail("wiegand", "needs both lines (D0 and D1)");
  const A = sliceChannel(d0, clockedOpts(cfg.threshold));
  const B = sliceChannel(d1, clockedOpts(cfg.thresholdB));
  if (!A.ok && !B.ok) return fail("wiegand", A.reason);
  if (!(colTimeS > 0)) return fail("wiegand", "no sample time");
  const ps = [];
  [A, B].forEach((S, v) => {
    if (!S.ok) return;
    for (const r of levelRuns(S)) {
      if (r.lvl === 0 && !r.open) {
        const w = runUs(r, colTimeS);
        if (w >= 10 && w <= 500) ps.push({ x0: r.x0, x1: r.x1, v });
      }
    }
  });
  ps.sort((p, q) => (p.x0 - q.x0) || (p.v - q.v));
  if (ps.length < 4) return fail("wiegand", "too few Wiegand pulses");
  const gaps = [];
  for (let k = 1; k < ps.length; k++) gaps.push(ps[k].x0 - ps[k - 1].x0);
  gaps.sort((p, q) => p - q);
  const split = Math.max(3 * gaps[gaps.length >> 1], 3e-3 / colTimeS);
  const n = Math.max(A.ok ? A.n : 0, B.ok ? B.n : 0);
  const spans = [], toks = [], units = [];
  let good = false, interval = 0;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
  const emit = (fr) => {
    const nb = fr.length;
    spans.push(pspan(fr[0].x0, fr[0].x0, n, "", "sof", 0));
    const lbl = nb + "b";
    spans.push(pspan(fr[0].x0, fr[nb - 1].x1, n, lbl, "rw", nb));
    toks.push(lbl);
    // TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
    const bits = (from, cnt) => { let v = 0, ones = 0; for (let k = 0; k < cnt; k++) { v = v * 2 + fr[from + k].v; ones += fr[from + k].v; } return { v, ones }; };
    // TRLC-LINKS: REQ-SDS-018, REQ-SDS-236
    const field = (from, cnt, pfx, bad) => {
      const { v } = bits(from, cnt);
      let txt = pfx + v, kind = "data";
      if (bad) { txt = "!" + txt; kind = "parity-error"; }
      spans.push(pspan(fr[from].x0, fr[from + cnt - 1].x1, n, txt, kind, v));
      toks.push(txt); units.push(v);
    };
    if (nb === 26 || nb === 34) {
      const h = nb / 2;
      const e = bits(0, h).ones, o = bits(h, h).ones;
      const fc = nb === 34 ? 16 : 8;
      field(1, fc, "F", e % 2 !== 0);
      field(1 + fc, 16, "C", o % 2 !== 1);
      good = good || (e % 2 === 0 && o % 2 === 1);
      return;
    }
    for (let k = 0; k < nb; k += 8) {
      const cnt = Math.min(8, nb - k);
      const { v } = bits(k, cnt);
      const txt = hex2(v);
      spans.push(pspan(fr[k].x0, fr[k + cnt - 1].x1, n, txt, "data", v));
      toks.push(txt); units.push(v);
    }
  };
  let start = 0;
  for (let k = 1; k <= ps.length; k++) {
    if (k === ps.length || ps[k].x0 - ps[k - 1].x0 > split) {
      if (k - start >= 4) {
        emit(ps.slice(start, k));
        if (interval === 0) interval = (ps[k - 1].x0 - ps[start].x0) / (k - 1 - start);
      }
      start = k;
    }
  }
  if (!spans.length) return fail("wiegand", "no Wiegand frame");
  const res = { ok: good, proto: "wiegand", spans, text: toks.join(" "), bytes: units,
    meta: { threshold: A.ok ? A.threshold : B.threshold, spb: interval } };
  if (interval > 0) res.meta.baud = Math.floor(1 / (interval * colTimeS) + 0.5);
  if (!good) res.error = "no 26- or 34-bit Wiegand frame whose parities check";
  return res;
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { decodePS2, decodeSWD, decodeMDIO, decodeWiegand };
