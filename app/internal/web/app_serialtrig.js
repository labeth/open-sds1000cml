// ENGMODEL-OWNER-UNIT: FU-WEB-APP-SERIALTRIG
// app_serialtrig.js — serial-trigger sub-panel, nested INSIDE the decode card.
// It reuses the live decode config (dcfg): protocol, channel roles, baud, bits,
// parity, CPOL/CPHA/MSB and threshold all come from decode, so the operator only
// enters the MATCH pattern here. Decode must be a concrete protocol to arm; the
// sub-panel is inside #decRoles, so it hides automatically when decode is off,
// and turning decode off auto-disarms. Config → POST /api/serial (the scope's
// SerialParams with this page's match and decoder settings), arm → /api/set
// serialmode. The scope is authoritative: the clock rate, the data delay and
// the receiver are the scope's, and the page sends only what is edited here.
"use strict";

// strict hex-token parse: "FF 3C" / "0x24" → [255,60] / 0x24; a token that is
// not 1-2 hex digits returns null so the caller can REJECT (never silently drop
// or truncate — "AA GG BB" must not become [AA,BB]).
// TRLC-LINKS: REQ-SDS-206
function stParseBytes(s, maxValue = 255) {
  const toks = (s || "").trim().split(/[\s,]+/).filter(x => x !== "");
  const out = [];
  for (const t of toks) {
    if (!/^(0x)?[0-9a-fA-F]{1,5}$/.test(t)) return null;
    const value = parseInt(t.replace(/^0x/i, ""), 16);
    if (value > maxValue) return null;
    out.push(value);
  }
  return out.slice(0, 64);
}
// stParsePattern reads the trigger pattern (ADR-PROTOCOL-SEQUENCE-TRIGGER):
// hex units, "??" for any unit, "?" digits as nibble wildcards (A? = A0..AF)
// ERR for a decoder-reported error, SOF for the start of a frame, CMD for a
// MIL-1553 command word and STS for a MIDI status byte (top bit set). Plain hex stays a byte list; any wildcard or token
// makes a token pattern. At most 32 units.
// TRLC-LINKS: REQ-SDS-206
function stParsePattern(s, maxValue = 255) {
  const toks = (s || "").trim().split(/[\s,]+/).filter(x => x !== "");
  if (toks.length > 32) return null;
  const pattern = [];
  let plain = true;
  for (const t of toks) {
    if (/^err(or)?$/i.test(t)) { pattern.push({ kind: 4, value: 0, mask: 0 }); plain = false; continue; }
    if (/^sof$/i.test(t)) { pattern.push({ kind: 1, value: 0, mask: 0 }); plain = false; continue; }
    if (/^cmd$/i.test(t)) { pattern.push({ kind: 2, value: 0x10000, mask: 0x10000 }); plain = false; continue; }
    if (/^sts$/i.test(t)) { pattern.push({ kind: 2, value: 0x80, mask: 0x80 }); plain = false; continue; }
    if (!/^(0x)?[0-9a-fA-F?]{1,5}$/.test(t)) return null;
    const digits = t.replace(/^0x/i, "");
    const value = parseInt(digits.replace(/\?/g, "0"), 16);
    let mask = /^\?+$/.test(digits) ? 0 : parseInt(digits.replace(/[0-9a-fA-F]/g, "F").replace(/\?/g, "0"), 16);
    if (value > maxValue) return null;
    // Leading digits beyond the token's width are compared as zero.
    if (mask !== 0 && !digits.includes("?")) mask = maxValue;
    else if (mask !== 0) mask = (mask | (maxValue & ~(16 ** digits.length - 1))) & maxValue;
    if (digits.includes("?")) plain = false;
    pattern.push({ kind: 2, value, mask });
  }
  return plain ? { bytes: pattern.map(p => p.value), pattern: [] } : { bytes: [], pattern };
}
// TRLC-LINKS: REQ-SDS-206
function stParseAddr(s) {
  s = (s || "").trim();
  if (s === "") return -1; // any
  if (!/^(0x)?[0-9a-fA-F]{1,2}$/.test(s)) return null;
  const v = parseInt(s.replace(/^0x/i, ""), 16);
  return v <= 127 ? v : null;
}

