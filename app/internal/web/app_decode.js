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
// At 100 us/div a peak-detect bucket is wider than an SPI clock, and the SPI
// decode reads no clock there.
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
  if (m.spb > 0) { // the decoders beyond the first ten (decode.FeatureSamples)
    if (["dali", "usbpd", "spdif", "irnec", "irrc5", "irrc6", "ps2", "swd", "mdio"].includes(r.proto)) return m.spb / 2;
    if (r.proto === "ws281x") return m.spb / 3;
    if (r.proto === "cec") return m.spb / 4;
    if (r.proto === "onewire") return 15 * m.spb;
    if (r.proto === "dht") return 26 * m.spb;
    if (r.proto === "wiegand") return m.spb / 20;
    return m.spb;
  }
  if (!(m.samplesPerBit > 0)) return 0;
  return ["manchester", "mil1553", "arinc429"].includes(r.proto) ? m.samplesPerBit / 2 : m.samplesPerBit;
}
// A live envelope holds a (min, max) pair per bucket, two points: a bus whose
// shortest feature is not well over a bucket aliases there (a fast I2C clock
// read as starts and stops, no bytes; a 115200 UART at 500 us/div's 3.6
// points a bit read 0A as 02), so it is not decoded from it
// (framecalc.MinEnvelopeFeature).
const DECODE_MIN_ENVELOPE_FEATURE = 4;
const DECODE_NAMES = { uart: "UART", i2c: "I2C", spi: "SPI", manchester: "Manchester", sent: "SENT", canfd: "CAN", mil1553: "MIL-1553", arinc429: "ARINC 429", usbls: "USB", flexray: "FlexRay",
  lin: "LIN", dmx512: "DMX512", midi: "MIDI", modbus: "Modbus RTU", onewire: "1-Wire", ws281x: "WS281x", dht: "DHT", cec: "HDMI-CEC", j1850: "J1850 VPW",
  irnec: "IR NEC", irsirc: "IR Sony", irrc5: "IR RC-5", irrc6: "IR RC-6", dali: "DALI", usbpd: "USB PD", spdif: "S/PDIF", ps2: "PS/2", swd: "SWD", mdio: "MDIO", wiegand: "Wiegand" };

// DEC_CLOCKED are the protocols on a clock (or D0) and a data (or D1) line
// besides SPI: they take the Clock/data roles.
const DEC_CLOCKED = ["ps2", "swd", "mdio", "wiegand"];

// DEC_FIXED_TIMING are the protocols whose timing is their own, no rate to set.
const DEC_FIXED_TIMING = ["dmx512", "midi", "onewire", "ws281x", "dht", "cec", "j1850", "irnec", "irsirc", "irrc5", "irrc6", "dali", "usbpd", "spdif"];

