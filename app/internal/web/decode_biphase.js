// ENGMODEL-OWNER-UNIT: FU-WEB-DECODE
// decode_biphase.js — DALI, USB Power Delivery (BMC) and S/PDIF, a classic
// script mirroring decode_biphase.go step for step. Uses sliceChannel, hex2,
// fail and snapRate from decode.js, pspan from decode_pulsewire.js and
// irMarks/irHalves from decode_ir.js.

// decodeDALI mirrors decode_biphase.go DecodeDALI.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
function decodeDALI(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("dali", S.reason);
  if (!(colTimeS > 0)) return fail("dali", "no sample time");
  const { marks, high } = irMarks(S, colTimeS);
  const n = S.n;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
  const us = (d) => d * colTimeS * 1e6;
  const half = 416.67;
  const hx = half / us(1);
  const spans = [], toks = [], units = [];
  let good = false;
  for (let i = 0; i < marks.length; i++) {
    if ((i > 0 && us(marks[i].x0 - marks[i - 1].x1) < 1600) || (i === 0 && us(marks[0].x0) < 900)) continue;
    const { lv, xs } = irHalves(marks, i, half, 2, us);
    if (!lv.length) continue;
    if (lv.length % 2 !== 0) { lv.push(0); xs.push(xs[xs.length - 1] + hx); }
    const bits = [];
    let ok = true;
    for (let b = 0; 2 * b + 1 < lv.length; b++) {
      if (lv[2 * b] === 1 && lv[2 * b + 1] === 0) bits.push(1);
      else if (lv[2 * b] === 0 && lv[2 * b + 1] === 1) bits.push(0);
      else ok = false;
    }
    spans.push(pspan(xs[0], xs[0] + 2 * hx, n, "START", "sof", 0));
    toks.push("START");
    const nb = bits.length - 1;
    // and quiet after it past where a next bit would move the line (half a
    // bit): a frame the record's end cuts reads as many bits as fit (an
    // Oregon burst's middle, cut at 16, read as a DALI frame)
    const cut = us(n - (xs[xs.length - 1] + hx)) < 500;
    if (!ok || cut || bits[0] !== 1 || (nb !== 8 && nb !== 16 && nb !== 24)) {
      spans.push(pspan(xs[0], xs[xs.length - 1] + hx, n, "!" + nb + "bits", "frame-error", 0));
      toks.push("!" + nb + "bits");
      continue;
    }
    for (let b = 0; b < nb / 8; b++) {
      let v = 0;
      for (let k = 0; k < 8; k++) v = (v << 1) | bits[1 + 8 * b + k];
      let txt = hex2(v);
      if (nb === 8) txt = "R" + txt;
      else if (b === 0) txt = "A" + txt;
      else if (nb === 16) txt = "D" + txt;
      const x0 = xs[2 * (1 + 8 * b)];
      spans.push(pspan(x0, x0 + 16 * hx, n, txt, "data", v));
      toks.push(txt); units.push(v);
    }
    good = true;
  }
  if (!spans.length) return fail("dali", "no DALI frame");
  const res = { ok: good, proto: "dali", spans, text: toks.join(" "), bytes: units,
    meta: { threshold: S.threshold, inverted: high, spb: 2 * hx, baud: 1200 } };
  if (!good) res.error = "no whole DALI frame";
  return res;
}

const PD_4B5B = [16, 16, 16, 16, 16, 16, 19, 20, 16, 1, 4, 5, 16, 22, 6, 7,
  16, 18, 8, 9, 2, 3, 10, 11, 17, 21, 12, 13, 14, 15, 0, 16];
const PD_ORDERED_SETS = [
  [[17, 17, 17, 18], "SOP"], [[17, 17, 19, 19], "SOP'"], [[17, 19, 17, 19], "SOP\""],
  [[17, 21, 21, 19], "SOP'D"], [[17, 21, 19, 18], "SOP\"D"],
  [[20, 17, 20, 19], "CRST"], [[20, 20, 20, 21], "HRST"],
];
const PD_CTRL_NAMES = { 1: "GoodCRC", 2: "GotoMin", 3: "Accept", 4: "Reject", 5: "Ping", 6: "PS_RDY",
  7: "GetSrcCap", 8: "GetSnkCap", 9: "DR_Swap", 10: "PR_Swap", 11: "VCONN_Swap", 12: "Wait", 13: "SoftReset" };
