// Serial-protocol decoders (UART / I2C / SPI), pure JS mirroring peaks.js:
// no DOM, no globals. Served at /decode.js, loaded by ui.html via <script src>,
// and require()d by decode.test.cjs under node. All column indices (i0/i1) are
// indices into the frame's sample arrays (codes 0..255; a value < 0 marks a
// gap). Per-column time is frame.col_span_s / n. Every decoder returns the SAME
// Result shape and NEVER emits garbage: when there are too few samples per bit
// it returns { ok:false, error } so the UI can say "raise time/div" instead.

// Frozen annotation-kind vocabulary — the UI colours spans by exactly these.
const KINDS = ["start", "stop", "addr", "rw", "ack", "nak", "data", "frame-error", "parity-error", "gap", "idle"];

function hex2(b) { return (b & 0xff).toString(16).toUpperCase().padStart(2, "0"); }
function popcount(v) { let c = 0; while (v) { c += v & 1; v >>= 1; } return c; }
function fail(proto, error, meta) {
  return { ok: false, error, proto, spans: [], text: "", bytes: [], meta: meta || {} };
}

// sliceChannel: analog code stream -> digital. Finds the two rails as
// count-weighted centroids (an idle line sits on one rail most of the time, so
// percentiles would bias the threshold), sets a mid threshold with a hysteresis
// band for edge/period detection, and records threshold crossings. The bit
// VALUE is never read from the hysteretic level[] (which lags); callers use
// logicAt(), which thresholds the raw code at the sample instant.
function sliceChannel(codes, opts) {
  opts = opts || {};
  const hystFrac = opts.hystFrac != null ? opts.hystFrac : 0.20;
  const minAmp = opts.minAmp != null ? opts.minAmp : 20;
  const n = codes.length;
  let valid = 0;
  const h = new Float64Array(256);
  for (let i = 0; i < n; i++) { const v = codes[i]; if (v >= 0 && v <= 255) { h[v | 0]++; valid++; } }
  if (valid < 8) return { ok: false, reason: "no/too-few valid samples" };
  const noiseFloor = Math.max(1, 0.001 * valid);
  let gmin = 0; while (gmin < 255 && h[gmin] < noiseFloor) gmin++;
  let gmax = 255; while (gmax > 0 && h[gmax] < noiseFloor) gmax--;
  if (gmax <= gmin) return { ok: false, reason: "flat/no transitions" };
  const mid0 = (gmin + gmax) / 2;
  let lw = 0, ls = 0, hw = 0, hs = 0;
  for (let c = gmin; c <= gmax; c++) {
    if (c <= mid0) { lw += h[c] * c; ls += h[c]; }
    else { hw += h[c] * c; hs += h[c]; }
  }
  const lowRail = ls ? lw / ls : gmin, highRail = hs ? hw / hs : gmax;
  const amp = highRail - lowRail;
  if (amp < minAmp) return { ok: false, reason: "amplitude " + amp.toFixed(0) + " < " + minAmp };
  const threshold = opts.threshold != null ? opts.threshold : (lowRail + highRail) / 2;
  const band = hystFrac * amp / 2, thHi = threshold + band, thLo = threshold - band;

  const level = new Int8Array(n);
  const edges = [];
  let cur = -1;
  for (let i = 0; i < n; i++) {
    const v = codes[i];
    if (v < 0) { level[i] = -1; continue; }
    let nl = cur;
    if (cur < 0) nl = v >= threshold ? 1 : 0;   // seed from the first valid sample
    else if (cur === 0 && v >= thHi) nl = 1;
    else if (cur === 1 && v <= thLo) nl = 0;
    if (cur >= 0 && nl !== cur) {
      // interpolate the threshold crossing between i-1 and i
      let p = i - 1; while (p >= 0 && codes[p] < 0) p--;
      let frac = i;
      if (p >= 0 && codes[i] !== codes[p]) frac = p + (threshold - codes[p]) / (codes[i] - codes[p]);
      edges.push({ i, dir: nl > cur ? 1 : -1, x: frac });
    }
    cur = nl; level[i] = nl;
  }
  return { ok: true, reason: null, n, codes, lowRail, highRail, amp, threshold, thHi, thLo, level, edges };
}

// logicAt: the bit-decision primitive — raw code vs threshold at column x
// (rounded). Returns 0/1, or -1 for a gap / out of range.
function logicAt(s, x) {
  const i = Math.round(x);
  if (i < 0 || i >= s.n) return -1;
  const v = s.codes[i];
  if (v < 0) return -1;
  return v >= s.threshold ? 1 : 0;
}

// minConsecutive returns the smallest column gap between successive edges whose
// direction passes `dirOk` (used for the too-fast resolution guard).
function minEdgeGap(edges, dirOk) {
  let prev = -1, min = Infinity;
  for (const e of edges) {
    if (!dirOk(e.dir)) continue;
    if (prev >= 0) min = Math.min(min, e.i - prev);
    prev = e.i;
  }
  return min;
}

