// ENGMODEL-OWNER-UNIT: FU-WEB-DECODE
// decode_rfid.js — 125 kHz RFID: EM4100 tag frames and a reader's EM4305
// and T55xx commands, a classic script mirroring decode_rfid.go step for
// step. Uses sliceChannel, hex2 and fail from decode.js, levelRuns/pspan
// from decode_pulsewire.js and quantile/shortestRunCluster from
// decode_rfcodes.js.

const RFID_COIL_HZ = 125000;
const EM4305_CMDS = ["CMD0", "LOGIN", "WRITE", "CMD3", "READ", "DISABLE", "PROTECT", "CMD7"];

// rfidGaps mirrors decode_rfid.go rfidGaps.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
function rfidGaps(S, fc) {
  const runs = levelRuns(S);
  // The closed runs' lengths straight into a typed array, sorted numerically
  // there (quantile's 0.5 point), not filtered, mapped and copied.
  let nl = 0;
  for (const r of runs) if (!r.open) nl++;
  const lens = new Float64Array(nl);
  nl = 0;
  for (const r of runs) if (!r.open) lens[nl++] = r.x1 - r.x0;
  lens.sort();
  const carrier = (nl ? lens[Math.trunc(0.5 * (nl - 1))] : 0) < 0.75 * fc;
  const out = [];
  for (const r of runs) {
    if (r.open) continue;
    if ((carrier && r.x1 - r.x0 >= 3 * fc) || (!carrier && r.lvl === 0)) out.push({ x0: r.x0, x1: r.x1 });
  }
  return { gaps: out, carrier };
}

// rfidOut mirrors decode_rfid.go rfidOut.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
function rfidOut(n) {
  const o = { n, spans: [], toks: [], units: [] };
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
  o.add = (x0, x1, text, kind, val, unit) => {
    o.spans.push(pspan(x0, x1, n, text, kind, val));
    if (text !== "") o.toks.push(text);
    if (unit) o.units.push(val);
  };
  return o;
}

// bitsMSB mirrors decode_rfid.go bitsMSB.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
function bitsMSB(b, at, n) {
  let v = 0;
  for (let i = 0; i < n; i++) v = (v << 1) | b[at + i].v;
  return v;
}

// evenOK mirrors decode_rfid.go evenOK.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
function evenOK(b, at, n) {
  let p = 0;
  for (let i = 0; i <= n; i++) p ^= b[at + i].v;
  return p === 0;
}

// em4305Fields mirrors decode_rfid.go em4305Fields.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
function em4305Fields(o, b) {
  if (b.length !== 50 && b.length !== 57) {
    for (const x of b) o.add(x.x0, x.x1, String(x.v), "rw", x.v, false);
    if (b.length > 0) o.add(b[b.length - 1].x1, b[b.length - 1].x1, "!", "frame-error", 0, false);
    return false;
  }
  let ok = true;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
  const unit = (at, n, txt, v, good) => {
    let kind = "data";
    if (!good) { txt = "!" + txt; kind = "parity-error"; ok = false; }
    o.add(b[at].x0, b[at + n - 1].x1, txt, kind, v, true);
  };
  const cmd = bitsMSB(b, 1, 3);
  unit(1, 4, EM4305_CMDS[cmd], cmd, b[0].v === 0 && evenOK(b, 1, 3));
  let at = 5;
  if (b.length === 57) {
    const a = b[5].v | (b[6].v << 1) | (b[7].v << 2) | (b[8].v << 3);
    unit(5, 7, "A" + a, a, evenOK(b, 5, 6));
    at = 12;
  }
  let col = 0;
  for (let k = 0; k < 4; k++) {
    const v = bitsMSB(b, at + 9 * k, 8);
    col ^= v;
    unit(at + 9 * k, 9, hex2(v), v, evenOK(b, at + 9 * k, 8));
  }
  if (bitsMSB(b, at + 36, 8) !== col || b[at + 44].v !== 0) {
    o.add(b[at + 36].x0, b[at + 44].x1, "!COL", "frame-error", 0, false);
    ok = false;
  }
  return ok;
}