// decodeMore decodes the protocols beyond the first ten (framecalc
// decodeMore): the line, or the clock and data lines, with the scope's
// settings.
// TRLC-LINKS: REQ-SDS-207
function decodeMore(p, colTimeS, cfg) {
  const a = decCodes(dcfg.line);
  const two = { threshold: cfg.threshold, thresholdB: cfg.threshold, format: cfg.fmt };
  const opts = Object.assign({}, cfg, { format: cfg.fmt });
  switch (p) {
    case "lin": return decodeLIN(a, colTimeS, Object.assign(opts, { baud: dcfg.baud > 0 ? dcfg.baud : 0 }));
    case "dmx512": return decodeDMX512(a, colTimeS, opts);
    case "midi": return decodeMIDI(a, colTimeS, opts);
    case "modbus": return decodeModbus(a, colTimeS, Object.assign(opts, { baud: dcfg.baud > 0 ? dcfg.baud : 0, parity: dcfg.modbusParity || "" }));
    case "onewire": return decodeOneWire(a, colTimeS, Object.assign(opts, { overdrive: dcfg.baud >= 100000 }));
    case "ws281x": return decodeWS281x(a, colTimeS, opts);
    case "dht": return decodeDHT(a, colTimeS, opts);
    case "cec": return decodeCEC(a, colTimeS, opts);
    case "j1850": return decodeJ1850(a, colTimeS, opts);
    case "irnec": return decodeIRNEC(a, colTimeS, opts);
    case "irsirc": return decodeIRSIRC(a, colTimeS, opts);
    case "irrc5": return decodeIRRC5(a, colTimeS, opts);
    case "irrc6": return decodeIRRC6(a, colTimeS, opts);
    case "dali": return decodeDALI(a, colTimeS, opts);
    case "usbpd": return decodeUSBPD(a, colTimeS, opts);
    case "spdif": return decodeSPDIF(a, colTimeS, opts);
    case "ps2": return decodePS2(decCodes(dcfg.clk), decCodes(dcfg.data), colTimeS, two);
    case "swd": return decodeSWD(decCodes(dcfg.clk), decCodes(dcfg.data), colTimeS, two);
    case "mdio": return decodeMDIO(decCodes(dcfg.clk), decCodes(dcfg.data), colTimeS, two);
    case "wiegand": return decodeWiegand(decCodes(dcfg.clk), decCodes(dcfg.data), colTimeS, two);
  }
  return null;
}

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
  if (dcfg.proto === "uart") {
    // the hardware matcher's character fixes the framing (frame.match_end_s)
    const n = frame.c1.length, anchor = frame.match_end_s != null && frame.edge_frac >= 0 && colTimeS > 0 ? frame.edge_frac * n + frame.match_end_s / colTimeS : null;
    r = decodeUART(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { inverted: !!dcfg.inverted, baud: dcfg.baud > 0 ? dcfg.baud : null, bits: dcfg.bits, parity: dcfg.parity, anchor }));
  }
  else if (dcfg.proto === "i2c")
    r = decodeI2C(decCodes(dcfg.scl), decCodes(dcfg.sda), colTimeS, cfg);
  else if (dcfg.proto === "spi")
    // dataLag: C2 samples 23/16 ns after C1 (decode.ChannelPhase16), in samples;
    // dataDelay: the scope's data delay (a device's late data), in samples
    r = decodeSPI(decCodes(dcfg.clk), decCodes(dcfg.data), colTimeS, Object.assign(cfg, { cpol: dcfg.cpol, cpha: dcfg.cpha, bitOrder: dcfg.msb ? "msb" : "lsb",
      dataLag: colTimeS > 0 ? (dcfg.clk === 2 ? 1 : -1) * 23 / 16e9 / colTimeS : 0, dataDelay: colTimeS > 0 ? (+dcfg.delayNs || 0) * 1e-9 / colTimeS : 0 }));
  else if (dcfg.proto === "manchester") // single line, self-clocking (auto bit rate)
    r = decodeManchester(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { bitrate: dcfg.baud > 0 ? dcfg.baud : 0, ieee: true, msb: true, bits: dcfg.bits || 8, haveThr: !dcfg.auto }));
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
  else r = decodeMore(dcfg.proto, colTimeS, cfg);
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

// Decode band (YT-only) in the lane under the plot (plotH: the plot ends
// above it). Uses the windowed xForCol so labels track the trace at any zoom;
// spans off-window are culled.
// TRLC-LINKS: REQ-SDS-207
function drawDecode(g) {
  if (view.mode !== "YT" || dcfg.proto === "off" || !frame || frame.is_env) return;
  const r = dcfg.result;
  if (!r || !r.ok || !r.spans.length || !frame.c1) return;
  const PH = plotH(), n = frame.c1.length, bandH = 20 * dpr, bandY = PH + 4 * dpr;
  const spans = r.spans, cy = bandY + bandH / 2;
  g.save();
  g.fillStyle = "#05080c"; g.fillRect(0, PH, CW, CH - PH);
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
  for (const c of document.querySelectorAll(".dec-clkdata")) c.style.display = p === "spi" || DEC_CLOCKED.includes(p) ? "flex" : "none";
  for (const c of document.querySelectorAll(".dec-sent")) c.style.display = p === "sent" ? "flex" : "none";
  const single = p !== "off" && p !== "i2c" && p !== "spi" && !DEC_CLOCKED.includes(p);
  for (const c of document.querySelectorAll(".dec-single")) c.style.display = single ? "flex" : "none";
  // a rate the decoder takes (the protocols of fixed timings take none)
  const rated = single && p !== "sent" && !DEC_FIXED_TIMING.includes(p);
  for (const c of document.querySelectorAll(".dec-rate")) c.style.display = rated ? "flex" : "none";
  for (const c of document.querySelectorAll(".dec-word")) c.style.display = p === "uart" || p === "manchester" ? "flex" : "none";
  for (const c of document.querySelectorAll(".dec-polarity")) c.style.display = p === "uart" || p === "i2c" || p === "spi" || p === "sent" || p === "mil1553" ? "flex" : "none";
  decLayerOptions();
  updateDecodeResults();
  updateCaptureList();
  if (typeof stOnDecodeChange === "function") stOnDecodeChange(); // serial trigger reuses this config
}

