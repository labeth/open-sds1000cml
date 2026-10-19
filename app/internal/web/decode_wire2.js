// ENGMODEL-OWNER-UNIT: FU-WEB-DECODE
// decode_wire2.js — SD card (SD mode CMD line), Atmel PDI, compact JTAG,
// SEGA Maple bus and digital calipers, a classic script mirroring
// decode_wire2.go step for step. Uses sliceChannel, logicAt, hex2 and fail
// from decode.js, clockedLines/clockedOpts from decode_clocked.js,
// medianOf from decode_motion.js and wireOut from decode_wire1.js.

// risingBits mirrors decode_wire2.go risingBits.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
function risingBits(a, b, cfg) {
  const L = clockedLines(a, b, cfg);
  if (L.why) return { why: L.why };
  const out = [], per = [];
  for (const e of L.E) {
    if (e.dir > 0) {
      if (out.length) per.push(e.x - out[out.length - 1].x);
      out.push({ v: e.d, x: e.x });
    }
  }
  return { B: out, per: medianOf(per), C: L.C, why: "" };
}

// crc7 mirrors decode_wire2.go crc7.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
function crc7(bits) {
  let c = 0;
  for (const b of bits) {
    const fb = ((c >> 6) ^ b) & 1;
    c = (c << 1) & 0x7f;
    if (fb) c ^= 0x09;
  }
  return c;
}

// bitsInt mirrors decode_wire2.go bitsInt.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
function bitsInt(b) {
  let v = 0;
  for (const x of b) v = (v << 1) | x;
  return v;
}

// decodeSDCard mirrors decode_wire2.go DecodeSDCard.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
function decodeSDCard(a, b, colTimeS, cfg) {
  cfg = cfg || {};
  const R = risingBits(a, b, cfg);
  if (R.why) return fail("sdcard", R.why);
  const { B, per, C } = R;
  const o = wireOut(C.n);
  let good = false, lastCmd = -1, acmd = false, nextACMD = false;
  for (let i = 0; i < B.length; i++) {
    if (B[i].v !== 0 || i + 1 >= B.length) continue;
    const host = B[i + 1].v === 1;
    let n = 48;
    if (!host && !acmd && (lastCmd === 2 || lastCmd === 9 || lastCmd === 10)) n = 136;
    if (i + n > B.length) break;
    const t = B.slice(i, i + n);
    const bits = t.map((x) => x.v);
    o.add(t[0].x, t[0].x, "", "sof", 0, false);
    let idx = 0;
    for (let k = 2; k < 8; k++) idx = (idx << 1) | bits[k];
    let name = "", ckOK = true;
    if (host) {
      const wasNext = nextACMD;
      acmd = wasNext;
      nextACMD = idx === 55 && !wasNext;
      name = (acmd ? "ACMD" : "CMD") + idx;
      lastCmd = idx;
      ckOK = crc7(bits.slice(0, 40)) === bitsInt(bits.slice(40, 47));
    } else if (n === 136) {
      name = "R2";
      ckOK = crc7(bits.slice(8, 128)) === bitsInt(bits.slice(128, 135));
    } else if (acmd && lastCmd === 41) {
      name = "R3";
    } else {
      name = "R1";
      if (!acmd && (lastCmd === 3 || lastCmd === 7)) name = "R6";
      else if (!acmd && lastCmd === 8) name = "R7";
      ckOK = crc7(bits.slice(0, 40)) === bitsInt(bits.slice(40, 47));
    }
    ckOK = ckOK && bits[n - 1] === 1;
    for (let k = 0; k < n / 8; k++) {
      const v = bitsInt(bits.slice(8 * k, 8 * k + 8));
      let txt = k === 0 ? name : hex2(v), kind = "data";
      if (k === n / 8 - 1 && !ckOK) { txt = "!" + txt; kind = "frame-error"; }
      o.add(t[8 * k].x, t[8 * k].x + 8 * per, txt, kind, v, true);
    }
    good = good || ckOK;
    i += n - 1;
  }
  if (!o.spans.length) return fail("sdcard", "no SD token");
  const res = { ok: good, proto: "sdcard", spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: C.threshold, spb: per } };
  if (colTimeS > 0 && per > 0) res.meta.baud = Math.round(1 / (per * colTimeS));
  if (!good) res.error = "no SD token whose CRC checks";
  return res;
}

const PDI_OPS = ["LDS", "LD", "STS", "ST", "LDCS", "REPEAT", "STCS", "KEY"];