// t55xxFields mirrors decode_rfid.go t55xxFields.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
function t55xxFields(o, b) {
  const n = b.length;
  if (n !== 70 && n !== 38 && n !== 2) {
    for (const x of b) o.add(x.x0, x.x1, String(x.v), "rw", x.v, false);
    if (n > 0) o.add(b[n - 1].x1, b[n - 1].x1, "!", "frame-error", 0, false);
    return false;
  }
  const op = bitsMSB(b, 0, 2);
  o.add(b[0].x0, b[1].x1, "OP" + (op >> 1) + (op & 1), "data", op, true);
  if (n === 2) return true;
  let at = 2;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
  const word = (prefix) => {
    for (let k = 0; k < 4; k++) {
      const v = bitsMSB(b, at + 8 * k, 8);
      o.add(b[at + 8 * k].x0, b[at + 8 * k + 7].x1, prefix + hex2(v), "data", v, true);
    }
    at += 32;
  };
  if (n === 70) word("P");
  o.add(b[at].x0, b[at].x1, "LK" + b[at].v, "data", b[at].v, true);
  at++;
  word("");
  const blk = bitsMSB(b, at, 3);
  o.add(b[at].x0, b[at + 2].x1, "BL" + blk, "data", blk, true);
  return true;
}

// rfidWrites mirrors decode_rfid.go rfidWrites.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
function rfidWrites(proto, codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail(proto, S.reason);
  if (!(colTimeS > 0)) return fail(proto, "no sample time");
  const coil = (cfg.baud | 0) > 0 ? cfg.baud | 0 : RFID_COIL_HZ;
  const fc = 1 / (coil * colTimeS);
  const { gaps, carrier } = rfidGaps(S, fc);
  const em = proto === "em4305";
  const start = em ? 40 : 20, wgap = em ? 12 : 20, end = em ? 300 : 64;
  const o = rfidOut(S.n);
  let good = false, frameBits = 0, bits = [], inCmd = false, last = 0;
  // a command opened within a command's end of the record's start may have
  // begun before it: one that fails its checks is marked cut ("?" gap)
  let cut = false, fromSpan = 0, fromTok = 0;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
  const finish = () => {
    if (!inCmd) return;
    inCmd = false;
    let ok;
    if (em) ok = em4305Fields(o, bits);
    else {
      ok = t55xxFields(o, bits);
      if (ok && frameBits === 0) frameBits = bits.length;
    }
    if (!ok && cut) {
      for (let i = fromSpan; i < o.spans.length; i++) if (o.spans[i].kind === "frame-error") { o.spans[i].kind = "gap"; o.spans[i].text = "?"; }
      for (let i = fromTok; i < o.toks.length; i++) if (o.toks[i] === "!") o.toks[i] = "?";
    }
    good = good || ok;
    bits = [];
  };
  for (const g of gaps) {
    if (inCmd && g.x0 - last > end * fc) finish();
    const glen = g.x1 - g.x0;
    if (!inCmd) {
      if (glen > start * fc) {
        inCmd = true; last = g.x1;
        cut = g.x0 < end * fc; fromSpan = o.spans.length; fromTok = o.toks.length;
        o.add(g.x0, g.x1, "", "sof", 0, false);
      }
      continue;
    }
    if (glen <= wgap * fc) continue;
    const d = g.x0 - last;
    if (em) {
      if (d > 15 * fc && d < 27 * fc) bits.push({ v: 0, x0: last, x1: g.x1 });
      else if (d > 32 * fc && d < 300 * fc) {
        const k = Math.trunc(d / (32 * fc));
        for (let i = 0; i < k; i++) { const x = last + i * 32 * fc; bits.push({ v: 1, x0: x, x1: x + 32 * fc }); }
        if (glen > 15 * fc && glen < 27 * fc) bits.push({ v: 0, x0: last + k * 32 * fc, x1: g.x1 });
      }
    } else if (d > 16 * fc && d < 31 * fc) bits.push({ v: 0, x0: last, x1: g.x0 });
    else if (d > 48 * fc && d < 63 * fc) bits.push({ v: 1, x0: last, x1: g.x0 });
    last = g.x1;
  }
  if (inCmd && S.n - last > end * fc) finish();
  if (!o.spans.length) return fail(proto, "no reader command");
  const res = { ok: good, proto, spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: S.threshold, spb: fc, baud: Math.round(coil), frameBits, inverted: carrier } };
  if (!good) res.error = "no whole reader command";
  return res;
}

