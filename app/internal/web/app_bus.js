// ENGMODEL-OWNER-UNIT: FU-WEB-APP-BUS
// app_bus.js — the BUS workspace: entering it with decoding off auto-detects the
// bus and turns decoding on (decoding then stays on); the packet table lists
// complete packets as hex plus a separate string column and holds still while
// the same messages repeat; a click zooms onto a packet; BUS again (or the
// pane's view switch) shows the eye diagram of the decoded line, then the
// scope's hardware stream of the bus. Classic script; shares app.js globals.
"use strict";

const busPk = new PacketList(200);
let busLive = [];           // the last capture's packets, cut ones included
let busSel = null;          // {i0, i1, n} of the packet zoomed onto, drawn on the trace
let busRenderT = 0, busRenderedV = -1, busDetecting = false;

// busListKey is the decoder settings and the timebase the packet list belongs
// to: a change (another protocol, rate, line or SPI mode; another timebase,
// whose records place the packets at other samples) starts it over, as the
// scope's list does, so no packet the old settings decoded is listed and no
// row keeps a place from another timebase (rows from a 500us/div record read
// times 0.4 of the true ones at 200us/div).
let busListKey = "";

// busOnDecode runs after every decode (updateDecodeResults): group the result
// into packets and refresh the table when its content changed.
// TRLC-LINKS: REQ-SDS-214
function busOnDecode() {
  const key = [decKey(), dcfg.baud, dcfg.cpol, dcfg.cpha, dcfg.msb, frame && frame.tdiv_s, dcfg.layer, dcfg.layerAddr, dcfg.layerDir, dcfg.parity].join("|");
  if (key !== busListKey) {
    busListKey = key;
    if (busPk.rows.length) busPk.clear();
    busLive = [];
  }
  if (dcfg.proto === "off") {
    if (busPk.rows.length) busPk.clear();
    busRenderTable();
    return;
  }
  const r = dcfg.result;
  if (!r || !r.ok || !frame || !frame.c1) { busRenderTable(); return; }
  if (frozen && dcfg.reviewIdx < 0 && busSel) return; // a zoomed packet holds the list
  busLive = decodePackets(r, frame.c1.length);
  if (dcfg.layer && typeof layerApply === "function") busLive = layerApply(dcfg.layer, r.proto, busLive, { addr: dcfg.layerAddr, dir: dcfg.layerDir });
  busPk.update(busLive);
  busRenderTable();
  if (typeof visOnDecode === "function") visOnDecode();
}

// busShownRows is what the packet table lists: the complete packets kept, or,
// when there are none, the capture's packets cut by the record edges (partial),
// as the scope's list does - a serial trigger at a fast timebase holds no
// whole message, and an empty list hid the bytes the strip shows.
// TRLC-LINKS: REQ-SDS-214
function busShownRows(kept, live) {
  if (kept.length) return { rows: kept, partial: false };
  return { rows: (live || []).filter((p) => p.bytes && p.bytes.length), partial: true };
}
// End of busShownRows.

// busTokens: the serial-trigger pattern as match tokens (rows holding it are marked).
// TRLC-LINKS: REQ-SDS-214
function busTokens() { return ($("stBytes").value || "").trim().split(/[\s,]+/).filter(Boolean).map((t) => t.replace(/^0x/i, "")); }

