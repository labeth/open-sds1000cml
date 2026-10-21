// ENGMODEL-OWNER-UNIT: FU-WEB-APP-ALLSET
// app_allset.js — "All settings" (Ctrl+,): every control of the app in one
// grouped, filterable list beside any workspace. Each row is a live mirror of
// the control (app_ui.js), so an edit here is the same edit as in its pane, and
// its arrow jumps to where the control lives (the pane flashes, the control
// takes focus). Also the top bar's Setups popover and Default setup. Classic
// script; shares app.js globals.
"use strict";

// The catalogue: [id, label, keywords]. Grouped; together with ALL_EXCLUDE it
// names every control in the page (allsettings_browser.mjs checks that).
const ALL_GROUPS = [
  ["Run control", [["run", "Run / Stop", "acquisition start halt space"], ["single", "Single shot", "one capture"], ["autoset", "Autoset", "auto fit"]]],
  ["Timebase & horizontal", [["tdiv", "Time/div", "timebase sweep horizontal scale", ["tdivDn", "tdivUp"]], ["tpos", "Trigger position", "horizontal position delay"],
    ["tposCentre", "Trigger to centre", "position"], ["zoomSeg", "Zoom", "magnify window"], ["rollStream", "Roll mode", "chunked stream slow"]]],
  ["Channel 1", [["tC1", "CH1 display", "show hide on off"], ["vdiv1", "CH1 V/div", "volts scale sensitivity", ["vdiv1Dn", "vdiv1Up"]], ["off1", "CH1 offset", "position"],
    ["zero1", "CH1 offset to 0 V", "zero"], ["cpl1", "CH1 coupling", "dc ac gnd"], ["probe1", "CH1 probe", "attenuation 10x"],
    ["bwl1", "CH1 bandwidth limit", "20 mhz bwl filter"], ["inv1", "CH1 invert", "upside down polarity"]]],
  ["Channel 2", [["tC2", "CH2 display", "show hide on off"], ["vdiv2", "CH2 V/div", "volts scale sensitivity", ["vdiv2Dn", "vdiv2Up"]], ["off2", "CH2 offset", "position"],
    ["zero2", "CH2 offset to 0 V", "zero"], ["cpl2", "CH2 coupling", "dc ac gnd"], ["probe2", "CH2 probe", "attenuation 10x"],
    ["bwl2", "CH2 bandwidth limit", "20 mhz bwl filter"], ["inv2", "CH2 invert", "upside down polarity"]]],
  ["Trigger", [["ttype", "Trigger type", "edge pulse slope video"], ["source", "Trigger source", "c1 c2"], ["slope", "Trigger slope", "rising falling edge"],
    ["lvl", "Trigger level", "threshold"], ["set50", "Level to 50 %", "fifty"], ["mode", "Trigger mode", "auto normal norm"], ["force", "Force trigger", ""],
    ["holdoff", "Holdoff", "rearm"], ["p-cond", "Pulse condition", "width qualifier"], ["p-min", "Pulse min width", "qualifier µs"], ["p-max", "Pulse max width", "qualifier µs"],
    ["p-lvl", "Pulse threshold", "qualifier percent"], ["s-cond", "Slope condition", "edge time qualifier"], ["s-min", "Slope min time", "qualifier"], ["s-max", "Slope max time", "qualifier"],
    ["s-lo", "Slope low threshold", "qualifier percent"], ["s-hi", "Slope high threshold", "qualifier percent"], ["v-std", "Video standard", "pal ntsc"],
    ["v-neg", "Video sync", "negative positive polarity"], ["v-line", "Video line", "tv"]]],
  ["Acquisition", [["acq", "Acquisition mode", "normal average eres peak precision"], ["acqn", "Averages / ERes length", "count"], ["memdepth", "Memory depth", "record length deep"],
    ["precisionRate", "Precision rate", "samples per second"], ["ets", "Equivalent time (ETS)", "sampling"]]],
  ["Display", [["viewScope", "Scope view", "y-t x-y xy lissajous"], ["tCursors", "Cursors", "measure time voltage"], ["tPersist", "Persistence", "afterglow"],
    ["freeze", "Freeze display", "hold"]]],
  ["Measure", [["measView", "Measurement view", "statistics values"], ["measCh", "Statistics channels", "c1 c2"], ["statsReset", "Reset statistics", "zero clear"]]],
  ["Spectrum", [["viewSpec", "Spectrum view", "fft spectrogram bode frequency"], ["pkCh", "Peaks channel", "fft"], ["fftN", "Peaks listed", "top count"],
    ["fftClear", "Clear picked peaks", "fft"], ["spgArm", "Spectrogram", "waterfall run"], ["spgCh", "Spectrogram channel", ""], ["spgFloor", "Spectrogram floor", "db"],
    ["spgClear", "Clear spectrogram", "waterfall"], ["bodeArm", "Bode / FRA", "frequency response sweep"], ["bodeRef", "Bode reference channel", "fra"],
    ["bodeDut", "Bode DUT channel", "fra"], ["bodeClear", "Clear Bode points", "fra"]]],
  ["Decode (BUS)", [["decProto", "Protocol", "decode uart i2c spi can sent manchester"], ["decDetect", "Auto-detect the bus", "find protocol"], ["busView", "BUS view", "packets log eye stream"], ["streamStops", "What stops the scope's stream", "stream trigger error value hand"],
    ["decScl", "I²C SCL channel", "i2c clock"], ["decSda", "I²C SDA channel", "i2c data"], ["decClk", "Clock channel (SPI, PS/2, SWD, MDIO, SD, PDI, cJTAG, Maple, caliper; Wiegand D0; encoder A; STEP)", ""], ["decData", "Data channel (SPI, PS/2, SWD, MDIO, SD, PDI, cJTAG, Maple, caliper; Wiegand D1; encoder B; DIR)", ""],
    ["decLine", "Decode line", "uart channel"], ["decBaud", "Baud / bit rate", "uart speed"], ["decBits", "Data bits", "uart word"], ["decParity", "UART parity", "none even odd"], ["decLayer", "Bus layer", "layer chip flash eeprom rtc itm sbus edid"],
    ["decSentTick", "SENT tick", "ns"], ["decSentNibbles", "SENT nibbles", ""], ["decCpol", "SPI CPOL", "mode"], ["decCpha", "SPI CPHA", "mode"], ["decMsb", "SPI bit order", "msb lsb"],
    ["dsClock", "SPI clock rate", "hz mhz frequency auto"], ["decInverted", "Inverted inputs", "polarity"], ["decAuto", "Auto threshold", "level"], ["decThr", "Decode threshold", "level"],
    ["decFmt", "Decode strip format", "hex ascii string"], ["decStream", "Stream log", "history stitch"], ["decHistClear", "Clear decode log", ""],
    ["decodeCopy", "Copy transcript", "clipboard"], ["decWatch", "Watch decoded windows", "capture review"], ["decWatchErr", "Watch errors", ""],
    ["decWatchMatch", "Watch match", "regex text"], ["capLive", "Back to live (captures)", "review"], ["capCopy", "Copy captures", ""], ["capClear", "Clear captures", ""]]],
  ["Serial trigger", [["stBytes", "Match bytes", "pattern hex trigger on bytes"], ["stInsAny", "Insert any unit (??)", "wildcard"], ["stInsErr", "Insert error (ERR)", ""],
    ["stClear", "Clear match", ""], ["stAddr", "I²C address", "i2c"], ["stRW", "I²C direction", "read write"], ["stArm", "Trigger on match", "arm serial"],
    ["dsStart", "Stream + trigger (retained capture)", "decoded capture"], ["dsRaw", "Download raw capture", ""], ["dsEnd", "End capture session", ""]]],
  ["Eye diagram", [["ejArm", "Eye / jitter", "arm eye diagram tie"], ["ejCh", "Eye channel", ""], ["ejOpt", "Optimize for the eye", "timebase"], ["ejReset", "Reset the eye", ""]]],
  ["Mask & zones", [["zmBuild", "Build mask", "golden"], ["zmN", "Mask captures", "n"], ["zmCh", "Mask channel", ""], ["zmTolT", "Mask time tolerance", "samples"],
    ["zmTolV", "Mask voltage tolerance", "codes"], ["zmMode", "Mask test", "count stop on fail"], ["zmClearStats", "Reset mask counts", "pass fail"],
    ["zmClearMask", "Clear mask", "remove delete"],
    ["zmDraw", "Draw zone", "zone trigger"], ["zmTrig", "Zone trigger", ""], ["zmClearZones", "Clear zones", ""]]],
  ["References & math", [["refSaveA", "Save REF A", "reference"], ["refShowA", "Show REF A", ""], ["refClearA", "Clear REF A", ""], ["refSaveB", "Save REF B", "reference"],
    ["refShowB", "Show REF B", ""], ["refClearB", "Clear REF B", ""], ["masterBtn", "Stack master", "average golden reference mask"], ["masterN", "Master captures", "average count"],
    ["mathFn", "Math function", "c1+c2 c1-c2 product residual"]]],
  ["Super-res", [["srArm", "Super-res stacking", "arm stack"], ["srShow", "View the stack", "review"], ["srAutoGate", "Autogate", "gate"], ["srGate", "Gate markers", ""],
    ["srFit", "Fit model (to REF B)", ""], ["srReset", "Reset the stack", ""], ["srPreset", "Super-res preset", "bandwidth enob"], ["srCh", "Super-res channel", "align"],
    ["srK", "Super-res grid", "factor"], ["srStopMode", "Stop on", "bits stacks time"], ["srStopVal", "Stop target", ""], ["srKernel", "Kernel", "interp cubic drizzle"],
    ["srDither", "Dither", ""], ["srEts", "Clock ETS", "free-run clock"], ["srEtsFreq", "ETS clock frequency", "mhz"], ["srEvt", "Decode-triggered stack", "uart byte"],
    ["srEvtByte", "Decode-trigger byte", ""], ["srComp", "BW compensation", "bandwidth"], ["srCompBw", "BW target", ""], ["srFpga", "FPGA stack", "stacking image"],
    ["srFpgaN", "FPGA records", ""], ["srFpgaMatch", "FPGA shape match", ""], ["srFpgaPts", "FPGA match points", ""], ["srFpgaTol", "FPGA match tolerance", ""]]],
  ["Precision", [["precisionModel", "Fit model", "sine triangle periodic"], ["precisionChannel", "Fit channel", ""], ["precisionFit", "Fit current view", "residual"]]],
  ["Sound", [["sndListen", "Listen", "audio play headphones probe tracer"], ["sndSrc", "Listen source", "c1 c2 stereo a-b difference"],
    ["sndMute", "Mute", "audio"], ["sndVol", "Listen volume", "audio"], ["sndGainMode", "Listen gain", "auto manual normalise"], ["sndGain", "Manual gain", "db"],
    ["sndHP", "High-pass 20 Hz", "dc block filter"], ["sndLP", "Low-pass 5 kHz", "filter"], ["sndNotch", "Notch the fundamental", "residual distortion"],
    ["sndView", "Sound view", "cycle envelope modulation spectrum a/b take"], ["sndCycles", "Cycles drawn", "pitch-locked waveform"], ["sndCh", "Tuned channel", "tuner pitch"],
    ["sndA4", "A4 reference", "tuner pitch 440 432"], ["sndRange", "Tuner range", "bass high"], ["sndSlow", "Tuner smoothing", "fast slow"],
    ["sndRef", "Meter 0 dB", "dbu dbv eurorack line guitar level"], ["sndRead", "Sound readouts", "adsr v/oct drift"], ["sndArm", "Arm the envelope capture", "adsr gate"],
    ["sndGate", "Envelope trigger", "gate signal level"], ["sndGateV", "Envelope trigger level", "volts"], ["sndEnvCV", "Envelope of", "tone cv"],
    ["sndVTake", "Take a V/oct point", "vco tracking cv"], ["sndVUndo", "Undo the last V/oct point", ""], ["sndVClear", "Clear the V/oct table", ""],
    ["sndVAuto", "V/oct points", "automatic by hand"], ["sndDriftOn", "Drift log", "pitch warm-up"], ["sndDriftClear", "Clear the drift log", ""],
    ["sndRun", "Sound RUN/STOP", "take freeze stop live"], ["sndSingle", "Sound SINGLE", "take trigger one shot"], ["sndTrigMode", "Sound trigger mode", "auto normal take"],
    ["sndPre", "Take pre-trigger", "seconds before"], ["sndPost", "Take post-trigger", "seconds after"], ["sndTrigFrom", "Take trigger", "trig edge gate"],
    ["sndLoop", "Take loop", "listen replay"], ["sndSpeed", "Take playback speed", "half quarter slow listen"], ["sndTakeTrig", "Take: trigger to centre", ""],
    ["sndTakeAll", "Take: all of it", "zoom out"]]],
  ["Export & system", [["ePNG", "Export PNG", "image screenshot"], ["eCSV", "Export CSV", "data"], ["eSR", "Export sigrok .sr", "pulseview"], ["eVCD", "Export VCD", "gtkwave"],
    ["eWAV", "Export WAV", "audio"], ["ePrint", "Device screenshot (PRINT)", "lcd"], ["defaultBtn", "Default setup", "reset factory"], ["helpBtn", "Help", "keys mouse"]]],
];
// Controls that are not settings: navigation, menus, dialogs and the read-only
// decode transcript.
const ALL_EXCLUDE = new Set(["decodeText", "tab-scope", "tab-bus", "tab-spectrum", "tab-measure", "tab-compare", "tab-sound", "allBtn", "allClose", "allFilter", "helpClose",
  "exportBtn", "setupsBtn", "quickMore", "pkDetect", "streamDetect", "tdivDn", "tdivUp", "vdiv1Dn", "vdiv1Up", "vdiv2Dn", "vdiv2Up", "mYT", "mXY", "mFFT", "mSPG", "mBODE"]);