function decodeUART(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const bits = cfg.bits || 8, parity = cfg.parity || "none", idle = cfg.idle != null ? cfg.idle : 1;
  const lsb = (cfg.bitOrder || "lsb") === "lsb", minSPB = cfg.minSPB || 3, guard = cfg.guard != null ? cfg.guard : 4;
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("uart", S.reason);
  const n = S.n;

  let SPB, baud;
  if (cfg.baud) { SPB = (1 / cfg.baud) / colTimeS; baud = cfg.baud; }
  else {
    const gaps = [];
    for (let k = 1; k < S.edges.length; k++) { const g = S.edges[k].i - S.edges[k - 1].i; if (g >= 2) gaps.push(g); }
    if (!gaps.length) return fail("uart", "no edges / cannot infer baud");
    SPB = Math.min.apply(null, gaps);
    for (const g of gaps) { const m = Math.round(g / SPB); if (m < 1 || Math.abs(g - m * SPB) > 0.25 * SPB) return fail("uart", "baud ambiguous — set it explicitly"); }
    baud = 1 / (SPB * colTimeS);
  }
  if (!(SPB >= minSPB)) return fail("uart", SPB.toFixed(1) + " samples/bit; need >= " + minSPB, { samplesPerBit: SPB, baud });

  const parityOf = v => { const p = popcount(v & ((1 << bits) - 1)) & 1; return parity === "even" ? p : (1 - p); };
  const spans = [], bytes = [], toks = [];
  const need = Math.ceil((bits + 2) * SPB);
  let i = guard;
  while (i < n - need - guard) {
    if (logicAt(S, i - 1) === idle && logicAt(S, i) === (1 - idle)) { // start edge
      const start = i;
      if (logicAt(S, Math.round(start + 0.5 * SPB)) === (1 - idle)) { // confirm start bit
        let val = 0, gap = false;
        for (let c = 0; c < bits; c++) {
          const b = logicAt(S, Math.round(start + (1.5 + c) * SPB));
          if (b < 0) { gap = true; break; }
          if (lsb) val |= (b << c); else val = (val << 1) | b;
        }
        const i1 = Math.min(n - 1, Math.round(start + (bits + 1) * SPB));
        if (gap) { spans.push({ i0: start, i1, text: "gap", kind: "gap" }); i = start + 1; continue; }
        let kind = "data", pfx = "", pc = 0;
        if (parity !== "none") {
          pc = 1;
          const pb = logicAt(S, Math.round(start + (1.5 + bits) * SPB));
          if (pb >= 0 && pb !== parityOf(val)) { kind = "parity-error"; pfx = "!"; }
        }
        const sb = logicAt(S, Math.round(start + (1.5 + bits + pc) * SPB));
        if (sb >= 0 && sb !== idle) { if (kind === "data") kind = "frame-error"; pfx = "!"; }
        spans.push({ i0: start, i1, text: pfx + hex2(val), kind });
        toks.push(pfx + hex2(val)); bytes.push(val);
        i = Math.round(start + (bits + 1 + pc) * SPB) + 1;
        continue;
      }
    }
    i++;
  }
  return { ok: true, error: null, proto: "uart", spans, text: toks.join(" "), bytes,
    meta: { baud: Math.round(baud), samplesPerBit: SPB, threshold: S.threshold, lowRail: S.lowRail, highRail: S.highRail } };
}