// decodePDI mirrors decode_wire2.go DecodePDI.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
function decodePDI(a, b, colTimeS, cfg) {
  cfg = cfg || {};
  const R = risingBits(a, b, cfg);
  if (R.why) return fail("pdi", R.why);
  const { B, per, C } = R;
  const o = wireOut(C.n);
  let good = false, groups = [], cur = [], rep = 0, isRep = false;
  for (let i = 0; i < B.length; i++) {
    if (B[i].v === 1) continue;
    let run = 0;
    while (i + run < B.length && B[i + run].v === 0) run++;
    if (run >= 11) {
      o.add(B[i].x, B[i + run - 1].x + per, "BRK", "rw", 0, false);
      i += run - 1;
      groups = []; cur = []; rep = 0;
      continue;
    }
    if (i + 11 > B.length) break;
    const c = B.slice(i, i + 11);
    let v = 0, p = 0;
    for (let k = 0; k < 8; k++) { v |= c[1 + k].v << k; p ^= c[1 + k].v; }
    i += 10;
    const x0 = c[0].x, x1 = c[10].x + per;
    if (p !== c[9].v || c[10].v !== 1) {
      o.add(x0, x1, "!" + hex2(v), "parity-error", v, true);
      groups = []; cur = [];
      continue;
    }
    if (!groups.length) {
      const op = v >> 5, na = ((v >> 2) & 3) + 1, nd = (v & 3) + 1;
      if (op === 0 || op === 2) groups = [na, nd];
      else if (op === 1 || op === 3) {
        groups = [nd];
        for (; rep > 0; rep--) groups.push(nd);
      } else if (op === 4 || op === 6) groups = [1];
      else if (op === 5) groups = [nd];
      else groups = [8];
      isRep = op === 5;
      o.add(x0, x0, "", "sof", 0, false);
      o.add(x0, x1, PDI_OPS[op], "data", v, true);
      good = true;
      continue;
    }
    o.add(x0, x1, hex2(v), "data", v, true);
    cur.push(v);
    if (cur.length === groups[0]) {
      if (isRep) {
        rep = 0;
        for (let k = cur.length - 1; k >= 0; k--) rep = Math.min(rep * 256 + cur[k], 4096);
      }
      groups = groups.slice(1); cur = [];
    }
  }
  if (!o.spans.length) return fail("pdi", "no PDI character");
  const res = { ok: good, proto: "pdi", spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: C.threshold, spb: per } };
  if (colTimeS > 0 && per > 0) res.meta.baud = Math.round(1 / (per * colTimeS));
  if (!good) res.error = "no PDI instruction";
  return res;
}

const JTAG_NEXT = [[1, 0], [1, 2], [3, 9], [4, 5], [4, 5], [6, 8], [6, 7], [4, 8], [1, 2],
  [10, 0], [11, 12], [11, 12], [13, 15], [13, 14], [11, 15], [1, 2]];