// decLayerOptions lists the layers the protocol carries in the Layer row
// (hidden for a bus without layers) and shows the one in use.
// TRLC-LINKS: REQ-SDS-241
function decLayerOptions() {
  const ls = typeof layersOn === "function" ? layersOn(dcfg.proto) : [["", "None"]];
  for (const c of document.querySelectorAll(".dec-layer")) c.style.display = ls.length > 1 ? "flex" : "none";
  const sel = $("decLayer");
  if (!sel) return;
  const html = ls.map((l) => `<option value="${l[0]}">${l[1]}</option>`).join("");
  if (sel.dataset.opts !== html) { sel.innerHTML = html; sel.dataset.opts = html; }
  if (!ls.some((l) => l[0] === dcfg.layer)) dcfg.layer = "";
  sel.value = dcfg.layer;
}

// syncDecodeControls: write dcfg back into the DOM controls (after the page
// takes the scope's settings, so the dropdowns/inputs show them).
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
  decLayerOptions();
}

// TRLC-LINKS: REQ-SDS-207
function setDetectMsg(t, err) {
  const el = $("decDetectMsg");
  el.style.display = t ? "" : "none";
  el.textContent = t || "";
  el.style.color = err ? "#e8604c" : "var(--dim)";
}

// ---- the scope's decoder settings ----
// The page decodes with the scope's own decoder settings (its BUS sheet):
// set on the LCD, a phone or here, or filled in by the scope's Auto, they are
// one set, shown alike everywhere. Auto-detect asks the scope's Auto, which
// detects the bus once and fills them in.

// DEC_SCOPE_PROTOS names the scope's protocol numbers as this page's; 1 is
// the scope's Auto, a detection, never a decoder.
const DEC_SCOPE_PROTOS = ["off", null, "uart", "i2c", "spi", "manchester", "sent", "can", "mil1553", "arinc429", "usb", "flexray",
  "lin", "dmx512", "midi", "modbus", "onewire", "ws281x", "dht", "cec", "j1850", "irnec", "irsirc", "irrc5", "irrc6", "dali",
  "usbpd", "spdif", "ps2", "swd", "mdio", "wiegand"];

// decScopeSettings is the scope's decode (the status's panel Display) as this
// page's settings: protocol, lines, rate, SPI mode and bit order; null when
// the status names none.
// TRLC-LINKS: REQ-SDS-207, REQ-SDS-214, REQ-SDS-222
function decScopeSettings(d) {
  const proto = d && DEC_SCOPE_PROTOS[d.DecProto];
  if (!proto) return null;
  const a = (d.DecChA & 1) + 1, b = (d.DecChB & 1) + 1, s = { proto };
  if (proto === "i2c") { s.scl = a; s.sda = b; }
  else if (proto === "spi") { s.clk = a; s.data = b; s.cpol = d.DecCPOL ? 1 : 0; s.cpha = d.DecCPHA ? 1 : 0; s.msb = !d.DecLSB; s.delayNs = +d.DecDelayNs || 0; }
  else if (DEC_CLOCKED.includes(proto)) { s.clk = a; s.data = b; }
  else if (proto !== "off") s.line = a;
  if (proto !== "off" && proto !== "i2c" && proto !== "spi" && proto !== "sent") s.baud = d.DecBaud > 0 ? d.DecBaud : 0;
  if (proto === "modbus") s.modbusParity = d.DecParity || "";
  if (proto === "uart") { s.parity = d.DecParity || "none"; s.inverted = !!d.DecInverted; } // the framing Auto or the S.BUS layer set
  if (proto !== "off") s.layer = d.DecLayer || ""; // the bus's layer (BUS sheet)
  if (proto === "i2c") { s.layerAddr = d.DecLayerAddr === undefined ? -1 : d.DecLayerAddr; s.layerDir = d.DecLayerDir || 0; }
  if (proto === "irsirc" || proto === "wiegand") s.frameBits = +d.DecBits || 0;
  return s;
}
// End of decScopeSettings.