let allBuilt = false, allW = 380;
// TRLC-LINKS: REQ-SDS-219
function allEntries() { const out = []; for (const [g, list] of ALL_GROUPS) for (const e of list) out.push({ group: g, id: e[0], label: e[1], kw: e[2] || "", also: e[3] || [] }); return out; }

// ALL_WHEN says when a control that is shown only in some settings shows: by
// the class or id of the element that hides it.
const ALL_WHEN = [
  [".dec-i2c", "with I²C decoding"], [".dec-spi", "with SPI decoding"], [".dec-clkdata", "with SPI or a clocked two-line bus (PS/2, SWD, MDIO, Wiegand, encoder, stepper, SD, PDI, cJTAG, Maple, caliper)"], [".dec-uart", "with UART decoding"],
  [".dec-sent", "with SENT decoding"], [".dec-word", "with UART or Manchester decoding"],
  [".dec-rate", "with a one-line protocol whose rate is a setting (UART, Manchester, CAN, MIL-1553, ARINC, USB, FlexRay, LIN, Modbus)"],
  [".dec-single", "with a one-line protocol"], [".dec-polarity", "with UART, I²C, SPI, SENT or MIL-1553 decoding"],
  ["#decRoles", "once a decode protocol is chosen"], ["#acqnRow", "in Average, ERes or Precision acquisition"],
  ["#precisionRateLabel", "in Precision acquisition"], ["#capLive", "while a captured window is reviewed"],
];