const PD_DATA_NAMES = { 1: "SrcCap", 2: "Request", 3: "BIST", 4: "SnkCap", 15: "VDM" };

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
function crc32IEEE(b) {
  let c = 0xFFFFFFFF;
  for (const v of b) {
    c = (c ^ (v & 0xff)) >>> 0;
    for (let i = 0; i < 8; i++) c = (c & 1) ? ((c >>> 1) ^ 0xEDB88320) >>> 0 : c >>> 1;
  }
  return (c ^ 0xFFFFFFFF) >>> 0;
}

// bmcBits mirrors decode_biphase.go bmcBits.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
function bmcBits(S, bit) {
  const out = [];
  let cur = [], half = false, hx = 0;
  const E = S.edges;
  for (let k = 1; k < E.length; k++) {
    const g = E[k].x - E[k - 1].x;
    if (g > 1.5 * bit) {
      if (cur.length) out.push(cur);
      cur = []; half = false;
    } else if (g > 0.75 * bit) {
      if (half) { out.push(cur); cur = []; half = false; continue; }
      cur.push({ v: 0, x0: E[k - 1].x });
    } else if (half) {
      cur.push({ v: 1, x0: hx }); half = false;
    } else {
      half = true; hx = E[k - 1].x;
    }
  }
  if (cur.length) out.push(cur);
  return out;
}

// decodeUSBPD mirrors decode_biphase.go DecodeUSBPD.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
function decodeUSBPD(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("usbpd", S.reason);
  const baud = cfg.baud > 0 ? cfg.baud : 300000;
  if (!(colTimeS > 0)) return fail("usbpd", "no sample time");
  const bit = 1 / (baud * colTimeS);
  if (!(bit >= 4)) return fail("usbpd", bit.toFixed(1) + " samples/bit; need >= 4");
  const n = S.n, spans = [], toks = [], units = [];
  let good = false;
  for (const burst of bmcBits(S, bit)) {
    if (burst.length < 60) continue;
    // TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
    const sym = (i) => {
      let v = 0;
      for (let k = 0; k < 5; k++) v |= burst[i + k].v << k;
      return PD_4B5B[v];
    };
    // The symbol at every offset, decoded once: the ordered-set search reads
    // each four times over for every set.
    const syms = new Array(Math.max(0, burst.length - 4));
    for (let i = 0; i < syms.length; i++) syms[i] = sym(i);
    let at = -1, name = "";
    for (let i = 0; i + 20 <= burst.length && at < 0; i++) {
      for (const [k, nm] of PD_ORDERED_SETS) {
        let hits = 0;
        for (let q = 0; q < 4; q++) if (syms[i + 5 * q] === k[q]) hits++;
        if (hits >= 3) { at = i; name = nm; break; }
      }
    }
    if (at < 0) continue;
    // TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
    const end = (i) => i < burst.length ? burst[i].x0 : burst[burst.length - 1].x0 + bit;
    spans.push(pspan(burst[at].x0, end(at + 20), n, name, "sof", 0));
    toks.push(name);
    if (name === "HRST" || name === "CRST") continue;
    let idx = at + 20, bad = false;
    const vals = [], xs = [];
    // TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
    const readByte = () => {
      if (idx + 10 > burst.length) return false;
      const lo = sym(idx), hi = sym(idx + 5);
      if (lo > 15 || hi > 15) { bad = true; return false; }
      vals.push(lo | (hi << 4)); xs.push(burst[idx].x0);
      idx += 10;
      return true;
    };
    if (!readByte() || !readByte()) {
      spans.push(pspan(end(idx), end(idx), n, "!", "frame-error", 0)); toks.push("!");
      continue;
    }
    const head = vals[0] | (vals[1] << 8);
    const count = (head >> 12) & 7;
    let ok = true;
    for (let k = 0; k < 4 * count + 4 && ok; k++) ok = readByte();
    let crcOK = ok && !bad;
    if (crcOK) {
      const L = vals.length;
      const got = (vals[L - 4] | (vals[L - 3] << 8) | (vals[L - 2] << 16) | (vals[L - 1] << 24)) >>> 0;
      crcOK = crc32IEEE(vals.slice(0, L - 4)) === got;
    }
    vals.forEach((v, k) => {
      let txt = hex2(v), kind = "data";
      if (k === 0) {
        const names = count > 0 ? PD_DATA_NAMES : PD_CTRL_NAMES;
        if (names[head & 0xf] !== undefined) txt = names[head & 0xf];
      }
      if (ok && !bad && k >= vals.length - 4 && !crcOK) { txt = "!" + txt; kind = "frame-error"; }
      spans.push(pspan(xs[k], end(at + 20 + 10 * (k + 1)), n, txt, kind, v));
      toks.push(txt); units.push(v);
    });
    if (!ok || bad) { spans.push(pspan(end(idx), end(idx), n, "!", "frame-error", 0)); toks.push("!"); continue; }
    if (idx + 5 <= burst.length && sym(idx) === 22) { spans.push(pspan(burst[idx].x0, end(idx + 5), n, "EOP", "rw", 0)); toks.push("EOP"); }
    good = good || crcOK;
  }
  if (!spans.length) return fail("usbpd", "no USB PD start of packet");
  const res = { ok: good, proto: "usbpd", spans, text: toks.join(" "), bytes: units, meta: { threshold: S.threshold, spb: bit, baud } };
  if (!good) res.error = "no USB PD message whose CRC checks";
  return res;
}

