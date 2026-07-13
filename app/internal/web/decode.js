// Serial-protocol decoders (UART / I2C / SPI here; the other seven protocols
// live in the sibling decode_*.js files), pure JS mirroring peaks.js:
// no DOM, no globals. Served at /decode.js, loaded by ui.html via <script src>,
// and require()d by decode.test.cjs under node. All column indices (i0/i1) are
// indices into the frame's sample arrays (codes 0..255; a value < 0 marks a
// gap). Per-column time is frameDtS(frame, n) — the server's dt_s when
// present (col_span_s is a display nominal on the 1-200 ns/div bands, where a
// baud derived from it reads 2x high), else col_span_s / n. Every decoder returns the SAME
// Result shape and NEVER emits garbage: when there are too few samples per bit
// it returns { ok:false, error } so the UI can say "raise time/div" instead.

// Frozen annotation-kind vocabulary — the UI colours spans by exactly these.
const KINDS = ["start", "stop", "addr", "rw", "ack", "nak", "data", "frame-error", "parity-error", "gap", "idle"];

// frameDtS / frameSpanS — the TRUE per-point time / record span of a frame
// object. The server's dt_s is authoritative: on the 1-200 ns/div bands
// col_span_s is a display nominal (2 ns capture shown at 1 ns, spec 04 §6),
// so any measurement derived from it — decode baud, FFT frequency axes —
// reads 2x off. Fall back to col_span_s for client-synthesized frames
// (superres/ETS view, mask replay — their spans are already true) and
// captures saved before dt_s existed. The on-screen time GRID deliberately
// keeps the nominal (it must match the labelled tdiv); these helpers are for
// measurements.
function frameDtS(frame, n) {
  if (frame.dt_s > 0) return frame.dt_s;
  return (frame.col_span_s || 0) / (n || 1);
}
function frameSpanS(frame, n) {
  if (frame.dt_s > 0) return frame.dt_s * n;
  return frame.col_span_s || 0;
}

function hex2(b) { return (b & 0xff).toString(16).toUpperCase().padStart(2, "0"); }
// UNSIGNED shift (>>>): a signed >> on a value with the sign bit set shifts
// in 1s and never reaches 0 — an infinite loop, the same DoS the Go popcount
// had. cfg.bits is bounded below so the assembled word stays non-negative.
function popcount(v) { let c = 0; v = v >>> 0; while (v) { c += v & 1; v >>>= 1; } return c; }
// fmtByte renders a payload byte per the display format: "hex" -> 48, "ascii" ->
// the printable char (else .), "both" -> 48·H. Applied to data bytes only.
function fmtByte(v, fmt) {
  v &= 0xff;
  const h = hex2(v), printable = v >= 0x20 && v < 0x7f, ch = printable ? String.fromCharCode(v) : ".";
  if (fmt === "ascii") return ch;
  if (fmt === "both") return printable ? h + "·" + ch : h;
  return h;
}
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
  // A frame may carry NO per-sample array for a channel (channel toggled off,
  // or an envelope/roll band ships env min/max instead) — decCodes()/autodetect
  // then hand us undefined. Report it like any other unusable input; every
  // caller already checks S.ok.
  if (!codes) return { ok: false, reason: "no channel data (channel off / envelope frame)" };
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

