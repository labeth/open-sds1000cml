// ENGMODEL-OWNER-UNIT: FU-WEB-DECODE
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
// TRLC-LINKS: REQ-SDS-018
function frameDtS(frame, n) {
  if (frame.dt_s > 0) return frame.dt_s;
  return (frame.col_span_s || 0) / (n || 1);
}
// TRLC-LINKS: REQ-SDS-018
function frameSpanS(frame, n) {
  if (frame.dt_s > 0) return frame.dt_s * n;
  return frame.col_span_s || 0;
}

// TRLC-LINKS: REQ-SDS-018
function hex2(b) { return (b & 0xff).toString(16).toUpperCase().padStart(2, "0"); }
// UNSIGNED shift (>>>): a signed >> on a value with the sign bit set shifts
// in 1s and never reaches 0 — an infinite loop, the same DoS the Go popcount
// had. cfg.bits is bounded below so the assembled word stays non-negative.
// TRLC-LINKS: REQ-SDS-018
function popcount(v) { let c = 0; v = v >>> 0; while (v) { c += v & 1; v >>>= 1; } return c; }
// fmtByte renders a payload byte per the display format: "hex" -> 48, "ascii" ->
// the printable char (else .), "both" -> 48·H. Applied to data bytes only.
// TRLC-LINKS: REQ-SDS-018
function fmtByte(v, fmt) {
  v &= 0xff;
  const h = hex2(v), printable = v >= 0x20 && v < 0x7f, ch = printable ? String.fromCharCode(v) : ".";
  if (fmt === "ascii") return ch;
  if (fmt === "both") return printable ? h + "·" + ch : h;
  return h;
}
// TRLC-LINKS: REQ-SDS-018
function fmtWord(v, bits, fmt) {
  if (bits <= 8) return fmtByte(v, fmt);
  bits = Math.min(16, bits); v &= 2 ** bits - 1;
  const h = v.toString(16).toUpperCase().padStart(Math.ceil(bits / 4), "0"), printable = v >= 32 && v < 127;
  if (fmt === "dec") return String(v);
  if (fmt === "bin") return v.toString(2).padStart(bits, "0");
  if (fmt === "ascii") return printable ? String.fromCharCode(v) : ".";
  return fmt === "both" && printable ? h + "·" + String.fromCharCode(v) : h;
}
// TRLC-LINKS: REQ-SDS-018
function fail(proto, error, meta) {
  return { ok: false, error, proto, spans: [], text: "", bytes: [], meta: meta || {} };
}

// decHeldBeyond counts the samples of a line's rarer side of thr that lie in
// runs of three or more: a pulse holds its level, a noise spike does not
// (decode.go heldBeyond).
// TRLC-LINKS: REQ-SDS-018
function decHeldBeyond(codes, thr) {
  const held = [0, 0];
  let run = 0;
  let side = -1;
  for (let i = 0; i < codes.length; i++) {
    const s = codes[i] >= thr ? 1 : 0;
    if (s === side) run++;
    else { run = 1; side = s; }
    if (run === 3) held[s] += 3;
    else if (run > 3) held[s]++;
  }
  return Math.min(held[0], held[1]);
}

// decRailsWithin is sliceChannel's rails over the histogram's codes
// gmin..gmax: each half's count-weighted centroid (decode.go railsWithin).
// TRLC-LINKS: REQ-SDS-018
function decRailsWithin(h, gmin, gmax, minAmp) {
  if (gmax <= gmin) return { reason: "flat/no transitions" };
  const mid0 = (gmin + gmax) / 2;
  let lw = 0;
  let ls = 0;
  let hw = 0;
  let hs = 0;
  for (let c = gmin; c <= gmax; c++) {
    if (c <= mid0) { lw += h[c] * c; ls += h[c]; }
    else { hw += h[c] * c; hs += h[c]; }
  }
  const lowRail = ls ? lw / ls : gmin;
  const highRail = hs ? hw / hs : gmax;
  const amp = highRail - lowRail;
  if (amp < minAmp) return { reason: "amplitude " + amp.toFixed(0) + " < " + minAmp, lowRail, highRail };
  return { lowRail, highRail };
}

