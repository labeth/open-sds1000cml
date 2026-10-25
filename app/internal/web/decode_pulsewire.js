// ENGMODEL-OWNER-UNIT: FU-WEB-DECODE
// decode_pulsewire.js — the pulse-coded single-wire protocols (1-Wire,
// WS281x, DHT/AM230x, HDMI-CEC, SAE J1850 VPW), a classic script mirroring
// decode_pulsewire.go step for step. Uses sliceChannel, logicAt, hex2,
// fmtByte and fail from decode.js and uiClamp from decode_uartproto.js.

// levelRuns: the sliced line as runs of one level (decode_pulsewire.go).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
function levelRuns(S) {
  const E = S.edges;
  if (!E.length) return [{ lvl: logicAt(S, 0), x0: 0, x1: S.n, open: true }];
  const out = new Array(E.length + 1); // sized once, not grown a run at a time
  out[0] = { lvl: E[0].dir > 0 ? 0 : 1, x0: 0, x1: E[0].x, open: true };
  for (let k = 0; k < E.length; k++) {
    const last = k + 1 >= E.length;
    out[k + 1] = { lvl: E[k].dir > 0 ? 1 : 0, x0: E[k].x, x1: last ? S.n : E[k + 1].x, open: last };
  }
  return out;
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
function runUs(r, colTimeS) { return (r.x1 - r.x0) * colTimeS * 1e6; }

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
function pspan(x0, x1, n, text, kind, val) { return { i0: uiClamp(x0, n), i1: uiClamp(x1, n), text, kind, val }; }

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
function crc8Maxim(b) {
  let c = 0;
  for (const v of b) {
    c ^= v & 0xff;
    for (let i = 0; i < 8; i++) c = (c & 1) ? (c >>> 1) ^ 0x8C : c >>> 1;
  }
  return c;
}

const ONEWIRE_ROM = { 0x33: "RDROM", 0x0F: "RDROM", 0x55: "MATCH", 0xF0: "SEARCH", 0xEC: "ALARM",
  0xCC: "SKIP", 0x3C: "ODSKIP", 0x69: "ODMATCH", 0xA5: "RESUME" };

// decodeOneWire mirrors decode_pulsewire.go DecodeOneWire.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
function decodeOneWire(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("onewire", S.reason);
  if (!(colTimeS > 0)) return fail("onewire", "no sample time");
  const runs = levelRuns(S), n = S.n;
  let od = !!cfg.overdrive;
  const spans = [], toks = [], units = [];
  let bits = [];
  let good = false, inTx = false, phase = 0, romLeft = 0, rom = [];
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
  const flushByte = (bs, v, txt, kind) => {
    spans.push(pspan(bs[0].x0, bs[bs.length - 1].x1, n, txt, kind, v));
    toks.push(txt); units.push(v);
  };
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
  const takeBits = () => {
    while (phase !== 2 && bits.length >= 8) {
      const b = bits.slice(0, 8);
      let v = 0;
      b.forEach((x, i) => { v |= x.v << i; });
      bits = bits.slice(8);
      let txt = fmtByte(v, cfg.format), kind = "data";
      if (phase === 0) {
        if (ONEWIRE_ROM[v] !== undefined) txt = ONEWIRE_ROM[v];
        if (v === 0x33 || v === 0x0F || v === 0x55 || v === 0x69) { phase = 1; romLeft = 8; rom = []; }
        else if (v === 0xF0 || v === 0xEC) { phase = 2; romLeft = 0; }
        else phase = 3;
        if (v === 0x3C || v === 0x69) od = true;
        good = good || inTx;
      } else if (phase === 1) {
        rom.push(v);
        txt = hex2(v);
        if (--romLeft === 0) {
          if (crc8Maxim(rom.slice(0, 7)) !== v) { txt = "!" + txt; kind = "frame-error"; }
          phase = 3;
        }
      }
      flushByte(b, v, txt, kind);
    }
    while (phase === 2 && bits.length >= 24) {
      let v = 0;
      for (let i = 0; i < 8; i++) v |= bits[3 * i + 2].v << i;
      flushByte(bits.slice(0, 24), v, hex2(v), "data");
      bits = bits.slice(24);
      if (++romLeft === 8) { phase = 3; romLeft = 0; }
    }
  };
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
  const endTx = () => {
    if (bits.length) { spans.push(pspan(bits[0].x0, bits[bits.length - 1].x1, n, "?", "gap", 0)); toks.push("?"); }
    bits = [];
  };
  for (let k = 0; k < runs.length; k++) {
    const r = runs[k];
    if (r.lvl !== 0 || r.open) continue;
    const w = runUs(r, colTimeS);
    let slotMax = 120, lowr = 15, pdh = 60;
    if (od) { slotMax = 16; lowr = 2; pdh = 6; }
    if (w >= 440 || (od && w >= 40 && w < 80)) {
      endTx();
      if (w >= 440) { od = false; pdh = 60; }
      spans.push(pspan(r.x0, r.x1, n, "RST", "sof", 0));
      toks.push("RST");
      phase = 0; romLeft = 0; inTx = false;
      if (k + 2 < runs.length && !runs[k + 2].open && runUs(runs[k + 1], colTimeS) <= pdh * 1.25) {
        const p = runs[k + 2];
        spans.push(pspan(p.x0, p.x1, n, "PRES", "ack", 1));
        toks.push("PRES");
        inTx = true;
        k += 2;
      } else {
        spans.push(pspan(r.x1, r.x1, n, "NOPRES", "nak", 0));
        toks.push("NOPRES");
      }
    } else if (w < slotMax * 1.25) {
      const v = w < lowr ? 1 : 0;
      let end = r.x0 + slotMax / 2 * 1e-6 / colTimeS;
      if (k + 1 < runs.length) end = Math.min(end, runs[k + 1].x1);
      end = Math.max(end, r.x1);
      bits.push({ x0: r.x0, x1: end, v });
      takeBits();
    }
  }
  endTx();
  if (!spans.length) return fail("onewire", "no 1-Wire reset or slot");
  const res = { ok: good, proto: "onewire", spans, text: toks.join(" "), bytes: units,
    meta: { threshold: S.threshold, baud: od ? 125000 : 16300, spb: 1e-6 / colTimeS } };
  if (!good) res.error = "no 1-Wire reset with presence and a byte after it";
  return res;
}

// WS_EDGE_LATCH_US is the low that latches a WS281x frame at the record's
// ends (decode_pulsewire.go wsEdgeLatchUs).
const WS_EDGE_LATCH_US = 20;

// decodeWS281x mirrors decode_pulsewire.go DecodeWS281x.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
function decodeWS281x(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("ws281x", S.reason);
  if (!(colTimeS > 0)) return fail("ws281x", "no sample time");
  const runs = levelRuns(S), n = S.n;
  const spans = [], toks = [], units = [], periods = [];
  let good = false;
  let frame = [];
  // a low that latches: whole, or at a record end
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
  const latched = (r) => runUs(r, colTimeS) >= 50 || (r.open && runUs(r, colTimeS) >= WS_EDGE_LATCH_US);
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
  const flush = (latch) => {
    if (!frame.length) return;
    let lo = Infinity, hi = 0;
    const pers = [];
    for (const b of frame) {
      lo = Math.min(lo, b.hi); hi = Math.max(hi, b.hi);
      if (b.per > 0) pers.push(b.per);
    }
    const ps = Float64Array.from(pers).sort(); // numeric without a comparator call
    const med = ps.length ? ps[ps.length >> 1] : 0;
    let steady = ps.length > 0;
    for (const p of ps) steady = steady && p >= 0.75 * med && p <= 1.25 * med;
    spans.push(pspan(frame[0].x0, frame[0].x0, n, "", "sof", 0));
    const nb = Math.floor(frame.length / 8);
    for (let b = 0; b < nb; b++) {
      let v = 0;
      for (let i = 0; i < 8; i++) {
        const f = frame[8 * b + i];
        const per = f.per === 0 ? med : f.per;
        let one = f.hi > 0.5 * per;
        if (hi > 1.5 * lo) one = f.hi > (lo + hi) / 2;
        if (one) v |= 1 << (7 - i);
      }
      const end = frame[8 * b + 7].x0 + med;
      const txt = fmtByte(v, cfg.format);
      spans.push(pspan(frame[8 * b].x0, end, n, txt, "data", v));
      toks.push(txt); units.push(v);
    }
    if (frame.length % 8 !== 0) {
      spans.push(pspan(frame[8 * nb].x0, frame[frame.length - 1].x0 + med, n, "?", "gap", 0));
      toks.push("?");
    }
    if (!latch.open || latch.x1 > latch.x0) { spans.push(pspan(latch.x0, latch.x1, n, "RST", "rw", 0)); toks.push("RST"); }
    if (steady && nb >= 3 && nb % 3 === 0 && frame.length % 8 === 0 && latched(latch)) { good = true; periods.push(med); }
    frame = [];
  };
  let started = false;
  for (let k = 0; k < runs.length; k++) {
    const r = runs[k];
    if (r.lvl === 0) {
      if (latched(r)) {
        if (frame.length) { frame[frame.length - 1].per = 0; flush(r); }
        started = !r.open || r.x0 === 0;
      }
      continue;
    }
    if (!started || r.open || k + 1 >= runs.length) continue;
    const per = k + 2 < runs.length ? runs[k + 2].x0 - r.x0 : 0;
    if (runUs(r, colTimeS) > 10) { frame = []; started = false; continue; }
    frame.push({ x0: r.x0, hi: r.x1 - r.x0, per });
  }
  if (!spans.length) return fail("ws281x", "no WS281x frame");
  const res = { ok: good, proto: "ws281x", spans, text: toks.join(" "), bytes: units, meta: { threshold: S.threshold } };
  if (periods.length) { res.meta.spb = periods[0]; res.meta.baud = snapRate(Math.round(1 / (periods[0] * colTimeS)), [400e3, 800e3]); }
  else res.error = "no whole WS281x frame";
  return res;
}

// decodeDHT mirrors decode_pulsewire.go DecodeDHT.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
function decodeDHT(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("dht", S.reason);
  if (!(colTimeS > 0)) return fail("dht", "no sample time");
  const runs = levelRuns(S), n = S.n;
  const spans = [], toks = [], units = [];
  let good = false;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
  const inr = (r, lo, hi) => { const w = runUs(r, colTimeS); return !r.open && w >= lo && w <= hi; };
  for (let k = 0; k + 4 < runs.length; k++) {
    const st = runs[k];
    if (st.lvl !== 0 || st.open || runUs(st, colTimeS) < 500 || runUs(st, colTimeS) > 30000) continue;
    if (!inr(runs[k + 1], 5, 250) || !inr(runs[k + 2], 40, 120) || !inr(runs[k + 3], 40, 120)) continue;
    spans.push(pspan(st.x0, st.x1, n, "START", "sof", 0));
    spans.push(pspan(runs[k + 2].x0, runs[k + 3].x1, n, "RESP", "ack", 0));
    toks.push("START", "RESP");
    let j = k + 4;
    const vals = [], xs = [];
    while (vals.length < 5 && j < runs.length) {
      let v = 0, ok = true;
      const x0 = runs[j].x0;
      for (let b = 0; b < 8; b++) {
        if (j + 1 >= runs.length || !inr(runs[j], 25, 90) || runs[j + 1].lvl !== 1 || !inr(runs[j + 1], 10, 100)) { ok = false; break; }
        if (runUs(runs[j + 1], colTimeS) > 48) v |= 1 << (7 - b);
        j += 2;
      }
      if (!ok) break;
      vals.push(v); xs.push([x0, runs[j - 1].x1]);
    }
    vals.forEach((v, i) => {
      let txt = fmtByte(v, cfg.format), kind = "data";
      if (i === 4) {
        txt = hex2(v);
        if (((vals[0] + vals[1] + vals[2] + vals[3]) & 0xff) !== v) { txt = "!" + txt; kind = "frame-error"; }
        else good = true;
      }
      spans.push(pspan(xs[i][0], xs[i][1], n, txt, kind, v));
      toks.push(txt); units.push(v);
    });
    if (vals.length < 5) {
      const at = runs[Math.min(j, runs.length - 1)].x0;
      spans.push(pspan(at, at, n, "?", "gap", 0)); toks.push("?");
    }
    k = j - 1;
  }
  if (!spans.length) return fail("dht", "no DHT start and response");
  const res = { ok: good, proto: "dht", spans, text: toks.join(" "), bytes: units, meta: { threshold: S.threshold, baud: 10000, spb: 1e-6 / colTimeS } };
  if (!good) res.error = "no DHT reading whose checksum checks";
  return res;
}

// decodeCEC mirrors decode_pulsewire.go DecodeCEC.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
function decodeCEC(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("cec", S.reason);
  if (!(colTimeS > 0)) return fail("cec", "no sample time");
  const runs = levelRuns(S), n = S.n;
  const spans = [], toks = [], units = [];
  let good = false;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
  const ms = (r) => runUs(r, colTimeS) / 1000;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
  const bitAt = (j) => {
    if (j + 1 >= runs.length || runs[j].lvl !== 0 || runs[j].open) return null;
    const lw = ms(runs[j]);
    let v;
    if (lw >= 0.4 && lw <= 0.8) v = 1;
    else if (lw >= 1.3 && lw <= 1.7) v = 0;
    else return null;
    const per = (runs[j + 1].x1 - runs[j].x0) * colTimeS * 1000;
    if (runs[j + 1].open || per > 2.75) return { v, end: runs[j].x0 + 2.4e-3 / colTimeS };
    if (per < 2.05) return null;
    return { v, end: runs[j + 1].x1 };
  };
  for (let k = 0; k + 1 < runs.length; k++) {
    const st = runs[k];
    if (st.lvl !== 0 || st.open || ms(st) < 3.5 || ms(st) > 3.9 || runs[k + 1].open) continue;
    const per = ms(st) + ms(runs[k + 1]);
    if (per < 4.3 || per > 4.7) continue;
    spans.push(pspan(st.x0, runs[k + 1].x1, n, "START", "sof", 0));
    toks.push("START");
    let j = k + 2, dest = -1, blocks = 0, clean = true;
    for (;;) {
      const bs = [], ends = [];
      const x0 = runs[Math.min(j, runs.length - 1)].x0;
      let got = 0;
      for (; got < 10; got++) {
        const b = bitAt(j);
        if (!b) break;
        bs.push(b.v); ends.push(b.end);
        j += 2;
      }
      if (got < 10) {
        clean = false;
        spans.push(pspan(x0, x0, n, "!", "frame-error", 0)); toks.push("!");
        break;
      }
      let v = 0;
      for (let b = 0; b < 8; b++) v = (v << 1) | bs[b];
      let txt = fmtByte(v, cfg.format);
      if (blocks === 0) { dest = v & 0xf; txt = (v >> 4).toString(16).toUpperCase() + ">" + (v & 0xf).toString(16).toUpperCase(); }
      spans.push(pspan(x0, ends[7], n, txt, "data", v));
      toks.push(txt); units.push(v);
      if (bs[8] === 1) { spans.push(pspan(ends[7], ends[8], n, "EOM", "rw", 1)); toks.push("EOM"); }
      const acked = dest === 0xf ? bs[9] === 1 : bs[9] === 0;
      spans.push(pspan(ends[8], ends[9], n, acked ? "A" : "N", acked ? "ack" : "nak", bs[9]));
      toks.push(acked ? "A" : "N");
      blocks++;
      if (bs[8] === 1) break;
    }
    if (blocks > 0 && clean) good = true;
    k = j - 1;
  }
  if (!spans.length) return fail("cec", "no CEC start bit");
  const res = { ok: good, proto: "cec", spans, text: toks.join(" "), bytes: units, meta: { threshold: S.threshold, baud: 417, spb: 2.4e-3 / colTimeS } };
  if (!good) res.error = "no complete CEC message";
  return res;
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
function j1850CRC(b) {
  let c = 0xff;
  for (const v of b) {
    c ^= v & 0xff;
    for (let i = 0; i < 8; i++) c = (c & 0x80) ? ((c << 1) ^ 0x1D) & 0xff : (c << 1) & 0xff;
  }
  return c ^ 0xff;
}

// decodeJ1850 mirrors decode_pulsewire.go DecodeJ1850.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
function decodeJ1850(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("j1850", S.reason);
  if (!(colTimeS > 0)) return fail("j1850", "no sample time");
  const runs = levelRuns(S), n = S.n;
  const spans = [], toks = [], units = [];
  let good = false;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
  const emit = (vals, xs, crc) => {
    vals.forEach((v, i) => {
      let txt = fmtByte(v, cfg.format), kind = "data";
      if (crc && i === vals.length - 1) {
        txt = hex2(v);
        if (vals.length < 2 || j1850CRC(vals.slice(0, vals.length - 1)) !== v) { txt = "!" + txt; kind = "frame-error"; }
        else good = true;
      }
      spans.push(pspan(xs[i][0], xs[i][1], n, txt, kind, v));
      toks.push(txt); units.push(v);
    });
  };
  for (let k = 0; k < runs.length; k++) {
    const r = runs[k];
    const w = runUs(r, colTimeS);
    if (r.lvl !== 1 || r.open || w < 163 || w > 239) continue;
    spans.push(pspan(r.x0, r.x1, n, "SOF", "sof", 0));
    toks.push("SOF");
    let vals = [], xs = [], v = 0, nb = 0, bx0 = 0, inIFR = false, ok = true;
    let j = k + 1;
    for (; j < runs.length; j++) {
      const q = runs[j];
      const qw = runUs(q, colTimeS);
      if (q.lvl === 0 && (q.open || qw >= 163)) {
        if (nb !== 0) ok = false;
        emit(vals, xs, !inIFR);
        vals = []; xs = []; v = 0; nb = 0;
        if (q.open || qw >= 239 || inIFR || j + 1 >= runs.length) break;
        const nbw = runUs(runs[j + 1], colTimeS);
        if (runs[j + 1].lvl !== 1 || runs[j + 1].open || nbw < 34 || nbw > 163) break;
        spans.push(pspan(q.x0, q.x1, n, "EOD", "rw", 0));
        toks.push("EOD");
        inIFR = true;
        j++;
        continue;
      }
      if (q.open || qw < 34 || qw > 163) { ok = false; break; }
      const long = qw >= 96;
      const bit = (q.lvl === 1) !== long ? 1 : 0;
      if (nb === 0) bx0 = q.x0;
      v = (v << 1) | bit;
      if (++nb === 8) { vals.push(v); xs.push([bx0, q.x1]); v = 0; nb = 0; }
    }
    if (!ok) {
      if (vals.length) emit(vals, xs, false);
      const at = runs[Math.min(j, runs.length - 1)].x0;
      spans.push(pspan(at, at, n, "!", "frame-error", 0)); toks.push("!");
    }
    k = j;
  }
  if (!spans.length) return fail("j1850", "no J1850 start of frame");
  const res = { ok: good, proto: "j1850", spans, text: toks.join(" "), bytes: units, meta: { threshold: S.threshold, baud: 10400, spb: 64e-6 / colTimeS } };
  if (!good) res.error = "no J1850 frame whose CRC checks";
  return res;
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { decodeOneWire, decodeWS281x, decodeDHT, decodeCEC, decodeJ1850, levelRuns };