// inferUARTspb: samples-per-bit from edge-gap statistics, robust to RINGY edges
// (1-3-sample spurious toggles around real transitions). Manchester-inference
// rigor: sub-sample crossings (edge.x), then a deterministic ascending CLUSTER
// WALK over the sorted gaps instead of a blind low percentile — ring-spur gaps
// form their own tiny cluster that a percentile would mistake for the bit
// width. Each cluster is tried as the 1-bit hypothesis: edges within the ring
// scale of the last kept edge are collapsed (a ring bounce is part of the SAME
// transition), the candidate is refined on the de-glitched 1-bit cluster, and it must
// explain >=70% of the de-glitched gaps as ~integer bit multiples. Among
// validating candidates the BEST-fitting one wins, ties to the larger period
// (ring debris at an exact sub-multiple of the true bit validates perfectly —
// but so does the true bit, and the true bit is the wide one); none =>
// genuinely ambiguous, be honest. Mirrors decode_uart.go inferUARTspb step for
// step. Returns { spb, reason }.
function inferUARTspb(S) {
  const gaps = [];
  for (let k = 1; k < S.edges.length; k++) { const g = S.edges[k].x - S.edges[k - 1].x; if (g >= 1) gaps.push(g); }
  if (gaps.length < 3) return { spb: 0, reason: "too few edges / cannot infer baud" };
  gaps.sort((a, b) => a - b);
  // Deterministic greedy clusters: each starts at the smallest unassigned gap
  // and absorbs everything within 1.5x its seed (bit multiples sit a full
  // factor of 2 up, so clusters never bleed into the next multiple).
  const cands = [];
  for (let i = 0; i < gaps.length;) {
    const seed = gaps[i];
    let sum = 0, j = i;
    for (; j < gaps.length && gaps[j] <= 1.5 * seed; j++) sum += gaps[j];
    cands.push(sum / (j - i));
    i = j;
  }
  let best = 0, bestFrac = -1;
  for (const cand of cands) {
    if (cand < 2.5) continue; // below the 3-samples/bit floor: a ring-spur cluster
    // De-glitch: collapse edges within the RING SCALE of the last KEPT edge —
    // ring bounces cluster tightly (a few samples) around the true transition
    // instant. The window is capped at 5 samples, NOT half the candidate:
    // ringing is a fixed-time artifact, so a window that scaled with the
    // candidate would let a 2x-bit hypothesis swallow real 1-bit edges and
    // then validate on the merged gaps it manufactured itself.
    const win = Math.min(0.5 * cand, 5);
    const kg = [];
    let prevX = S.edges[0].x;
    for (let k = 1; k < S.edges.length; k++) {
      const g = S.edges[k].x - prevX;
      if (g >= win) { kg.push(g); prevX = S.edges[k].x; }
    }
    if (kg.length < 3) continue;
    // Refine on the de-glitched 1-bit cluster by MEAN-SHIFT (two passes,
    // re-centering the ±0.35 window on the running estimate): the raw cluster
    // mean is ring-shortened, and a single window around it clips the upper
    // quantization branch of the true bit — biasing spb low enough to
    // mis-sample late bits. A WIDER window instead would merge genuinely
    // incommensurate pulse widths and destroy the ambiguity honesty;
    // re-centering does not.
    let ref = cand;
    for (let pass = 0; pass < 2; pass++) {
      let sum = 0, cnt = 0;
      for (const g of kg) if (Math.abs(g - ref) <= 0.35 * ref) { sum += g; cnt++; }
      if (cnt) ref = sum / cnt;
    }
    let good = 0;
    for (const g of kg) { const m = Math.round(g / ref); if (m >= 1 && Math.abs(g - m * ref) <= 0.35 * ref) good++; }
    // >= keeps the LARGER candidate on an exact tie (candidates ascend).
    const frac = good / kg.length;
    if (frac >= 0.7 && frac >= bestFrac) { best = ref; bestFrac = frac; }
  }
  if (best > 0) return { spb: best, reason: null };
  return { spb: 0, reason: "baud ambiguous — set it explicitly" };
}