// allGo shows where a control lives: its workspace, pane and view (or its
// menu); the pane flashes and the control takes focus. Strip and top-bar
// controls flash in place. A control shown only in some settings (a trigger
// qualifier, a protocol's role) lands on what selects it, or says when it shows
// — its row here edits it either way.
// TRLC-LINKS: REQ-SDS-219
function allGo(id) {
  const el = $(id);
  if (!el) return false;
  const label = (allEntries().find((e) => e.id === id) || { label: id }).label;
  if (!revealControl(el)) {
    // a trigger qualifier shows only under its trigger type: land on the type
    const q = el.closest(".qb");
    if (q) { revealControl($("ttype")); flash($("p-trig")); $("ttype").querySelector("button.on, button").focus(); toast("That setting shows when the trigger type is " + { "qp-pulse": "Pulse", "qp-slope": "Slope", "qp-video": "Video" }[q.id] + " — pick it here"); return true; }
    return false;
  }
  const pane = el.closest(".pane") || el.closest(".pop") || el.closest(".sg") || el;
  flash(pane);
  if (!el.getClientRects().length) {
    let h = el;
    while (h && h !== pane && !(h.hidden || getComputedStyle(h).display === "none")) h = h.parentElement;
    const w = h && ALL_WHEN.find(([sel]) => h.matches(sel));
    toast(label + " shows " + (w ? w[1] : "when it applies") + ". Its row in All settings edits it now.");
    return true;
  }
  const f = el.classList.contains("seg") ? (el.querySelector("button.on") || el.querySelector("button")) : el;
  try { f.focus({ preventScroll: false }); } catch (e) { /* hidden */ }
  return true;
}

