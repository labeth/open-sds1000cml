// ENGMODEL-OWNER-UNIT: FU-WEB-APP-DECODE
// app_decode.js — protocol-decode UI (classic script; shares app.js globals).

// ---- protocol decode: compute + render ----
"use strict";
// TRLC-LINKS: REQ-SDS-207
function decCodes(role) {
  const c = role === 2 ? (frame && frame.c2) : (frame && frame.c1);
  return c;
}

// renewDecodeLease keeps the engine sending samples, not peak-detect pairs,
// while this browser decodes (the lease lapses a few seconds after it stops).
// At 100 us/div a peak-detect bucket is wider than an SPI clock, and auto-detect
// names it Manchester or UART.
// TRLC-LINKS: REQ-SDS-207
let decodeLeaseT = 0;
// TRLC-LINKS: REQ-SDS-207
function renewDecodeLease() {
  const now = performance.now();
  if (now - decodeLeaseT < 1000) return;
  decodeLeaseT = now;
  send("decodeview", 3);
}

// A held trigger or image switch can outlast a frame-driven lease. Keep the
// request alive while the visible browser is decoding, even without new frames.
setInterval(() => {
  if (dcfg.proto !== "off" && !document.hidden && !superseded) renewDecodeLease();
}, 1000);

// decodeFeatureSamples is the shortest feature of a decode's bus in samples
// (decode.FeatureSamples): a bit of an NRZ line, half a clock (I2C, SPI),
// half a bit of a line code (Manchester, MIL-1553, ARINC 429), SENT's
// shortest pulse of four ticks; 0 when the result names none.
// TRLC-LINKS: REQ-SDS-207
function decodeFeatureSamples(r) {
  const m = (r && r.ok && r.meta) || {};
  if (m.colsPerClock > 0) return m.colsPerClock / 2;
  if (m.tickSamples > 0) return 4 * m.tickSamples;
  if (!(m.samplesPerBit > 0)) return 0;
  return ["manchester", "mil1553", "arinc429"].includes(r.proto) ? m.samplesPerBit / 2 : m.samplesPerBit;
}
// A live envelope holds a (min, max) pair per bucket, two points: a bus whose
// shortest feature is not well over a bucket aliases there (a fast I2C clock
// read as starts and stops, no bytes; a 115200 UART at 500 us/div's 3.6
// points a bit read 0A as 02), so it is not decoded from it
// (framecalc.MinEnvelopeFeature).
const DECODE_MIN_ENVELOPE_FEATURE = 4;
const DECODE_NAMES = { uart: "UART", i2c: "I2C", spi: "SPI", manchester: "Manchester", sent: "SENT", canfd: "CAN", mil1553: "MIL-1553", arinc429: "ARINC 429", usbls: "USB LS", flexray: "FlexRay" };

// decKey names the bus a decode reads: protocol, lines, polarity.
// TRLC-LINKS: REQ-SDS-207
function decKey() { return [dcfg.proto, dcfg.line, dcfg.scl, dcfg.sda, dcfg.clk, dcfg.data, !!dcfg.inverted].join(","); }