// DEC_PUSH_HOLD_MS is how long after this page set one of the scope's rows a
// status is not followed: one read before the set reached the scope would put
// the old value back.
const DEC_PUSH_HOLD_MS = 1500;
let decScopeSig = "", decPushT = -Infinity;

// decFollowScope takes the scope's decoder settings when they changed (the
// scope's keys, a phone, SCPI, Auto, this page), so the page decodes the bus
// the LCD decodes with the same settings. Called with each status.
// TRLC-LINKS: REQ-SDS-207, REQ-SDS-214, REQ-SDS-222
function decFollowScope() {
  const d = st && st.panel && st.panel.Display;
  const s = decScopeSettings(d);
  if (!s || performance.now() - decPushT < DEC_PUSH_HOLD_MS) return;
  const sig = JSON.stringify(s);
  if (sig === decScopeSig) return;
  decScopeSig = sig;
  Object.assign(dcfg, s);
  if (d.DecAuto) dcfg.auto = true; // Auto's settings: the threshold tracks the signal, as on the scope
  if (s.proto === "sent") $("decSentTick").value = 0; // the tick from the SYNC, as the scope decodes it
  syncDecodeControls();
  updateDecodePanel();
  recompute();
  if (typeof wsDots === "function") wsDots();
}

// decPush sets one of the scope's BUS sheet rows from this page's control; a
// refusal puts the scope's settings back on the page.
// TRLC-LINKS: REQ-SDS-207, REQ-SDS-214, REQ-SDS-222
async function decPush(row, value) {
  decPushT = performance.now();
  const r = await sheetSet(row, value);
  decPushT = performance.now();
  if (!r || r.ok === false) { decScopeSig = ""; decPushT = -Infinity; }
  return r;
}

// decPushRate sets the scope's UART baud or bit rate from the page's.
// TRLC-LINKS: REQ-SDS-207, REQ-SDS-222
function decPushRate() {
  if (dcfg.proto === "sent" || dcfg.proto === "i2c" || dcfg.proto === "spi" || dcfg.proto === "off") return;
  decPush(dcfg.proto === "uart" ? "bus.baud" : "bus.bit_rate", dcfg.baud);
}

// decPageEdit gives a decoder setting edited here that the scope keeps no row
// for to the serial trigger this page armed.
// TRLC-LINKS: REQ-SDS-206, REQ-SDS-207
function decPageEdit() { if (typeof stOnPageEdit === "function") stOnPageEdit(); }