function decodeUART(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const bits = cfg.bits || 8, parity = cfg.parity || "none", idle = cfg.idle != null ? cfg.idle : 1;
  if (bits < 1 || bits > 16) return fail("uart", "data bits out of range (1..16)"); // guard shift/mask math
  const lsb = (cfg.bitOrder || "lsb") === "lsb", minSPB = cfg.minSPB || 3, guard = cfg.guard != null ? cfg.guard : 4;
  const fmt = cfg.fmt || "hex";
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("uart", S.reason);
  const n = S.n;

  let SPB, baud;
  if (cfg.baud) { SPB = (1 / cfg.baud) / colTimeS; baud = cfg.baud; }
  else {
    const inf = inferUARTspb(S);
    if (inf.reason) return fail("uart", inf.reason);
    SPB = inf.spb;
    baud = 1 / (SPB * colTimeS);
  }
  if (!(SPB >= minSPB)) return fail("uart", SPB.toFixed(1) + " samples/bit; need >= " + minSPB, { samplesPerBit: SPB, baud });

  const parityOf = v => { const p = popcount(v & ((1 << bits) - 1)) & 1; return parity === "even" ? p : (1 - p); };
  const spans = [], bytes = [], toks = [];
  // The parity bit lengthens the frame — count it so a frame near the record end
  // isn't started with its stop bit off the captured range.
  const pcNeed = parity !== "none" ? 1 : 0;
  const need = Math.ceil((bits + 2 + pcNeed) * SPB);
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
        if (sb < 0) { spans.push({ i0: start, i1, text: "gap", kind: "gap" }); i = start + 1; continue; } // stop bit off the record: incomplete frame
        if (sb !== idle) { if (kind === "data") kind = "frame-error"; pfx = "!"; } // wrong stop level = framing error
        spans.push({ i0: start, i1, text: pfx + fmtByte(val, fmt), kind, val });
        toks.push(pfx + fmtByte(val, fmt)); bytes.push(val);
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
  const fmt = cfg.fmt || "hex";
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
            spans.push({ i0: bitStart, i1: i, text: fmtByte(val, fmt), kind: "data", val });
            toks.push(fmtByte(val, fmt)); bytes.push(val);
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
  const fmt = cfg.fmt || "hex";
  const CK = sliceChannel(clk, cfg), DA = sliceChannel(data, cfg);
  if (!CK.ok) return fail("spi", "CLK " + CK.reason);
  if (!DA.ok) return fail("spi", "DATA " + DA.reason);
  const n = Math.min(CK.n, DA.n);
  const eIn = CK.edges.filter(e => e.i < n);
  if (eIn.length < 2) return fail("spi", "no CLK edges");

  const sampleRising = cpol === cpha; // modes 0 & 3 sample on rising, 1 & 2 on falling
  // Frame-split threshold from the SAMPLING-edge cadence — the edges actually
  // used for bits — never from the minimum gap over ALL edges. On a real
  // (rebuilt) clock a single narrow half-cycle (partial first cycle, duty skew)
  // makes min-gap*3 land BELOW one true period (HW: sampling gaps 374-376 cols
  // vs 3*124=372), so every inter-bit gap "exceeded" the reset and the byte
  // assembly restarted on each bit — 0 bytes from a clean signal. Cluster the
  // sampling-edge gaps like the UART/Manchester inference: seed on a low
  // percentile, average the cluster (±0.5 window absorbs quantization/jitter),
  // and reset at 1.5x the TYPICAL clock period. Mirrors decode_i2c_spi.go.
  const dirWant = sampleRising ? 1 : -1;
  const sgaps = [];
  let prevI = -1;
  for (const e of eIn) {
    if (e.dir !== dirWant) continue;
    if (prevI >= 0 && e.i > prevI) sgaps.push(e.i - prevI);
    prevI = e.i;
  }
  if (!sgaps.length) return fail("spi", "no CLK sampling edges");
  sgaps.sort((a, b) => a - b);
  let period = sgaps[Math.floor(sgaps.length * 0.1)];
  let pSum = 0, pCnt = 0;
  for (const g of sgaps) if (Math.abs(g - period) <= 0.5 * period) { pSum += g; pCnt++; }
  if (pCnt) period = pSum / pCnt;
  if (period < 6) return fail("spi", period.toFixed(1) + " cols/clock; too few samples/bit", { colsPerClock: period });
  // With no CS to frame bytes, re-align on a clock-idle gap: consecutive
  // sampling edges are one clock period apart within a burst; a gap longer than
  // ~1.5 typical periods means a new transaction, so restart the byte.
  const gapReset = 1.5 * period;
  const spans = [], bytes = [], toks = [];
  let ck = -1, bitCount = 0, val = 0, bitStart = 0, lastSample = -1;
  // sampleMargin = how far the sampled data sits from the threshold, averaged over
  // all bits (1 = dead on a rail, 0 = on the threshold). The CORRECT clock phase
  // samples mid-bit where data is stable (high margin); the wrong phase samples on
  // the data transition (low margin). autodetect uses this to pick CPHA.
  const halfAmp = DA.amp / 2 || 1;
  let mSum = 0, mN = 0;
  for (let i = 0; i < n; i++) {
    const l = CK.level[i];
    if (l < 0) { ck = -1; continue; }
    const pck = ck; ck = l;
    if (pck < 0) continue;
    const rising = pck === 0 && ck === 1, falling = pck === 1 && ck === 0;
    if ((sampleRising && rising) || (!sampleRising && falling)) {
      const bit = logicAt(DA, i);
      if (bit < 0) continue;
      const cd = DA.codes[i];
      if (cd >= 0) { mSum += Math.min(1, Math.abs(cd - DA.threshold) / halfAmp); mN++; }
      if (lastSample >= 0 && i - lastSample > gapReset && bitCount > 0) { bitCount = 0; val = 0; } // idle gap → new frame
      lastSample = i;
      if (bitCount === 0) { bitStart = i; val = 0; }
      if (msb) val = (val << 1) | bit; else val |= (bit << bitCount);
      bitCount++;
      if (bitCount === 8) {
        spans.push({ i0: bitStart, i1: i, text: fmtByte(val, fmt), kind: "data", val: val & 0xff });
        toks.push(fmtByte(val, fmt)); bytes.push(val & 0xff);
        bitCount = 0; val = 0;
      }
    }
  }
  return { ok: true, error: null, proto: "spi", spans, text: toks.join(" "), bytes,
    meta: { cpol, cpha, sampleOnRising: sampleRising, bitOrder: msb ? "msb" : "lsb", noCS: true, colsPerClock: period, threshold: CK.threshold, sampleMargin: mN ? mSum / mN : 0 } };
}