// TRLC-LINKS: REQ-SDS-207
function computeDecode() {
  dcfg.result = null;
  if (dcfg.proto !== "off") renewDecodeLease();
  if (dcfg.proto === "off" || !frame || frame.is_env || (frame.peak_detect && !frame.ordered) || !frame.c1) { updateDecodeResults(); return; }
  const colTimeS = frameDtS(frame, frame.c1.length); // dt_s-aware: true baud on the 1-200 ns/div nominal bands
  const cfg = { inverted: !!dcfg.inverted, threshold: dcfg.auto ? null : +$("decThr").value, guard: 4, fmt: dcfg.fmt };
  let r = null;
  if (dcfg.proto === "uart")
    r = decodeUART(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { inverted: !!dcfg.inverted, baud: dcfg.baud > 0 ? dcfg.baud : null, bits: dcfg.bits, parity: dcfg.parity }));
  else if (dcfg.proto === "i2c")
    r = decodeI2C(decCodes(dcfg.scl), decCodes(dcfg.sda), colTimeS, cfg);
  else if (dcfg.proto === "spi")
    r = decodeSPI(decCodes(dcfg.clk), decCodes(dcfg.data), colTimeS, Object.assign(cfg, { cpol: dcfg.cpol, cpha: dcfg.cpha, bitOrder: dcfg.msb ? "msb" : "lsb" }));
  else if (dcfg.proto === "manchester") // single line, self-clocking (auto bit rate)
    r = decodeManchester(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { bitrate: dcfg.baud > 0 ? dcfg.baud : 0, ieee: true, msb: dcfg.msb, bits: dcfg.bits || 8, haveThr: !dcfg.auto }));
  else if (dcfg.proto === "sent") // single line, tick auto-derived from the 56-tick sync
    r = decodeSENT(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { tickNs: +$("decSentTick").value, nibbles: +$("decSentNibbles").value, pausePulse: false, haveThr: !dcfg.auto }));
  else if (dcfg.proto === "can") // single line (dominant = low), nominal baud auto or from decBaud
    r = decodeCANFD(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { nominalBaud: dcfg.baud > 0 ? dcfg.baud : 0, dataBaud: 0, dominantLow: true, haveThr: !dcfg.auto }));
  else if (dcfg.proto === "mil1553") // single line, 1Mbit Manchester + 3-bit sync
    r = decodeMIL1553(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { bitrate: dcfg.baud > 0 ? dcfg.baud : 0, haveThr: !dcfg.auto }));
  else if (dcfg.proto === "arinc429") // single line, bipolar tri-level RZ
    r = decodeARINC429(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { bitrate: dcfg.baud > 0 ? dcfg.baud : 0, haveThr: !dcfg.auto }));
  else if (dcfg.proto === "usb") // single line = D+ single-ended, NRZI
    r = decodeUSBLS(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { bitrate: dcfg.baud > 0 ? dcfg.baud : 0, haveThr: !dcfg.auto }));
  else if (dcfg.proto === "flexray") // single line, BSS-framed bytes
    r = decodeFlexRay(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { bitrate: dcfg.baud > 0 ? dcfg.baud : 0, haveThr: !dcfg.auto }));
  if (r && r.ok && frame.peak_detect && frame.ordered) {
    const fs = decodeFeatureSamples(r);
    if (fs > 0 && fs < DECODE_MIN_ENVELOPE_FEATURE)
      r = { ok: false, proto: r.proto, error: (DECODE_NAMES[r.proto] || r.proto) + " not resolved at this timebase - faster time/div, or SINGLE", spans: [], text: "", bytes: [], meta: {} };
  }
  dcfg.result = r;
  if (r && r.ok) dcfg.good = { key: decKey(), r, dt: colTimeS };
  if (r && r.ok && dcfg.auto && r.meta && r.meta.threshold != null) {
    $("decThr").value = Math.round(r.meta.threshold); $("decThrV").textContent = Math.round(r.meta.threshold);
  }
  // STREAM history: each new stitch window (stream_seq advances) appends its
  // transcript to a scrolling packet log, so you accumulate everything the bus
  // shows across back-to-back captures — the "complete picture" over time.
  if (dcfg.stream && frame && frame.stream_seq && frame.stream_seq !== dcfg.lastStreamSeq) {
    dcfg.lastStreamSeq = frame.stream_seq;
    if (r && r.ok && r.text) {
      dcfg.hist.push("#" + frame.stream_seq + (frame.gap_ns ? " (gap " + (frame.gap_ns / 1e6).toFixed(1) + "ms)" : "") + ": " + r.text);
      if (dcfg.hist.length > 200) dcfg.hist.shift();
    }
  }
  // WATCH: save any window matching the rule to the review buffer (not while
  // reviewing a frozen capture). Dedup consecutive identical hits.
  if (dcfg.watch && dcfg.reviewIdx < 0 && r && r.ok) {
    const reason = watchReason(r);
    const key = reason + "|" + r.text; // dedup consecutive identical matches
    if (reason && key !== dcfg.lastCapKey) {
      dcfg.lastCapKey = key;
      dcfg.captures.push({ seq: frame.stream_seq || frame.seq || 0, reason, text: r.text, snap: frame, t: Date.now() });
      if (dcfg.captures.length > 80) dcfg.captures.shift();
      updateCaptureList();
    }
  }
  updateDecodeResults();
}

// watchReason returns why a decode matches the watch rule ("" = no match): an
// error kind (frame-error/parity-error, or NAK) and/or a transcript match of the
// user string ("/re/" = regex, else case-insensitive substring).
// TRLC-LINKS: REQ-SDS-207
function watchReason(r) {
  let reason = "";
  if (dcfg.watchErr && r.spans.some(s => s.kind === "frame-error" || s.kind === "parity-error" || s.kind === "nak")) reason = "error";
  const m = (dcfg.watchMatch || "").trim();
  if (m) {
    let hit = false;
    if (m.length > 2 && m[0] === "/" && m[m.length - 1] === "/") { try { hit = new RegExp(m.slice(1, -1), "i").test(r.text); } catch (e) {} }
    else hit = r.text.toLowerCase().includes(m.toLowerCase());
    if (hit) reason = reason ? reason + "+" + m : "“" + m + "”";
  }
  return reason;
}

// TRLC-LINKS: REQ-SDS-207
function updateCaptureList() {
  const card = $("captureCard");
  if (dcfg.proto === "off" || (!dcfg.watch && !dcfg.captures.length)) { card.style.display = "none"; return; }
  card.style.display = "";
  $("captureCount").textContent = dcfg.captures.length + (dcfg.reviewIdx >= 0 ? " · reviewing #" + (dcfg.reviewIdx + 1) : (dcfg.watch ? " · watching…" : ""));
  let html = "";
  for (let i = dcfg.captures.length - 1; i >= 0; i--) {
    const c = dcfg.captures[i], sel = i === dcfg.reviewIdx, err = c.reason.startsWith("error");
    html += `<div class="cap" data-i="${i}" style="cursor:pointer;padding:2px 4px;border-radius:3px;${sel ? "background:rgba(255,210,63,.18)" : ""}">` +
      `<span style="color:var(--dim)">${new Date(c.t).toLocaleTimeString()} </span>` +
      `<span style="color:${err ? "#e8604c" : "#35c8e8"}">${c.reason}</span> ` +
      `<span style="color:var(--text)">${c.text.slice(0, 46)}${c.text.length > 46 ? "…" : ""}</span></div>`;
  }
  if (!dcfg.captures.length) html = `<div style="color:var(--dim);padding:2px 4px">${dcfg.watch ? "watching — no matches yet" : "watch off"}</div>`;
  $("captureList").innerHTML = html;
  $("capLive").hidden = dcfg.reviewIdx < 0;
}