// decDetect is Auto-detect: the scope's Auto detects the bus once and fills
// in its decoder settings, which this page then follows. It resolves true
// when a bus is decoded after it.
// TRLC-LINKS: REQ-SDS-207, REQ-SDS-214, REQ-SDS-222
async function decDetect() {
  setDetectMsg("the scope is detecting the bus…");
  const r = await sheetSet("bus.protocol", 1);
  if (!r || r.ok === false) { setDetectMsg("the scope did not detect" + (r && r.error ? ": " + r.error : ""), true); return false; }
  // Auto marks itself detecting before the set answers: a status that says it
  // no longer is says it has ended.
  for (const t0 = performance.now(); performance.now() - t0 < 30000;) {
    let s = null;
    try { s = await (await fetch("/api/status")).json(); } catch (e) { break; }
    const d = (s && s.panel && s.panel.Display) || {};
    if (!d.DecDetecting) {
      st = s; applyStatus();
      decPushT = -Infinity; // what Auto filled in, followed at once (unchanged settings stay as they are)
      decFollowScope();
      const found = dcfg.proto !== "off" && !/^no /.test(d.DecNote || ""); // "no bus found ...", "no new capture ..."
      setDetectMsg(found ? "detected " + (d.DecNote || $("decProto").selectedOptions[0].textContent) : (d.DecNote || "no bus found"), !found);
      return dcfg.proto !== "off";
    }
    await new Promise((res) => setTimeout(res, 250));
  }
  setDetectMsg("the scope's detection did not answer", true);
  return false;
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
  $("decSentTick").onchange = $("decSentNibbles").onchange = () => { recompute(); decPageEdit(); };
  $("decDetect").onclick = decDetect;
  // TRLC-LINKS: REQ-SDS-207
  $("decProto").onchange = () => { dcfg.proto = $("decProto").value; setDetectMsg(""); updateDecodePanel(); recompute(); decPush("bus.protocol", DEC_SCOPE_PROTOS.indexOf(dcfg.proto)); };
  // TRLC-LINKS: REQ-SDS-207
  $("decScl").onchange = () => { dcfg.scl = +$("decScl").value; recompute(); decPush("bus.scl", dcfg.scl - 1); };
  // TRLC-LINKS: REQ-SDS-207
  $("decSda").onchange = () => { dcfg.sda = +$("decSda").value; recompute(); decPush("bus.sda", dcfg.sda - 1); };
  // TRLC-LINKS: REQ-SDS-207
  $("decClk").onchange = () => { dcfg.clk = +$("decClk").value; recompute(); decPush("bus.clk", dcfg.clk - 1); };
  // TRLC-LINKS: REQ-SDS-207
  $("decData").onchange = () => { dcfg.data = +$("decData").value; recompute(); decPush("bus.data", dcfg.data - 1); };
  // TRLC-LINKS: REQ-SDS-207
  $("decLine").onchange = () => { dcfg.line = +$("decLine").value; recompute(); decPush("bus.source", dcfg.line - 1); };
  // TRLC-LINKS: REQ-SDS-207
  $("decAuto").onclick = () => { dcfg.auto = !dcfg.auto; $("decAuto").classList.toggle("on", dcfg.auto); recompute(); decPageEdit(); };
  // TRLC-LINKS: REQ-SDS-207
  $("decThr").oninput = () => { dcfg.auto = false; $("decAuto").classList.remove("on"); $("decThrV").textContent = $("decThr").value; recompute(); };
  $("decThr").onchange = decPageEdit;
  // TRLC-LINKS: REQ-SDS-207
  $("decBaud").onchange = () => {
    const v = +$("decBaud").value | 0;
    if (!(v >= 50)) { $("decBaud").value = dcfg.baud; return; } // a rate the scope takes: Auto-detect finds one
    dcfg.baud = v; recompute(); decPushRate();
  };
  // TRLC-LINKS: REQ-SDS-207
  // UART frames carry 5..9 data bits; Manchester words 1..16.
  $("decBits").onchange = () => {
    const manchester = dcfg.proto === "manchester";
    dcfg.bits = Math.max(manchester ? 1 : 5, Math.min(manchester ? 16 : 9, +$("decBits").value | 0)); recompute(); decPageEdit();
  };
  // TRLC-LINKS: REQ-SDS-207
  $("decParity").onchange = () => {
    dcfg.parity = $("decParity").value; recompute(); decPageEdit();
    if (dcfg.proto === "uart") decPush("bus.parity", { even: 1, odd: 2, none: 3 }[dcfg.parity] || 3); // the scope's UART framing too
  };
  // TRLC-LINKS: REQ-SDS-207
  $("decInverted").onchange = () => {
    dcfg.inverted = $("decInverted").checked; recompute(); decPageEdit();
    if (dcfg.proto === "uart") decPush("bus.uart_line", dcfg.inverted ? 1 : 0);
  };
  // TRLC-LINKS: REQ-SDS-241
  $("decLayer").onchange = () => {
    dcfg.layer = $("decLayer").value;
    recompute();
    decPush("bus.layer", LAYERS.findIndex((l) => l[0] === dcfg.layer));
  };
  // TRLC-LINKS: REQ-SDS-207
  $("decCpol").onchange = () => { dcfg.cpol = +$("decCpol").value; recompute(); decPush("bus.spi_mode", dcfg.cpol * 2 + dcfg.cpha); };
  // TRLC-LINKS: REQ-SDS-207
  $("decCpha").onchange = () => { dcfg.cpha = +$("decCpha").value; recompute(); decPush("bus.spi_mode", dcfg.cpol * 2 + dcfg.cpha); };
  // TRLC-LINKS: REQ-SDS-207
  $("decMsb").onchange = () => { dcfg.msb = $("decMsb").value === "1"; recompute(); decPush("bus.bit_order", dcfg.msb ? 0 : 1); };
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