// busRenderTable rebuilds the rows only when the list changed (new content or
// the match pattern): while traffic repeats nothing in the table moves.
// TRLC-LINKS: REQ-SDS-214
function busRenderTable() {
  const off = dcfg.proto === "off";
  // this runs after every decode: the frame around the rows is written only
  // when it changes
  const empty = $("pkEmpty"), table = $("pktable");
  if (empty.hidden !== !off) empty.hidden = !off;
  if (table.hidden !== off) table.hidden = off;
  const r = dcfg.result;
  if (off) { $("pkNote").textContent = ""; busRenderedV = -1; $("pkBody").innerHTML = ""; return; }
  const toks = busTokens();
  const shown = busShownRows(busPk.rows, busLive);
  const layer = dcfg.layer && typeof LAYERS !== "undefined" ? (LAYERS.find((l) => l[0] === dcfg.layer) || [])[1] : "";
  const strHead = $("pkStrHead");
  if (strHead.textContent !== (layer || "String")) strHead.textContent = layer || "String";
  // TRLC-LINKS: REQ-SDS-214, REQ-SDS-241
  const cell = (p, str) => (layer ? `<td class="str${p.noteErr ? " bad" : ""}" title="${escHTML(p.note || "")}">${escHTML(p.note || "")}</td>` : `<td class="str" title="${escHTML(str)}">${escHTML(str)}</td>`);
  const sig = busPk.version + "|" + toks.join(" ") + "|" + layer + "|" + (r && !r.ok ? r.error : "") +
    (shown.partial ? "|" + shown.rows.map((p) => pkHex(p)).join("/") : "");
  if (sig === busRenderedV) return;
  busRenderedV = sig;
  const tb = $("pkBody");
  const focusId = document.activeElement && document.activeElement.parentElement === tb ? document.activeElement.dataset.id : null;
  if (r && !r.ok) {
    tb.innerHTML = `<tr><td class="e" colspan="6">${escHTML(r.error || "no decode")} — adjust the decoder or the timebase.</td></tr>`;
    $("pkNote").textContent = "";
    return;
  }
  const n = frame && frame.c1 ? frame.c1.length : 0;
  const dt = frame && n ? frameDtS(frame, n) : 0;
  const edge = frame && frame.edge_frac >= 0 ? frame.edge_frac * (n - 1) : 0;
  let html = "";
  if (shown.partial) {
    shown.rows.forEach((p, i) => {
      const t = dt > 0 ? eng((p.i0 - edge) * dt, "s", 3) : "—";
      const addr = pkAddrText(r && r.proto, p);
      const hex = pkHex(p), hit = toks.length && pkMatch(p, toks);
      html += `<tr class="cut${hit ? " hit" : ""}" title="Cut by the record edges"><td class="n">${i + 1}${hit ? " ◆" : ""}</td><td>${t}</td><td class="addr">${addr}</td>` +
        `<td class="hex${p.err ? " bad" : ""}">${escHTML(hex)}</td>${cell(p, pkString(p))}<td>${p.bytes.length}</td></tr>`;
    });
  }
  busPk.rows.forEach((p, i) => {
    const t = dt > 0 ? eng((p.i0 - edge) * dt, "s", 3) : "—";
    const addr = pkAddrText(r && r.proto, p);
    const hex = pkHex(p), str = pkString(p), hit = toks.length && pkMatch(p, toks);
    html += `<tr data-i="${i}" data-id="${p.id}" tabindex="0" class="${busPk.selKey === p.key ? "is-sel" : ""}${hit ? " hit" : ""}" title="Zoom onto this packet (Enter)">` +
      `<td class="n">${i + 1}${hit ? " ◆" : ""}</td><td>${t}</td><td class="addr">${addr}</td>` +
      `<td class="hex${p.err ? " bad" : ""}" title="${escHTML(hex)}">${escHTML(hex) || "(no data bytes)"}</td>` +
      `${cell(p, str)}<td>${p.bytes.length}</td></tr>`;
  });
  if (!shown.rows.length) html = `<tr><td class="e" colspan="6">No packet in this capture yet. A slower timebase shows more of the bus.</td></tr>`;
  tb.innerHTML = html;
  if (focusId) { const row = tb.querySelector(`tr[data-id="${focusId}"]`); if (row) row.focus({ preventScroll: true }); }
  $("pkNote").textContent = shown.partial && shown.rows.length ? "Cut by the record edges: no whole message in this capture. A slower timebase shows them whole." + (toks.length ? " ◆ marks packets holding the trigger pattern." : "") :
    busPk.rows.length ? (frozen ? "Click a packet to zoom onto it." : "Click a packet to zoom onto it (the display freezes so it holds still).") +
    (toks.length ? " ◆ marks packets holding the trigger pattern." : "") : "";
}