// decRailsAround is the rails of a line sliced at a stated threshold when
// the histogram finds none: a line holding its rarer level at all (runs of
// three samples or more) is a line, however seldom pulsed; each rail the
// mean of its side's codes (decode.go railsAround).
// TRLC-LINKS: REQ-SDS-018
function decRailsAround(codes, thr, minAmp) {
  if (decHeldBeyond(codes, thr) < 3) return null;
  let ls = 0;
  let lw = 0;
  let hs = 0;
  let hw = 0;
  for (let i = 0; i < codes.length; i++) {
    const v = codes[i];
    if (v < 0) continue;
    if (v >= thr) { hs++; hw += v; } else { ls++; lw += v; }
  }
  const lowRail = lw / ls;
  const highRail = hw / hs;
  if (highRail - lowRail < minAmp) return null;
  return { lowRail, highRail };
}

// sliceChannel: analog code stream -> digital. Finds the two rails as
// count-weighted centroids (an idle line sits on one rail most of the time, so
// percentiles would bias the threshold), sets a mid threshold with a hysteresis
// band for edge/period detection, and records threshold crossings. The bit
// VALUE is never read from the hysteretic level[] (which lags); callers use
// logicAt(), which thresholds the raw code at the sample instant.
// TRLC-LINKS: REQ-SDS-018
function sliceChannel(codes, opts) {
  // A frame may carry NO per-sample array for a channel (channel toggled off,
  // or an envelope/roll band ships env min/max instead) — decCodes()
  // then hand us undefined. Report it like any other unusable input; every
  // caller already checks S.ok.
  if (!codes) return { ok: false, reason: "no channel data (channel off / envelope frame)" };
  opts = opts || {};
  const hystFrac = opts.hystFrac != null ? opts.hystFrac : 0.20;
  const minAmp = opts.minAmp != null ? opts.minAmp : 20;
  const n = codes.length;
  let valid = 0;
  const h = new Uint32Array(256); // integer counts: no float add a sample
  for (let i = 0; i < n; i++) { const v = codes[i]; if (v >= 0 && v <= 255) { h[v | 0]++; valid++; } }
  if (valid < 8) return { ok: false, reason: "no/too-few valid samples" };
  const noiseFloor = Math.max(1, 0.001 * valid);
  let gmin = 0; while (gmin < 255 && h[gmin] < noiseFloor) gmin++;
  let gmax = 255; while (gmax > 0 && h[gmax] < noiseFloor) gmax--;
  let r = decRailsWithin(h, gmin, gmax, minAmp);
  if (r.reason) {
    // A line pulsed seldom (a Wiegand line) holds no code of its other level
    // as often as the floor: its tails are cut where their counts add up to it
    // (decode.go rails).
    let cmin = 0;
    for (let acc = h[0]; cmin < 255 && acc < noiseFloor; acc += h[cmin]) cmin++;
    let cmax = 255;
    for (let acc = h[255]; cmax > 0 && acc < noiseFloor; acc += h[cmax]) cmax--;
    const c = decRailsWithin(h, cmin, cmax, minAmp);
    if (!c.reason && decHeldBeyond(codes, (c.lowRail + c.highRail) / 2) >= noiseFloor) r = c;
  }
  if (r.reason && opts.threshold != null) {
    const a = decRailsAround(codes, opts.threshold, minAmp);
    if (a) r = a;
  }
  if (r.reason) return { ok: false, reason: r.reason };
  const lowRail = r.lowRail;
  const highRail = r.highRail;
  const amp = highRail - lowRail;
  const threshold = opts.threshold != null ? opts.threshold : (lowRail + highRail) / 2;
  const band = hystFrac * amp / 2, thHi = threshold + band, thLo = threshold - band;

  const level = new Int8Array(n);
  const edges = [];
  // Seed from the first valid sample, then one compare a sample against the
  // band edge the current level leaves by. p is the last valid sample, the
  // other end of a crossing interpolated at the threshold.
  let cur = -1;
  let i = 0;
  for (; i < n; i++) {
    const v = codes[i];
    if (v < 0) { level[i] = -1; continue; }
    cur = v >= threshold ? 1 : 0;
    level[i++] = cur;
    break;
  }
  let p = i - 1;
  for (; i < n; i++) {
    const v = codes[i];
    if (v < 0) { level[i] = -1; continue; }
    if (cur === 0 ? v >= thHi : v <= thLo) {
      const a = codes[p];
      edges.push({ i, dir: cur === 0 ? 1 : -1, x: v !== a ? p + (threshold - a) / (v - a) : i });
      cur ^= 1;
    }
    level[i] = cur;
    p = i;
  }
  return { ok: true, reason: null, n, codes, lowRail, highRail, amp, threshold, thHi, thLo, level, edges };
}

