// End-to-end test for the protocol decoders in decode.js — the same code
// ui.html runs. Synthetic generators render ideal logic into 0..255 code arrays
// at a chosen SPB (columns per bit); colTimeS is picked so baud = 1/(SPB*colTimeS).
// Run: node decode.test.cjs   (exit 0 = pass).
const { fmtByte, sliceChannel, logicAt, decodeUART, decodeI2C, decodeSPI, autodetect } = require("./decode.js");

let failed = 0;
function ok(c, m) { if (!c) { console.error("FAIL:", m); failed++; } else { console.log("ok  -", m); } }
function near(a, b, tol, m) { ok(Math.abs(a - b) <= tol, `${m} (got ${a}, want ${b}±${tol})`); }

const LO = 40, HI = 210;
const lvl = b => (b ? HI : LO);

// --- generators --------------------------------------------------------------
function uartGen(bytes, SPB, o) {
  o = o || {}; const idle = o.idle != null ? o.idle : 1, noise = o.noise || 0;
  const out = []; let seed = 12345 >>> 0;
  const rnd = () => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return (seed / 0x7fffffff - 0.5) * 2; };
  const push = (c, b) => { for (let k = 0; k < c; k++) out.push(Math.max(0, Math.min(255, lvl(b) + Math.round(noise * rnd())))); };
  push(SPB * 4, idle);
  for (const v of bytes) {
    push(SPB, 1 - idle);                    // start
    for (let b = 0; b < 8; b++) push(SPB, (v >> b) & 1); // 8 data, LSB first
    push(SPB, idle);                        // stop
  }
  push(SPB * 4, idle);
  return out;
}

// I2C: build SCL+SDA together. half = SCL half-period; one bit = SPB cols.
function i2cGen(events, SPB) {
  const half = Math.max(1, Math.round(SPB / 2));
  const scl = [], sda = [];
  const seg = (c, s, d) => { for (let k = 0; k < c; k++) { scl.push(lvl(s)); sda.push(lvl(d)); } };
  const bit = b => { seg(half, 0, b); seg(half, 1, b); }; // SDA set while SCL low, sampled on SCL rising
  seg(SPB * 2, 1, 1);                       // idle
  for (const ev of events) {
    if (ev.type === "start") { seg(half, 1, 1); seg(half, 1, 0); }             // SDA falls while SCL high
    else if (ev.type === "stop") { seg(half, 0, 0); seg(half, 1, 0); seg(half, 1, 1); } // SDA rises while SCL high
    else if (ev.type === "byte") { for (let b = 7; b >= 0; b--) bit((ev.val >> b) & 1); } // MSB first
    else if (ev.type === "ack") bit(0);
    else if (ev.type === "nak") bit(1);
  }
  seg(SPB * 2, 1, 1);
  return { scl, sda };
}

// SPI: mode-aware — DATA is centred on each mode's sampling edge (rising iff cpol==cpha).
function spiGen(bytes, SPB, o) {
  o = o || {}; const cpol = o.cpol ? 1 : 0, cpha = o.cpha ? 1 : 0, msb = (o.bitOrder || "msb") === "msb";
  const half = Math.max(2, Math.round(SPB / 2));
  const bits = [];
  for (const B of bytes) for (let b = 0; b < 8; b++) bits.push((B >> (msb ? 7 - b : b)) & 1);
  const nHalf = 2 * bits.length + 2;
  const clkLvl = m => (m % 2 === 0) ? cpol : 1 - cpol;   // start idle at cpol, toggle each half
  const clk = new Array(nHalf * half), data = new Array(nHalf * half).fill(LO);
  for (let m = 0; m < nHalf; m++) for (let k = 0; k < half; k++) clk[m * half + k] = lvl(clkLvl(m));
  const sampleRising = cpol === cpha;
  const sampCols = [];
  for (let m = 1; m < nHalf; m++) {
    const rising = clkLvl(m - 1) === 0 && clkLvl(m) === 1, falling = clkLvl(m - 1) === 1 && clkLvl(m) === 0;
    if ((sampleRising && rising) || (!sampleRising && falling)) sampCols.push(m * half);
  }
  for (let k = 0; k < bits.length && k < sampCols.length; k++) {
    const c = sampCols[k], v = lvl(bits[k]);
    for (let j = c - half; j < c + half; j++) if (j >= 0 && j < data.length) data[j] = v;
  }
  return { clk, data };
}

// SPB=40 cols/bit; colTimeS chosen so baud=1/(40*colTimeS)=115200.
const SPB = 40, COLT = 1 / (SPB * 115200);