// Review a captured window: freeze on its snapshot so you can zoom/navigate/decode
// it; "live" resumes.
// TRLC-LINKS: REQ-SDS-207
function reviewCapture(i) {
  const c = dcfg.captures[i];
  if (!c) return;
  dcfg.reviewIdx = i; frozen = true; $("freeze").classList.add("on");
  frame = c.snap; view.win.a = 0; view.win.b = 1; userZoomed = false;
  computeDecode(); redraw(); updateMeas(); updateCursors(); updateCaptureList();
}

// TRLC-LINKS: REQ-SDS-207
function reviewLive() {
  dcfg.reviewIdx = -1; frozen = false; $("freeze").classList.remove("on");
  updateCaptureList();
}

// updateDecodeResults fills BUS's transcript (the Log view) and its count, then
// lets the packet table (app_bus.js) group the new result.
// TRLC-LINKS: REQ-SDS-207
function updateDecodeResults() {
  if (typeof busOnDecode === "function") busOnDecode();
  if (dcfg.proto === "off") { $("decodeText").value = ""; $("decodeCount").textContent = ""; return; }
  const r = dcfg.result;
  if (frame && (frame.is_env || (frame.peak_detect && !frame.ordered))) { $("decodeText").value = "(waiting for sampled waveform — min/max envelopes cannot be decoded)"; $("decodeCount").textContent = "sampled data required"; return; }
  if (dcfg.stream) { // show the accumulated packet history (newest last), auto-scroll
    const ta = $("decodeText");
    ta.value = dcfg.hist.join("\n");
    ta.scrollTop = ta.scrollHeight;
    $("decodeCount").textContent = dcfg.hist.length + " windows" + (frame && frame.gap_ns ? " · duty " + (frame.window_ns / (frame.window_ns + frame.gap_ns) * 100).toFixed(0) + "%" : "");
    return;
  }
  if (!r) { $("decodeText").value = ""; $("decodeCount").textContent = ""; return; }
  if (!r.ok) { $("decodeText").value = "(no decode)"; $("decodeCount").textContent = r.error || "no signal"; return; }
  $("decodeText").value = r.text;
  $("decodeCount").textContent = r.bytes.length + " bytes" + (r.meta && r.meta.noCS ? " · no CS" : "");
}

// On-trace decode band (YT-only), placed in a reserved bottom lane. Uses the
// windowed xForCol so labels track the trace at any zoom; spans off-window are
// culled.
// TRLC-LINKS: REQ-SDS-207
function drawDecode(g) {
  if (view.mode !== "YT" || dcfg.proto === "off" || !frame || frame.is_env) return;
  const r = dcfg.result;
  if (!r || !r.ok || !r.spans.length || !frame.c1) return;
  const n = frame.c1.length, bandH = 20 * dpr, bandY = CH - bandH - 6 * dpr;
  const spans = r.spans, cy = bandY + bandH / 2;
  g.save();
  g.fillStyle = "rgba(5,8,12,0.6)"; g.fillRect(0, bandY - 2 * dpr, CW, bandH + 4 * dpr);
  g.textBaseline = "middle"; g.font = "bold " + (11 * dpr) + "px system-ui";
  for (let i = 0; i < spans.length; i++) {
    const s = spans[i];
    let x0 = xForCol(s.i0, n), x1 = xForCol(s.i1, n);
    if (x1 < 0 || x0 > CW) continue;
    const col = DECCOL[s.kind] || "#7c8894";
    if (s.kind === "start" || s.kind === "stop") {
      g.fillStyle = col; g.fillRect(x0 - dpr, bandY, 2 * dpr, bandH);
      g.beginPath(); g.moveTo(x0 - 4 * dpr, bandY); g.lineTo(x0 + 4 * dpr, bandY); g.lineTo(x0, bandY + 5 * dpr); g.fill();
      continue;
    }
    x1 = Math.max(x1, x0 + 2 * dpr); const w = x1 - x0;
    g.fillStyle = hexA(col, 0.2); g.fillRect(x0, bandY, w, bandH);
    g.strokeStyle = col; g.lineWidth = dpr; g.strokeRect(x0 + .5, bandY + .5, w - dpr, bandH - dpr);
    // Label: center it when it fits inside the box; when the box is too narrow
    // (zoomed out) let it spill RIGHT into the idle/gap up to the next token, so
    // a byte never loses its value just because its box shrank.
    g.fillStyle = col;
    if (g.measureText(s.text).width <= w - 6 * dpr) {
      g.textAlign = "center"; g.fillText(s.text, (x0 + x1) / 2, cy);
    } else {
      const nextX0 = i + 1 < spans.length ? xForCol(spans[i + 1].i0, n) : CW;
      const label = fitLabel(g, s.text, Math.min(nextX0, CW) - x0 - 4 * dpr);
      if (label) { g.textAlign = "left"; g.fillText(label, x0 + 3 * dpr, cy); }
    }
  }
  if (typeof busDrawSel === "function") busDrawSel(g, bandY, bandH); // the packet BUS zoomed onto
  g.restore(); g.textAlign = "left"; g.textBaseline = "alphabetic";
}