// TRLC-LINKS: REQ-SDS-214
function escHTML(s) { return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;"); }

// busZoom zooms the scope onto a packet: the display freezes first (a running
// scope moves its packets with every trigger), the frozen capture is decoded
// again and the packet with the same content nearest the row is framed.
// TRLC-LINKS: REQ-SDS-214
function busZoom(i) {
  const row = busPk.select(i);
  if (!row || !frame || !frame.c1) return;
  if (!frozen) {
    frozen = true; $("freeze").classList.add("on"); refreshAria();
    toast("Display frozen so the packet holds still — Freeze (Z) resumes the live view");
  }
  computeDecode();
  const n = frame.c1.length;
  const cands = decodePackets(dcfg.result, n).filter((p) => p.complete && pkKey(p) === row.key);
  let p = row;
  for (const c of cands) if (p === row || Math.abs(c.i0 - row.i0) < Math.abs(p.i0 - row.i0)) p = c;
  const a = p.i0 / (n - 1), b = p.i1 / (n - 1), pad = Math.max((b - a) * 0.15, 8 / n);
  view.win.a = a - pad; view.win.b = b + pad; view.vwin.a = 0; view.vwin.b = 1;
  userZoomed = true;
  busSel = { i0: p.i0, i1: p.i1, n };
  busRenderedV = -1; busRenderTable();
  clearPersist(); redraw();
}

// busDrawSel outlines the zoomed packet on the decode strip (called by drawDecode).
// TRLC-LINKS: REQ-SDS-214
function busDrawSel(g, bandY, bandH) {
  if (!busSel || !frame || !frame.c1 || frame.c1.length !== busSel.n) return;
  const x0 = xForCol(busSel.i0, busSel.n), x1 = xForCol(busSel.i1, busSel.n);
  if (x1 < 0 || x0 > CW) return;
  g.strokeStyle = TRIGCOL; g.lineWidth = 2 * dpr;
  g.strokeRect(x0 - 2 * dpr, bandY - 4 * dpr, Math.max(4 * dpr, x1 - x0 + 4 * dpr), bandH + 8 * dpr);
  g.lineWidth = dpr;
}

$("pkBody").addEventListener("click", (ev) => {
  const tr = ev.target.closest("tr[data-i]");
  if (tr) busZoom(+tr.dataset.i);
});
$("pkBody").addEventListener("keydown", (ev) => {
  const tr = ev.target.closest("tr[data-i]");
  if (!tr) return;
  if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); ev.stopPropagation(); busZoom(+tr.dataset.i); }
  else if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
    const nx = ev.key === "ArrowDown" ? tr.nextElementSibling : tr.previousElementSibling;
    if (nx && nx.tabIndex === 0) { ev.preventDefault(); nx.focus(); }
  }
});
// Unfreezing (or leaving the zoom) releases the packet highlight.
$("freeze").addEventListener("click", () => { if (!frozen) { busSel = null; busRenderedV = -1; } });

// ---- views: packets / log / eye / stream ----------------------------------------
// The Stream view is the scope's: choosing it puts the scope's BUS on its
// Stream view (the BUS sheet's Stream row), which streams the bus, and another
// view here takes the scope back to its packets. byScope: the scope's view
// changed and the page follows it (nothing is sent back).
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-215, REQ-SDS-228
function busSetView(v, byScope) {
  const was = ws.busView;
  ws.busView = v;
  $("busView").value = v;
  for (const el of document.querySelectorAll("#p-packets .bview")) el.hidden = el.dataset.view !== v;
  $("pkTitle").textContent = { packets: "Packets", log: "Decode log", eye: "Eye diagram", stream: "Stream", visual: "Visual" }[v];
  if (v === "eye") busEyeOn(); else busEyeOff();
  busRoom(v === "eye" || v === "visual");
  if (v === "packets") { busRenderedV = -1; busRenderTable(); }
  if (v === "visual" && typeof visDraw === "function") {
    if (!vis.s || vis.proto !== dcfg.proto) visReset();
    // MIDI is music: follow the scope's gap-free stream (once; the button turns it off)
    if (dcfg.proto === "midi" && !vis.autoStream && !busScopeStreaming()) { vis.autoStream = true; visAskStream(true); }
    visSide(true); setTimeout(() => visDraw(true), 30);
  }
  if (!byScope && v === "stream") busAskStream(true);
  // the Visual view follows the stream of a bus it reads byte for byte: it keeps it
  const keep = v === "visual" && typeof VIS_STREAMED !== "undefined" && VIS_STREAMED.includes(dcfg.proto);
  if (!byScope && was === "stream" && v !== "stream" && !keep) busAskStream(false);
  busScopeStream();
}

