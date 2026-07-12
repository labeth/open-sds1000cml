// sigrok_export.js — pure encoders for sigrok-compatible waveform export:
// srzip (.sr, the PulseView / sigrok-cli session format), VCD, and float32
// WAV. Classic script, zero deps; node-testable (sigrok_export.test.cjs).
//
// Format ground truth is libsigrok's own reader code, not the wiki:
// session_file.c + session_driver.c (.sr), input/vcd.c, input/wav.c. The
// reader facts these encoders are built to:
//  - .sr is a plain ZIP read BY NAME (entry order free; stored entries are
//    fine). "version" holds the single byte "2". In metadata, `total analog`
//    MUST precede the analogK name keys (the loader creates channels at
//    `total analog` and fails on analogK for a channel it hasn't created);
//    samplerate is parsed as integer Hz (SI suffixes optional). Analog chunk
//    entries are named analog-1-<k>-<chunk> with <chunk> starting at 1 and
//    consecutive — the unchunked analog-1-<k> form silently drops every
//    channel after the first, so it is never written here. Chunk payload is
//    raw float32 volts, little-endian, ≤4 MiB per chunk (libsigrok's own
//    flush size). Analog .sr needs libsigrok ≥ 0.5.0 (2017).
//  - VCD analog = `$var real 64 <id> <name>` declarations + `r<value> <id>`
//    value changes; a $scope named exactly "libsigrok" is stripped on
//    re-import so channels come back as plain CH1/CH2. Analog VCD *import*
//    needs libsigrok git newer than 0.5.2 (.sr and WAV import everywhere).
//  - WAV must be format 3 (IEEE float32, 32-bit only); the sample rate is a
//    uint32 integer Hz, so fine-grid rates above 4.29 GHz cannot be
//    represented (sigrokWAV returns null; use .sr for those).
"use strict";

const SIGROK_CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