// ---- decode wiring ----
// TRLC-LINKS: REQ-SDS-207
function updateDecodePanel() {
  const p = dcfg.proto;
  $("decRoles").style.display = p === "off" ? "none" : "";
  for (const c of document.querySelectorAll(".dec-uart")) c.style.display = p === "uart" ? "flex" : "none";
  for (const c of document.querySelectorAll(".dec-i2c")) c.style.display = p === "i2c" ? "flex" : "none";
  for (const c of document.querySelectorAll(".dec-spi")) c.style.display = p === "spi" ? "flex" : "none";
  for (const c of document.querySelectorAll(".dec-sent")) c.style.display = p === "sent" ? "flex" : "none";
  const single = p !== "off" && p !== "i2c" && p !== "spi";
  for (const c of document.querySelectorAll(".dec-single")) c.style.display = single ? "flex" : "none";
  for (const c of document.querySelectorAll(".dec-rate")) c.style.display = single && p !== "sent" ? "flex" : "none";
  for (const c of document.querySelectorAll(".dec-word")) c.style.display = p === "uart" || p === "manchester" ? "flex" : "none";
  for (const c of document.querySelectorAll(".dec-polarity")) c.style.display = p === "uart" || p === "i2c" || p === "spi" || p === "sent" || p === "mil1553" ? "flex" : "none";
  updateDecodeResults();
  updateCaptureList();
  if (typeof stOnDecodeChange === "function") stOnDecodeChange(); // serial trigger reuses this config
}

// syncDecodeControls: write dcfg back into the DOM controls (used after
// autodetect picks settings so the dropdowns/inputs reflect what it chose).
// TRLC-LINKS: REQ-SDS-207
function syncDecodeControls() {
  $("decProto").value = dcfg.proto;
  $("decScl").value = String(dcfg.scl); $("decSda").value = String(dcfg.sda);
  $("decClk").value = String(dcfg.clk); $("decData").value = String(dcfg.data);
  $("decLine").value = String(dcfg.line);
  $("decBaud").value = dcfg.baud; $("decBits").value = dcfg.bits; $("decParity").value = dcfg.parity;
  $("decInverted").checked = !!dcfg.inverted;
  $("decCpol").value = String(dcfg.cpol); $("decCpha").value = String(dcfg.cpha);
  $("decMsb").value = dcfg.msb ? "1" : "0"; $("decFmt").value = dcfg.fmt;
  $("decAuto").classList.toggle("on", dcfg.auto);
}

// detectProtoUI maps a decoder's canonical proto name to the #decProto select
// value (the decoders say "canfd"/"usbls"; the UI options are "can"/"usb").
// TRLC-LINKS: REQ-SDS-207
function detectProtoUI(p) { return p === "canfd" ? "can" : p === "usbls" ? "usb" : p; }

// TRLC-LINKS: REQ-SDS-207
function detectLabel(d) {
  const b = d.result && d.result.meta || {};
  if (d.proto === "uart") return "UART · C" + d.roles.line + " · " + (b.baud ? b.baud + " bd" : "auto");
  if (d.proto === "i2c") return "I²C · SCL=C" + d.roles.scl + " SDA=C" + d.roles.sda;
  if (d.proto === "spi") return "SPI · CLK=C" + d.roles.clk + " DATA=C" + d.roles.data + " · mode " + (d.cfg.cpol * 2 + d.cfg.cpha) + " · " + (d.cfg.msb ? "MSB" : "LSB");
  // Single-wire protocols: name · line · detected bit rate (when known).
  const names = { manchester: "Manchester", sent: "SENT", canfd: "CAN", mil1553: "MIL-1553B", arinc429: "ARINC 429", usbls: "USB LS/FS", flexray: "FlexRay" };
  const name = names[d.proto] || d.proto;
  return name + (d.roles.line ? " · C" + d.roles.line : "") + (b.bitrate ? " · " + b.bitrate + " bd" : "");
}

// TRLC-LINKS: REQ-SDS-207
function setDetectMsg(t, err) {
  const el = $("decDetectMsg");
  el.style.display = t ? "" : "none";
  el.textContent = t || "";
  el.style.color = err ? "#e8604c" : "var(--dim)";
}

// fetchSampledFrame recalls one sampled record of the live acquisition that
// accept takes, or null within waitMs: the full-rate feed the eye diagram and
// super-res read (lease "raw"), or a decimated record (lease "samples": a
// few thousand filtered samples a screen, read in tens of milliseconds).
// TRLC-LINKS: REQ-SDS-207
async function fetchSampledFrame(waitMs, accept, lease) {
  const t0 = performance.now();
  let since = 0;
  while (performance.now() - t0 < waitMs) {
    try {
      const r = await fetch("/api/frame.bin?since=" + since + "&waitms=1000&raw=1&lease=" + (lease || "raw"));
      if (!r.ok) return null;
      const f = decodeBinFrame(await r.arrayBuffer());
      if (f && !f.unchanged) {
        since = f.seq;
        if (f.c1 && !f.is_env && !f.peak_detect && (!accept || accept(f))) return f;
      }
    } catch { return null; }
  }
  return null;
}