// busAskStream puts the scope on its Stream view or back on its packets. Until
// the status shows the scope there (or 5 s pass), a status from before is
// not the scope's view changing.
// TRLC-LINKS: REQ-SDS-228
function busAskStream(on) {
  busStreamAsk = { on, until: Date.now() + 5000 };
  busScopeWas = on;
  sheetSet("bus.stream", on ? 1 : 0);
}

// busScopeStreaming reports the scope's BUS on its Stream view.
// TRLC-LINKS: REQ-SDS-228
function busScopeStreaming() {
  const p = st && st.panel;
  return !!(p && p.WorkspaceID === "bus" && p.View === "Stream");
}
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-215
$("busView").onchange = () => { if (ws.cur !== "bus") setWs("bus"); busSetView($("busView").value); };

// busRoom gives the eye and the Visual view room: the lower pane grows to at
// least half the column while one is shown and returns to its size after.
// TRLC-LINKS: REQ-SDS-215, REQ-SDS-278
function busRoom(room) {
  const n = paneNode($("p-packets"));
  if (!n || !n.parent) return;
  if (room) {
    const col = n.parent.el.getBoundingClientRect().height;
    if (n.sizeBeforeEye === undefined) n.sizeBeforeEye = n.size;
    n.size = Math.max(n.size, Math.round(col * 0.55));
  } else if (n.sizeBeforeEye !== undefined) { n.size = n.sizeBeforeEye; delete n.sizeBeforeEye; }
  applySizes(n.parent);
}

// The eye folds the decoded data line: arm it on that channel when the eye view
// opens (as BUS again does on the scope) and stop it when the view closes, so the
// raw feed it leases is released.
// TRLC-LINKS: REQ-SDS-215
function busEyeOn() {
  const p = dcfg.proto;
  const ch = p === "spi" || DEC_CLOCKED.includes(p) ? dcfg.data : p === "i2c" ? dcfg.sda : p !== "off" ? dcfg.line : +$("ejCh").value;
  if (ej.armed && ej.ch && ej.ch !== ch) ejStop("channel changed");
  $("ejCh").value = String(ch === 2 ? 2 : 1);
  if (!ej.armed) $("ejArm").click();
  setTimeout(() => ejRender(true), 60);
}
// TRLC-LINKS: REQ-SDS-215
function busEyeOff() { if (ej.armed) ejStop("eye view closed"); }

// ---- detection on entry ---------------------------------------------------------
// BUS decodes the bus the scope decodes (the page follows the scope's decoder
// settings: decFollowScope). With the scope's decode off, entering BUS asks
// the scope's Auto to find the bus and fill in its settings, as F2 on the
// scope does; the scope steps its timebase to one that reads the bus.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
async function busDetect(onEntry) {
  if (busDetecting) return;
  busDetecting = true;
  let found = false;
  try {
    // the scope decodes a bus already (a status not yet followed): take it,
    // never detect again over it (that could step the scope's timebase)
    const scope = decScopeSettings(st && st.panel && st.panel.Display);
    if (onEntry && scope && scope.proto !== "off") { decScopeSig = ""; decFollowScope(); return; }
    found = await decDetect();
  } finally { busDetecting = false; }
  if (typeof wsDots === "function") wsDots(); // the tab's dot with the notice
  const note = $("decDetectMsg").textContent;
  if (found) toast("BUS: " + note + " — decoding is on" + (onEntry ? " (it stays on in the other workspaces)" : ""));
  else toast("BUS: " + (note || "no bus detected — choose a protocol in the Decoder pane"), "warn");
  busRenderTable();
}
// TRLC-LINKS: REQ-SDS-214
$("pkDetect").onclick = () => busDetect(false);