// decodeEM4305 mirrors decode_rfid.go DecodeEM4305.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
function decodeEM4305(codes, colTimeS, cfg) { return rfidWrites("em4305", codes, colTimeS, cfg); }

// decodeT55xx mirrors decode_rfid.go DecodeT55xx.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
function decodeT55xx(codes, colTimeS, cfg) { return rfidWrites("t55xx", codes, colTimeS, cfg); }

// manchesterStream mirrors decode_rfid.go manchesterStream.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
function manchesterStream(runs, hp) {
  const lv = [], xs = [];
  for (let k = 0; k < runs.length; k++) {
    const r = runs[k];
    let q = Math.round((r.x1 - r.x0) / hp);
    if (r.open && q > 2 && k > 0) q = 2;
    if (k === 0) q = 3;
    if (q < 1 || q > 2) {
      lv.push(-1); xs.push(r.x0);
      if (q > 2) { lv.push(r.lvl); xs.push(r.x1 - hp); }
      continue;
    }
    for (let j = 0; j < q; j++) { lv.push(r.lvl); xs.push(r.x0 + j * (r.x1 - r.x0) / q); }
  }
  const bits = [];
  for (let j = 0; j + 1 < lv.length;) {
    if (lv[j] < 0 || lv[j + 1] < 0 || lv[j] === lv[j + 1]) {
      if (bits.length > 0 && bits[bits.length - 1].v >= 0) bits.push({ v: -1, x0: xs[j], x1: xs[j] });
      j++;
      continue;
    }
    bits.push({ v: lv[j], x0: xs[j], x1: xs[j] + 2 * hp });
    j += 2;
  }
  return bits;
}

// decodeEM4100 mirrors decode_rfid.go DecodeEM4100.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-252
function decodeEM4100(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("em4100", S.reason);
  const runs = levelRuns(S);
  let hp = shortestRunCluster(runs);
  const baud = cfg.baud | 0;
  if (baud > 0 && colTimeS > 0) hp = 1 / (2 * baud * colTimeS);
  if (!(hp > 0)) return fail("em4100", "no bits");
  const bits = manchesterStream(runs, hp);
  const o = rfidOut(S.n);
  let good = false;
  for (let i = 0; i + 64 <= bits.length; i++) {
    if (i > 0 && bits[i - 1].v === 1) continue;
    let hdr = true;
    for (let k = 0; k < 9; k++) hdr = hdr && bits[i + k].v === 1;
    if (!hdr) continue;
    const f = bits.slice(i, i + 64);
    if (!f.every((x) => x.v >= 0)) continue;
    o.add(f[0].x0, f[0].x0, "", "sof", 0, false);
    let ok = true, col = 0;
    for (let by = 0; by < 5; by++) {
      const r0 = 9 + 10 * by, r1 = 14 + 10 * by;
      const v = (bitsMSB(f, r0, 4) << 4) | bitsMSB(f, r1, 4);
      col ^= bitsMSB(f, r0, 4) ^ bitsMSB(f, r1, 4);
      let txt = hex2(v), kind = "data";
      if (!evenOK(f, r0, 4) || !evenOK(f, r1, 4)) { txt = "!" + txt; kind = "parity-error"; ok = false; }
      o.add(f[r0].x0, f[r1 + 4].x1, txt, kind, v, true);
    }
    if (bitsMSB(f, 59, 4) !== col || f[63].v !== 0) { o.add(f[59].x0, f[63].x1, "!COL", "frame-error", 0, false); ok = false; }
    good = good || ok;
    i += 63;
  }
  if (!o.spans.length) return fail("em4100", "no EM4100 frame");
  const res = { ok: good, proto: "em4100", spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: S.threshold, spb: 2 * hp } };
  if (colTimeS > 0) res.meta.baud = Math.round(1 / (2 * hp * colTimeS));
  if (!good) res.error = "no EM4100 frame whose parities check";
  return res;
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { decodeEM4100, decodeEM4305, decodeT55xx };