// detectStands returns detection d, unless src is the live envelope (no
// sampled record came: Normal with nothing triggering) and the bus d names is
// too fine for it, which aliases into another (I2C's data line read as
// Manchester at 102 kBd): then a refusal that says why.
// TRLC-LINKS: REQ-SDS-207
function detectStands(d, src, waiting) {
  if (d.proto === "off" || !(src.peak_detect && src.ordered) || !d.result) return d;
  const fs = decodeFeatureSamples(d.result);
  if (!(fs > 0 && fs < DECODE_MIN_ENVELOPE_FEATURE)) return d;
  return { proto: "off", reason: waiting ? "no new capture: the scope waits for a trigger - Auto or Force, then Detect"
    : "the live view cannot resolve it - a faster time/div, then Detect" };
}

// DETECT_DECIMATED_FEATURE is the samples of a decimated record a bus's
// shortest feature must span for a detection on it to stand (a 115200 Bd UART
// at 200 us/div spans 35); a faster bus is looked at at the full rate, as the
// scope's BUS search does (panel decimatedFeature). DETECT_CHECKED_FEATURE is
// the bound for a bus found by its frames' CRCs (CAN's CRC-15, FlexRay's
// header CRC-11), which a misread record does not pass: a 500 kbit/s CAN bus
// at 200 us/div spans 7.8 (panel decimatedCheckedFeature).
const DETECT_DECIMATED_FEATURE = 8, DETECT_CHECKED_FEATURE = 6;

// detectStandsDecimated reports whether a detection d on a decimated record
// stands (DETECT_DECIMATED_FEATURE, DETECT_CHECKED_FEATURE). A UART at a rate
// no UART uses does not: a faster bus the record aliases reads as one
// (1 Mbit/s Manchester sampled every 1 us: "UART 32917"). As the scope's BUS
// search has it (panel decimatedStands).
// TRLC-LINKS: REQ-SDS-207
function detectStandsDecimated(d) {
  const fs = d.proto !== "off" && d.result ? decodeFeatureSamples(d.result) : 0;
  if (d.proto === "canfd" || d.proto === "flexray") return fs >= DETECT_CHECKED_FEATURE;
  if (d.proto === "uart") return fs >= DETECT_DECIMATED_FEATURE && detectStandardBaud(d.result.meta && d.result.meta.baud);
  return fs >= DETECT_DECIMATED_FEATURE;
}

// detectStandardBaud reports whether baud is a rate UARTs use (within 3 %:
// decode.js STANDARD_BAUDS, decode.StandardBaud).
// TRLC-LINKS: REQ-SDS-207
function detectStandardBaud(baud) {
  return baud > 0 && STANDARD_BAUDS.some((b) => Math.abs(baud - b) <= 0.03 * b);
}

// DETECT_SCANT_BYTES is the bytes a UART found on the search's first record
// must hold to end the search: a byte or two, or a rate no UART uses, on a
// short record is as likely part of another bus's frame (CAN at 5 us/div read
// as "UART 500000", a FlexRay frame failing its CRC as "UART 10002156"), so
// the search looks on and names it only when it finds nothing else (panel
// scantBytes, scantUART).
const DETECT_SCANT_BYTES = 4;

// detectScantUART reports a UART detection d of under DETECT_SCANT_BYTES
// bytes or at a rate no UART uses.
// TRLC-LINKS: REQ-SDS-207
function detectScantUART(d) {
  return d.proto === "uart" && !!d.result && ((d.result.bytes || []).length < DETECT_SCANT_BYTES || !detectStandardBaud(d.result.meta && d.result.meta.baud));
}

// detectSampled detects on a sampled record of the live acquisition that
// accept takes: a decimated one first, read in tens of milliseconds, standing
// when the bus spans enough of its samples (detectStandsDecimated); else a
// full-rate one within rawWaitMs. A short screen fits the sample lease
// undecimated (its sample interval at most 1.25 fullS): that record was the
// full-rate one, and what was found on it stands. A record that resolves its
// every edge (a UART's bits at 10 us/div), or a timebase slower than
// rawMaxTdiv, reads no full-rate record (a megasample at 200 us/div: seconds;
// the search at the full-rate timebase reads a short one instead). It returns
// {d, src, dec} (dec the decimated record's detection), or null when no
// record came.
// TRLC-LINKS: REQ-SDS-207
async function detectSampled(accept, rawWaitMs, fullS, rawMaxTdiv) {
  const dec = await detectDecimated(accept);
  if (dec && (dec.stands || (fullS > 0 && dec.src.sample_s > 0 && dec.src.sample_s <= 1.25 * fullS))) return Object.assign({}, dec, { dec });
  // a record that resolves its every edge shows at the full rate what it showed
  if (dec && (dec.resolved || (rawMaxTdiv > 0 && dec.src.tdiv_s > rawMaxTdiv * 1.001))) return { d: { proto: "off", reason: "no protocol matched" }, src: dec.src, dec };
  // the full-rate record, not a decimated one still in flight
  const raw = await detectRaw((f) => (!accept || accept(f)) && !(fullS > 0 && f.sample_s > 1.25 * fullS), rawWaitMs);
  return raw ? Object.assign(raw, { dec }) : null;
}