// ---- autodetect -------------------------------------------------------------
// Score a decode Result so competing protocol/role hypotheses can be ranked.
// The discriminators are STRUCTURAL, layered by how hard each protocol's own
// validity signals are to forge: CRC-validated protocols (CAN CRC-15, FlexRay
// header CRC-11, SENT CRC-4) outscore parity/structure-validated ones
// (MIL-1553B word parity, ARINC 429 word parity, USB PID complement, I2C
// START/addr/ACK framing), which outscore the purely heuristic ones (UART stop
// bits, Manchester mid-cell coding, and SPI — no framing at all, the
// fallback). A bad hypothesis racks up frame-errors and scores negative, so
// the genuine match wins. Mirrors decode_autodetect.go scoreResult exactly.
function scoreResult(r) {
  if (!r || !r.ok) return -1e9;
  const spans = r.spans || [], bytes = r.bytes ? r.bytes.length : 0;
  const kind = k => spans.filter(s => s.kind === k).length;
  if (r.proto === "i2c") {
    // Score on real CONTENT, not bare framing: a channel-swapped mis-read floods
    // "START STOP START STOP" (0 addresses, 0 data), which must NOT beat the true
    // ordering. A real transaction addresses a device and clocks bytes+ACKs.
    const addrs = kind("addr"), acks = kind("ack") + kind("nak"), datas = kind("data"), starts = kind("start");
    if (addrs === 0) return -1e9;                  // no addressed device -> not real I2C
    return addrs * 100 + acks * 30 + datas * 20 + starts * 5;
  }
  if (r.proto === "uart") {
    if (bytes === 0) return -1e9;
    return bytes * 10 - kind("frame-error") * 35 - kind("parity-error") * 18 + 15;
  }
  if (r.proto === "spi") {
    if (bytes === 0) return -1e9;
    const margin = (r.meta && r.meta.sampleMargin) || 0;
    // MSB vs LSB is signal-ambiguous (same bytes, bit-reversed) — prefer the order
    // that yields readable ASCII (real text almost always means that order is
    // right); binary data ties and falls back to MSB (SPI convention, tried first).
    const printable = r.bytes.filter(b => b >= 0x20 && b < 0x7f).length / bytes;
    return bytes * 2 + margin * 10 + printable * 6; // no framing -> weakest; margin breaks CPHA, printable breaks bit-order
  }
  if (r.proto === "manchester") {
    // Heuristic (no CRC): weighted between UART and SPI; violations pull hard.
    if (bytes === 0) return -1e9;
    return bytes * 8 + kind("start") * 10 - kind("frame-error") * 30;
  }
  if (r.proto === "sent") {
    // ok already gates on >=1 CRC-4-valid frame; score by validated frames.
    const crcs = kind("crc");
    if (crcs === 0) return -1e9;
    return crcs * 300 + kind("data") * 4 - kind("frame-error") * 20;
  }
  if (r.proto === "canfd") {
    // Require a CRC-15-validated classic frame (FD trailers are best-effort and
    // carry no verified CRC here — never auto-claim CAN on those alone).
    const crcs = kind("crc");
    if (crcs === 0) return -1e9;
    return crcs * 500 + bytes * 2 - kind("frame-error") * 60;
  }
  if (r.proto === "mil1553") {
    // One "data" span per word; a parity-failed word adds one frame-error.
    const datas = kind("data"), ferr = kind("frame-error"), good = datas - ferr;
    if (good <= 0) return -1e9;
    return good * 150 + datas * 10 - ferr * 40;
  }
  if (r.proto === "arinc429") {
    // One "addr" (label) span per word; a parity-failed word adds "!P".
    const words = kind("addr"), ferr = kind("frame-error"), good = words - ferr;
    if (good <= 0) return -1e9;
    return good * 150 + words * 15 - ferr * 40;
  }
  if (r.proto === "usbls") {
    // A valid PID (nibble + matching complement) spans "addr"; bad -> error.
    const pids = kind("addr");
    if (pids === 0) return -1e9;
    return pids * 120 + bytes * 4 - kind("frame-error") * 30;
  }
  if (r.proto === "flexray") {
    // The header note span is "addr" only when the header CRC-11 verified.
    const hdrs = kind("addr");
    if (hdrs === 0) return -1e9;
    return hdrs * 400 + bytes * 2 - kind("frame-error") * 50;
  }
  return -1e9;
}

