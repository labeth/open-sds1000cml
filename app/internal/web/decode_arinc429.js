// decode_arinc429.js — ARINC 429 single-channel decoder, a classic script that
// mirrors decode_arinc429.go step for step so the web overlay and the on-device
// LCD agree byte-for-byte. Self-contained (no dependency on decode.js): ARINC is
// a BIPOLAR return-to-zero line with THREE levels — HI(+), NULL(0), LO(-) — so it
// needs its own tri-level slicer rather than the shared two-level sliceChannel.
//
// A logic 1 is a HI pulse (first half of the bit cell) then a return to NULL; a 0
// is a LO pulse then NULL. Every bit cell carries exactly one RZ pulse whose
// polarity is the bit. Words are 32 bits (bit 1 first): label(8, 3-digit octal,
// bit-reversed), SDI(2), DATA(19), SSM(2), parity(1, odd), separated by >= 4
// bit-times of NULL. cfg.bitrate>0 pins the bit period; 0 auto-infers it from the
// pulse spacing. cfg.threshold overrides the auto NULL (mid) level.

function decodeARINC429(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const proto = "arinc429";
  const minSPB = 4;
  const fail = (error) => ({ ok: false, error, proto, spans: [], text: "", bytes: [], meta: {} });
  // The role channel's array can be absent (channel toggled off / envelope
  // frame); this decoder has its own slicer, so guard here like sliceChannel.
  const n = codes ? codes.length : 0;
  if (n < 8) return fail("no/too-few samples");

  // --- tri-level slice: histogram -> NULL(mid), HI rail(gmax), LO rail(gmin).
  const h = new Float64Array(256);
  for (let i = 0; i < n; i++) { const v = codes[i]; if (v >= 0 && v <= 255) h[v | 0]++; }
  const noiseFloor = Math.max(1, 0.001 * n);
  let gmin = 0; while (gmin < 255 && h[gmin] < noiseFloor) gmin++;
  let gmax = 255; while (gmax > 0 && h[gmax] < noiseFloor) gmax--;
  if (gmax <= gmin) return fail("flat/no transitions");
  // NULL level = the dominant (mode) code in the active range.
  let mid = gmin, best = -1;
  for (let c = gmin; c <= gmax; c++) { if (h[c] > best) { best = h[c]; mid = c; } }
  let midf = mid;
  if (cfg.threshold != null) midf = cfg.threshold;
  let rangeUp = gmax - midf, rangeDn = midf - gmin;
  const span = Math.max(rangeUp, rangeDn);
  if (span < 16) return fail("amplitude too small / not a bipolar RZ signal");
  // Mirror a missing polarity (all-1s or all-0s word) so its threshold holds.
  if (rangeUp < 0.25 * span) rangeUp = span;
  if (rangeDn < 0.25 * span) rangeDn = span;
  const thrHi = midf + 0.4 * rangeUp, thrLo = midf - 0.4 * rangeDn;
  const exitHi = midf + 0.15 * rangeUp, exitLo = midf - 0.15 * rangeDn; // hysteretic return to NULL

  // --- detect pulses: NULL->HI / NULL->LO transitions. Each RZ pulse is one bit
  // cell; its polarity is the bit (HI=1, LO=0) and its start locates it.
  const pulses = [];
  let state = 0; // 0 NULL, +1 HI, -1 LO
  for (let i = 0; i < n; i++) {
    const v = codes[i];
    if (v < 0) continue; // gap sample
    if (state === 0) {
      if (v >= thrHi) { state = 1; pulses.push({ i, sign: 1 }); }
      else if (v <= thrLo) { state = -1; pulses.push({ i, sign: -1 }); }
    } else if (state === 1) {
      if (v <= thrLo) { state = -1; pulses.push({ i, sign: -1 }); }
      else if (v < exitHi) { state = 0; }
    } else { // -1
      if (v >= thrHi) { state = 1; pulses.push({ i, sign: 1 }); }
      else if (v > exitLo) { state = 0; }
    }
  }
  if (pulses.length < 2) return fail("no ARINC pulses");

  // --- bit period T (samples). cfg.bitrate pins it; else infer from the pulse-
  // start spacing (intra-word gaps ~1*T, inter-word >= ~5*T => low cluster = T).
  let T;
  if (cfg.bitrate > 0) {
    T = (1 / cfg.bitrate) / colTimeS;
  } else {
    const gaps = [];
    for (let k = 1; k < pulses.length; k++) { const g = pulses[k].i - pulses[k - 1].i; if (g >= 1) gaps.push(g); }
    if (gaps.length < 3) return fail("too few pulses / cannot infer bitrate");
    gaps.sort((a, b) => a - b);
    let p = gaps[Math.floor(gaps.length * 0.1)];
    let sum = 0, cnt = 0;
    for (const g of gaps) if (Math.abs(g - p) <= 0.35 * p) { sum += g; cnt++; }
    if (cnt) p = sum / cnt;
    T = p;
  }
  if (!isFinite(T) || !(T >= minSPB)) return fail(T.toFixed(1) + " samples/bit; need >= " + minSPB);

  // --- segment pulses into 32-bit WORDS on the inter-word NULL gap (> 2.5*T).
  const segs = [];
  let segStart = 0;
  for (let k = 1; k < pulses.length; k++) {
    if (pulses[k].i - pulses[k - 1].i > 2.5 * T) { segs.push([segStart, k - 1]); segStart = k; }
  }
  segs.push([segStart, pulses.length - 1]);

  const spans = [], bytes = [], toks = [];
  let words = 0;
  const oct3 = (v) => (v & 0xff).toString(8).padStart(3, "0");
  const hex5 = (v) => (v & 0x7ffff).toString(16).toUpperCase().padStart(5, "0");
  for (const sg of segs) {
    const s0 = pulses[sg[0]].i;
    const bits = new Array(32).fill(-1);
    let filled = 0, hits = 0;
    for (let j = sg[0]; j <= sg[1]; j++) {
      const k = Math.round((pulses[j].i - s0) / T);
      if (k < 0 || k >= 32) continue; // stray pulse outside the 32-bit window
      hits++;                         // pulses that land inside the word window
      if (bits[k] === -1) filled++;
      bits[k] = pulses[j].sign > 0 ? 1 : 0;
    }
    // A real ARINC word is one RZ pulse per bit cell => ~32 pulses. Far more means
    // MANY per cell — dense noise that merely happened to fill all 32 slots. The
    // 1-bit odd parity passes ~50% of the time, so without this density gate noise
    // is confidently mis-accepted.
    if (filled !== 32 || hits > 34) continue; // partial word, or noise (multiple pulses/cell)

    // Fields, transmission order (bits[0] = first bit on the wire = ARINC #1).
    let labelRev = 0;
    for (let i = 0; i < 8; i++) labelRev = (labelRev << 1) | bits[i]; // bit 1 -> octal MSB
    let dataVal = 0;
    for (let i = 0; i < 19; i++) dataVal |= bits[10 + i] << i;
    const ssm = bits[29] | (bits[30] << 1);
    let word32 = 0;
    for (let i = 0; i < 32; i++) word32 = word32 | (bits[i] << i); // ARINC bit 1 = LSB
    let ones = 0, u = word32 >>> 0; while (u) { ones += u & 1; u >>>= 1; }
    const parityOdd = (ones & 1) === 1; // odd parity over all 32 bits

    const cellStart = (k) => { let p = Math.round(s0 + k * T); if (p < 0) p = 0; if (p >= n) p = n - 1; return p; };
    const cellEnd = (k) => { let p = Math.round(s0 + (k + 1) * T) - 1; if (p < 0) p = 0; if (p >= n) p = n - 1; return p; };

    if (words > 0) { spans.push({ i0: cellStart(0), i1: cellStart(0), text: "", kind: "gap" }); toks.push("|"); }
    words++;
    const lblTxt = oct3(labelRev);
    spans.push({ i0: cellStart(0), i1: cellEnd(7), text: lblTxt, kind: "addr", val: labelRev });
    const dataTxt = hex5(dataVal);
    spans.push({ i0: cellStart(10), i1: cellEnd(28), text: dataTxt, kind: "data", val: dataVal });
    const ssmTxt = "SSM" + ssm;
    spans.push({ i0: cellStart(29), i1: cellEnd(30), text: ssmTxt, kind: "rw", val: ssm });
    toks.push(lblTxt, dataTxt, ssmTxt);
    if (!parityOdd) { spans.push({ i0: cellStart(31), i1: cellEnd(31), text: "!P", kind: "frame-error" }); toks.push("!P"); }
    // Result.bytes = the 4 raw bytes of the 32-bit word (little-endian: byte 0 is
    // the label field, ... byte 3 holds SSM+parity).
    bytes.push(word32 & 0xff, (word32 >>> 8) & 0xff, (word32 >>> 16) & 0xff, (word32 >>> 24) & 0xff);
  }

  if (words === 0) return fail("no complete ARINC 429 word found");
  let baud = cfg.bitrate || 0;
  if (colTimeS > 0) baud = Math.round(1 / (T * colTimeS));
  return {
    ok: true, error: null, proto, spans, text: toks.join(" "), bytes,
    meta: { bitrate: baud, samplesPerBit: T, threshold: midf, lowRail: gmin, highRail: gmax }
  };
}

if (typeof module !== "undefined" && module.exports) module.exports = { decodeARINC429 };