function decodeI2C(scl, sda, colTimeS, cfg) {
  cfg = cfg || {};
  const CL = sliceChannel(scl, cfg), DA = sliceChannel(sda, cfg);
  if (!CL.ok) return fail("i2c", "SCL " + CL.reason);
  if (!DA.ok) return fail("i2c", "SDA " + DA.reason);
  const n = Math.min(CL.n, DA.n);
  const risings = CL.edges.filter(e => e.dir > 0 && e.i < n);
  if (risings.length < 2) return fail("i2c", "no SCL clock edges");
  const colsPerClock = minEdgeGap(CL.edges.filter(e => e.i < n), d => d > 0);
  if (colsPerClock < 3) return fail("i2c", colsPerClock.toFixed(1) + " cols/clock; too few samples/bit", { colsPerClock });

  const spans = [], bytes = [], toks = [];
  let cl = -1, da = -1, inTxn = false, expectAddr = false, bitCount = 0, val = 0, bitStart = 0;
  for (let i = 0; i < n; i++) {
    const l = CL.level[i], d = DA.level[i];
    if (l < 0 || d < 0) { cl = -1; da = -1; continue; }
    const pcl = cl, pda = da; cl = l; da = d;
    if (pcl < 0 || pda < 0) continue;
    if (cl === 1 && pda === 1 && da === 0) { // START (SDA falls while SCL high)
      spans.push({ i0: i, i1: i, text: "S", kind: "start" }); toks.push("START");
      inTxn = true; expectAddr = true; bitCount = 0; val = 0; continue;
    }
    if (cl === 1 && pda === 0 && da === 1) { // STOP (SDA rises while SCL high)
      spans.push({ i0: i, i1: i, text: "P", kind: "stop" }); toks.push("STOP");
      inTxn = false; bitCount = 0; continue;
    }
    if (pcl === 0 && cl === 1 && inTxn) { // sample on SCL rising
      const bit = logicAt(DA, i);
      if (bit < 0) continue;
      if (bitCount < 8) {
        if (bitCount === 0) { bitStart = i; val = 0; }
        val = (val << 1) | bit; bitCount++;
        if (bitCount === 8) {
          if (expectAddr) {
            const addr = val >> 1;
            spans.push({ i0: bitStart, i1: i, text: hex2(addr), kind: "addr" });
            spans.push({ i0: i, i1: i, text: (val & 1) ? "R" : "W", kind: "rw" });
            toks.push(hex2(addr), (val & 1) ? "R" : "W");
            expectAddr = false;
          } else {
            spans.push({ i0: bitStart, i1: i, text: hex2(val), kind: "data" });
            toks.push(hex2(val)); bytes.push(val);
          }
        }
      } else { // 9th clock = ACK/NAK
        spans.push({ i0: i, i1: i, text: bit === 0 ? "A" : "N", kind: bit === 0 ? "ack" : "nak" });
        toks.push(bit === 0 ? "ACK" : "NAK");
        bitCount = 0; val = 0;
      }
    }
  }
  if (inTxn) spans.push({ i0: n - 1, i1: n - 1, text: "(no STOP)", kind: "frame-error" });
  return { ok: true, error: null, proto: "i2c", spans, text: toks.join(" "), bytes,
    meta: { threshold: CL.threshold, lowRail: CL.lowRail, highRail: CL.highRail, colsPerClock } };
}

function decodeSPI(clk, data, colTimeS, cfg) {
  cfg = cfg || {};
  const cpol = cfg.cpol ? 1 : 0, cpha = cfg.cpha ? 1 : 0, msb = (cfg.bitOrder || "msb") === "msb";
  const CK = sliceChannel(clk, cfg), DA = sliceChannel(data, cfg);
  if (!CK.ok) return fail("spi", "CLK " + CK.reason);
  if (!DA.ok) return fail("spi", "DATA " + DA.reason);
  const n = Math.min(CK.n, DA.n);
  const eIn = CK.edges.filter(e => e.i < n);
  if (eIn.length < 2) return fail("spi", "no CLK edges");
  const halfGap = minEdgeGap(eIn, () => true);
  if (halfGap < 3) return fail("spi", halfGap.toFixed(1) + " cols/edge; too few samples/bit", { colsPerClock: halfGap * 2 });

  const sampleRising = cpol === cpha; // modes 0 & 3 sample on rising, 1 & 2 on falling
  const spans = [], bytes = [], toks = [];
  let ck = -1, bitCount = 0, val = 0, bitStart = 0;
  for (let i = 0; i < n; i++) {
    const l = CK.level[i];
    if (l < 0) { ck = -1; continue; }
    const pck = ck; ck = l;
    if (pck < 0) continue;
    const rising = pck === 0 && ck === 1, falling = pck === 1 && ck === 0;
    if ((sampleRising && rising) || (!sampleRising && falling)) {
      const bit = logicAt(DA, i);
      if (bit < 0) continue;
      if (bitCount === 0) { bitStart = i; val = 0; }
      if (msb) val = (val << 1) | bit; else val |= (bit << bitCount);
      bitCount++;
      if (bitCount === 8) {
        spans.push({ i0: bitStart, i1: i, text: hex2(val), kind: "data" });
        toks.push(hex2(val)); bytes.push(val & 0xff);
        bitCount = 0; val = 0;
      }
    }
  }
  return { ok: true, error: null, proto: "spi", spans, text: toks.join(" "), bytes,
    meta: { cpol, cpha, sampleOnRising: sampleRising, bitOrder: msb ? "msb" : "lsb", noCS: true, colsPerClock: halfGap * 2, threshold: CK.threshold } };
}

// decode: dispatcher used by node tests. frame = { c1, c2, col_span_s }.
// cfg.protocol in {uart,i2c,spi}; role fields are 1|2 (=> c1|c2).
function decode(frame, cfg) {
  cfg = cfg || {};
  const n = frame.c1.length, colTimeS = (frame.col_span_s || 0) / n;
  const pick = r => (r === 2 || r === "c2") ? frame.c2 : frame.c1;
  const p = cfg.protocol || cfg.proto;
  if (p === "uart") return decodeUART(pick(cfg.line || 1), colTimeS, cfg);
  if (p === "i2c") return decodeI2C(pick(cfg.scl || 1), pick(cfg.sda || 2), colTimeS, cfg);
  if (p === "spi") return decodeSPI(pick(cfg.clk || 1), pick(cfg.data || 2), colTimeS, cfg);
  return fail(p || "off", "unknown protocol");
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { KINDS, sliceChannel, logicAt, decodeUART, decodeI2C, decodeSPI, decode };