// --- 1. slicing --------------------------------------------------------------
{
  const codes = uartGen([0x55], SPB, { noise: 3 }); // 0x55 = alternating bits
  const s = sliceChannel(codes, {});
  ok(s.ok, "slice: bimodal signal slices ok");
  near(s.highRail - s.lowRail, HI - LO, 12, "slice: amplitude ~= rail span");
  near(s.threshold, (HI + LO) / 2, 12, "slice: threshold ~= midpoint");
  ok(s.edges.length >= 8, `slice: found ${s.edges.length} edges`);
  ok(logicAt(s, SPB * 4 + SPB * 0.5) === 0, "logicAt reads the start bit low (raw threshold)");
  const flat = new Array(500).fill(128);
  ok(!sliceChannel(flat, {}).ok, "slice: flat DC -> not ok");
}

// --- 2. UART auto-baud -------------------------------------------------------
{
  const r = decodeUART(uartGen([0x48, 0x69, 0x21], SPB), COLT, {});
  ok(r.ok, "UART: decodes ok");
  ok(JSON.stringify(r.bytes) === JSON.stringify([0x48, 0x69, 0x21]), `UART bytes = ${r.bytes.map(b => b.toString(16))}`);
  ok(r.text === "48 69 21", `UART text = "${r.text}"`);
  near(r.meta.samplesPerBit, SPB, 2, "UART auto-baud samples/bit");
  near(r.meta.baud, 115200, 6000, "UART auto-baud ~115200");
  // display format: ascii / both
  ok(fmtByte(0x48, "ascii") === "H" && fmtByte(0x00, "ascii") === ".", "fmtByte ascii");
  ok(fmtByte(0x48, "both") === "48·H", "fmtByte both");
  const ra = decodeUART(uartGen([0x48, 0x69, 0x21], SPB), COLT, { fmt: "ascii" });
  ok(ra.text === "H i !", `UART ascii text = "${ra.text}"`);
  const rb = decodeUART(uartGen([0x48, 0x69, 0x21], SPB), COLT, { fmt: "both" });
  ok(rb.text === "48·H 69·i 21·!", `UART both text = "${rb.text}"`);
}

// --- 3. UART framing + parity ------------------------------------------------
{
  // Corrupt the stop bit of the (single) byte -> frame-error.
  const codes = uartGen([0x3C], SPB);
  const stopCol = SPB * 4 + SPB * (1 + 8) + Math.round(SPB / 2); // inside the stop bit
  for (let k = -2; k <= 2; k++) codes[stopCol + k] = LO;         // force stop low
  const r = decodeUART(codes, COLT, { baud: 115200 });
  ok(r.ok && r.spans.length >= 1, "UART framing: still decodes");
  ok(r.spans.some(s => s.kind === "frame-error"), "UART: broken stop bit -> frame-error span");
  ok(r.text.includes("!"), `UART: faulty byte marked with ! ("${r.text}")`);
}

// --- 4. UART auto-baud reject ------------------------------------------------
{
  // A single wide low pulse then idle: no 1-bit reference -> ambiguous OR too few bytes,
  // but crucially must NOT crash and must be honest.
  const codes = new Array(400).fill(HI);
  for (let i = 100; i < 130; i++) codes[i] = LO; // one lone pulse
  const r = decodeUART(codes, COLT, {});
  ok(r.ok === false || r.bytes.length === 0, `UART lone pulse: honest (ok=${r.ok}, err="${r.error}")`);
}

// --- 5. I2C ------------------------------------------------------------------
{
  const ev = [
    { type: "start" },
    { type: "byte", val: (0x50 << 1) | 0 }, { type: "ack" }, // addr 0x50 W + ACK
    { type: "byte", val: 0x00 }, { type: "ack" },
    { type: "byte", val: 0xFF }, { type: "nak" },
    { type: "stop" },
  ];
  const { scl, sda } = i2cGen(ev, SPB);
  const r = decodeI2C(scl, sda, COLT, {});
  ok(r.ok, `I2C: decodes ok (${r.error || ""})`);
  ok(r.text === "START 50 W ACK 00 ACK FF NAK STOP", `I2C text = "${r.text}"`);
  ok(JSON.stringify(r.bytes) === JSON.stringify([0x00, 0xFF]), `I2C data bytes = ${r.bytes.map(b => b.toString(16))}`);
  ok(r.spans[0].kind === "start" && r.spans[r.spans.length - 1].kind === "stop", "I2C: START/STOP spans bracket the txn");
  // low resolution -> honest failure
  const low = i2cGen(ev, 2);
  const rl = decodeI2C(low.scl, low.sda, COLT, {});
  ok(!rl.ok && /cols\/clock/.test(rl.error), `I2C low-res -> ok:false ("${rl.error}")`);
}

