// Node tests for sigrok_export.js — CRC32 vectors, stored-ZIP structure, the
// srzip/VCD/WAV encoders against the byte layouts libsigrok's readers parse
// (session_file.c / session_driver.c / input/vcd.c / input/wav.c), and the
// frame→series calibration contract shared with the CSV export. Run by
// sigrok_export_node_test.go.
"use strict";
const {
  sigrokCrc32, sigrokZip, sigrokSeries, sigrokSR, sigrokVCD, sigrokWAV, sigrokLogicBytes,
  sigrokVcdTimescale, sigrokVcdPeriod, SIGROK_CHUNK_SAMPLES,
} = require("./sigrok_export.js");

let fails = 0;
function check(name, ok, detail) {
  console.log((ok ? "ok   " : "FAIL ") + name + (!ok && detail !== undefined ? "  [" + detail + "]" : ""));
  if (!ok) fails++;
}
const bytes = (s) => new TextEncoder().encode(s);
const text = (u8) => new TextDecoder().decode(u8);

// Minimal stored-ZIP reader: walks the EOCD/central directory like a real
// extractor (libzip reads the CD, not the local headers) and cross-checks the
// local header for each entry. Throws on any structural lie.
function unzip(u8) {
  const dv = new DataView(u8.buffer, u8.byteOffset, u8.byteLength);
  const eocd = u8.length - 22; // no comment in our writer
  if (dv.getUint32(eocd, true) !== 0x06054b50) throw new Error("EOCD magic");
  const count = dv.getUint16(eocd + 10, true);
  let p = dv.getUint32(eocd + 16, true);
  if (dv.getUint32(eocd + 12, true) !== eocd - p) throw new Error("CD size mismatch");
  const entries = [];
  for (let i = 0; i < count; i++) {
    if (dv.getUint32(p, true) !== 0x02014b50) throw new Error("CD magic @" + p);
    const method = dv.getUint16(p + 10, true);
    const crc = dv.getUint32(p + 16, true);
    const csize = dv.getUint32(p + 20, true), usize = dv.getUint32(p + 24, true);
    const nlen = dv.getUint16(p + 28, true), xlen = dv.getUint16(p + 30, true), clen = dv.getUint16(p + 32, true);
    const off = dv.getUint32(p + 42, true);
    const name = text(u8.subarray(p + 46, p + 46 + nlen));
    if (method !== 0 || csize !== usize) throw new Error("not stored: " + name);
    // local header must agree
    if (dv.getUint32(off, true) !== 0x04034b50) throw new Error("LFH magic: " + name);
    if (text(u8.subarray(off + 30, off + 30 + dv.getUint16(off + 26, true))) !== name) throw new Error("LFH name: " + name);
    if (dv.getUint32(off + 14, true) !== crc || dv.getUint32(off + 22, true) !== usize) throw new Error("LFH fields: " + name);
    const data = u8.subarray(off + 30 + nlen + dv.getUint16(off + 28, true), off + 30 + nlen + dv.getUint16(off + 28, true) + usize);
    if (sigrokCrc32(data) !== crc) throw new Error("CRC mismatch: " + name);
    entries.push({ name, data });
    p += 46 + nlen + xlen + clen;
  }
  return entries;
}

// ---- CRC32 known-answer vectors (ITU-T V.42 / zlib) ----
{
  check("crc32 empty = 0", sigrokCrc32(bytes("")) === 0);
  check("crc32 '123456789' = 0xCBF43926", sigrokCrc32(bytes("123456789")) === 0xcbf43926);
  check("crc32 'a' = 0xE8B7BE43", sigrokCrc32(bytes("a")) === 0xe8b7be43);
}

// ---- ZIP writer: structure survives an independent CD-driven extraction ----
{
  const z = sigrokZip([
    { name: "version", data: bytes("2") },
    { name: "hello", data: bytes("world") },
  ]);
  const es = unzip(z);
  check("zip has both entries in order", es.length === 2 && es[0].name === "version" && es[1].name === "hello");
  check("zip payloads roundtrip", text(es[0].data) === "2" && text(es[1].data) === "world");
  const empty = unzip(sigrokZip([{ name: "e", data: bytes("") }]));
  check("zip zero-length entry survives", empty.length === 1 && empty[0].data.length === 0);
}