WS_HOOKS.push({
  // TRLC-LINKS: REQ-SDS-214
  enter(id) {
    if (id !== "bus") return;
    // the bus decoded, when one is, is kept (and a scope streaming is not
    // stopped); else the scope's Auto finds it
    if (dcfg.proto === "off" && !(st && st.stream)) busDetect(true);
    if (ws.busView === "eye") busEyeOn();
    busRenderedV = -1; busRenderTable();
  },
  // TRLC-LINKS: REQ-SDS-214
  leave(prev) { if (prev === "bus") busEyeOff(); },
});

// ---- the scope's own stream ----------------------------------------------------------
// While the scope streams its bus in hardware (its BUS Stream view, entered on
// the scope, the phone or here) the Stream view shows it: its status and newest
// transcript lines as the LCD's list writes them. The page follows the scope:
// the scope entering its Stream view shows this one, leaving it the packets.
let busStreamBusy = false, busScopeWas = false, busStreamAsk = null;
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-228
async function busScopeStream() {
  const on = busScopeStreaming();
  if (busStreamAsk && (on === busStreamAsk.on || Date.now() > busStreamAsk.until)) busStreamAsk = null;
  if (!busStreamAsk && on !== busScopeWas) {
    busScopeWas = on;
    // the Visual view follows the scope's stream itself: it stays
    if (on && ws.busView !== "stream" && ws.busView !== "visual") busSetView("stream", true);
    else if (!on && ws.busView === "stream") busSetView("packets", true);
  }
  const d = st && st.panel && st.panel.Display;
  $("streamEmpty").hidden = !(ws.busView === "stream" && d && d.DecProto === 0 && !d.DecDetecting);
  const s = st && st.stream, box = $("scopeStream");
  if (!s || !s.state) { if (!box.hidden) { box.hidden = true; $("scopeStreamText").textContent = ""; } return; }
  box.hidden = false;
  const head = "On the scope: " + s.state + " · " + s.label + " · " + s.units + " units" + (s.units_per_s > 0 ? " · " + eng(s.units_per_s, "/s", 3) : "") +
    " · err " + s.errors + " · lost " + s.lost + (s.mode ? " · " + s.mode : "") + (s.err ? " · " + s.err : "") + (s.note ? " · " + s.note : "");
  if ($("scopeStreamHead").textContent !== head) $("scopeStreamHead").textContent = head;
  if (ws.cur !== "bus" || busStreamBusy || document.hidden) return;
  busStreamBusy = true;
  try {
    const r = await fetch("/api/stream/window?rows=12");
    if (r.ok) {
      const p = await r.json(), t = [p.Header, ...(p.Lines || [])].join("\n");
      if ($("scopeStreamText").textContent !== t) $("scopeStreamText").textContent = t;
    }
  } catch (e) { /* the next tick reads again */ } finally { busStreamBusy = false; }
}
setInterval(busScopeStream, 500);
// TRLC-LINKS: REQ-SDS-228
$("streamStops").onchange = () => sheetSet("bus.stream_trigger", +$("streamStops").value);
// TRLC-LINKS: REQ-SDS-228
$("streamDetect").onclick = () => busDetect(false);