function sigrokCrc32(u8) {
  let c = 0xffffffff;
  for (let i = 0; i < u8.length; i++) c = SIGROK_CRC_TABLE[(c ^ u8[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

// sigrokZip assembles entries [{name, data:Uint8Array}] into a stored
// (method 0, no compression) ZIP. Sizes are known up front so no data
// descriptors; a fixed 1980-01-01 DOS timestamp keeps the output
// deterministic (nothing reads it). Entry names are ASCII by construction.
function sigrokZip(entries) {
  const enc = new TextEncoder();
  const es = entries.map((e) => ({ name: enc.encode(e.name), data: e.data, crc: sigrokCrc32(e.data), off: 0 }));
  let total = 22;
  for (const e of es) total += 30 + 46 + 2 * e.name.length + e.data.length;
  const out = new Uint8Array(total);
  const dv = new DataView(out.buffer);
  let p = 0;
  for (const e of es) {
    e.off = p;
    dv.setUint32(p, 0x04034b50, true); // local file header
    dv.setUint16(p + 4, 20, true); // version needed to extract
    dv.setUint16(p + 12, 0x21, true); // DOS date 1980-01-01 (time stays 0)
    dv.setUint32(p + 14, e.crc, true);
    dv.setUint32(p + 18, e.data.length, true);
    dv.setUint32(p + 22, e.data.length, true);
    dv.setUint16(p + 26, e.name.length, true);
    out.set(e.name, p + 30);
    out.set(e.data, p + 30 + e.name.length);
    p += 30 + e.name.length + e.data.length;
  }
  const cd = p;
  for (const e of es) {
    dv.setUint32(p, 0x02014b50, true); // central directory entry
    dv.setUint16(p + 4, 20, true); // version made by
    dv.setUint16(p + 6, 20, true); // version needed
    dv.setUint16(p + 14, 0x21, true);
    dv.setUint32(p + 16, e.crc, true);
    dv.setUint32(p + 20, e.data.length, true);
    dv.setUint32(p + 24, e.data.length, true);
    dv.setUint16(p + 28, e.name.length, true);
    dv.setUint32(p + 42, e.off, true);
    out.set(e.name, p + 46);
    p += 46 + e.name.length;
  }
  dv.setUint32(p, 0x06054b50, true); // end of central directory
  dv.setUint16(p + 8, es.length, true);
  dv.setUint16(p + 10, es.length, true);
  dv.setUint32(p + 12, p - cd, true);
  dv.setUint32(p + 16, cd, true);
  return out;
}

// sigrokSeries extracts the calibrated series an export runs on — the SAME
// contract as the CSV export: the current-view frame, volts =
// (code−128)·vpc − off (vpc/off already include probe attenuation and
// coupling — never re-apply either), code < 0 = no sample → NaN. dt prefers
// the server's dt_s (the TRUE capture pitch; col_span_s is a display nominal
// on the 1–200 ns/div bands) and falls back to col_span_s/N for
// client-synthesized frames (superres/ETS view, mask replay) and captures
// saved before dt_s existed. Contiguous all-NaN head/tail margins are
// trimmed; interior gaps (unfilled superres bins) stay NaN and each encoder
// documents its own gap policy. Returns null when there is nothing to
// export (no frame, envelope/roll frame, no time base, all margin).
function sigrokSeries(frame) {
  if (!frame || !frame.c1 || !frame.c1.length) return null;
  const n = frame.c1.length;
  const dt = frame.dt_s > 0 ? frame.dt_s : (frame.col_span_s || 0) / n;
  if (!(dt > 0 && isFinite(dt))) return null;
  const chans = [["CH1", frame.c1, frame.vpc1 || 1 / 25, frame.off1_v || 0]];
  if (frame.c2 && frame.c2.length === n) chans.push(["CH2", frame.c2, frame.vpc2 || 1 / 25, frame.off2_v || 0]);
  const ch = chans.map(([name, codes, vpc, off]) => {
    const v = new Float64Array(n);
    for (let i = 0; i < n; i++) v[i] = codes[i] < 0 ? NaN : (codes[i] - 128) * vpc - off;
    return { name, v };
  });
  const gap = (i) => ch.every((c) => Number.isNaN(c.v[i]));
  let lo = 0, hi = n;
  while (lo < hi && gap(lo)) lo++;
  while (hi > lo && gap(hi - 1)) hi--;
  if (lo >= hi) return null;
  if (lo > 0 || hi < n) for (const c of ch) c.v = c.v.subarray(lo, hi);
  return { n: hi - lo, dt, rateHz: Math.max(1, Math.round(1 / dt)), ch, seq: frame.seq || 0 };
}

const SIGROK_CHUNK_SAMPLES = 1 << 20; // 4 MiB of float32 — libsigrok's flush size

// sigrokSR encodes a series as a .sr (srzip v2) archive. Gaps are written as
// float32 NaN — the honest equivalent of the CSV's empty cell.
function sigrokSR(series) {
  const enc = new TextEncoder();
  let meta = "[global]\nsigrok version=0.5.2\n\n[device 1]\nsamplerate=" + series.rateHz + "\ntotal analog=" + series.ch.length + "\n";
  series.ch.forEach((c, i) => (meta += "analog" + (i + 1) + "=" + c.name + "\n"));
  const entries = [
    { name: "version", data: enc.encode("2") },
    { name: "metadata", data: enc.encode(meta) },
  ];
  series.ch.forEach((c, i) => {
    for (let s = 0, chunk = 1; s < c.v.length; s += SIGROK_CHUNK_SAMPLES, chunk++) {
      const m = Math.min(SIGROK_CHUNK_SAMPLES, c.v.length - s);
      const data = new Uint8Array(4 * m);
      const dv = new DataView(data.buffer);
      for (let j = 0; j < m; j++) dv.setFloat32(4 * j, c.v[s + j], true);
      entries.push({ name: "analog-1-" + (i + 1) + "-" + chunk, data });
    }
  });
  return sigrokZip(entries);
}

// sigrokVcdTimescale mirrors libsigrok output/vcd.c get_timescale_freq(): the
// smallest power of 10 ≥ rate, bumped by up to two more decades hunting for
// exact divisibility (a residual remainder is accepted, as sigrok accepts it).
function sigrokVcdTimescale(rateHz) {
  let ts = 1;
  while (ts < rateHz && ts < 1e15) ts *= 10;
  for (let extra = 0; extra < 2 && ts % rateHz !== 0 && ts < 1e15; extra++) ts *= 10;
  return ts;
}

// sigrokVcdPeriod renders a power-of-10 timescale frequency as the VCD
// `1|10|100 s..fs` period string (10^k always maps onto a legal pair).
function sigrokVcdPeriod(tsHz) {
  const exp = Math.round(Math.log10(tsHz));
  const grp = Math.ceil(exp / 3);
  return Math.pow(10, grp * 3 - exp) + " " + ["s", "ms", "us", "ns", "ps", "fs"][grp];
}

const SIGROK_VCD_CAP = 131072; // points before stride-decimation — the CSV export's cap

// sigrokVCD encodes a series as a Value Change Dump: values are emitted only
// when they change; NaN gaps emit nothing (the previous value holds, which is
// what value-change semantics mean); the final bare timestamp marks the
// capture length. No $date header — output is deterministic for a given series.
// Huge series are stride-decimated like the CSV export: a superres stack's K×
// fine grid is interpolation, and >1M per-point text changes block the UI for
// seconds (and load just as badly in GTKWave) for sub-sample rows that carry
// no new data. The decimated rate keeps the time axis true and a $comment
// records what was dropped; .sr and WAV are binary and stay verbatim.
function sigrokVCD(series) {
  const step = series.n > SIGROK_VCD_CAP ? Math.ceil(series.n / SIGROK_VCD_CAP) : 1;
  const rate = series.rateHz / step;
  const rows = Math.ceil(series.n / step);
  const ts = sigrokVcdTimescale(rate);
  const id = (i) => String.fromCharCode(33 + i); // '!' onward, like sigrok
  const lines = ["$version open-sds1000cml sigrok export $end"];
  if (step > 1) lines.push("$comment decimated " + step + "x of " + series.n + " points $end");
  lines.push("$timescale " + sigrokVcdPeriod(ts) + " $end", "$scope module libsigrok $end");
  series.ch.forEach((c, i) => lines.push("$var real 64 " + id(i) + " " + c.name + " $end"));
  lines.push("$upscope $end", "$enddefinitions $end");
  const last = series.ch.map(() => NaN);
  for (let i = 0, row = 0; i < series.n; i += step, row++) {
    let stamped = false;
    for (let k = 0; k < series.ch.length; k++) {
      const v = series.ch[k].v[i];
      if (Number.isNaN(v) || v === last[k]) continue;
      if (!stamped) {
        lines.push("#" + Math.round((row * ts) / rate));
        stamped = true;
      }
      lines.push("r" + v + " " + id(k));
      last[k] = v;
    }
  }
  lines.push("#" + Math.round((rows * ts) / rate), "");
  return lines.join("\n");
}

// sigrokWAV encodes a series as an N-channel IEEE-float32 WAV (18-byte fmt
// chunk, format code 3 — what sigrok's own wav output writes). Volts go in
// verbatim, no ±1 normalization. NaN gaps hold the previous value (0 before
// the first sample) — WAV has no way to say "no sample". Returns null when
// the rate does not fit the format's uint32 Hz field (fine-grid superres).
function sigrokWAV(series) {
  const rate = series.rateHz;
  if (!(rate >= 1) || rate > 0xffffffff) return null;
  const nch = series.ch.length;
  const dataBytes = 4 * nch * series.n;
  const out = new Uint8Array(46 + dataBytes);
  const dv = new DataView(out.buffer);
  const tag = (p, s) => { for (let i = 0; i < 4; i++) out[p + i] = s.charCodeAt(i); };
  tag(0, "RIFF");
  dv.setUint32(4, 38 + dataBytes, true);
  tag(8, "WAVE");
  tag(12, "fmt ");
  dv.setUint32(16, 18, true);
  dv.setUint16(20, 3, true); // WAVE_FORMAT_IEEE_FLOAT
  dv.setUint16(22, nch, true);
  dv.setUint32(24, rate, true);
  dv.setUint32(28, Math.min(rate * nch * 4, 0xffffffff), true); // byte rate (advisory)
  dv.setUint16(32, nch * 4, true); // block align — what sigrok's reader divides
  dv.setUint16(34, 32, true);
  dv.setUint16(36, 0, true); // cbSize
  tag(38, "data");
  dv.setUint32(42, dataBytes, true);
  const last = new Float64Array(nch);
  let p = 46;
  for (let i = 0; i < series.n; i++)
    for (let k = 0; k < nch; k++, p += 4) {
      const v = series.ch[k].v[i];
      if (!Number.isNaN(v)) last[k] = v;
      dv.setFloat32(p, last[k], true);
    }
  return out;
}

if (typeof module !== "undefined")
  module.exports = { sigrokCrc32, sigrokZip, sigrokSeries, sigrokSR, sigrokVCD, sigrokWAV, sigrokVcdTimescale, sigrokVcdPeriod, SIGROK_CHUNK_SAMPLES };