// ---- frame → series calibration (the CSV contract) ----
{
  check("series: null frame -> null", sigrokSeries(null) === null);
  check("series: envelope frame (no c1) -> null", sigrokSeries({ is_env: true, e1min: new Int16Array(8) }) === null);
  check("series: no time base -> null", sigrokSeries({ c1: new Int16Array([1, 2]), col_span_s: 0 }) === null);
  check("series: all-margin frame -> null", sigrokSeries({ c1: new Int16Array([-1, -1]), c2: new Int16Array([-1, -1]), col_span_s: 1e-3 }) === null);

  const f = {
    seq: 42,
    c1: new Int16Array([-1, 128, 130, 130, -1]),
    c2: new Int16Array([-1, 132, -1, 124, -1]),
    vpc1: 0.25, vpc2: 0.5, off1_v: 0, off2_v: 1,
    col_span_s: 5e-6, // 5 pts -> nominal dt 1e-6
  };
  const s = sigrokSeries(f);
  check("series: seq/channels/count", s && s.seq === 42 && s.ch.length === 2 && s.n === 3);
  check("series: head/tail margins trimmed", s && s.ch[0].v.length === 3 && s.ch[0].v[0] === 0);
  check("series: volts = (code-128)*vpc - off", s && s.ch[0].v[1] === 0.5 && s.ch[1].v[0] === 1 && s.ch[1].v[2] === -3);
  check("series: interior gap stays NaN", s && Number.isNaN(s.ch[1].v[1]));
  check("series: dt from col_span_s fallback", s && Math.abs(s.dt - 1e-6) < 1e-18 && s.rateHz === 1000000);

  const s2 = sigrokSeries(Object.assign({}, f, { dt_s: 2e-6 }));
  check("series: dt_s wins over col_span_s (the 1-200 ns/div nominal fix)", s2 && s2.dt === 2e-6 && s2.rateHz === 500000);

  const sr = sigrokSeries({ seq: 1, c1: Float32Array.from([129.5, -1, 130.25]), col_span_s: 3e-9, vpc1: 1 });
  check("series: fractional superres codes + single channel", sr && sr.ch.length === 1 && sr.ch[0].v[0] === 1.5 && sr.ch[0].v[2] === 2.25);
  check("series: c2 of mismatched length dropped", sigrokSeries({ c1: new Int16Array(4), c2: new Int16Array(3), col_span_s: 1e-6 }).ch.length === 1);
}

// ---- srzip ----
{
  const f = {
    seq: 7,
    c1: new Int16Array([128, 129, 130, 131]),
    c2: new Int16Array([120, 121, 122, 123]),
    vpc1: 0.25, vpc2: 0.25, off1_v: 0, off2_v: 0,
    dt_s: 2e-9, col_span_s: 8e-9,
  };
  const es = unzip(sigrokSR(sigrokSeries(f)));
  const names = es.map((e) => e.name);
  check("sr: entry set (logic-1 chunk + shifted analog indices)",
    JSON.stringify(names) === JSON.stringify(["version", "metadata", "logic-1-1", "analog-1-3-1", "analog-1-4-1"]), JSON.stringify(names));
  check("sr: version is the single byte '2'", es[0].data.length === 1 && text(es[0].data) === "2");
  check("sr: metadata exact (counts precede name keys; analog index = probes+K; unitsize last)",
    text(es[1].data) === "[global]\nsigrok version=0.5.2\n\n[device 1]\ncapturefile=logic-1\ntotal probes=2\nsamplerate=500000000\ntotal analog=2\nprobe1=D1\nprobe2=D2\nanalog3=CH1\nanalog4=CH2\nunitsize=1\n",
    JSON.stringify(text(es[1].data)));
  // Logic digitization: CH1 codes 128..131 -> volts 0..0.75, mid-rail 0.375
  // -> bits 0,0,1,1; CH2 codes 120..123 -> bits 0,0,1,1 on bit 1.
  check("sr: logic chunk thresholds each channel at its mid-rail",
    es[2].data.length === 4 && es[2].data[0] === 0 && es[2].data[1] === 0 && es[2].data[2] === 3 && es[2].data[3] === 3,
    JSON.stringify([...es[2].data]));
  const dv = new DataView(es[3].data.buffer, es[3].data.byteOffset, es[3].data.byteLength);
  check("sr: chunk is float32 LE volts", es[3].data.length === 16 && dv.getFloat32(0, true) === 0 && dv.getFloat32(4, true) === 0.25 && dv.getFloat32(12, true) === 0.75);

  // interior gap -> NaN in the payload (the honest empty cell)
  const gap = unzip(sigrokSR(sigrokSeries({ seq: 1, c1: new Int16Array([128, -1, 128]), c2: new Int16Array([128, 128, 128]), col_span_s: 3e-6 })));
  const gdv = new DataView(gap[3].data.buffer, gap[3].data.byteOffset, gap[3].data.byteLength);
  check("sr: interior gap encodes as NaN", Number.isNaN(gdv.getFloat32(4, true)));

  // chunking: cross the 1 Mi-sample flush boundary like libsigrok's writer —
  // with TWO channels, pinning that chunk numbering restarts at 1 per channel
  // (the reader probes analog-1-<k>-1, -2, ... independently per channel).
  const big = {
    seq: 2,
    c1: new Int16Array(SIGROK_CHUNK_SAMPLES + 5).fill(128),
    c2: new Int16Array(SIGROK_CHUNK_SAMPLES + 5).fill(130),
    col_span_s: (SIGROK_CHUNK_SAMPLES + 5) * 1e-8,
  };
  const bes = unzip(sigrokSR(sigrokSeries(big)));
  check("sr: >1Mi samples split into per-channel chunks numbered from 1",
    JSON.stringify(bes.map((e) => e.name)) ===
    JSON.stringify(["version", "metadata", "logic-1-1", "analog-1-3-1", "analog-1-3-2", "analog-1-4-1", "analog-1-4-2"]) &&
    bes[3].data.length === 4 * SIGROK_CHUNK_SAMPLES && bes[4].data.length === 20 &&
    bes[5].data.length === 4 * SIGROK_CHUNK_SAMPLES && bes[6].data.length === 20, JSON.stringify(bes.map((e) => e.name)));
}