// detectDecimated detects on one decimated record that accept takes (lease
// "samples", given back at once): {d, src, stands, resolved} - stands when
// the bus found stands on it (detectStandsDecimated), resolved when no run
// between two edges of either channel is under twice DETECT_DECIMATED_FEATURE
// samples (panel runsResolved) - or null when no record came.
// TRLC-LINKS: REQ-SDS-207
async function detectDecimated(accept) {
  const src = await fetchSampledFrame(1500, accept, "samples");
  fetch("/api/frame.bin?lease=samples&end=1").catch(() => {});
  if (!src) return null;
  const d = autodetect(src, { fmt: dcfg.fmt });
  const r1 = detectShortestRun(src.c1), r2 = detectShortestRun(src.c2);
  const resolved = (!r1 || r1 >= 2 * DETECT_DECIMATED_FEATURE) && (!r2 || r2 >= 2 * DETECT_DECIMATED_FEATURE);
  return { d, src, stands: detectStandsDecimated(d), resolved };
}

// detectShortestRun is the fewest samples between two edges of channel c
// (sliced as the decoders slice it), 0 when it holds under three
// (decode.ShortestRun).
// TRLC-LINKS: REQ-SDS-207
function detectShortestRun(c) {
  const S = c ? sliceChannel(c) : null;
  if (!S || !S.ok || S.edges.length < 3) return 0;
  let run = 0;
  for (let k = 1; k < S.edges.length; k++) {
    const g = S.edges[k].i - S.edges[k - 1].i;
    if (!run || g < run) run = g;
  }
  return run;
}

// detectRaw detects on one full-rate record that accept takes, within waitMs;
// it returns {d, src}, or null when no record came.
// TRLC-LINKS: REQ-SDS-207
async function detectRaw(accept, waitMs) {
  const raw = await fetchSampledFrame(waitMs, accept);
  // given back at once: a full-rate lease left running outranks the next
  // search's decimated one (and holds the live view at a frame in seconds)
  fetch("/api/frame.bin?lease=raw&end=1").catch(() => {});
  return raw ? { d: autodetect(raw, { fmt: dcfg.fmt }), src: raw } : null;
}

// DETECT_WINDOW_DIVS is how much shorter than the slowest full-rate
// timebase's the record a fast bus is first looked for on is (panel
// windowDivs): 500 us at 50 us/div, read in under a second against about four
// for the whole 2 ms record (a tenth held too few frames: a FlexRay pair every
// 240 us, one failing its CRC, read as a 10 MBd UART).
const DETECT_WINDOW_DIVS = 4;

// detectWindowTdiv is the slowest of tdivs at most a DETECT_WINDOW_DIVS-th of
// the full-rate timebase full (0 for none), as panel windowTdiv picks it.
// TRLC-LINKS: REQ-SDS-207
function detectWindowTdiv(tdivs, full) {
  let best = 0;
  for (const t of tdivs || []) if (t <= full / DETECT_WINDOW_DIVS * 1.001 && t > best) best = t;
  return best;
}

// detectAtFullRate looks again at the slowest full-rate timebase full when the
// record on screen hid the bus: its record decimated (a slow bus, or long
// frames; first, when the timebase on screen is full and its decimated record
// was read), then a short full-rate record (detectWindowTdiv), then the whole
// record at the full rate (a sparse bus); fullS is the full rate's sample
// interval. It returns {found, stepped, within}: the timebase it was found at
// (left set; 0: the one on screen) and, for the short record, the timebase up
// to which BUS steps to the slowest that reads it; with none found the
// timebase goes back to prev.
// TRLC-LINKS: REQ-SDS-207
async function detectAtFullRate(full, prev, tdivs, first, fullS) {
  // a record of the timebase t (a frame that names none: the second after)
  // TRLC-LINKS: REQ-SDS-207
  const at = (t) => { let seen = 0; return (f) => f.tdiv_s > 0 ? Math.abs(f.tdiv_s - t) <= 1e-3 * t : ++seen >= 2; };
  // ... at the full rate, not a decimated one still in flight
  // TRLC-LINKS: REQ-SDS-207
  const atRaw = (t) => { const a = at(t); return (f) => !(fullS > 0 && f.sample_s > 1.25 * fullS) && a(f); };
  let dec = first;
  if (!dec) {
    setDetectMsg("looking again at " + fmtTdiv(full) + "/div…");
    await send("tdiv", full);
    dec = await detectDecimated(at(full));
    if (dec && dec.stands) return { found: dec, stepped: full, within: 0 };
  }
  const w = detectWindowTdiv(tdivs, full);
  if (w > 0 && w < full) {
    setDetectMsg("looking at " + fmtTdiv(w) + "/div at the full rate…");
    await send("tdiv", w);
    const got = await detectRaw(atRaw(w), 6000);
    if (got && got.d.proto !== "off") return { found: got, stepped: w, within: full };
    await send("tdiv", full);
  }
  const got = await detectRaw(atRaw(full), 6000);
  if (got && got.d.proto !== "off") return { found: got, stepped: Math.abs(full - prev) > 1e-3 * full ? full : 0, within: 0 };
  if (Math.abs(full - prev) > 1e-3 * full) await send("tdiv", prev);
  return null;
}