// clockScore: how clock-like a channel is, ROBUST TO IDLE GAPS. A real SPI/I2C
// clock bursts (uniform half-period toggling) then idles between transactions, so
// its edge-gap variance is huge — CV is useless. Instead take the dominant
// half-period as a low percentile of the gaps (ignores the few big idle gaps) and
// report uniFrac = the fraction of gaps that ARE that half-period. A clock is
// ~near-1; a data line, whose edges land on data-dependent bit boundaries, is low.
function clockScore(codes, opts) {
  const S = sliceChannel(codes, opts || {});
  if (!S.ok || S.edges.length < 6) return { ok: false, uniFrac: 0, halfPeriod: 0, S };
  const gaps = [];
  for (let k = 1; k < S.edges.length; k++) gaps.push(S.edges[k].i - S.edges[k - 1].i);
  const hp = gaps.slice().sort((a, b) => a - b)[Math.floor(gaps.length * 0.2)];
  if (hp <= 0) return { ok: false, uniFrac: 0, halfPeriod: 0, S };
  // Absolute tolerance floor: at a fast clock (few samples/half-period) ±1-sample
  // edge quantization already spends most of a pure 35%·hp band, so a plainly
  // regular clock would score low. Floor the band at 2.5 samples.
  const tol = Math.max(0.4 * hp, 2.5);
  let uni = 0;
  for (const g of gaps) if (Math.abs(g - hp) <= tol) uni++;
  return { ok: true, uniFrac: uni / gaps.length, halfPeriod: hp, edges: S.edges.length, S };
}