// TRLC-LINKS: REQ-SDS-219
function allBuild() {
  allBuilt = true;
  const list = $("allList");
  for (const [g, entries] of ALL_GROUPS) {
    const sec = document.createElement("section");
    sec.className = "ag"; sec.dataset.group = g;
    const head = document.createElement("button");
    head.type = "button"; head.className = "agh"; head.setAttribute("aria-expanded", "true");
    head.innerHTML = `<span>▾</span><span>${g}</span><span class="agn">${entries.length}</span>`;
    const box = document.createElement("div");
    box.className = "agb";
    // TRLC-LINKS: REQ-SDS-219
    head.onclick = () => { box.hidden = !box.hidden; head.setAttribute("aria-expanded", String(!box.hidden)); head.firstChild.textContent = box.hidden ? "▸" : "▾"; };
    sec.append(head, box);
    for (const [id, label, kw, also] of entries) {
      const src = $(id);
      if (!src) continue;
      const row = document.createElement("div");
      row.className = "ar"; row.dataset.entry = id;
      row.dataset.text = (label + " " + g + " " + id + " " + (kw || "")).toLowerCase();
      const l = document.createElement("span");
      l.className = "al"; l.textContent = label; l.title = label;
      const c = document.createElement("span");
      c.className = "ac";
      if (also && also[0]) c.appendChild(mirrorOf(also[0]));
      c.appendChild(mirrorOf(src, { ariaLabel: label }));
      if (also && also[1]) c.appendChild(mirrorOf(also[1]));
      const go = document.createElement("button");
      go.type = "button"; go.className = "go"; go.textContent = "→";
      go.setAttribute("aria-label", "Show " + label + " where it lives");
      go.title = "Show where it lives";
      // TRLC-LINKS: REQ-SDS-219
      go.onclick = () => allGo(id);
      row.append(l, c, go);
      box.appendChild(row);
    }
    list.appendChild(sec);
  }
  // Save/recall setups by slot
  const sec = document.createElement("section");
  sec.className = "ag"; sec.dataset.group = "Setups";
  sec.innerHTML = `<button type="button" class="agh" aria-expanded="true"><span>▾</span><span>Setups</span><span class="agn">20</span></button>` +
    `<div class="agb"><div class="ar" data-text="setup save recall slot memory store load"><span class="al">Setup slot</span><span class="ac">` +
    `<select id="allSlot" aria-label="Setup slot">${Array.from({ length: 20 }, (_, i) => `<option value="${i + 1}">Slot ${i + 1}</option>`).join("")}</select>` +
    `<button type="button" class="xs" id="allSave">Save</button><button type="button" class="xs" id="allRecall">Recall</button></span></div></div>`;
  list.appendChild(sec);
  // TRLC-LINKS: REQ-SDS-219
  $("allSave").onclick = () => setupOp("save", +$("allSlot").value);
  // TRLC-LINKS: REQ-SDS-219
  $("allRecall").onclick = () => setupOp("recall", +$("allSlot").value);
  syncMirrors();
}