// logicAt: the bit-decision primitive — raw code vs threshold at column x
// (rounded). Returns 0/1, or -1 for a gap / out of range.
// TRLC-LINKS: REQ-SDS-018
function logicAt(s, x) {
  const i = Math.round(x);
  if (i < 0 || i >= s.n) return -1;
  const v = s.codes[i];
  if (v < 0) return -1;
  return v >= s.threshold ? 1 : 0;
}

// minConsecutive returns the smallest column gap between successive edges whose
// direction passes `dirOk` (used for the too-fast resolution guard).
// TRLC-LINKS: REQ-SDS-018
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
// TRLC-LINKS: REQ-SDS-018
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
    if (frac >= 0.7 && frac >= bestFrac) {
      // Final estimate over every fitting gap, multi-bit runs included: total
      // time over total bits (decode_uart.go inferUARTspb). The 1-bit mean
      // alone carries the edge quantization of each short gap; peak-detect
      // frames read 115200 Bd 1.2% fast that way.
      let sumG = 0, sumM = 0;
      for (const g of kg) { const m = Math.round(g / ref); if (m >= 1 && Math.abs(g - m * ref) <= 0.35 * ref) { sumG += g; sumM += m; } }
      if (sumM > 0) ref = sumG / sumM;
      best = ref; bestFrac = frac;
    }
  }
  if (best > 0) return { spb: best, reason: null };
  return { spb: 0, reason: "baud ambiguous — set it explicitly" };
}

// STANDARD_BAUDS mirrors decode_uart.go's standardBauds.
const STANDARD_BAUDS = [300, 600, 1200, 2400, 4800, 9600, 14400, 19200, 28800, 38400,
  57600, 76800, 115200, 230400, 250000, 460800, 500000, 921600, 1000000, 1500000, 2000000, 3000000];

// snapStandardBaud moves an inferred samples-per-bit onto the nearest standard
// rate when one lies within 1.5% (decode_uart.go snapStandardBaud): an estimate
// from a short record is a fraction of a sample off per bit, and the rate on
// the wire is almost always a standard one. Otherwise the estimate stands.
// TRLC-LINKS: REQ-SDS-018
function snapStandardBaud(spb, colTimeS) {
  if (!(spb > 0) || !(colTimeS > 0)) return spb;
  const baud = 1 / (spb * colTimeS);
  for (const std of STANDARD_BAUDS) if (Math.abs(baud - std) <= 0.015 * std) return 1 / (std * colTimeS);
  return spb;
}

// UART_SYNC_FRAMES bounds the sync-in (Go uartSyncFrames).
const UART_SYNC_FRAMES = 8;

// uartSyncIn is Go's syncIn: a record opens idle when the line is high for
// confirm bit times before the first start edge (then every frame stands);
// else the frames back to back with the first are suspect - in sync when none
// of the first UART_SYNC_FRAMES is an error and there are two or more, else
// from two clean frames after the last error or the first frame after an
// idle, if that comes within UART_SYNC_FRAMES frames. Reports a change.
// TRLC-LINKS: REQ-SDS-018
function uartSyncIn(spans, S, SPB, frameBits, confirm, idle) {
  if (!spans.length) return false;
  const run = Math.ceil(confirm * SPB);
  if (spans[0].i0 >= run) {
    let opened = true;
    for (let j = spans[0].i0 - run; j < spans[0].i0 && opened; j++) opened = logicAt(S, j) === idle;
    if (opened) return false; // opened idle
  }
  // TRLC-LINKS: REQ-SDS-018
  const clean = (k) => spans[k].kind === "data";
  let end = 1; // the frames back to back with the first: [0, end)
  while (end < spans.length && spans[end].i0 - (spans[end - 1].i0 + frameBits * SPB) < confirm * SPB) end++;
  let lastErr = -1;
  for (let k = 0; k < end && k < UART_SYNC_FRAMES; k++) if (!clean(k)) lastErr = k;
  let sync = end; // the first frame after an idle
  if (lastErr < 0 && end >= 2) return false; // in sync from the first frame
  if (lastErr >= 0 && lastErr + 2 < end && clean(lastErr + 1) && clean(lastErr + 2)) sync = lastErr + 1;
  if (sync > UART_SYNC_FRAMES || sync >= spans.length) return false;
  for (let k = 0; k < sync; k++) { spans[k].kind = "gap"; spans[k].text = "gap"; }
  return true;
}