// TRLC-LINKS: REQ-SDS-206
function stProtoNum() {
  return { uart: 1, i2c: 2, spi: 3, manchester: 4, sent: 5, can: 6, mil1553: 7, arinc429: 8, usb: 9, flexray: 10,
    lin: 11, dmx512: 12, midi: 13, modbus: 14, onewire: 15, ws281x: 16, dht: 17, cec: 18, j1850: 19, irnec: 20,
    irsirc: 21, irrc5: 22, irrc6: 23, dali: 24, usbpd: 25, spdif: 26, ps2: 27, swd: 28, mdio: 29, wiegand: 30 }[dcfg.proto] || 0;
}
// TRLC-LINKS: REQ-SDS-206
function stArmed() { return $("stArm") && $("stArm").classList.contains("on"); }

// stScope is the scope's serial trigger as /api/serial last gave it: what the
// page builds on, so the clock rate, the data delay and the receiver set on
// the scope (its rows, AUTO in BUS, a phone) are never replaced by the page's.
let stScope = null;

// build the SerialParams: the scope's (base), with the live decode config and
// the match inputs; returns null if the match pattern has invalid hex.
// TRLC-LINKS: REQ-SDS-206
function stParams(base = stScope) {
  const P = stProtoNum();
  // TRLC-LINKS: REQ-SDS-206
  const rc = r => (r === 2 ? 1 : 0);             // dcfg role 1/2 → engine chan 0/1
  const addr = stParseAddr($("stAddr").value);
  const bits = Math.max(1, Math.min(16, dcfg.bits || 8));
  const maxValue = P === 7 ? 65535 : P === 8 ? 0x7ffff : P === 5 ? 15 : P === 1 || P === 4 ? 2 ** bits - 1 : 255;
  const parsed = stParsePattern($("stBytes").value, maxValue);
  if (addr === null || parsed === null) return null;
  const bytes = parsed.bytes;
  return {
    ...(base || {}),
    proto: P,
    chA: P === 2 ? rc(dcfg.scl) : P === 3 || P >= 27 ? rc(dcfg.clk) : rc(dcfg.line),
    chB: P === 2 ? rc(dcfg.sda) : P === 3 || P >= 27 ? rc(dcfg.data) : 0,
    baud: dcfg.baud > 0 ? dcfg.baud : 0,
    spiClockHz: P === 3 && base ? Math.max(0, base.spiClockHz || 0) : 0, // the scope's Clock rate row (0: Auto)
    bits: dcfg.bits || 8,
    parity: P === 14 ? (dcfg.modbusParity || "even") : dcfg.parity || "none",
    inverted: !!dcfg.inverted,
    cpol: !!dcfg.cpol, cpha: !!dcfg.cpha, msb: P !== 3 || !!dcfg.msb, // the bit order is SPI's; the others MSB first, as the scope decodes them
    ieee: true, tickNs: P === 5 ? +$("decSentTick").value : 0, nibbles: P === 5 ? +$("decSentNibbles").value : 0, dataBaud: 0,
    threshold: dcfg.auto ? 0 : (+$("decThr").value || 0),
    haveThr: !dcfg.auto,
    addr, rw: +$("stRW").value, bytes, pattern: parsed.pattern,
  };
}

// stLastSig is the scope's match as this page last read or sent it: a
// different one read later was set on the scope (the LCD, a phone, AUTO in
// BUS, another page), which ends this page's ownership.
let stLastSig = "";
let stConfigError = "";
// stOwn: this page armed the trigger, or its match was edited here since. Only
// then does the page send an edit or disarm: a trigger armed on the LCD, a
// phone or another page is shown here as the scope has it (stAdopt), never
// replaced by this page's empty match or its own decode settings. A match
// changed on the scope ends it.
let stOwn = false, stAdopting = false;
// stSendGen counts this page's sends: a read begun before one may still hold
// the match it replaced.
let stSendGen = 0;

// stMatchSig is a trigger's match: units, I2C address and direction.
// TRLC-LINKS: REQ-SDS-206
function stMatchSig(p) { return JSON.stringify([p.bytes || [], p.pattern || [], p.addr == null ? -1 : p.addr, p.rw == null ? 2 : p.rw]); }

