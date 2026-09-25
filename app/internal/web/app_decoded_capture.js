// ENGMODEL-OWNER-UNIT: FU-WEB-APP-SERIALTRIG
"use strict";
const ds = { id: null, abort: null, events: 0, lost: 0, lines: [], words: 0, reading: false, statusBusy: false };
// TRLC-LINKS: REQ-SDS-206
function dsQuery(id) { return `epoch=${id.epoch}&record=${id.record}`; }
// TRLC-LINKS: REQ-SDS-206
function dsMessage(text) { $("dsStatus").textContent = text; }
// TRLC-LINKS: REQ-SDS-206
function dsConfig() {
  const p = stParams();
  if (!p || ![1, 2, 3, 5, 7, 9].includes(p.proto)) throw Error("Hardware streaming is currently available for UART, I²C, SPI, SENT, MIL-STD-1553 and USB.");
  if (!p.haveThr) throw Error("Set a manual decode threshold before streaming.");
  if (p.bytes.length > 4) throw Error("Use a trigger pattern of at most four bytes.");
  const pattern = p.bytes.reduce((v, b) => ((v << 8) | b) >>> 0, 0);
  const cfg = { source: 0, normal: true, pre_words: 262144, post_words: 262144, trigger_level: Math.round(p.threshold), trigger_hysteresis: 4 };
  if (p.proto === 1) {
    if (p.baud <= 0 || p.bits !== 8 || p.parity !== "none") throw Error("UART streaming requires an explicit baud rate and 8N1.");
    cfg.uart = { enabled: true, channel: p.chA, inverted: p.inverted, bit_ticks: Math.round(125e6 / p.baud), pattern, length: p.bytes.length };
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
    if (p.proto === 2) cfg.i2c = { enabled: true, clock_channel: p.chA, inverted: p.inverted, address: p.addr, direction: p.rw, pattern, length: p.bytes.length };
    else {
      const hz = +$("dsClock").value;
      if (!Number.isFinite(hz) || hz <= 0) throw Error("Enter the SPI clock frequency.");
      cfg.spi = { enabled: true, clock_channel: p.chA, inverted: p.inverted, cpol: p.cpol, cpha: p.cpha, msb: p.msb, gap_ticks: Math.round(1.5 * 125e6 / hz), pattern, length: p.bytes.length };
    }
  }
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
    const response = await dsRequest("/api/decoded/start", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(cfg) });
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
  } catch (error) { if (ds.id === id) dsMessage(error.message); }
  finally { ds.statusBusy = false; }
}
$("dsStart").onclick = dsStart; $("dsEnd").onclick = dsEnd; $("dsRaw").onclick = dsDownload;
setInterval(dsPoll, 500);