// TRLC-LINKS: REQ-SDS-018
function decodeUART(codes, colTimeS, cfg) {
  cfg = cfg || {};
  if (cfg.inverted) {
    codes = Array.from(codes, x => x < 0 ? x : 255 - x);
    cfg = Object.assign({}, cfg);
    if (cfg.threshold != null) cfg.threshold = 255 - cfg.threshold;
  }
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
    SPB = snapStandardBaud(inf.spb, colTimeS);
    baud = 1 / (SPB * colTimeS);
  }
  if (!(SPB >= minSPB)) return fail("uart", SPB.toFixed(1) + " samples/bit; need >= " + minSPB, { samplesPerBit: SPB, baud });

  // TRLC-LINKS: REQ-SDS-018
  const parityOf = v => { const p = popcount(v & ((1 << bits) - 1)) & 1; return parity === "even" ? p : (1 - p); };
  // The parity bit lengthens the frame — count it so a frame near the record end
  // isn't started with its stop bit off the captured range.
  const pcNeed = parity !== "none" ? 1 : 0;
  const need = Math.ceil((bits + 2 + pcNeed) * SPB);
  // frameAt reads the frame whose start bit begins at start (Go frameAt):
  // { sp, next } or null when no start bit is there.
  // TRLC-LINKS: REQ-SDS-018
  const frameAt = (start) => {
    if (logicAt(S, Math.round(start + 0.5 * SPB)) !== (1 - idle)) return null; // confirm start bit
    let val = 0, gap = false;
    for (let c = 0; c < bits; c++) {
      const b = logicAt(S, Math.round(start + (1.5 + c) * SPB));
      if (b < 0) { gap = true; break; }
      if (lsb) val |= (b << c); else val = (val << 1) | b;
    }
    const i1 = Math.min(n - 1, Math.round(start + (bits + 1) * SPB));
    if (gap) return { sp: { i0: start, i1, text: "gap", kind: "gap" }, next: start + 1 };
    let kind = "data", pfx = "", pc = 0;
    if (parity !== "none") {
      pc = 1;
      const pb = logicAt(S, Math.round(start + (1.5 + bits) * SPB));
      if (pb >= 0 && pb !== parityOf(val)) { kind = "parity-error"; pfx = "!"; }
    }
    const sb = logicAt(S, Math.round(start + (1.5 + bits + pc) * SPB));
    if (sb < 0) return { sp: { i0: start, i1, text: "gap", kind: "gap" }, next: start + 1 }; // stop bit off the record: incomplete frame
    if (sb !== idle) { if (kind === "data") kind = "frame-error"; pfx = "!"; } // wrong stop level = framing error
    return { sp: { i0: start, i1, text: pfx + fmtWord(val, bits, fmt), kind, val }, next: Math.round(start + (bits + 1 + pc) * SPB) + 1 };
  };
  // scan reads the frames whose start edges lie in [from, to) onto spans.
  // TRLC-LINKS: REQ-SDS-018
  // The level a sample at a time is carried from one to the next: two
  // logicAt calls (each a Math.round) a sample were most of a decode.
  const sc = S.codes, thr = S.threshold, sn = S.n;
  // TRLC-LINKS: REQ-SDS-018
  const lv = (k) => (k < 0 || k >= sn || sc[k] < 0 ? -1 : sc[k] >= thr ? 1 : 0);
  // TRLC-LINKS: REQ-SDS-018
  const scan = (spans, from, to) => {
    let prev = lv(from - 1);
    for (let i = from; i < n - need - guard && i < to; i++) {
      const cur = lv(i);
      if (prev === idle && cur === (1 - idle)) { // start edge
        const fr = frameAt(i);
        if (fr) { spans.push(fr.sp); i = fr.next - 1; prev = lv(i); continue; }
      }
      prev = cur;
    }
    return spans;
  };
  let spans;
  const chain = uartAnchorChain(S, cfg, SPB, bits + pcNeed, guard, idle, frameAt);
  // Sync-in (mirrors Go syncIn): the frames a record opened inside traffic
  // with are gaps - read from a data bit's edge, they are no bytes on the line.
  if (!chain.length) {
    spans = scan([], guard, n);
    uartSyncIn(spans, S, SPB, bits + 2 + pcNeed, bits - 0.5, idle);
  } else {
    // before the chain: the frames that end before it, read in sync as any
    // record's first frames are
    const frameLen = (bits + 2 + pcNeed) * SPB;
    spans = scan([], guard, Math.trunc(chain[0].i0 - frameLen + 0.5 * SPB));
    uartSyncIn(spans, S, SPB, bits + 2 + pcNeed, bits - 0.5, idle);
    for (const sp of chain) spans.push(sp);
    scan(spans, frameAt(chain[chain.length - 1].i0).next, n);
  }
  const bytes = [], toks = [];
  for (const sp of spans) if (sp.kind !== "gap") { toks.push(sp.text); bytes.push(sp.val); }
  return { ok: true, error: null, proto: "uart", spans, text: toks.join(" "), bytes,
    meta: { baud: Math.round(baud), samplesPerBit: SPB, threshold: S.threshold, lowRail: S.lowRail, highRail: S.highRail } };
}