// spdifHalf mirrors decode_biphase.go spdifHalf.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
function spdifHalf(S) {
  const gaps = [];
  for (let k = 1; k < S.edges.length; k++) gaps.push(S.edges[k].x - S.edges[k - 1].x);
  if (gaps.length < 64) return 0;
  gaps.sort((a, b) => a - b);
  const g0 = gaps[Math.floor(gaps.length / 20)];
  let sum = 0, cnt = 0;
  for (const g of gaps) if (g >= 0.7 * g0 && g <= 1.35 * g0) { sum += g; cnt++; }
  return cnt ? sum / cnt : 0;
}

// decodeSPDIF mirrors decode_biphase.go DecodeSPDIF.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
function decodeSPDIF(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("spdif", S.reason);
  const h = spdifHalf(S);
  if (!(h >= 2)) return fail("spdif", "no biphase-mark cells (half cell under 2 samples)");
  const E = S.edges, n = S.n;
  const k = new Array(E.length).fill(0);
  for (let i = 1; i < E.length; i++) {
    const q = Math.round((E[i].x - E[i - 1].x) / h);
    if (q >= 1 && q <= 3) k[i] = q;
  }
  const spans = [], toks = [], units = [];
  let goodN = 0;
  for (let i = 1; i + 3 < E.length; i++) {
    if (k[i] !== 3) continue;
    let name;
    if (k[i + 1] === 1 && k[i + 2] === 1 && k[i + 3] === 3) name = "B";
    else if (k[i + 1] === 3 && k[i + 2] === 1 && k[i + 3] === 1) name = "M";
    else if (k[i + 1] === 2 && k[i + 2] === 1 && k[i + 3] === 2) name = "W";
    else continue;
    let j = i + 4;
    const bits = [];
    while (bits.length < 28 && j < E.length) {
      if (k[j] === 2) { bits.push(0); j++; }
      else if (k[j] === 1 && j + 1 < E.length && k[j + 1] === 1) { bits.push(1); j += 2; }
      else break;
    }
    const x0 = E[i - 1].x;
    spans.push(pspan(x0, E[i + 3].x, n, name, name === "B" ? "sof" : "rw", 0));
    toks.push(name);
    if (bits.length < 28) {
      if (j < E.length) { spans.push(pspan(E[i + 3].x, E[i + 3].x, n, "!", "frame-error", 0)); toks.push("!"); }
      i = j - 1;
      continue;
    }
    let v = 0, ones = 0;
    for (let b = 0; b < 28; b++) { if (b < 24) v |= bits[b] << b; ones += bits[b]; }
    let txt = v.toString(16).toUpperCase().padStart(6, "0"), kd = "data";
    if (ones % 2 !== 0) { txt = "!" + txt; kd = "parity-error"; } else goodN++;
    spans.push(pspan(E[i + 3].x, E[j - 1].x + 2 * h, n, txt, kd, v));
    toks.push(txt); units.push(v);
    i = j - 1;
  }
  if (!spans.length) return fail("spdif", "no S/PDIF preamble");
  const res = { ok: goodN >= 2, proto: "spdif", spans, text: toks.join(" "), bytes: units, meta: { threshold: S.threshold, spb: 2 * h } };
  if (colTimeS > 0) res.meta.baud = snapRate(Math.round(1 / (2 * h * colTimeS)), [64 * 32e3, 64 * 44.1e3, 64 * 48e3, 64 * 88.2e3, 64 * 96e3, 64 * 176.4e3, 64 * 192e3]);
  if (goodN < 2) res.error = "no S/PDIF subframes whose parity checks";
  return res;
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { decodeDALI, decodeUSBPD, decodeSPDIF };