// idleLevel: the rail a sliced channel rests on most of the time (a clock idles
// at its CPOL rail between transactions). 1 = idle high, 0 = idle low.
function idleLevel(S) {
  if (!S || !S.ok) return 0;
  let hi = 0, lo = 0;
  for (let i = 0; i < S.level.length; i++) { if (S.level[i] === 1) hi++; else if (S.level[i] === 0) lo++; }
  return hi > lo ? 1 : 0;
}

// autodetect: try every plausible protocol + channel-role + sub-setting
// hypothesis against the two analog channels and return the best-scoring one,
// ready to drop into the decode config. { proto, roles, cfg, result, score,
// candidates[], reason }. proto === "off" means nothing matched.
function autodetect(frame, opts) {
  opts = opts || {};
  const fmt = opts.fmt || "hex";
  const n = frame.c1 ? frame.c1.length : 0;
  const colTimeS = frameDtS(frame, n);
  const chans = { 1: frame.c1, 2: frame.c2 };
  const slOpts = { minAmp: opts.minAmp != null ? opts.minAmp : 20 };
  const active = [1, 2].filter(k => {
    const c = chans[k]; if (!c) return false;
    const S = sliceChannel(c, slOpts);
    return S.ok && S.edges.length >= 2;
  });
  const cands = [];
  const add = (proto, roles, cfg, r) => cands.push({ proto, roles, cfg, result: r, score: scoreResult(r) });

  // Clock vs data, ROBUST at fast clocks. A single absolute uniFrac cutoff fails
  // when few samples/edge drag a real clock down to ~0.80 — right next to a data
  // line's ~0.70. The stronger signal is ASYMMETRY: a clocked bus (SPI) has one
  // channel markedly more uniform than the other, while a UART line has no uniform
  // partner. Measured @fast clock: SPI clk 0.80 vs data 0.53; UART line 0.65.
  const clk1 = clockScore(chans[1], slOpts), clk2 = clockScore(chans[2], slOpts);
  const u1 = clk1.uniFrac, u2 = clk2.uniFrac, hi = Math.max(u1, u2), lo = Math.min(u1, u2);
  const clockedPair = active.length >= 2 && hi > 0.72 && hi > lo + 0.12;
  const isClocky = k => { const c = k === 1 ? clk1 : clk2; return c.uniFrac > 0.78 && c.edges >= 40; };

  // UART — async single-wire. Suppress on a clocked pair (that IS SPI — a data line
  // paired with a clock) and on a lone clock (else it auto-bauds into bogus 0x55).
  if (!clockedPair)
    for (const k of active)
      if (!isClocky(k))
        add("uart", { line: k }, { baud: 0, bits: 8, parity: "none" },
          decodeUART(chans[k], colTimeS, { baud: null, bits: 8, parity: "none", fmt }));

  // Single-wire protocol twins live in their own scripts (decode_*.js); resolve
  // them lazily so decode.js alone (the node unit test) still loads — in the
  // browser and the parity bundle every twin is present and every hypothesis
  // runs, mirroring decode_autodetect.go.
  const fn = name => (typeof globalThis !== "undefined" && typeof globalThis[name] === "function") ? globalThis[name] : null;
  const dManch = fn("decodeManchester"), dSent = fn("decodeSENT"), dCan = fn("decodeCANFD"),
    dMil = fn("decodeMIL1553"), dArinc = fn("decodeARINC429"), dUsb = fn("decodeUSBLS"), dFlex = fn("decodeFlexRay");

  // Manchester — heuristic like UART, and a bare square wave IS valid
  // constant-bit Manchester, so apply the same clock suppression: never claim
  // Manchester on a clocked pair (that's SPI) or on a lone clock line.
  if (!clockedPair && dManch)
    for (const k of active)
      if (!isClocky(k))
        add("manchester", { line: k }, { baud: 0, msb: true, bits: 8 },
          dManch(chans[k], colTimeS, { bitrate: 0, ieee: true, msb: true, bits: 8, fmt }));

  // The remaining single-wire protocols self-validate (CRC-4/CRC-15/CRC-11,
  // word parity, PID complement, strict framing), so their scoring gates make
  // false claims on foreign signals cosmically unlikely — try every channel.
  for (const k of active) {
    if (dSent) add("sent", { line: k }, { baud: 0 }, dSent(chans[k], colTimeS, { tickNs: 0, nibbles: 0, fmt }));
    if (dCan) add("canfd", { line: k }, { baud: 0 }, dCan(chans[k], colTimeS, { nominalBaud: 0, dominantLow: true, fmt }));
    if (dMil) add("mil1553", { line: k }, { baud: 0 }, dMil(chans[k], colTimeS, { bitrate: 0, fmt }));
    if (dArinc) add("arinc429", { line: k }, { baud: 0 }, dArinc(chans[k], colTimeS, { bitrate: 0, fmt }));
    if (dUsb) add("usbls", { line: k }, { baud: 0 }, dUsb(chans[k], colTimeS, { bitrate: 0, fmt }));
    if (dFlex) add("flexray", { line: k }, { baud: 0 }, dFlex(chans[k], colTimeS, { bitrate: 0, fmt }));
  }

  if (active.length >= 2) {
    // I2C — try both SCL/SDA orderings (scoring, not clock-detection, resolves it).
    for (const [scl, sda] of [[1, 2], [2, 1]])
      add("i2c", { scl, sda }, {}, decodeI2C(chans[scl], chans[sda], colTimeS, { fmt }));
    // SPI — only a clocked pair. CLK = the more-uniform channel; CPOL from its idle
    // rail. Try BOTH CPHA phases and let scoreResult keep the cleaner one (higher
    // sampleMargin — the phase whose samples land on stable data, not a transition).
    if (clockedPair) {
      const clk = u1 >= u2 ? 1 : 2, data = clk === 1 ? 2 : 1;
      const cpol = idleLevel(clk === 1 ? clk1.S : clk2.S);
      for (const cpha of [0, 1])
        for (const bo of ["msb", "lsb"])   // msb first => the default on a binary tie
          add("spi", { clk, data }, { cpol, cpha, msb: bo === "msb" },
            decodeSPI(chans[clk], chans[data], colTimeS, { cpol, cpha, bitOrder: bo, fmt }));
    }
  }

  cands.sort((a, b) => b.score - a.score);
  const best = cands[0];
  if (!best || best.score <= -1e8)
    return { proto: "off", roles: {}, cfg: {}, result: null, score: -Infinity, candidates: cands,
      reason: active.length ? "no protocol matched" : "no active signal" };
  return { proto: best.proto, roles: best.roles, cfg: best.cfg, result: best.result, score: best.score, candidates: cands };
}

// decode: dispatcher used by node tests. frame = { c1, c2, col_span_s }.
// cfg.protocol in {uart,i2c,spi}; role fields are 1|2 (=> c1|c2).
function decode(frame, cfg) {
  cfg = cfg || {};
  const n = frame.c1.length, colTimeS = frameDtS(frame, n);
  const pick = r => (r === 2 || r === "c2") ? frame.c2 : frame.c1;
  const p = cfg.protocol || cfg.proto;
  if (p === "uart") return decodeUART(pick(cfg.line || 1), colTimeS, cfg);
  if (p === "i2c") return decodeI2C(pick(cfg.scl || 1), pick(cfg.sda || 2), colTimeS, cfg);
  if (p === "spi") return decodeSPI(pick(cfg.clk || 1), pick(cfg.data || 2), colTimeS, cfg);
  return fail(p || "off", "unknown protocol");
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { KINDS, fmtByte, frameDtS, frameSpanS, sliceChannel, logicAt, decodeUART, decodeI2C, decodeSPI, decode,
    scoreResult, clockScore, idleLevel, autodetect };