// snapRate is a bit rate read from a record's edges moved onto the nearest of a
// bus's standard rates within 1.5 %; otherwise it stands (Go snapRate).
// TRLC-LINKS: REQ-SDS-018
function snapRate(baud, rates) {
  for (const r of rates) if (Math.abs(baud - r) <= 0.015 * r) return Math.round(r);
  return baud;
}

// uartAnchorChain is the frames a hardware match fixes the framing of, in time
// order (Go uartAnchorChain): cfg.anchor is a sample within the matched
// character's stop bit; the matched frame and the frames back to back before
// it, while each reads clean. [] without an anchor or when the matched frame
// does not read clean. dataBits counts the parity bit.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
function uartAnchorChain(S, cfg, spb, dataBits, guard, idle, frameAt) {
  const a = cfg.anchor;
  if (!(a >= 0) || !(spb > 0)) return [];
  const startDir = idle === 1 ? -1 : 1;
  // TRLC-LINKS: REQ-SDS-018
  const startNear = (lo, hi) => {
    for (const e of S.edges) if (e.dir === startDir && e.x >= lo && e.x <= hi) return e.i;
    return -1;
  };
  // TRLC-LINKS: REQ-SDS-018
  const clean = (start) => {
    if (start < guard) return null;
    const fr = frameAt(start);
    return fr && fr.sp.kind === "data" ? fr.sp : null;
  };
  const first = clean(startNear(a - (dataBits + 2.1) * spb, a - (dataBits + 0.9) * spb));
  if (!first) return [];
  const chain = [first], frameLen = (dataBits + 2) * spb;
  while (chain.length < 4096) {
    const at = chain[0].i0 - frameLen;
    const sp = clean(startNear(at - 0.6 * spb, at + 0.6 * spb));
    if (!sp) break;
    chain.unshift(sp);
  }
  return chain;
}