// --- 6. SPI, all four modes --------------------------------------------------
{
  const bytes = [0xA5, 0x3C, 0x00, 0xFF];
  for (const cpol of [0, 1]) for (const cpha of [0, 1]) {
    const { clk, data } = spiGen(bytes, SPB, { cpol, cpha });
    const r = decodeSPI(clk, data, COLT, { cpol, cpha });
    ok(r.ok && JSON.stringify(r.bytes) === JSON.stringify(bytes),
      `SPI mode ${cpol}${cpha}: bytes = ${r.bytes.map(b => b.toString(16))}`);
    ok(r.meta.noCS === true, `SPI mode ${cpol}${cpha}: meta.noCS`);
  }
  // LSB order
  const g = spiGen([0x80], SPB, { cpol: 0, cpha: 0, bitOrder: "lsb" });
  const rl = decodeSPI(g.clk, g.data, COLT, { cpol: 0, cpha: 0, bitOrder: "lsb" });
  ok(rl.ok && rl.bytes[0] === 0x80, `SPI LSB order: byte = ${rl.bytes[0]?.toString(16)}`);

  // No-CS re-alignment: a PARTIAL first burst (capture started mid-transaction),
  // an idle gap, then a clean transaction. The gap must re-align byte boundaries
  // so the clean transaction decodes correctly (this is the real-scope case).
  const clean = spiGen(bytes, SPB, { cpol: 0, cpha: 0 });
  const gapCols = new Array(SPB * 20).fill(LO); // clk idle low (cpol0), data low
  const clk2 = clean.clk.concat(gapCols, clean.clk);
  const data2 = clean.data.concat(gapCols, clean.data);
  const cut = Math.round(SPB * 3.5); // slice off ~3.5 bits so the FIRST burst is misaligned
  const rg = decodeSPI(clk2.slice(cut), data2.slice(cut), COLT, { cpol: 0, cpha: 0 });
  const tail = rg.bytes.slice(-4);
  ok(JSON.stringify(tail) === JSON.stringify(bytes),
    `SPI re-aligns after an idle gap: last 4 bytes = ${tail.map(b => b.toString(16))}`);
}

// --- 7. failure + invariants -------------------------------------------------
{
  const flat = new Array(500).fill(128);
  ok(!decodeUART(flat, COLT, { baud: 115200 }).ok || decodeUART(flat, COLT, { baud: 115200 }).bytes.length === 0, "UART flat -> no bytes");
  ok(!decodeI2C(flat, flat, COLT, {}).ok, "I2C flat -> ok:false");
  const r = decodeI2C(...Object.values(i2cGen([{ type: "start" }, { type: "byte", val: 0xA0 }, { type: "ack" }, { type: "stop" }], SPB)), COLT, {});
  ok(r.spans.every(s => s.i0 >= 0 && s.i0 <= s.i1 && s.i1 < r.meta.threshold + 100000), "all spans have 0<=i0<=i1");
  const gaps = new Array(500).fill(-1);
  ok(!sliceChannel(gaps, {}).ok, "all-gap channel -> slice not ok (no crash)");
}