// TRLC-LINKS: REQ-SDS-219
function allFilterApply() {
  const words = $("allFilter").value.toLowerCase().split(/\s+/).filter(Boolean);
  let shown = 0;
  for (const sec of $("allList").querySelectorAll(".ag")) {
    let n = 0;
    for (const r of sec.querySelectorAll(".ar")) {
      const hit = !words.length || words.every((w) => r.dataset.text.includes(w));
      r.hidden = !hit;
      if (hit) n++;
    }
    sec.hidden = !n;
    const cnt = sec.querySelector(".agn");
    if (cnt) cnt.textContent = words.length ? n + " found" : String(sec.querySelectorAll(".ar").length);
    if (words.length && n) { const b = sec.querySelector(".agb"); b.hidden = false; }
    shown += n;
  }
  $("allNone").hidden = shown > 0;
  $("allNone").textContent = 'No setting matches "' + $("allFilter").value.trim() + '". Try a shorter word: trig, ch1, decode, cursor.';
}

// allFit keeps the workspace at least as wide as its panes need.
// TRLC-LINKS: REQ-SDS-219
function allFit() {
  if ($("allcol").hidden || !ws.cur || !ws.trees[ws.cur]) return;
  const inner = $("wsbody").clientWidth - 12;
  const need = minOf(ws.trees[ws.cur], "row");
  const w = Math.max(280, Math.min(allW, inner - PGAP - need, 640));
  $("allcol").style.width = w + "px";
}