// ---- trigger on bytes --------------------------------------------------------------
// TRLC-LINKS: REQ-SDS-206
function busInsert(tok) {
  const el = $("stBytes"), v = el.value.trim();
  el.value = (v ? v + " " : "") + tok;
  el.dispatchEvent(new Event("change", { bubbles: true }));
  busRenderedV = -1; busRenderTable();
}
// TRLC-LINKS: REQ-SDS-206
$("stInsAny").onclick = () => busInsert("??");
// TRLC-LINKS: REQ-SDS-206
$("stInsErr").onclick = () => busInsert("ERR");
// TRLC-LINKS: REQ-SDS-206
$("stClear").onclick = () => { $("stBytes").value = ""; $("stBytes").dispatchEvent(new Event("change", { bubbles: true })); busRenderedV = -1; busRenderTable(); };
// Enter in the match field arms the trigger (detecting the bus first when decoding is off).
$("stBytes").addEventListener("keydown", (ev) => {
  if (ev.key !== "Enter") return;
  ev.preventDefault();
  if (!stArmed()) $("stArm").click(); else $("stBytes").dispatchEvent(new Event("change", { bubbles: true }));
});
$("stBytes").addEventListener("input", () => { busRenderedV = -1; busRenderTable(); });

// busSummary: one line saying what the serial trigger does now (and why it cannot fire).
// TRLC-LINKS: REQ-SDS-206
function busSummary() {
  const el = $("stSum");
  const armed = stArmed(), pat = $("stBytes").value.trim() || "any unit";
  let cls = "tsum", txt;
  const scopeProto = ["", "UART", "I²C", "SPI", "Manchester", "SENT", "CAN", "MIL-1553", "ARINC 429", "USB", "FlexRay",
    "LIN", "DMX512", "MIDI", "Modbus RTU", "1-Wire", "WS281x", "DHT", "HDMI-CEC", "J1850 VPW", "IR NEC", "IR Sony", "IR RC-5",
    "IR RC-6", "DALI", "USB PD", "S/PDIF", "PS/2", "SWD", "MDIO", "Wiegand", "Quadrature", "Stepper", "PWM", "RC encode", "OOK",
    "Miller", "Morse", "DCF77", "EM4100", "EM4305", "T55xx", "SWIM", "SDQ", "PJON", "DSI", "Qi", "SD card", "PDI", "cJTAG", "Maple",
    "Caliper", "IR multi"][stScopeProto] || "";
  if (!armed) txt = "Not armed — the trigger is " + ["Edge", "Pulse", "Slope", "Video"][(st && st.trig_type) || 0] + " on C" + ((st && st.trig_source) === 1 ? 2 : 1) + ".";
  else if (dcfg.proto === "off" && !stOwn && scopeProto) { cls += " is-on"; txt = "Armed on the scope: triggering on " + pat + " in its " + scopeProto + " decode (decode here to list the packets)."; }
  else if (dcfg.proto === "off") { cls += " is-warn"; txt = "Armed, but decoding is off — choose a protocol."; }
  else if (stBandInactive()) { cls += " is-warn"; txt = "Armed, but inactive on this band (envelope, roll, stream or ETS)."; }
  else { cls += " is-on"; txt = "Armed: triggering on " + pat + " in the " + $("decProto").selectedOptions[0].textContent + " decode."; }
  if (el.className !== cls) el.className = cls;
  if (el.textContent !== txt) el.textContent = txt;
  // the same state, briefly, in SCOPE's trigger pane
  const note = $("serialNote");
  note.hidden = !armed;
  if (armed) note.textContent = "Serial trigger armed on " + pat + " — edit it in BUS (Alt+2).";
}

// TRLC-LINKS: REQ-SDS-214
function busStatusLine() {
  const p = dcfg.proto, r = dcfg.result;
  let t = "decode off";
  if (p !== "off") {
    const name = $("decProto").selectedOptions[0].textContent;
    t = name + (r && r.ok ? " · " + r.bytes.length + " units" + (r.meta && r.meta.baud ? " · " + r.meta.baud + " bd" : "") : r ? " · " + (r.error || "no decode") : "");
  }
  if ($("busSub").textContent !== t) $("busSub").textContent = t;
  const d = p === "off" ? "off" : $("decProto").selectedOptions[0].textContent;
  if ($("decSub").textContent !== d) $("decSub").textContent = d;
}
setInterval(() => { busSummary(); busStatusLine(); }, 500);
