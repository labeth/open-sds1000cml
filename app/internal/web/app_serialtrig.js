// app_serialtrig.js — serial/protocol trigger panel (classic script; shares
// app.js globals). Configures the ENGINE-side serial trigger (serialtrig.go):
// only frames whose decoded UART/I2C/SPI stream contains the pattern publish,
// centred on the match. Config → POST /api/serial; arm → /api/set serialmode.
"use strict";

// "FF 3C" / "0xff,0x3c" → [255,60]; blank → []
function stParseBytes(s) {
  return (s || "").trim().split(/[\s,]+/).filter(x => x !== "")
    .map(x => parseInt(x.replace(/^0x/i, ""), 16))
    .filter(x => Number.isInteger(x) && x >= 0 && x <= 255);
}
// "50" / "0x50" → 0x50 (7-bit); blank/invalid → -1 (any)
function stParseAddr(s) {
  s = (s || "").trim();
  if (s === "") return -1;
  const v = parseInt(s.replace(/^0x/i, ""), 16);
  return (Number.isInteger(v) && v >= 0 && v <= 127) ? v : -1;
}

function stParams() {
  const p = +$("stProto").value;
  return {
    proto: p,
    chA: p === 1 ? +$("stLine").value : p === 2 ? +$("stScl").value : +$("stClk").value,
    chB: p === 2 ? +$("stSda").value : p === 3 ? +$("stData").value : 0,
    baud: Math.max(0, +$("stBaud").value | 0),
    cpol: $("stCpol").value === "1",
    cpha: $("stCpha").value === "1",
    msb: $("stMsb").value === "1",
    addr: stParseAddr($("stAddr").value),
    rw: +$("stRW").value,
    bytes: stParseBytes($("stBytes").value),
  };
}

function stPush() {
  fetch("/api/serial", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(stParams()) }).catch(() => {});
}

// show only the rows relevant to the selected protocol
function updateStPanel() {
  const p = +$("stProto").value;
  $("stRoles").style.display = p === 0 ? "none" : "flex";
  for (const [cls, on] of [["st-uart", p === 1], ["st-i2c", p === 2], ["st-spi", p === 3]])
    for (const el of document.querySelectorAll("." + cls)) el.style.display = on ? "" : "none";
  $("stUartRow").style.display = p === 1 ? "flex" : "none";
  $("stSpiRow").style.display = p === 3 ? "flex" : "none";
  $("stI2cRow").style.display = p === 2 ? "flex" : "none";
  $("stPatRow").style.display = p === 0 ? "none" : "flex";
}

function stStatus(m) { $("stStats").textContent = m; }

// ==== wiring ====
(function initSerialTrig() {
  if (!$("stCard")) return;
  for (const id of ["stLine", "stScl", "stSda", "stClk", "stData"])
    $(id).innerHTML = '<option value="0">C1</option><option value="1">C2</option>';
  $("stSda").value = "1"; $("stData").value = "1"; // SDA/DATA default to C2, clock to C1

  $("stProto").onchange = () => { updateStPanel(); stPush(); };
  for (const id of ["stLine", "stScl", "stSda", "stClk", "stData", "stBaud",
    "stCpol", "stCpha", "stMsb", "stAddr", "stRW", "stBytes"])
    $(id).onchange = stPush;

  $("stArm").onclick = () => {
    const on = !$("stArm").classList.contains("on");
    $("stArm").classList.toggle("on", on);
    if (on) stPush();                 // push the latest config before arming
    send("serialmode", on ? 1 : 0);
    stStatus(on ? "armed — waiting for match" : "off");
  };
  updateStPanel();
})();

// live match count from the engine status poll (st.serial_*)
setInterval(() => {
  if (!st || !$("stCard")) return;
  const armed = $("stArm").classList.contains("on");
  if (st.serial_mode && !armed) $("stArm").classList.add("on"); // resync after a reload
  if (armed || st.serial_mode)
    stStatus(`armed · ${st.serial_matches || 0} matches` + (st.serial_skip ? ` · ${st.serial_skip} skipped` : ""));
}, 1000);