// TRLC-LINKS: REQ-SDS-219
function allToggle(force) {
  const open = force === undefined ? $("allcol").hidden : !!force;
  if (open && !allBuilt) allBuild();
  $("allcol").hidden = !open; $("allgut").hidden = !open;
  $("allBtn").classList.toggle("on", open);
  $("allBtn").setAttribute("aria-pressed", String(open));
  if (open) { allFit(); syncMirrors(); $("allFilter").focus(); }
}
// TRLC-LINKS: REQ-SDS-219
$("allBtn").onclick = () => allToggle();
// TRLC-LINKS: REQ-SDS-219
$("allClose").onclick = () => allToggle(false);
$("allFilter").addEventListener("input", allFilterApply);
$("allFilter").addEventListener("keydown", (ev) => { if (ev.key === "Escape" && $("allFilter").value) { $("allFilter").value = ""; allFilterApply(); ev.stopPropagation(); } });
$("allgut").addEventListener("pointerdown", (ev) => {
  if (ev.button !== 0) return;
  ev.preventDefault();
  const x0 = ev.clientX, w0 = $("allcol").getBoundingClientRect().width;
  try { $("allgut").setPointerCapture(ev.pointerId); } catch (e) { /* synthetic */ }
  // TRLC-LINKS: REQ-SDS-219
  const move = (e2) => { allW = Math.max(280, Math.min(640, w0 - (e2.clientX - x0))); allFit(); };
  // TRLC-LINKS: REQ-SDS-219
  const end = () => { $("allgut").removeEventListener("pointermove", move); $("allgut").removeEventListener("pointerup", end); };
  $("allgut").addEventListener("pointermove", move); $("allgut").addEventListener("pointerup", end);
});
$("allgut").addEventListener("keydown", (ev) => {
  if (ev.key !== "ArrowLeft" && ev.key !== "ArrowRight") return;
  ev.preventDefault();
  allW = Math.max(280, Math.min(640, allW + (ev.key === "ArrowLeft" ? 1 : -1) * (ev.shiftKey ? 64 : 16)));
  allFit();
});
window.addEventListener("resize", allFit);

