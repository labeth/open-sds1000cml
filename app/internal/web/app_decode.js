// app_decode.js — protocol-decode UI (classic script; shares app.js globals).

// ---- protocol decode: compute + render ----
"use strict";
function decCodes(role) { return role === 2 ? (frame && frame.c2) : (frame && frame.c1); }

function computeDecode() {
  dcfg.result = null;
  if (dcfg.proto === "off" || !frame || frame.is_env || !frame.c1) { updateDecodeResults(); return; }
  const colTimeS = (frame.col_span_s || 0) / frame.c1.length;
  const cfg = { threshold: dcfg.auto ? null : +$("decThr").value, guard: 4, fmt: dcfg.fmt };
  let r = null;
  if (dcfg.proto === "uart")
    r = decodeUART(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { baud: dcfg.baud > 0 ? dcfg.baud : null, bits: dcfg.bits, parity: dcfg.parity }));
  else if (dcfg.proto === "i2c")
    r = decodeI2C(decCodes(dcfg.scl), decCodes(dcfg.sda), colTimeS, cfg);
  else if (dcfg.proto === "spi")
    r = decodeSPI(decCodes(dcfg.clk), decCodes(dcfg.data), colTimeS, Object.assign(cfg, { cpol: dcfg.cpol, cpha: dcfg.cpha, bitOrder: dcfg.msb ? "msb" : "lsb" }));
  else if (dcfg.proto === "manchester") // single line, self-clocking (auto bit rate)
    r = decodeManchester(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { bitrate: dcfg.baud > 0 ? dcfg.baud : 0, ieee: true, msb: dcfg.msb, bits: dcfg.bits || 8, haveThr: !dcfg.auto }));
  else if (dcfg.proto === "sent") // single line, tick auto-derived from the 56-tick sync
    r = decodeSENT(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { tickNs: 0, nibbles: 0, pausePulse: false, haveThr: !dcfg.auto }));
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
  dcfg.result = r;
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
  $("capLive").style.display = dcfg.reviewIdx >= 0 ? "" : "none";
}

// Review a captured window: freeze on its snapshot so you can zoom/navigate/decode
// it; "live" resumes.
function reviewCapture(i) {
  const c = dcfg.captures[i];
  if (!c) return;
  dcfg.reviewIdx = i; frozen = true; $("freeze").classList.add("on");
  frame = c.snap; view.win.a = 0; view.win.b = 1; userZoomed = false;
  computeDecode(); redraw(); updateMeas(); updateCursors(); updateCaptureList();
}

function reviewLive() {
  dcfg.reviewIdx = -1; frozen = false; $("freeze").classList.remove("on");
  updateCaptureList();
}

function updateDecodeResults() {
  const card = $("decodeResultCard");
  if (dcfg.proto === "off") { card.style.display = "none"; return; }
  card.style.display = "";
  const r = dcfg.result;
  if (frame && frame.is_env) { $("decodeText").value = "(envelope — lower time/div to decode)"; $("decodeCount").textContent = "raise time/div"; return; }
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
  g.restore(); g.textAlign = "left"; g.textBaseline = "alphabetic";
}

// ---- decode wiring ----
function updateDecodePanel() {
  const p = dcfg.proto;
  $("decRoles").style.display = p === "off" ? "none" : "";
  for (const c of document.querySelectorAll(".dec-uart")) c.style.display = p === "uart" ? "flex" : "none";
  for (const c of document.querySelectorAll(".dec-i2c")) c.style.display = p === "i2c" ? "flex" : "none";
  for (const c of document.querySelectorAll(".dec-spi")) c.style.display = p === "spi" ? "flex" : "none";
  updateDecodeResults();
  updateCaptureList();
  if (typeof stOnDecodeChange === "function") stOnDecodeChange(); // serial trigger reuses this config
}

// syncDecodeControls: write dcfg back into the DOM controls (used after
// autodetect picks settings so the dropdowns/inputs reflect what it chose).
function syncDecodeControls() {
  $("decProto").value = dcfg.proto;
  $("decScl").value = String(dcfg.scl); $("decSda").value = String(dcfg.sda);
  $("decClk").value = String(dcfg.clk); $("decData").value = String(dcfg.data);
  $("decLine").value = String(dcfg.line);
  $("decBaud").value = dcfg.baud; $("decBits").value = dcfg.bits; $("decParity").value = dcfg.parity;
  $("decCpol").value = String(dcfg.cpol); $("decCpha").value = String(dcfg.cpha);
  $("decMsb").value = dcfg.msb ? "1" : "0"; $("decFmt").value = dcfg.fmt;
  $("decAuto").classList.toggle("on", dcfg.auto);
}

// detectProtoUI maps a decoder's canonical proto name to the #decProto select
// value (the decoders say "canfd"/"usbls"; the UI options are "can"/"usb").
function detectProtoUI(p) { return p === "canfd" ? "can" : p === "usbls" ? "usb" : p; }

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