// runAutodetect: analyse the current frame, pick the best protocol + roles +
// sub-settings, apply them to dcfg + the DOM controls, and re-decode.
// TRLC-LINKS: REQ-SDS-207
async function runAutodetect() {
  if (!frame || !frame.c1 || frame.is_env) { setDetectMsg("no live waveform to analyse", true); return; }
  let src = frame, found = null;
  if (frame.peak_detect && !frozen) {
    // Detect on samples, as the scope's BUS entry does: the live feed is a
    // screen of min/max pairs (at 500 us/div after Default setup, 2.4 us a
    // point: no protocol matches with a UART on screen), so recall a sampled
    // record and detect on that.
    setDetectMsg("fetching a sampled record…");
    found = await detectSampled(null, 4000, st && st.full_rate_s, st && st.full_rate_tdiv_s > 0 ? detectWindowTdiv(st.tdivs, st.full_rate_tdiv_s) : 0);
    if (found) src = found.src;
    if (!frame || !frame.c1) { setDetectMsg("no live waveform to analyse", true); return; }
  }
  if ((src.peak_detect && !src.ordered) || src.is_env) { setDetectMsg("Sampled data required — select Normal acquisition and Run before detecting.", true); return; }
  let d = detectStands(found ? found.d : autodetect(src, { fmt: dcfg.fmt }), src, !!(st && st.waiting)), stepped = 0;
  // A record that hid the bus - too short (Autoset's 10 us/div holds about one
  // UART byte) or decimated - is looked at again at the slowest
  // full-rate timebase, kept when it finds the bus, else the timebase goes
  // back: as the scope's BUS entry does.
  const full = st && st.full_rate_tdiv_s, prev = st && st.tdiv_s;
  let within = 0;
  const scant = detectScantUART(d);
  if ((d.proto === "off" || scant) && !frozen && st.running && full > 0 && prev > 0) {
    // the timebase on screen is the full-rate one: its decimated record was read
    const first = Math.abs(prev - full) <= 1e-3 * full && found ? found.dec : null;
    const again = await detectAtFullRate(full, prev, st.tdivs, first, st.full_rate_s);
    if (again) { d = again.found.d; src = again.found.src; stepped = again.stepped; within = again.within; }
  }
  // the found bus's shortest feature in seconds, for BUS's timebase step
  dcfg.detectFeatureS = d.proto !== "off" && d.result ? decodeFeatureSamples(d.result) * frameDtS(src, src.c1.length) : 0;
  // found on the short full-rate record: the slowest timebase that reads it
  // live shows more of it (the timebase the scope's BUS steps to)
  if (within > 0 && dcfg.detectFeatureS > 0 && typeof busSlowestReadable === "function") {
    const t = busSlowestReadable(dcfg.detectFeatureS, within);
    if (t > 0 && Math.abs(t - stepped) > 1e-3 * t) { await send("tdiv", t); stepped = t; }
  }
  if (d.proto === "off") {
    const why = d.reason && d.reason !== "no protocol matched" ? d.reason : "show more of the signal (slower t/div) or check the probes";
    setDetectMsg("no protocol matched — " + why, true); return;
  }
  dcfg.proto = detectProtoUI(d.proto);
  if (d.roles.line) dcfg.line = d.roles.line;
  if (d.roles.scl) { dcfg.scl = d.roles.scl; dcfg.sda = d.roles.sda; }
  if (d.roles.clk) { dcfg.clk = d.roles.clk; dcfg.data = d.roles.data; }
  if (d.cfg.baud != null) dcfg.baud = d.cfg.baud;     // 0 = keep auto-baud
  if (d.cfg.bits) dcfg.bits = d.cfg.bits;
  if (d.cfg.parity) dcfg.parity = d.cfg.parity;
  if (d.cfg.cpol != null) dcfg.cpol = d.cfg.cpol;
  if (d.cfg.cpha != null) dcfg.cpha = d.cfg.cpha;
  if (d.cfg.msb != null) dcfg.msb = d.cfg.msb;
  // SENT reads tick and nibble count from its own inputs: set them to what the
  // detector decoded with, or the display decodes with the old ones ("detected
  // SENT · 28 bytes" over "(no decode)").
  if (d.cfg.sentNibbles) { $("decSentTick").value = d.cfg.sentTickNs; $("decSentNibbles").value = d.cfg.sentNibbles; }
  dcfg.auto = true;                                    // let the threshold auto-track
  if (d.result && d.result.ok) dcfg.good = { key: decKey(), r: d.result, dt: frameDtS(src, src.c1.length) };
  syncDecodeControls();
  updateDecodePanel();
  recompute();
  const nb = d.result && d.result.bytes ? d.result.bytes.length : 0;
  setDetectMsg("detected " + detectLabel(d) + " · " + nb + " bytes" + (stepped ? " · " + fmtTdiv(stepped) + "/div to read it" : ""));
}

// ==== wiring ====

// ---- decode panel init + wiring ----