// --- 8. autodetect -----------------------------------------------------------
{
  const mkFrame = (c1, c2) => ({ c1, c2, col_span_s: c1.length * COLT });
  const flat = n => new Array(n).fill(HI); // idle-high inactive line

  const i2c = i2cGen([
    { type: "start" }, { type: "byte", val: (0x50 << 1) }, { type: "ack" },
    { type: "byte", val: 0x00 }, { type: "ack" }, { type: "byte", val: 0xFF }, { type: "nak" }, { type: "stop" },
  ], SPB);
  let d = autodetect(mkFrame(i2c.scl, i2c.sda), {});
  ok(d.proto === "i2c" && d.roles.scl === 1 && d.roles.sda === 2, `autodetect I2C -> ${d.proto} scl=${d.roles.scl} sda=${d.roles.sda}`);
  d = autodetect(mkFrame(i2c.sda, i2c.scl), {}); // swapped channels -> roles flip
  ok(d.proto === "i2c" && d.roles.scl === 2 && d.roles.sda === 1, `autodetect I2C swapped -> scl=${d.roles.scl} sda=${d.roles.sda}`);

  const spi = spiGen([0xA5, 0x3C, 0x5A, 0xC3], SPB, { cpol: 0, cpha: 0 });
  d = autodetect(mkFrame(spi.clk, spi.data), {});
  ok(d.proto === "spi" && d.roles.clk === 1 && d.roles.data === 2, `autodetect SPI -> ${d.proto} clk=${d.roles.clk} data=${d.roles.data}`);

  const u = uartGen([0x48, 0x69, 0x21], SPB);
  d = autodetect(mkFrame(u, flat(u.length)), {});
  ok(d.proto === "uart" && d.roles.line === 1, `autodetect UART -> ${d.proto} line=${d.roles.line}`);
  ok(d.result && d.result.text === "48 69 21", `autodetect UART decoded "${d.result && d.result.text}"`);

  d = autodetect(mkFrame(flat(500), flat(500)), {});
  ok(d.proto === "off", `autodetect flat -> ${d.proto} (${d.reason})`);

  // CPHA disambiguation needs REAL edges: the wrong phase samples ON the data
  // transition. Idealised instant edges sample a clean (shifted) rail either way,
  // so add a linear ramp at each data transition; then the correct phase (samples
  // mid-bit) has a high margin and the wrong phase (samples on the ramp) low.
  const ramp = (arr, w) => {
    const out = arr.slice(), h = Math.floor(w / 2);
    for (let i = 1; i < arr.length; i++) if (arr[i] !== arr[i - 1])
      for (let k = -h; k <= h; k++) { const j = i + k; if (j >= 0 && j < out.length) out[j] = Math.round(arr[i - 1] + (arr[i] - arr[i - 1]) * (k + h) / w); }
    return out;
  };
  for (const cpha of [0, 1]) {
    const g = spiGen([0xA5, 0x3C, 0x99, 0x66], SPB, { cpol: 0, cpha });
    const f = { c1: g.clk, c2: ramp(g.data, Math.round(SPB * 0.6)), col_span_s: g.clk.length * COLT };
    const dd = autodetect(f, {});
    ok(dd.proto === "spi" && dd.cfg.cpha === cpha, `autodetect SPI picks CPHA=${cpha} by sample margin (got proto=${dd.proto} cpha=${dd.cfg.cpha})`);
  }
  // MSB/LSB: a text payload decodes to readable ASCII in the true order and to
  // gibberish in the reversed order -> printable heuristic picks the true order.
  for (const bo of ["msb", "lsb"]) {
    const g = spiGen([0x48, 0x65, 0x6C, 0x6C, 0x6F], SPB, { cpol: 0, cpha: 0, bitOrder: bo }); // "Hello"
    const f = { c1: g.clk, c2: g.data, col_span_s: g.clk.length * COLT };
    const dd = autodetect(f, {});
    ok(dd.proto === "spi" && dd.cfg.msb === (bo === "msb"), `autodetect SPI picks bit order ${bo} by ASCII (got proto=${dd.proto} msb=${dd.cfg.msb})`);
  }
}

// --- fuzz: hostile input must never hang or throw (parity of the Go fuzz) ---
{
  let s = 12345 >>> 0;
  const rnd = () => (s = (s + 0x6d2b79f5) | 0, ((Math.imul(s ^ (s >>> 15), 1 | s) + 0x6d2b79f5) >>> 0) / 4294967296);
  const t0 = Date.now();
  for (let i = 0; i < 2000; i++) {
    const n = Math.floor(rnd() * 3000);
    const a = new Uint8Array(n), b = new Uint8Array(n);
    for (let k = 0; k < n; k++) { a[k] = Math.floor(rnd() * 256); b[k] = Math.floor(rnd() * 256); }
    const ct = [0, 1e-12, 800e-9, 1][Math.floor(rnd() * 4)];
    const fm = ["hex", "ascii", "both", "x"][Math.floor(rnd() * 4)];
    try {
      decodeUART(a, ct, { baud: [0, -9600, 110, 115200][Math.floor(rnd() * 4)], bits: [0, 5, 8, 9, 64][Math.floor(rnd() * 5)], parity: ["none", "even", "odd"][Math.floor(rnd() * 3)], fmt: fm });
      decodeI2C(a, b, ct, { fmt: fm });
      decodeSPI(a, b, ct, { cpol: rnd() < 0.5, cpha: rnd() < 0.5, fmt: fm });
      autodetect({ c1: a, c2: b, col_span_s: n * ct }, {});
    } catch (e) { ok(false, `fuzz iter ${i} threw: ${e.message}`); break; }
    if (Date.now() - t0 > 15000) { ok(false, `fuzz hung after ${i} iters (DoS)`); break; }
  }
  ok(true, `decoder fuzz survived hostile input (${Date.now() - t0}ms)`);
}

console.log(failed ? `\n${failed} FAILED` : "\nALL PASS");
process.exit(failed ? 1 : 0);