// stShowClock shows the scope's SPI Clock rate row in the page's field (MHz;
// empty is Auto, its placeholder the rate Auto measured), not while it is
// being typed.
// TRLC-LINKS: REQ-SDS-206, REQ-SDS-229
function stShowClock(hz, text) {
  const el = $("dsClock");
  if (!el || document.activeElement === el) return;
  const v = hz > 0 ? String(+(hz / 1e6).toPrecision(9)) : "";
  if (el.value !== v) el.value = v;
  if (text !== undefined) el.placeholder = hz > 0 ? "" : text;
}
// stSetClock writes the field's clock rate to the scope's Clock rate row, the
// row the LCD and the phone set (empty: Auto).
// TRLC-LINKS: REQ-SDS-206, REQ-SDS-229
function stSetClock() {
  const t = String($("dsClock").value).trim();
  const hz = t === "" ? 0 : Math.round(+t * 1e6);
  if (!Number.isFinite(hz) || hz < 0) { stShowClock(stScope ? stScope.spiClockHz : 0); return Promise.resolve(); }
  return sheetSet("bus.spi_clock", hz).then(() => stAdopt());
}

// stMatchText writes the scope's match as this page types it: hex units, "??"
// any unit, "A?" a nibble wildcard, ERR a decoder error, SOF a frame's start,
// CMD a MIL-1553 command word.
// TRLC-LINKS: REQ-SDS-206
function stMatchText(p) {
  // TRLC-LINKS: REQ-SDS-206
  const hex = (v, n) => v.toString(16).toUpperCase().padStart(n, "0");
  if (p.pattern && p.pattern.length) {
    return p.pattern.map((u) => {
      if (u.kind === 4) return "ERR";
      if (u.kind === 1) return "SOF";
      if (u.mask & u.value & 0x10000) return "CMD";
      if ((u.mask & 0xff) === 0x80 && (u.value & 0x80)) return "STS";
      const n = Math.max(2, (u.value || 0).toString(16).length);
      if (!u.mask) return "?".repeat(n);
      let t = "";
      for (let i = n - 1; i >= 0; i--) t += ((u.mask >> (4 * i)) & 15) === 15 ? hex((u.value >> (4 * i)) & 15, 1) : "?";
      return t;
    }).join(" ");
  }
  return (p.bytes || []).map((b) => hex(b, 2)).join(" ");
}

// stScopeProto is the protocol of the scope's serial trigger (stAdopt), named
// when it was armed on the scope or the phone and this page decodes nothing.
let stScopeProto = 0;
// stAdopt reads the scope's serial trigger: its settings for the page to
// build on and show (the clock rate), and, unless this page owns it, the
// match, I2C address and direction (not while one of them is being typed). A
// match changed on the scope since this page read or sent it ends the
// page's ownership: the scope's is shown from then on.
// TRLC-LINKS: REQ-SDS-206
function stAdopt() {
  if (stAdopting) return Promise.resolve();
  stAdopting = true;
  const gen = stSendGen;
  return fetch("/api/serial").then((r) => r.json()).then((j) => {
    if (!j || !j.ok || gen !== stSendGen) return;
    const p = j.params || {};
    stScope = p;
    stShowClock(p.spiClockHz || 0);
    const sig = stMatchSig(p);
    if (stOwn && stLastSig && sig !== stLastSig) stOwn = false;
    stLastSig = sig;
    if (stOwn) return;
    const a = document.activeElement;
    if (a && (a.id === "stBytes" || a.id === "stAddr" || (a.closest && a.closest("#stRW")))) return;
    stScopeProto = p.proto || 0;
    $("stBytes").value = stMatchText(p);
    $("stAddr").value = p.addr >= 0 ? p.addr.toString(16).toUpperCase().padStart(2, "0") : "";
    $("stRW").value = String(p.rw == null ? 2 : p.rw);
    stStatus();
  }).catch(() => {}).finally(() => { stAdopting = false; });
}
// stFollowScope drops this page's own edits and shows the scope's match
// (Default setup cleared it).
// TRLC-LINKS: REQ-SDS-206
function stFollowScope() { stOwn = false; stLastSig = ""; return stAdopt(); }
let stModePending = null, stModeGeneration = 0, stArmGeneration = 0;
// stPush sends this page's match and decoder settings, on an edit made here,
// over the scope's serial trigger as it is now (read first), so the scope's
// own settings stay.
// TRLC-LINKS: REQ-SDS-206
function stPush() {
  if (!stParams()) return Promise.reject(new Error("bad pattern")); // stStatus() surfaces the invalid hex
  const gen = ++stSendGen;
  return fetch("/api/serial").then((r) => r.json()).then((j) => { if (j && j.ok && j.params) stScope = j.params; }, () => {})
    .then(() => {
      const p = stParams();
      if (!p) throw new Error("bad pattern");
      if (!p.proto) throw new Error("decode off"); // turned off meanwhile: nothing to send
      return fetch("/api/serial", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(p) })
        .then(async r => { const j = await r.json(); if (!r.ok || !j || !j.ok) throw new Error("rejected"); return p; });
    })
    .then((p) => { stScope = p; stLastSig = stMatchSig(p); if (gen === stSendGen) ++stSendGen; if (stConfigError) { stConfigError = ""; stStatus(); } })
    .catch(error => {
      if (error.message !== "decode off") { stConfigError = "Trigger settings were not applied. Check the connection and retry."; stStatus(); }
      throw error;
    });
}