// TRLC-LINKS: REQ-SDS-018
function decodeI2C(scl, sda, colTimeS, cfg) {
  cfg = cfg || {};
  if (cfg.inverted) {
    scl = Array.from(scl, x => x < 0 ? x : 255-x);
    sda = Array.from(sda, x => x < 0 ? x : 255-x);
    cfg = Object.assign({}, cfg);
    if (cfg.threshold != null) cfg.threshold = 255-cfg.threshold;
  }
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

// TRLC-LINKS: REQ-SDS-018
function decodeSPI(clk, data, colTimeS, cfg) {
  cfg = cfg || {};
  if (cfg.inverted) {
    clk = Array.from(clk, x => x < 0 ? x : 255-x);
    data = Array.from(data, x => x < 0 ? x : 255-x);
    cfg = Object.assign({}, cfg);
    if (cfg.threshold != null) cfg.threshold = 255-cfg.threshold;
  }
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
  // Under six samples a clock period, on the converters' own samples (2 ns or
  // finer), the data line is taken at the clock's interpolated crossing plus
  // cfg.dataLag (the channels' sampling phase), as the FPGA's receiver on the
  // samples does; with cfg.dataDelay (a device's late data, in samples) at
  // any samples a period, that much later. Mirrors decode_i2c_spi.go.
  const fine = period < 6 && colTimeS <= 2.05e-9, dataLag = +cfg.dataLag || 0, dataDelay = +cfg.dataDelay || 0;
  if ((period < 6 && !fine) || period < 3.5) return fail("spi", period.toFixed(1) + " cols/clock; too few samples/bit", { colsPerClock: period });
  // With no CS to frame bytes, re-align on a clock-idle gap: consecutive
  // sampling edges are one clock period apart within a burst; a gap longer than
  // ~1.5 typical periods means a new transaction, so restart the byte.
  const gapReset = 1.5 * period;
  const spans = [], bytes = [], toks = [];
  let ck = -1, bitCount = 0, val = 0, bitStart = 0, lastSample = -1;
  // sampleMargin = how far the sampled data sits from the threshold, averaged over
  // all bits (1 = dead on a rail, 0 = on the threshold). The CORRECT clock phase
  // samples mid-bit where data is stable (high margin); the wrong phase samples on
  // the data transition (low margin): the detection picks CPHA by it (Go Result.Margin).
  const halfAmp = DA.amp / 2 || 1;
  let mSum = 0, mN = 0;
  // Under six samples a clock period the clock's edges are the FPGA
  // receiver's, each direction armed on its own (decode_i2c_spi.go
  // armedCrossings): a sampled half-period short of the band keeps its bit.
  const armed = fine ? armedCrossings(CK, n) : null;
  for (let i = 0; i < n; i++) {
    const l = CK.level[i];
    if (l < 0) { ck = -1; continue; }
    const pck = ck; ck = l;
    if (pck < 0) continue;
    let rising = pck === 0 && ck === 1, falling = pck === 1 && ck === 0;
    if (armed) { rising = armed.rise[i] === 1; falling = armed.fall[i] === 1; }
    if ((sampleRising && rising) || (!sampleRising && falling)) {
      const at = fine || dataDelay ? crossingAt(CK, i, rising) + dataLag + dataDelay : i;
      const bit = logicAt(DA, at);
      if (bit < 0) continue;
      const cd = DA.codes[Math.round(at)];
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

// armedCrossings: a sliced line's crossings as the FPGA's receiver on the
// samples takes them, each direction armed by a sample beyond the opposite
// edge of the band; a missing sample disarms both (decode_i2c_spi.go).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-229
function armedCrossings(s, n) {
  const rise = new Uint8Array(n);
  const fall = new Uint8Array(n);
  let upArmed = false, downArmed = false;
  for (let i = 0; i < n; i++) {
    const c = s.codes[i];
    if (c < 0) { upArmed = false; downArmed = false; continue; }
    if (upArmed && c >= s.threshold) { rise[i] = 1; upArmed = false; }
    if (downArmed && c < s.threshold) { fall[i] = 1; downArmed = false; }
    if (c <= s.thLo) upArmed = true;
    if (c >= s.thHi) downArmed = true;
  }
  return { rise, fall };
}

// crossingAt: where a sliced line crossed its threshold before the sample i
// its level changed on, linear between the two samples around the threshold
// (decode_i2c_spi.go crossingAt).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-229
function crossingAt(s, i, rising) {
  let j = i;
  while (j > 0 && s.codes[j - 1] >= 0 && (s.codes[j - 1] >= s.threshold) === rising) j--;
  if (j === 0 || s.codes[j] < 0 || (s.codes[j] >= s.threshold) !== rising || s.codes[j - 1] < 0) return i;
  const a = s.codes[j - 1], b = s.codes[j];
  return j - 1 + (s.threshold - a) / (b - a);
}

// decode: dispatcher used by node tests. frame = { c1, c2, col_span_s }.
// cfg.protocol in {uart,i2c,spi}; role fields are 1|2 (=> c1|c2).
// TRLC-LINKS: REQ-SDS-018
function decode(frame, cfg) {
  cfg = cfg || {};
  const n = frame.c1.length, colTimeS = frameDtS(frame, n);
  // TRLC-LINKS: REQ-SDS-018
  const pick = r => (r === 2 || r === "c2") ? frame.c2 : frame.c1;
  const p = cfg.protocol || cfg.proto;
  if (p === "uart") return decodeUART(pick(cfg.line || 1), colTimeS, cfg);
  if (p === "i2c") return decodeI2C(pick(cfg.scl || 1), pick(cfg.sda || 2), colTimeS, cfg);
  if (p === "spi") return decodeSPI(pick(cfg.clk || 1), pick(cfg.data || 2), colTimeS, cfg);
  return fail(p || "off", "unknown protocol");
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { KINDS, fmtByte, fmtWord, frameDtS, frameSpanS, sliceChannel, logicAt, decodeUART, decodeI2C, decodeSPI, decode,
    snapRate };