// decodeCJTAG mirrors decode_wire2.go DecodeCJTAG.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
function decodeCJTAG(a, b, colTimeS, cfg) {
  cfg = cfg || {};
  if (!b) return fail("cjtag", "needs both lines (TCKC and TMSC)");
  const K = sliceChannel(a, clockedOpts(cfg.threshold));
  if (!K.ok) return fail("cjtag", K.reason);
  const M = sliceChannel(b, clockedOpts(cfg.thresholdB));
  if (!M.ok) return fail("cjtag", M.reason);
  const o = wireOut(K.n);
  const rises = K.edges.filter((e) => e.dir > 0).map((e) => e.x);
  const esc = new Array(rises.length + 1).fill(0);
  let ke = 0, mi = 0;
  for (let i = 0; i < rises.length; i++) {
    const r = rises[i];
    while (ke < K.edges.length && K.edges[ke].x <= r) ke++;
    const fall = ke < K.edges.length ? K.edges[ke].x : K.n;
    while (mi < M.edges.length && M.edges[mi].x <= r) mi++;
    while (mi < M.edges.length && M.edges[mi].x <= fall) { esc[i + 1]++; mi++; }
  }
  const FOUR = 0, OAC = 1, OSCAN = 2;
  let mode = FOUR, oacp = 0, oaclen = 12, cyc = 0, state = 1, tdi = 0, ti = 0, to = 0, nb = 0, x0 = 0, good = false;
  const per = [];
  for (let i = 0; i < rises.length; i++) {
    const r = rises[i];
    if (i > 0) per.push(r - rises[i - 1]);
    const e = esc[i];
    if (e >= 8) {
      mode = FOUR;
      o.add(r, r, "", "sof", 0, false);
      o.add(r, r, "ESC" + e, "rw", e, false);
    } else if (e === 6) {
      mode = OAC; oacp = 0; oaclen = 12;
      o.add(r, r, "", "sof", 0, false);
      o.add(r, r, "ESC6", "rw", 6, false);
    }
    const tmsc = logicAt(M, r);
    if (mode === OAC) {
      oacp++;
      if (oacp === 8 && tmsc === 0) oaclen = 36;
      if (oacp > oaclen) { mode = OSCAN; cyc = 1; state = 0; good = true; }
      continue;
    }
    if (mode !== OSCAN) continue;
    if (cyc === 0) { tdi = 1 - tmsc; cyc = 1; continue; }
    if (cyc === 1) { cyc = 2; continue; }
    cyc = 0;
    const tms = logicAt(M, rises[i - 1]);
    const tdo = tmsc;
    if (state === 4 || state === 11) {
      if (nb === 0) x0 = rises[i - 2];
      if (nb < 31) { ti |= tdi << nb; to |= tdo << nb; }
      nb++;
    }
    state = JTAG_NEXT[state][tms & 1];
    if (state === 8 || state === 15) {
      const reg = state === 15 ? "IR" : "DR";
      o.add(x0, r, reg + ti.toString(16).toUpperCase(), "data", ti, true);
      o.add(x0, r, "TDO" + to.toString(16).toUpperCase(), "rw", to, false);
      good = good || nb > 0;
      ti = 0; to = 0; nb = 0;
    }
  }
  if (!o.spans.length) return fail("cjtag", "no cJTAG escape");
  const T = medianOf(per);
  let regular = 0;
  for (const p of per) if (p >= 0.8 * T && p <= 1.2 * T) regular++;
  const res = { ok: good, proto: "cjtag", spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: K.threshold, spb: T } };
  if (per.length > 0) res.meta.cover = regular / per.length;
  if (colTimeS > 0 && T > 0) res.meta.baud = Math.round(1 / (T * colTimeS));
  if (!good) res.error = "no cJTAG scan";
  return res;
}

const MAPLE_NAMES = ["L", "S", "D", "C"];