// ---- VCD ----
{
  check("vcd: timescale 500 MHz -> 1 GHz", sigrokVcdTimescale(500e6) === 1e9);
  check("vcd: timescale divisible stays at first decade", sigrokVcdTimescale(250e6) === 1e9);
  check("vcd: timescale 1 Hz -> 1 Hz", sigrokVcdTimescale(1) === 1);
  check("vcd: timescale hunts then accepts residual (32 GHz)", sigrokVcdTimescale(32e9) === 1e13);
  check("vcd: period strings", sigrokVcdPeriod(1e9) === "1 ns" && sigrokVcdPeriod(1e11) === "10 ps" && sigrokVcdPeriod(1) === "1 s" && sigrokVcdPeriod(1e13) === "100 fs");

  const f = {
    seq: 3,
    c1: new Int16Array([128, 128, 132, -1, 132, 133]),
    c2: new Int16Array([128, 129, 129, 129, 129, 129]),
    vpc1: 0.25, vpc2: 0.25, off1_v: 0, off2_v: 0,
    dt_s: 2e-9, // 500 MHz -> 2 ticks/sample on the 1 ns scale
  };
  const v = sigrokVCD(sigrokSeries(f));
  const want = [
    "$version open-sds1000cml sigrok export $end",
    "$timescale 1 ns $end",
    "$scope module libsigrok $end",
    "$var real 64 ! CH1 $end",
    '$var real 64 " CH2 $end',
    "$upscope $end",
    "$enddefinitions $end",
    "#0",
    "r0 !",
    'r0 "',
    "#2",
    'r0.25 "',
    "#4",
    "r1 !",
    "#10",
    "r1.25 !",
    "#12",
    "",
  ].join("\n");
  check("vcd: golden (changes only, NaN holds, 2 ticks/sample, final stamp)", v === want, JSON.stringify(v));
}

// ---- VCD decimation cap (the CSV policy: superres fine grids are interpolation) ----
{
  const n = 300000; // > cap -> step 3, 100000 rows
  const v = new Float64Array(n).fill(0.5);
  const big = sigrokVCD({ n, rateHz: 3000000, ch: [{ name: "CH1", v }], seq: 0, dt: 1 / 3000000 });
  const lines = big.split("\n");
  check("vcd: cap comment records decimation", lines.includes("$comment decimated 3x of 300000 points $end"));
  check("vcd: decimated rate keeps the axis true (1 MHz -> 1 us ticks)", lines.includes("$timescale 1 us $end"));
  check("vcd: constant channel emits once", lines.filter((l) => l.startsWith("r")).length === 1);
  check("vcd: final stamp at decimated length", lines[lines.length - 2] === "#100000");
  const small = sigrokVCD({ n: 10, rateHz: 1000, ch: [{ name: "CH1", v: new Float64Array(10) }], seq: 0, dt: 1e-3 });
  check("vcd: no cap comment under the threshold", !small.includes("$comment"));
}

// ---- WAV ----
{
  const f = {
    seq: 9,
    c1: new Int16Array([128, 132, -1]),
    c2: new Int16Array([120, -1, 124]),
    vpc1: 0.25, vpc2: 0.25, off1_v: 0, off2_v: 0,
    dt_s: 1e-6,
  };
  const w = sigrokWAV(sigrokSeries(f));
  const dv = new DataView(w.buffer);
  check("wav: RIFF size + WAVE + fmt at 12", text(w.subarray(0, 4)) === "RIFF" && dv.getUint32(4, true) === w.length - 8 && text(w.subarray(8, 16)) === "WAVEfmt ");
  check("wav: 18-byte fmt, code 3 (IEEE float), 2ch", dv.getUint32(16, true) === 18 && dv.getUint16(20, true) === 3 && dv.getUint16(22, true) === 2);
  check("wav: rate/block align/bits", dv.getUint32(24, true) === 1000000 && dv.getUint16(32, true) === 8 && dv.getUint16(34, true) === 32);
  check("wav: data chunk sized 3 frames", text(w.subarray(38, 42)) === "data" && dv.getUint32(42, true) === 24 && w.length === 70);
  const s = (i) => dv.getFloat32(46 + 4 * i, true);
  check("wav: interleaved ch order", s(0) === 0 && s(1) === -2 && s(2) === 1 && s(3) === -2);
  check("wav: NaN gap holds previous value", s(4) === 1 && s(5) === -1);
  check("wav: >uint32 rate refused (use .sr)", sigrokWAV({ n: 1, rateHz: 32e9, ch: [{ name: "CH1", v: new Float64Array(1) }] }) === null);
}

if (fails) {
  console.log(fails + " FAILURES");
  process.exit(1);
}
console.log("ALL PASS");