// TRLC-LINKS: REQ-SDS-207
(function initDecode() {
  for (const sel of document.querySelectorAll(".rolesel")) {
    sel.innerHTML = '<option value="1">C1</option><option value="2">C2</option>';
  }
  $("decSda").value = "2"; $("decData").value = "2"; $("decLine").value = "1";
  // TRLC-LINKS: REQ-SDS-207
  $("decSentTick").onchange = $("decSentNibbles").onchange = () => { recompute(); if (typeof stOnDecodeChange === "function") stOnDecodeChange(); };
  $("decDetect").onclick = runAutodetect;
  // TRLC-LINKS: REQ-SDS-207
  $("decProto").onchange = () => { dcfg.proto = $("decProto").value; setDetectMsg(""); updateDecodePanel(); recompute(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decScl").onchange = () => { dcfg.scl = +$("decScl").value; recompute(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decSda").onchange = () => { dcfg.sda = +$("decSda").value; recompute(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decClk").onchange = () => { dcfg.clk = +$("decClk").value; recompute(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decData").onchange = () => { dcfg.data = +$("decData").value; recompute(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decLine").onchange = () => { dcfg.line = +$("decLine").value; recompute(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decAuto").onclick = () => { dcfg.auto = !dcfg.auto; $("decAuto").classList.toggle("on", dcfg.auto); recompute(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decThr").oninput = () => { dcfg.auto = false; $("decAuto").classList.remove("on"); $("decThrV").textContent = $("decThr").value; recompute(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decBaud").onchange = () => { dcfg.baud = Math.max(0, +$("decBaud").value | 0); recompute(); }; // 0 = auto-baud
  // TRLC-LINKS: REQ-SDS-207
  // UART frames carry 5..9 data bits; Manchester words 1..16.
  $("decBits").onchange = () => {
    const manchester = dcfg.proto === "manchester";
    dcfg.bits = Math.max(manchester ? 1 : 5, Math.min(manchester ? 16 : 9, +$("decBits").value | 0)); recompute();
  };
  // TRLC-LINKS: REQ-SDS-207
  $("decParity").onchange = () => { dcfg.parity = $("decParity").value; recompute(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decInverted").onchange = () => { dcfg.inverted = $("decInverted").checked; recompute(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decCpol").onchange = () => { dcfg.cpol = +$("decCpol").value; recompute(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decCpha").onchange = () => { dcfg.cpha = +$("decCpha").value; recompute(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decMsb").onchange = () => { dcfg.msb = $("decMsb").value === "1"; recompute(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decFmt").onchange = () => { dcfg.fmt = $("decFmt").value; recompute(); }; // hex / ascii / both
  // TRLC-LINKS: REQ-SDS-207
  $("decStream").onclick = async () => {
    const requested = !dcfg.stream;
    const result = await send("stream", requested ? 1 : 0);
    if (!result.ok || !!result.applied !== requested) { setDetectMsg("Capture streaming is unavailable here; use Stream + trigger.", true); return; }
    dcfg.stream = requested; $("decStream").classList.toggle("on", dcfg.stream);
    dcfg.hist = []; dcfg.lastStreamSeq = 0;
    updateDecodeResults();
  };
  // TRLC-LINKS: REQ-SDS-207
  $("decHistClear").onclick = () => { dcfg.hist = []; dcfg.lastStreamSeq = 0; updateDecodeResults(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decWatch").onclick = () => { dcfg.watch = !dcfg.watch; $("decWatch").classList.toggle("on", dcfg.watch); updateCaptureList(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decWatchErr").onchange = () => { dcfg.watchErr = $("decWatchErr").checked; };
  // TRLC-LINKS: REQ-SDS-207
  $("decWatchMatch").oninput = () => { dcfg.watchMatch = $("decWatchMatch").value; };
  $("captureList").addEventListener("click", ev => { const d = ev.target.closest(".cap"); if (d) reviewCapture(+d.dataset.i); });
  $("capLive").onclick = reviewLive;
  // TRLC-LINKS: REQ-SDS-207
  $("capClear").onclick = () => { dcfg.captures = []; dcfg.reviewIdx = -1; dcfg.lastCapKey = ""; if (frozen) reviewLive(); updateCaptureList(); };
  // TRLC-LINKS: REQ-SDS-207
  $("capCopy").onclick = () => {
    const t = dcfg.captures.map(c => new Date(c.t).toISOString() + " #" + c.seq + " [" + c.reason + "] " + c.text).join("\n");
    if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(t).catch(() => {});
    else { // plain-HTTP fallback (no navigator.clipboard off https/localhost)
      const ta = document.createElement("textarea");
      ta.value = t; ta.style.position = "fixed"; ta.style.opacity = "0";
      document.body.appendChild(ta); ta.select();
      try { document.execCommand("copy"); } catch (e) {}
      document.body.removeChild(ta);
    }
    $("capCopy").textContent = "copied"; setTimeout(() => $("capCopy").textContent = "Copy", 900);
  };
  // TRLC-LINKS: REQ-SDS-207
  $("decodeCopy").onclick = () => {
    const t = $("decodeText").value;
    if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(t).catch(() => {});
    else { $("decodeText").select(); try { document.execCommand("copy"); } catch (e) {} }
    $("decodeCopy").textContent = "copied"; setTimeout(() => $("decodeCopy").textContent = "Copy", 900);
  };
  updateDecodePanel();
})();