// decodeMaple mirrors decode_wire2.go DecodeMaple.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
function decodeMaple(a, b, colTimeS, cfg) {
  cfg = cfg || {};
  if (!b) return fail("maple", "needs both lines (SDCKA and SDCKB)");
  const A = sliceChannel(a, clockedOpts(cfg.threshold));
  if (!A.ok) return fail("maple", A.reason);
  const Bs = sliceChannel(b, clockedOpts(cfg.thresholdB));
  if (!Bs.ok) return fail("maple", Bs.reason);
  const evs = [];
  let la = A.level[0], lb = Bs.level[0], ia = 0, ib = 0;
  while (ia < A.edges.length || ib < Bs.edges.length) {
    if (ib >= Bs.edges.length || (ia < A.edges.length && A.edges[ia].x <= Bs.edges[ib].x)) {
      const e = A.edges[ia++];
      la = (e.dir + 1) / 2;
      evs.push({ x: e.x, line: 0, fall: e.dir < 0, la, lb });
    } else {
      const e = Bs.edges[ib++];
      lb = (e.dir + 1) / 2;
      evs.push({ x: e.x, line: 1, fall: e.dir < 0, la, lb });
    }
  }
  const o = wireOut(A.n);
  let good = false;
  const bitT = [];
  for (let k = 0; k < evs.length; k++) {
    if (!(evs[k].line === 0 && evs[k].fall && evs[k].lb === 1)) continue;
    let cnt = 0, j = k + 1;
    for (; j < evs.length && !(evs[j].line === 0 && !evs[j].fall); j++) if (evs[j].line === 1 && evs[j].fall) cnt++;
    if (j >= evs.length || evs[j].lb !== 1 || (cnt !== 4 && cnt !== 6)) continue;
    o.add(evs[k].x, evs[j].x, "", "sof", 0, false);
    const bytes = [], bx = [];
    let ended = false, endX0 = 0, endX1 = 0;
    for (let byteDone = true; byteDone;) {
      byteDone = false;
      let ca = 0, cb = 0, v = 0, initial = true, x0 = 0;
      for (j++; j < evs.length && cb < 4; j++) {
        const e = evs[j];
        if (!e.fall) continue;
        if (e.line === 0) {
          if (ca === cb) {
            if (ca === 0) x0 = e.x;
            v = (v << 1) | e.lb;
            ca++;
            continue;
          }
          if (ca === 1 && cb === 0 && v === 0 && e.lb === 0) {
            for (let q = j + 1; q < evs.length; q++) {
              if (evs[q].fall) break;
              if (evs[q].la === 1 && evs[q].lb === 1) {
                ended = true; endX0 = e.x; endX1 = evs[q].x;
                j = q;
                break;
              }
            }
          }
          break;
        }
        if (ca === cb + 1) { v = (v << 1) | e.la; cb++; continue; }
        if (ca === 0 && cb === 0 && e.la === 1 && initial) { initial = false; continue; }
        break;
      }
      if (cb === 4) {
        bytes.push(v); bx.push(x0);
        bitT.push((evs[j - 1].x - x0) / 7);
        byteDone = true;
        j--;
      }
    }
    const want = bytes.length > 0 ? 4 * (bytes[0] + 1) + 1 : 0;
    let ck = 0;
    const T = medianOf(bitT);
    for (let i = 0; i < bytes.length; i++) {
      const x = bytes[i];
      let txt = hex2(x), kind = "data";
      if (i < 4) txt = MAPLE_NAMES[i] + txt;
      else if (i === bytes.length - 1) {
        txt = "K" + txt;
        if (x !== ck || bytes.length !== want || !ended) { txt = "!" + txt; kind = "frame-error"; } else good = true;
      }
      ck ^= x;
      o.add(bx[i], bx[i] + 8 * T, txt, kind, x, true);
    }
    if (ended) o.add(endX0, endX1, "END", "stop", 0, false);
    k = j;
  }
  if (!o.spans.length) return fail("maple", "no Maple frame");
  const T = medianOf(bitT);
  const res = { ok: good, proto: "maple", spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: A.threshold, spb: T } };
  if (colTimeS > 0 && T > 0) res.meta.baud = Math.round(1 / (T * colTimeS));
  if (!good) res.error = "no Maple frame whose checksum checks";
  return res;
}

// decodeCaliper mirrors decode_wire2.go DecodeCaliper.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
function decodeCaliper(a, b, colTimeS, cfg) {
  cfg = cfg || {};
  const R = risingBits(a, b, cfg);
  if (R.why) return fail("caliper", R.why);
  if (!(colTimeS > 0)) return fail("caliper", "no sample time");
  const { B, per, C } = R;
  const quiet = 10e-3 / colTimeS;
  const o = wireOut(C.n);
  let good = false;
  for (let i = 0; i + 24 <= B.length;) {
    if (i > 0 && B[i].x - B[i - 1].x < quiet) { i++; continue; }
    const f = B.slice(i, i + 24);
    let whole = true;
    for (let k = 1; k < 24; k++) whole = whole && f[k].x - f[k - 1].x < quiet;
    if (!whole) { i++; continue; }
    o.add(f[0].x, f[0].x, "", "sof", 0, false);
    let num = 0, flags = 0;
    for (let k = 0; k < 16; k++) num |= f[k].v << k;
    for (let k = 0; k < 8; k++) flags |= f[16 + k].v << k;
    o.add(f[0].x, f[7].x + per, hex2(num & 0xff), "data", num & 0xff, true);
    o.add(f[8].x, f[15].x + per, hex2(num >> 8), "data", num >> 8, true);
    o.add(f[16].x, f[23].x + per, hex2(flags), "data", flags, true);
    let v = num;
    if (((flags >> 4) & 1) === 1 && num !== 0) v = -v;
    const txt = ((flags >> 7) & 1) === 1 ? String(v / 2000) + "in" : String(v / 100) + "mm";
    o.add(f[0].x, f[23].x + per, txt, "rw", num, false);
    good = true;
    i += 24;
  }
  if (!o.spans.length) return fail("caliper", "no caliper frame");
  const res = { ok: good, proto: "caliper", spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: C.threshold, spb: per } };
  if (per > 0) res.meta.baud = Math.round(1 / (per * colTimeS));
  return res;
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { decodeSDCard, decodePDI, decodeCJTAG, decodeMaple, decodeCaliper };
