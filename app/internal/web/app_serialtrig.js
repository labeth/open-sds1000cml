// app_serialtrig.js — serial-trigger sub-panel, nested INSIDE the decode card.
// It reuses the live decode config (dcfg): protocol, channel roles, baud, bits,
// parity, CPOL/CPHA/MSB and threshold all come from decode, so the operator only
// enters the MATCH pattern here. Decode must be a concrete protocol to arm; the
// sub-panel is inside #decRoles, so it hides automatically when decode is off,
// and turning decode off auto-disarms. Config → POST /api/serial (the whole
// SerialParams, assembled from dcfg + the match), arm → /api/set serialmode.
"use strict";

// strict hex-token parse: "FF 3C" / "0x24" → [255,60] / 0x24; a token that is
// not 1-2 hex digits returns null so the caller can REJECT (never silently drop
// or truncate — "AA GG BB" must not become [AA,BB]).
function stParseBytes(s) {
  const toks = (s || "").trim().split(/[\s,]+/).filter(x => x !== "");
  const out = [];
  for (const t of toks) {
    if (!/^(0x)?[0-9a-fA-F]{1,2}$/.test(t)) return null;
    out.push(parseInt(t.replace(/^0x/i, ""), 16));
  }
  return out.slice(0, 64);
}
function stParseAddr(s) {
  s = (s || "").trim();
  if (s === "") return -1; // any
  if (!/^(0x)?[0-9a-fA-F]{1,2}$/.test(s)) return null;
  const v = parseInt(s.replace(/^0x/i, ""), 16);
  return v <= 127 ? v : null;
}

function stProtoNum() { return { uart: 1, i2c: 2, spi: 3 }[dcfg.proto] || 0; }
function stArmed() { return $("stArm") && $("stArm").classList.contains("on"); }

// build the full SerialParams from the live decode config + the match inputs;
// returns null if the match pattern has invalid hex.
function stParams() {
  const P = stProtoNum();
  const rc = r => (r === 2 ? 1 : 0);             // dcfg role 1/2 → engine chan 0/1
  const addr = stParseAddr($("stAddr").value);
  const bytes = stParseBytes($("stBytes").value);
  if (addr === null || bytes === null) return null;
  return {
    proto: P,
    chA: P === 1 ? rc(dcfg.line) : P === 2 ? rc(dcfg.scl) : rc(dcfg.clk),
    chB: P === 2 ? rc(dcfg.sda) : P === 3 ? rc(dcfg.data) : 0,
    baud: dcfg.baud > 0 ? dcfg.baud : 0,
    bits: dcfg.bits || 8,
    parity: dcfg.parity || "none",
    cpol: !!dcfg.cpol, cpha: !!dcfg.cpha, msb: !!dcfg.msb,
    threshold: dcfg.auto ? 0 : (+$("decThr").value || 0),
    haveThr: !dcfg.auto,
    addr, rw: +$("stRW").value, bytes,
  };
}

let stLastSig = "";
function stPush() {
  const p = stParams();
  if (!p) return Promise.reject(new Error("bad pattern")); // stStatus() surfaces the invalid hex
  stLastSig = JSON.stringify(p);
  return fetch("/api/serial", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(p) })
    .then(r => r.json()).then(j => { if (!j || !j.ok) throw new Error("rejected"); });
}

function stSetArmed(on) { $("stArm").classList.toggle("on", on); send("serialmode", on ? 1 : 0); }

// serial is only tested on the real-time bands that flow through the trigger
// gate — envelope/roll/stream/ETS bypass it, so warn when armed on those.
function stBandInactive() {
  return !!(st && (st.band === "envelope" || st.band === "roll" || st.stream || st.ets));
}

function stStatus(msg) {
  if (!$("stStats")) return;
  if (msg !== undefined) { $("stStats").textContent = msg; return; }
  if (stProtoNum() && stParams() === null) { $("stStats").textContent = "invalid hex in the match pattern"; return; }
  if (!stArmed()) { $("stStats").textContent = ""; return; }
  if (stBandInactive()) { $("stStats").textContent = "⚠ inactive on this band (env/roll/stream/ETS)"; return; }
  $("stStats").textContent = `armed · ${(st && st.serial_matches) || 0} matches`;
}

// called by the decode panel (updateDecodePanel) when the decode config changes:
// disarm if decode was turned off, else re-push the (now different) config.
function stOnDecodeChange() {
  if (!$("stArm")) return;
  if (stProtoNum() === 0) { if (stArmed()) stSetArmed(false); stStatus(""); return; }
  if (stArmed()) stPush().catch(() => {});
}

// ==== wiring ====
(function initSerialTrig() {
  if (!$("stArm")) return;
  $("stArm").onclick = async () => {
    if (stProtoNum() === 0) { stStatus("turn on decode to arm a serial trigger"); return; }
    const on = !stArmed();
    if (on) {
      try { await stPush(); } catch { stStatus(); return; } // install config BEFORE arming; abort on invalid/rejected
    }
    stSetArmed(on);
    stStatus();
  };
  for (const id of ["stAddr", "stRW", "stBytes"])
    $(id).onchange = () => { if (stArmed()) stPush().catch(() => {}); stStatus(); };
})();

// resync + live status (both directions); re-push if the config drifted.
setInterval(() => {
  if (!$("stArm") || !st) return;
  if (stProtoNum() === 0) { if (stArmed()) stSetArmed(false); return; }
  if (st.serial_mode && !stArmed()) $("stArm").classList.add("on");   // engine armed, UI didn't know
  if (!st.serial_mode && stArmed()) $("stArm").classList.remove("on"); // engine disarmed elsewhere
  if (stArmed()) {
    const p = stParams();
    if (p && JSON.stringify(p) !== stLastSig) stPush().catch(() => {}); // decode/match changed
  }
  stStatus();
}, 1000);