// TRLC-LINKS: REQ-SDS-206
function stSetArmed(on) {
  const generation = ++stModeGeneration;
  stModePending = on ? 1 : 0;
  $("stArm").classList.toggle("on", on);
  return send("serialmode", stModePending).then(r => {
    if (generation !== stModeGeneration) return;
    if (!r.ok) {
      stModePending = null;
      $("stArm").classList.toggle("on", !!(st && st.serial_mode));
      stConfigError = "Trigger mode was not applied. Check the connection and retry.";
      stStatus();
    }
  });
}

// serial is only tested on the real-time bands that flow through the trigger
// gate — envelope/roll/stream/ETS bypass it, so warn when armed on those.
// TRLC-LINKS: REQ-SDS-206
function stBandInactive() {
  return !!(st && (st.band === "envelope" || st.band === "roll" || st.stream || st.ets));
}

// A message (arming cancelled, turn on decode) stays a few seconds over the
// live status, which would otherwise wipe it within a second; an error or
// this page's own arm or edit replaces it (stStatusHeld = 0).
let stStatusHeld = 0;

// TRLC-LINKS: REQ-SDS-206
function stStatus(msg) {
  if (!$("stStats")) return;
  if (msg !== undefined) { stStatusHeld = Date.now() + 3000; $("stStats").textContent = msg; return; }
  if (Date.now() < stStatusHeld && !stConfigError) return;
  if (stProtoNum() && stParams() === null) { $("stStats").textContent = "invalid hex in the match pattern" + (stArmed() ? " · previous settings remain armed" : ""); return; }
  if (stConfigError) { $("stStats").textContent = stConfigError + (stArmed() ? " Previous settings remain armed." : ""); return; }
  if (!stArmed()) { $("stStats").textContent = ""; return; }
  if (stBandInactive()) { $("stStats").textContent = "⚠ inactive on this band (env/roll/stream/ETS)"; return; }
  // The FPGA decides a serial trigger; "none: <why>" is an armed match it
  // cannot take yet (the levels still being learned, no matcher in the image).
  const backendName = (st && st.serial_backend) || "";
  if (backendName.startsWith("none: ")) { $("stStats").textContent = `armed · no hardware trigger yet: ${backendName.slice(6)}`; return; }
  const name = backendName.replace(/^hardware-/, "");
  const protocol = name.replace(/-sequence$/, "");
  const labels = {uart: "UART", i2c: "I²C", spi: "SPI", sent: "SENT", mil1553: "MIL-1553", usbls: "USB", manchester: "Manchester", can: "CAN", arinc429: "ARINC 429", flexray: "FlexRay"};
  const backend = /^hardware-/.test(backendName)
    ? `hardware ${labels[protocol] || protocol}${name.endsWith("-sequence") ? " sequence" : ""}` : "waiting for the scope";
  $("stStats").textContent = `armed · ${backend} · ${(st && st.serial_matches) || 0} matches`;
}

