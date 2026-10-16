// ENGMODEL-OWNER-UNIT: FU-WEB-APP-SERIALTRIG
"use strict";
const ds = { id: null, abort: null, events: 0, lost: 0, lines: [], words: 0, reading: false, statusBusy: false };
// TRLC-LINKS: REQ-SDS-206
function dsQuery(id) { return `epoch=${id.epoch}&record=${id.record}`; }
// TRLC-LINKS: REQ-SDS-206
function dsMessage(text) { $("dsStatus").textContent = text; }
// TRLC-LINKS: REQ-SDS-206
// dsSequence mirrors the engine's planSequence: the pattern as event
// elements in the hardware's value layout (ADR-PROTOCOL-SEQUENCE-TRIGGER).
// TRLC-LINKS: REQ-SDS-206
function dsSequence(p) {
  const unit = p.proto === 7 ? 0xffff : p.proto === 8 ? 0x7ffff : p.proto === 5 ? 0xf : p.proto === 4 ? 2 ** (p.bits || 8) - 1 : 0xff;
  const tokens = p.pattern.length ? p.pattern : p.bytes.map(value => ({ kind: 2, value, mask: unit }));
  let dataOnly = true;
  const elements = tokens.map(t => {
    if (t.kind === 4 || t.kind === 1) { dataOnly = false; return { kind: t.kind, value: 0, mask: 0 }; }
    let value = t.value & t.mask, mask = t.mask;
    if (p.proto === 8) { value = (value << 10) >>> 0; mask = (mask << 10) >>> 0; }
    if (p.proto === 2) mask |= 0x100;
    return { kind: 2, value, mask };
  });
  return { length: elements.length, qualify: dataOnly && [4, 5, 6, 8, 9, 10].includes(p.proto), end_bad_value: p.proto === 4 || p.proto === 8, elements };
}
// dsStandardBaud snaps a measured UART rate to a standard one within 3 %.
// TRLC-LINKS: REQ-SDS-206
function dsStandardBaud(b) {
  for (const s of [1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200, 230400, 460800, 921600, 1e6, 2e6, 3e6])
    if (Math.abs(b - s) <= 0.03 * s) return s;
  return Math.round(b);
}
// dsAutoFill takes what the decoder found on auto settings: the threshold it
// sliced at, the bit rate, an SPI clock rate, SENT's tick, so Stream + trigger
// does not refuse the decoder's defaults ("Set a manual decode threshold"); a
// figure the decode did not find stays missing and dsConfig names it. The
// figures are the last decode that read this bus: right after a detection the
// newest frame can still be one the detection's new timebase replaces,
// unread.
// TRLC-LINKS: REQ-SDS-206
function dsAutoFill(p) {
  const g = dcfg.good && dcfg.good.key === decKey() ? dcfg.good : null;
  const r = dcfg.result && dcfg.result.ok ? dcfg.result : g && g.r, m = r && r.ok && r.meta;
  if (!m) return p;
  const q = { ...p };
  const dt = r === dcfg.result ? (frame && frame.c1 ? frameDtS(frame, frame.c1.length) : 0) : g.dt;
  if (!q.haveThr && Number.isFinite(m.threshold)) { q.threshold = Math.round(m.threshold); q.haveThr = true; }
  if (!(q.baud > 0)) { // a UART's baud, a line code's bit rate (CAN's from its samples per bit)
    const rate = m.baud || m.bitrate || (m.samplesPerBit > 0 && dt > 0 ? 1 / (m.samplesPerBit * dt) : 0);
    if (rate > 0) q.baud = q.proto === 1 ? dsStandardBaud(rate) : Math.round(rate);
  }
  if (q.proto === 3 && !(q.spiClockHz > 0) && m.colsPerClock > 0 && dt > 0) q.spiClockHz = Math.round(1 / (m.colsPerClock * dt));
  if (q.proto === 2 && !(q.i2cClockHz > 0) && m.colsPerClock > 0 && dt > 0) q.i2cClockHz = Math.round(1 / (m.colsPerClock * dt));
  if (q.proto === 5 && !(q.tickNs > 0) && m.tickSamples > 0 && dt > 0) q.tickNs = m.tickSamples * dt * 1e9;
  return q;
}
// TRLC-LINKS: REQ-SDS-206
function dsConfig() {
  let p = stParams();
  if (!p || ![1, 2, 3, 4, 5, 6, 7, 8, 9, 10].includes(p.proto)) throw Error("Choose a protocol decoder to stream.");
  p = dsAutoFill(p);
  if (!p.haveThr) throw Error("Set a manual decode threshold before streaming.");
  // Manchester, CAN, ARINC 429 and FlexRay stream from the packet image, which
  // the instrument loads on start; their patterns may exceed 32 bits.
  const packet = [4, 6, 8, 10].includes(p.proto);
  // Beyond the decoder's own matcher, the sequence trigger matches and the
  // decoder runs unfiltered.
  // Only the general image's UART, I²C and SPI match patterns natively; the
  // packet and line images match every pattern with the sequence trigger.
  const native = [1, 2, 3].includes(p.proto) ? 4 : 0;
  const sequence = p.pattern.length || p.bytes.length > native ? dsSequence(p) : null;
  if (sequence) p = { ...p, bytes: [] };
  if (!packet && p.bytes.length > 4) throw Error("Use a trigger pattern of at most four bytes.");
  const pattern = p.bytes.reduce((v, b) => ((v << 8) | b) >>> 0, 0);
  const cfg = { source: 0, normal: true, pre_words: 262144, post_words: 262144, trigger_level: Math.round(p.threshold), trigger_hysteresis: 4 };
  if (sequence) cfg.sequence = sequence;
  if (packet) return dsPacketConfig(p, cfg);
  if (p.proto === 1) {
    if (p.baud <= 0) throw Error("UART streaming requires an explicit baud rate.");
    const bits = p.bits || 8, parity = { none: 0, even: 1, odd: 2 }[p.parity || "none"];
    if (bits === 8 && parity === 0 && dsGeneralUART(p.baud)) {
      cfg.uart = { enabled: true, channel: p.chA, inverted: p.inverted, bit_ticks: Math.round(125e6 / p.baud), pattern, length: p.bytes.length };
    } else {
      // Past the general image's receiver, or in another format: the fast
      // image's receiver on the samples (uart_sample.v), a bit time in
      // sixteenths of a nanosecond, every edge re-timing the bits after it.
      let period16 = Math.round(16e9 / p.baud);
      if (period16 < 256 && 50 * period16 >= 49 * 256) period16 = 256; // up to 2 % past 62.5 MBd: it re-times on every edge
      if (period16 < 256 || period16 > 0xffffff || bits < 5 || bits > 9 || parity === undefined || p.bytes.some(b => b >= 1 << bits))
        throw Error("UART streaming takes 5 to 9 data bits, no, even or odd parity, and 954 Bd to 62.5 MBd.");
      cfg.uart = { enabled: true, channel: p.chA, inverted: p.inverted, bit_ticks: 0, period16, bits, parity, resync: true, pattern, length: p.bytes.length };
    }
  } else if (p.proto === 9) {
    const ticks = Math.round(125e6 * 256 / p.baud);
    if (p.baud <= 0 || ticks < 2048 || ticks > 0xffffff) throw Error("USB streaming requires an explicit bit rate within the hardware range.");
    cfg.usbls = { enabled: true, channel: p.chA, inverted: p.inverted, bit_ticks_q8: ticks, pattern, length: p.bytes.length };
  } else if (p.proto === 7) {
    const ticks = Math.round(125e6 / p.baud);
    if (p.baud <= 0 || ticks < 8 || ticks > 0x3fffff || p.bytes.length > 1) throw Error("MIL-STD-1553 streaming requires an explicit bit rate and at most one 16-bit word.");
    cfg.mil1553 = { enabled: true, channel: p.chA, inverted: p.inverted, bit_ticks: ticks, pattern: p.bytes[0] || 0, match_any: p.bytes.length === 0 };
  } else if (p.proto === 5) {
    const ticks = Math.round(p.tickNs / 8);
    if (!Number.isFinite(ticks) || ticks < 4 || ticks > 0xffffff || p.nibbles < 1 || p.nibbles > 64) throw Error("Enter a SENT tick of at least 32 ns and 1–64 nibbles.");
    cfg.sent = { enabled: true, channel: p.chA, inverted: p.inverted, tick_ticks: ticks, nibbles: p.nibbles, pattern, length: p.bytes.length };
  } else {
    if (p.chA === p.chB) throw Error("Clock and data must use different channels.");
    if (p.proto === 2) {
      cfg.i2c = { enabled: true, clock_channel: p.chA, inverted: p.inverted, address: p.addr, direction: p.rw, pattern, length: p.bytes.length };
      // High-speed mode, past the general image's receiver on 125 MHz line
      // levels: the fast image's on the samples, SDA read at SCL's crossing
      // plus the channels' phase (C2 23/16 ns after C1).
      if (p.i2cClockHz > 1.5e6) Object.assign(cfg.i2c, { samples: true, delay: p.chA === 1 ? 23 : -23 });
    }
    else {
      const hz = p.spiClockHz; // the scope's Clock rate row, or on Auto the rate the decode measured
      if (!Number.isFinite(hz) || hz <= 0) throw Error("Enter the SPI clock frequency.");
      // A gap of 1.5 clock periods; under nine receiver clocks (a clock past
      // about 20 MHz) the fast image's receiver on the samples (spi_sample.v)
      // takes it, with a gap at least two clocks past one period and the data
      // line at the clock's crossing, C2 sampling 23/16 ns after C1.
      const period = 125e6 / hz, gap = Math.round(1.5 * period);
      cfg.spi = { enabled: true, clock_channel: p.chA, inverted: p.inverted, cpol: p.cpol, cpha: p.cpha, msb: p.msb,
        gap_ticks: gap, pattern, length: p.bytes.length };
      if (gap < 9) Object.assign(cfg.spi, { gap_ticks: Math.max(gap, Math.ceil(period) + 2), delay: p.chA === 1 ? 23 : -23 });
    }
  }
  return cfg;
}
// dsGeneralUART mirrors the engine's generalUARTReach: the general image's
// receiver counts whole 125 MHz ticks a bit and never re-times, so the bit's
// rounding and a transmitter's 2 % over a 10-bit frame, and a tick each for
// the start edge and the slicing, must stay inside half a bit (8N1 to 5 MBd).
// TRLC-LINKS: REQ-SDS-206, REQ-SDS-229
function dsGeneralUART(baud) {
  const bit = 125e6 / baud, ticks = Math.round(bit);
  return ticks >= 4 && ticks <= 0xffffff && 9.5 * (Math.abs(ticks - bit) + 0.02 * bit) + 2 <= bit / 2;
}
// dsPacketConfig builds the packet image's Manchester or packet block. The
// 64-bit pattern is a BigInt, written into the JSON text as a plain integer.
// TRLC-LINKS: REQ-SDS-206
function dsPacketConfig(p, cfg) {
  if (p.baud <= 0) throw Error("Streaming requires an explicit bit rate.");
  const ch = p.chA;
  let pattern = 0n;
  if (p.proto === 4) {
    const bits = p.bits || 8, ticks = Math.round(125e6 / p.baud);
    if (ticks < 8 || ticks > 0x3fffff || p.bytes.length > 4) throw Error("Manchester streaming needs a bit rate up to 15.6 Mbit/s and at most four words.");
    for (const w of p.bytes) pattern = (pattern << 16n) | BigInt(w);
    cfg.manchester = { enabled: true, channel: ch, inverted: false, ieee: p.ieee, msb: p.msb, bits, bit_ticks: ticks, pattern: "@P", length: p.bytes.length };
  } else if (p.proto === 8) {
    const ticks = Math.round(125e6 / p.baud);
    if (ticks < 4 || ticks > 0xffffff || p.bytes.length > 1) throw Error("ARINC 429 streaming matches at most one data field.");
    if (p.bytes.length) pattern = (0x7ffffn << 42n) | (BigInt(p.bytes[0]) << 10n);
    // The instrument supplies the slicer levels from the displayed signal.
    cfg.packet = { enabled: true, protocol: 8, channel: ch, ticks, aux: 0, pattern: "@P", length: 0 };
  } else {
    const ticks = Math.round(125e6 * 256 / p.baud);
    if (ticks < (p.proto === 6 ? 2048 : 1024) || ticks > 0xffffff || p.bytes.length > 8) throw Error("CAN and FlexRay streaming need a bit rate within the hardware range and at most eight bytes.");
    for (const b of p.bytes) pattern = (pattern << 8n) | BigInt(b);
    cfg.packet = { enabled: true, protocol: p.proto, channel: ch, ticks, aux: p.proto === 6 ? (p.dataBaud > 0 ? Math.round(125e6 * 256 / p.dataBaud) : ticks) : 0, pattern: "@P", length: p.bytes.length };
  }
  cfg.json = JSON.stringify(cfg).replace('"@P"', pattern.toString());
  return cfg;
}
// TRLC-LINKS: REQ-SDS-206
async function dsRequest(url, options) {
  const response = await fetch(url, options);
  if (!response.ok) throw Error(await response.text());
  return response;
}
// TRLC-LINKS: REQ-SDS-206
async function dsRead(id, controller) {
  try {
    const response = await dsRequest(`/api/decoded/events.bin?${dsQuery(id)}`, { signal: controller.signal });
    const reader = response.body.getReader(); let pending = new Uint8Array(0), sequence = 0;
    while (ds.id === id) {
      const { value, done } = await reader.read();
      // Fetch does not expose HTTP trailers consistently. An unsolicited EOF
      // is always incomplete here; only an explicit session end is clean.
      if (done) throw Error("Decoded stream ended; transcript is incomplete.");
      const bytes = new Uint8Array(pending.length + value.length); bytes.set(pending); bytes.set(value, pending.length);
      let offset = 0;
      for (; offset + 32 <= bytes.length; offset += 32) {
        const v = new DataView(bytes.buffer, offset, 32), kind = v.getUint8(1);
        if (v.getUint8(0) !== 1 || v.getUint32(4, true) !== id.epoch || v.getUint32(8, true) !== sequence) throw Error("Decoded sequence gap; transcript is incomplete.");
        sequence = (sequence + 1) >>> 0; ds.events++;
        if (kind === 5) { const count = v.getUint32(28, true); ds.lost = count === 0 ? Infinity : ds.lost + count; }
        const names = ["", "START", "DATA", "END", "ERROR", "LOSS", "TRIGGER", "RETAINED"];
        ds.lines.push(`${names[kind] || "UNKNOWN"} ${v.getUint32(24, true).toString(16).toUpperCase()}${kind === 5 ? ` (${v.getUint32(28, true) || "unknown"} lost)` : ""}`);
      }
      pending = bytes.slice(offset); ds.lines = ds.lines.slice(-24);
      $("dsEvents").textContent = ds.lines.join("\n");
      $("dsCount").textContent = `${ds.events} events · ${ds.lost} lost`;
    }
  } catch (error) { if (!controller.signal.aborted && ds.id === id) dsMessage(error.message); }
  finally { if (ds.id === id) ds.reading = false; }
}
// TRLC-LINKS: REQ-SDS-206
async function dsStart() {
  $("dsStart").disabled = true;
  try {
    const cfg = dsConfig();
    const response = await dsRequest("/api/decoded/start", { method: "POST", headers: { "Content-Type": "application/json" }, body: cfg.json || JSON.stringify(cfg) });
    const id = await response.json(); ds.id = id; ds.abort = new AbortController(); ds.events = 0; ds.lost = 0; ds.lines = []; ds.words = 0; ds.reading = true;
    $("dsEnd").disabled = false; $("dsRaw").disabled = true; $("dsEvents").textContent = "";
    dsMessage("Streaming decoded events. Waiting for the trigger."); void dsRead(id, ds.abort);
  } catch (error) { dsMessage(error.message); $("dsStart").disabled = false; }
}
// TRLC-LINKS: REQ-SDS-206
async function dsEnd() {
  const id = ds.id; if (!id) return;
  try {
    await dsRequest(`/api/decoded/end?${dsQuery(id)}`, { method: "POST" });
    ds.abort.abort(); ds.id = null; ds.reading = false; $("dsEnd").disabled = true; $("dsRaw").disabled = true; $("dsStart").disabled = false;
    dsMessage("Session ended. Press Run to resume waveform acquisition.");
  } catch (error) { dsMessage(error.message); }
}
// TRLC-LINKS: REQ-SDS-206
async function dsDownload() {
  const id = ds.id; if (!id) return;
  $("dsRaw").disabled = true;
  try {
    const response = await dsRequest(`/api/decoded/record.bin?${dsQuery(id)}&offset=0&words=${ds.words}`);
    const blob = await response.blob(), url = URL.createObjectURL(blob), a = document.createElement("a");
    a.href = url; a.download = `scope-epoch-${id.epoch}-record-${id.record}.bin`; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch (error) { dsMessage(error.message); }
  finally { if (ds.id === id && ds.words) $("dsRaw").disabled = false; }
}
// dsGone resets the page when its session was ended elsewhere: Run, the
// scope's keys, or no client for two minutes.
// TRLC-LINKS: REQ-SDS-206
function dsGone() {
  if (ds.abort) ds.abort.abort();
  ds.id = null; ds.reading = false; $("dsEnd").disabled = true; $("dsRaw").disabled = true; $("dsStart").disabled = false;
  dsMessage("The session ended: Run, the scope's keys or two minutes without this page took the acquisition back.");
}
// TRLC-LINKS: REQ-SDS-206
async function dsPoll() {
  const id = ds.id; if (!id || ds.statusBusy) return; ds.statusBusy = true;
  try {
    const response = await dsRequest(`/api/decoded/status?${dsQuery(id)}`), m = await response.json();
    if (ds.id !== id) return;
    if (m.data_fault) { dsMessage("Acquisition data fault; raw capture is invalid."); $("dsRaw").disabled = true; return; }
    if (m.frozen && m.ready) {
      ds.words = m.words; $("dsRaw").disabled = false;
      if (ds.reading) dsMessage(`${m.triggered ? "Triggered" : "Stopped"}: ${m.words * m.samples_per_word} samples/channel retained. Download before ending the session.`);
    }
  } catch (error) { if (ds.id === id) { if (/no longer active/.test(error.message)) dsGone(); else dsMessage(error.message); } }
  finally { ds.statusBusy = false; }
}
$("dsStart").onclick = dsStart; $("dsEnd").onclick = dsEnd; $("dsRaw").onclick = dsDownload;
setInterval(dsPoll, 500);