// ---- setups ------------------------------------------------------------------------
// setupOp saves or recalls a whole setup in the scope's numbered slots (the
// same store as the front panel's SAVE/RECALL and SCPI *SAV/*RCL).
// TRLC-LINKS: REQ-SDS-219
async function setupOp(op, slot) {
  try {
    const r = await fetch("/api/setup", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ op, slot }) });
    const j = await r.json().catch(() => ({}));
    if (!r.ok || !j.ok) { toast((op === "save" ? "Save" : "Recall") + " slot " + slot + ": " + (j.err || "setup memory is not available on this device"), "warn"); return false; }
    toast(op === "save" ? "Setup saved in slot " + slot : "Setup " + slot + " recalled");
    kickStatus();
    setupsRefresh();
    return true;
  } catch (e) { toast("Setups: no connection", "err"); return false; }
}
// setupsRefresh shows what each slot holds, as the scope's FILE tab does: an
// empty slot cannot be recalled, and saving over a used one replaces it (the
// tiles named no slot's setup, so the owner's could be overwritten unseen).
// TRLC-LINKS: REQ-SDS-219
async function setupsRefresh() {
  let slots = null;
  try { const j = await (await fetch("/api/setup")).json(); if (j && j.ok && Array.isArray(j.slots)) slots = j.slots; } catch (e) { /* the tiles stay plain */ }
  for (const el of document.querySelectorAll("#setSlots [data-slot]")) {
    const n = +el.dataset.slot, s = slots && slots.find((x) => x.slot === n), used = !!(s && s.used);
    const info = el.querySelector(".i");
    info.textContent = slots ? (used ? s.summary || "used" : "empty") : "";
    info.title = info.textContent;
    el.querySelector('[data-op="recall"]').disabled = !!slots && !used;
    const save = el.querySelector('[data-op="save"]');
    save.textContent = used ? "Replace" : "Save";
    save.setAttribute("aria-label", (used ? "Replace setup " : "Save setup ") + n);
  }
}
// TRLC-LINKS: REQ-SDS-219
(function setupsInit() {
  let html = "";
  for (let n = 1; n <= 20; n++) html += `<div class="slot-t" data-slot="${n}"><div class="h"><b>Slot ${n}</b></div><div class="i"></div><div class="b">` +
    `<button type="button" class="xs" data-op="save" aria-label="Save setup ${n}">Save</button><button type="button" class="xs" data-op="recall" aria-label="Recall setup ${n}">Recall</button></div></div>`;
  $("setSlots").innerHTML = html;
  $("setSlots").addEventListener("click", (ev) => {
    const b = ev.target.closest("button[data-op]");
    if (!b) return;
    setupOp(b.dataset.op, +b.closest("[data-slot]").dataset.slot);
  });
})();
// TRLC-LINKS: REQ-SDS-219
$("setupsBtn").onclick = () => { popToggle($("setpop"), $("setupsBtn")); if (!$("setpop").hidden) setupsRefresh(); };

// Default setup is the front panel's DEFAULT SETUP key: one implementation.
// TRLC-LINKS: REQ-SDS-219
$("defaultBtn").onclick = async () => {
  if (await panelPress("defaultsetup")) {
    toast("Default setup applied"); setTimeout(goHome, 400);
    if (typeof stFollowScope === "function") stFollowScope(); // the match it cleared, not this page's
  }
  else toast("Default setup is not available on this server", "warn");
};