// called by the decode panel (updateDecodePanel) when the decode config changes,
// here or on the scope: disarm if decode was turned off. The bus itself (its
// protocol, lines, mode) the scope gives its armed trigger.
// TRLC-LINKS: REQ-SDS-206
function stOnDecodeChange() {
  if (!$("stArm")) return;
  if (stProtoNum() === 0) { ++stArmGeneration; if (stOwn && (stArmed() || stModePending === 1)) stSetArmed(false); stStatus(""); }
}
// stOnPageEdit sends a decoder setting edited on this page that the scope has
// no row for (UART word and parity, inverted inputs, the threshold, SENT's
// tick and nibbles) to a trigger this page armed.
// TRLC-LINKS: REQ-SDS-206
function stOnPageEdit() {
  if ($("stArm") && stProtoNum() && stArmed() && stOwn) stPush().catch(() => {});
}

// ==== wiring ====
// TRLC-LINKS: REQ-SDS-206
(function initSerialTrig() {
  if (!$("stArm")) return;
  stAdopt(); // the scope's stored match, armed or not
  let arming = 0, cancel = false; // the arm under way (its generation): a bus search and the config push take seconds
  // TRLC-LINKS: REQ-SDS-206
  $("stArm").onclick = async () => {
    // A click while an arm is under way cancels it, not starts an arm of its
    // own (two quick clicks would arm).
    if (arming) { cancel = true; ++stArmGeneration; stStatus("arming cancelled"); return; }
    const on = !stArmed(), generation = ++stArmGeneration;
    if (on) { arming = generation; cancel = false; }
    try {
      // With decoding off, find the bus first (BUS's Trigger on bytes pane): a
      // serial trigger needs the decoder's protocol and settings.
      if (on && stProtoNum() === 0 && typeof decDetect === "function") {
        stStatus("detecting the bus…");
        await decDetect();
        if (generation !== stArmGeneration) return;
      }
      if (stProtoNum() === 0) { stStatus("turn on decode to arm a serial trigger"); return; }
      // Arming a match not edited here arms the scope's stored one (set on the
      // LCD or a phone, kept while disarmed), not "any byte" from the empty
      // field.
      if (on && !stOwn) await stAdopt();
      if (generation !== stArmGeneration) return;
      stOwn = on;
      if (on) {
        try { await stPush(); } catch { stStatusHeld = 0; stStatus(); return; } // install config BEFORE arming; abort on invalid/rejected
      }
      if (generation !== stArmGeneration || stProtoNum() === 0) return;
      await stSetArmed(on);
      stStatusHeld = 0;
      stStatus();
    } finally {
      if (arming === generation) {
        arming = 0;
        // cancelled once the arm was on its way to the scope: take it back
        if (cancel && stArmed()) { stOwn = false; await stSetArmed(false); stStatus("arming cancelled"); }
        cancel = false;
      }
    }
  };
  for (const id of ["stAddr", "stRW", "stBytes"])
    // TRLC-LINKS: REQ-SDS-206
    $(id).onchange = () => { stOwn = true; if (stArmed()) stPush().catch(() => {}); stStatusHeld = 0; stStatus(); };
  $("dsClock").onchange = stSetClock;
})();

// resync + live status: the page follows the scope and sends nothing here.
setInterval(() => {
  if (!$("stArm") || !st || (typeof superseded !== "undefined" && superseded)) return;
  // the scope's trigger, armed or not: its settings, and its match unless
  // this page edited its own (set or cleared elsewhere - the LCD, a phone,
  // Default setup, AUTO - the field follows; armed, the strip's caption
  // names it too)
  if (!document.hidden) stAdopt();
  // Ignore pre-command status polls until one acknowledges the requested mode.
  if (stModePending !== null) {
    if ((st.serial_mode || 0) === stModePending) stModePending = null;
    else { stStatus(); return; }
  }
  // the scope's state, whatever this page decodes: armed on the scope or the
  // phone with this page's decode off it is still armed
  if (st.serial_mode && !stArmed()) $("stArm").classList.add("on");   // engine armed, UI didn't know
  if (!st.serial_mode && stArmed()) { $("stArm").classList.remove("on"); stOwn = false; } // engine disarmed elsewhere
  if (stProtoNum() === 0) { if (stOwn && stArmed()) stSetArmed(false); stStatus(); return; }
  stStatus();
}, 1000);