function setDetectMsg(t, err) {
  const el = $("decDetectMsg");
  el.style.display = t ? "" : "none";
  el.textContent = t || "";
  el.style.color = err ? "#e8604c" : "var(--dim)";
}

// runAutodetect: analyse the current frame, pick the best protocol + roles +
// sub-settings, apply them to dcfg + the DOM controls, and re-decode.
function runAutodetect() {
  if (!frame || !frame.c1 || frame.is_env) { setDetectMsg("no live waveform to analyse", true); return; }
  const d = autodetect(frame, { fmt: dcfg.fmt });
  if (d.proto === "off") { setDetectMsg("no protocol matched — " + (d.reason || "check the probes"), true); return; }
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
  dcfg.auto = true;                                    // let the threshold auto-track
  syncDecodeControls();
  updateDecodePanel();
  recompute();
  const nb = d.result && d.result.bytes ? d.result.bytes.length : 0;
  setDetectMsg("detected " + detectLabel(d) + " · " + nb + " bytes");
}

// ==== wiring ====

// ---- decode panel init + wiring ----

(function initDecode() {
  for (const sel of document.querySelectorAll(".rolesel")) {
    sel.innerHTML = '<option value="1">C1</option><option value="2">C2</option>';
  }
  $("decSda").value = "2"; $("decData").value = "2"; $("decLine").value = "1";
  $("decDetect").onclick = runAutodetect;
  $("decProto").onchange = () => { dcfg.proto = $("decProto").value; setDetectMsg(""); updateDecodePanel(); recompute(); };
  $("decScl").onchange = () => { dcfg.scl = +$("decScl").value; recompute(); };
  $("decSda").onchange = () => { dcfg.sda = +$("decSda").value; recompute(); };
  $("decClk").onchange = () => { dcfg.clk = +$("decClk").value; recompute(); };
  $("decData").onchange = () => { dcfg.data = +$("decData").value; recompute(); };
  $("decLine").onchange = () => { dcfg.line = +$("decLine").value; recompute(); };
  $("decAuto").onclick = () => { dcfg.auto = !dcfg.auto; $("decAuto").classList.toggle("on", dcfg.auto); recompute(); };
  $("decThr").oninput = () => { dcfg.auto = false; $("decAuto").classList.remove("on"); $("decThrV").textContent = $("decThr").value; recompute(); };
  $("decBaud").onchange = () => { dcfg.baud = Math.max(0, +$("decBaud").value | 0); recompute(); }; // 0 = auto-baud
  $("decBits").onchange = () => { dcfg.bits = Math.max(5, Math.min(9, +$("decBits").value | 0)); recompute(); };
  $("decParity").onchange = () => { dcfg.parity = $("decParity").value; recompute(); };
  $("decCpol").onchange = () => { dcfg.cpol = +$("decCpol").value; recompute(); };
  $("decCpha").onchange = () => { dcfg.cpha = +$("decCpha").value; recompute(); };
  $("decMsb").onchange = () => { dcfg.msb = $("decMsb").value === "1"; recompute(); };
  $("decFmt").onchange = () => { dcfg.fmt = $("decFmt").value; recompute(); }; // hex / ascii / both
  $("decStream").onclick = () => {
    dcfg.stream = !dcfg.stream; $("decStream").classList.toggle("on", dcfg.stream);
    dcfg.hist = []; dcfg.lastStreamSeq = 0;
    fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ control: "stream", value: dcfg.stream ? 1 : 0 }) }).catch(() => {});
    updateDecodeResults();
  };
  $("decHistClear").onclick = () => { dcfg.hist = []; dcfg.lastStreamSeq = 0; updateDecodeResults(); };
  $("decWatch").onclick = () => { dcfg.watch = !dcfg.watch; $("decWatch").classList.toggle("on", dcfg.watch); updateCaptureList(); };
  $("decWatchErr").onchange = () => { dcfg.watchErr = $("decWatchErr").checked; };
  $("decWatchMatch").oninput = () => { dcfg.watchMatch = $("decWatchMatch").value; };
  $("captureList").addEventListener("click", ev => { const d = ev.target.closest(".cap"); if (d) reviewCapture(+d.dataset.i); });
  $("capLive").onclick = reviewLive;
  $("capClear").onclick = () => { dcfg.captures = []; dcfg.reviewIdx = -1; dcfg.lastCapKey = ""; if (frozen) reviewLive(); updateCaptureList(); };
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
    $("capCopy").textContent = "copied"; setTimeout(() => $("capCopy").textContent = "copy", 900);
  };
  $("decodeCopy").onclick = () => {
    const t = $("decodeText").value;
    if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(t).catch(() => {});
    else { $("decodeText").select(); try { document.execCommand("copy"); } catch (e) {} }
    $("decodeCopy").textContent = "copied"; setTimeout(() => $("decodeCopy").textContent = "copy", 900);
  };
  updateDecodePanel();
})();
