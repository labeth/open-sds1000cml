// ENGMODEL-OWNER-UNIT: FU-WEB-APP-CONTROLS
// app_controls.js — control wiring: acquisition/trigger/timebase/vertical + view/channel/export (classic script; loaded after app.js state).

// ---- acquisition, trigger, timebase, vertical + view/channel/export controls ----
"use strict";
$("autoset").onclick = autoset;
// TRLC-LINKS: REQ-SDS-204
$("run").onclick = async () => { if (autosetBusy && !await autosetDone) return; const r = await send("runtoggle", 0); if (r.ok && st) { st.running = !!r.applied; st.single = false; applyStatus(); } };
// TRLC-LINKS: REQ-SDS-204
$("single").onclick = async () => {
  if (!(await send("single", 1)).ok) return;
  if (st) { st.norm = st.running = st.single = true; applyStatus(); }
  // A one-shot self-stops on capture. Poll fast to update its indicator.
  // Run/Stop toggles at the device, independently of this display refresh.
  // Bounded; the steady 1 s poll takes over after.
  let n = 0;
  // TRLC-LINKS: REQ-SDS-204
  const chk = async () => {
    try { st = await (await fetch("/api/status")).json(); applyStatus(); } catch (e) {}
    if (st && st.running && st.single && n++ < 50) setTimeout(chk, 200);
  };
  setTimeout(chk, 200);
};
// TRLC-LINKS: REQ-SDS-204
$("tpos").oninput = () => { $("tposv").textContent = Math.round(+$("tpos").value * 100) + " %"; send("trigpos", +$("tpos").value); };
// TRLC-LINKS: REQ-SDS-204
$("tposCentre").onclick = () => { $("tpos").value = 0.5; $("tpos").dispatchEvent(new Event("input")); };
// The strip's trigger controls are segmented choices (they fire "change").
// TRLC-LINKS: REQ-SDS-204
$("mode").onchange = () => { const on = $("mode").value === "1"; send("norm", on ? 1 : 0); if (st) { st.norm = on; applyStatus(); } };
// TRLC-LINKS: REQ-SDS-204
$("slope").onchange = () => { const r = $("slope").value === "1"; send("trigslope", r ? 1 : 0); if (st) { st.trig_rising = r; applyStatus(); } };
// TRLC-LINKS: REQ-SDS-204
$("source").onchange = () => { const c = $("source").value === "1" ? 1 : 0; send("trigsource", c); if (st) { st.trig_source = c; applyStatus(); } };
// TRLC-LINKS: REQ-SDS-204
$("rollStream").onchange = () => { const on = $("rollStream").value === "1"; send("rollstream", on ? 1 : 0); if (st) { st.roll_stream = on; applyStatus(); } };
// TRLC-LINKS: REQ-SDS-204
$("ets").onclick = () => { const on = !(st && st.ets); send("ets", on ? 1 : 0); if (st) { st.ets = on; applyStatus(); } };
// TRLC-LINKS: REQ-SDS-204
$("tdiv").onchange = () => { $("tdiv")._userAt = performance.now(); send("tdiv", +$("tdiv").value); };
// TRLC-LINKS: REQ-SDS-204
$("vdiv1").onchange = () => { $("vdiv1")._userAt = performance.now(); send("vdiv1", +$("vdiv1").value); };
// TRLC-LINKS: REQ-SDS-204
$("vdiv2").onchange = () => { $("vdiv2")._userAt = performance.now(); send("vdiv2", +$("vdiv2").value); };
// −/+ steppers and the knob wheel rule: up (or −) = a bigger trace. The ladders
// are in ascending order, so "faster / more sensitive" is the previous option.
for (const [id, control] of [["tdiv", "tdiv"], ["vdiv1", "vdiv1"], ["vdiv2", "vdiv2"]]) {
  // TRLC-LINKS: REQ-SDS-204
  const step = (dir) => stepSelect($(id), dir, control);
  $(id)._knob = step;
  knobWheel($(id), step);
  // TRLC-LINKS: REQ-SDS-204
  $(id + "Dn").onclick = () => step(-1);
  // TRLC-LINKS: REQ-SDS-204
  $(id + "Up").onclick = () => step(1);
}
// 50 % and Force are the front panel's keys: one implementation, on the device.
// TRLC-LINKS: REQ-SDS-204
async function panelPress(name) {
  try {
    const r = await fetch("/api/panel", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ button: name }) });
    const j = await r.json();
    kickStatus();
    return !!(j && j.ok);
  } catch (e) { return false; }
}
// TRLC-LINKS: REQ-SDS-204
$("set50").onclick = async () => {
  if (await panelPress("set50")) return;
  // No panel (bench server): place the level at the source's mid-swing here.
  const m = frame && (st && st.trig_source === 1 ? frame.m2 : frame.m1);
  if (!m || !(m.vmax > m.vmin)) { toast("50 %: no signal on the trigger source", "warn"); return; }
  const mid = (m.vmax + m.vmin) / 2;
  st.trig_volts = mid; $("lvl").value = mid; $("lvlv").textContent = eng(mid, "V");
  send("triglevelcode", trigCodeFor(mid)); redraw();
};
// TRLC-LINKS: REQ-SDS-204
$("force").onclick = async () => { if (!(await panelPress("force"))) toast("Force trigger is not available on this server", "warn"); };
for (const ch of [1, 2]) {
  // TRLC-LINKS: REQ-SDS-204
  $("zero" + ch).onclick = () => { $("off" + ch).value = 0; $("off" + ch + "v").textContent = eng(0, "V"); send("offset" + ch, 0); };
}
// Zoom about the trigger: the client-side window, the same one wheel and box
// zoom move (the hardware timebase is untouched).
// TRLC-LINKS: REQ-SDS-202
$("zoomSeg").onchange = () => {
  const z = +$("zoomSeg").value || 1;
  if (!frame) return;
  if (z === 1) { goHome(); return; }
  const h = homeWindow(frame), span = (h.b - h.a) / z;
  const c = frame.edge_frac >= 0 ? frame.edge_frac : (h.a + h.b) / 2;
  const pf = (st && Number.isFinite(st.trig_pos_frac)) ? st.trig_pos_frac : 0.5;
  view.win.a = c - span * pf; view.win.b = view.win.a + span; userZoomed = true;
  clearPersist(); redraw();
};
// TRLC-LINKS: REQ-SDS-204
$("probe1").onchange = () => send("probe1", +$("probe1").value);
// TRLC-LINKS: REQ-SDS-204
$("probe2").onchange = () => send("probe2", +$("probe2").value);
// TRLC-LINKS: REQ-SDS-204
$("cpl1").onchange = () => send("coupling1", +$("cpl1").value);
// TRLC-LINKS: REQ-SDS-204
$("cpl2").onchange = () => send("coupling2", +$("cpl2").value);
// TRLC-LINKS: REQ-SDS-203
$("refSaveA").onclick = () => saveRef("A");
// TRLC-LINKS: REQ-SDS-203
$("refSaveB").onclick = () => saveRef("B");
// TRLC-LINKS: REQ-SDS-204
$("holdoff").onchange = () => send("holdoff", +$("holdoff").value);
for (const [rng, lbl, ctl, ch] of [["off1", "off1v", "offset1", 1], ["off2", "off2v", "offset2", 2]]) {
  // TRLC-LINKS: REQ-SDS-204
  $(rng).oninput = () => { offDragging = true; $(lbl).textContent = eng(+$(rng).value, "V"); };
  // TRLC-LINKS: REQ-SDS-204
  $(rng).onchange = () => { offDragging = false; send(ctl, +$(rng).value / probeOf(ch)); };
}
// TRLC-LINKS: REQ-SDS-204
$("lvl").oninput = () => { lvlDragging = true; $("lvlv").textContent = eng(+$("lvl").value, "V"); };
// TRLC-LINKS: REQ-SDS-204
$("lvl").onchange = () => { lvlDragging = false; send("triglevelcode", trigCodeFor(+$("lvl").value)); };

// TRLC-LINKS: REQ-SDS-204
$("ttype").onchange = () => { send("trigtype", +$("ttype").value); if (st) st.trig_type = +$("ttype").value; updateQualRow(); };
// TRLC-LINKS: REQ-SDS-023
let qualifierQueue = Promise.resolve(), qualifierPending = 0;
// TRLC-LINKS: REQ-SDS-023
function sendParams(control, extra) {
  const body = JSON.stringify(Object.assign({ control, value: 0 }, extra));
  qualifierPending++;
  // Each edit contains the entire qualifier group. Preserve edit order so an
  // earlier request cannot overwrite the last condition/limit the user chose.
  const request = qualifierQueue.then(() => fetch("/api/set", {
    method: "POST", headers: { "Content-Type": "application/json" }, body,
  })).catch(() => {}).finally(() => { qualifierPending--; });
  qualifierQueue = request;
  return request;
}
for (const id of ["p-lvl", "p-min", "p-max", "p-cond"]) $(id).onchange = sendPulse;
for (const id of ["s-lo", "s-hi", "s-min", "s-max", "s-cond"]) $(id).onchange = sendSlope;
for (const id of ["v-std", "v-line", "v-neg"]) $(id).onchange = sendVideo;

// TRLC-LINKS: REQ-SDS-204
$("acq").onchange = () => { send("acqmode", +$("acq").value); if (st) st.acq_mode = +$("acq").value; updateAcqN(); };
// TRLC-LINKS: REQ-SDS-204
$("acqn").onchange = () => send([1,4].includes(+$("acq").value) ? "avgcount" : "eres", +$("acqn").value);
// TRLC-LINKS: REQ-SDS-204
$("memdepth").onchange = () => { send("memdepth", +$("memdepth").value); userZoomed = false; }; // deeper = more to scroll, fewer fps

// TRLC-LINKS: REQ-SDS-204
$("tPersist").onclick = () => { view.persist = !view.persist; $("tPersist").classList.toggle("on", view.persist); clearPersist(); redraw(); };
// TRLC-LINKS: REQ-SDS-204
$("tCursors").onclick = () => { view.cursors = !view.cursors; $("tCursors").classList.toggle("on", view.cursors); updateCursors(); redraw(); };
// TRLC-LINKS: REQ-SDS-204
$("tC1").onclick = () => { view.c1 = !view.c1; $("tC1").classList.toggle("on", view.c1); $("tC1").textContent = view.c1 ? "On" : "Off"; redraw(); };
// TRLC-LINKS: REQ-SDS-204
$("tC2").onclick = () => { view.c2 = !view.c2; $("tC2").classList.toggle("on", view.c2); $("tC2").textContent = view.c2 ? "On" : "Off"; redraw(); };
// TRLC-LINKS: REQ-SDS-204
$("freeze").onclick = () => { frozen = !frozen; $("freeze").classList.toggle("on", frozen); };

// TRLC-LINKS: REQ-SDS-069
$("ePNG").onclick = () => {
  // The scope is a WebGL canvas without preserveDrawingBuffer, so its buffer is
  // cleared after compositing — repaint synchronously in THIS tick so toDataURL
  // reads a live frame instead of a blank buffer.
  redraw();
  const a = document.createElement("a");
  a.download = "scope-" + (frame ? frame.seq : 0) + ".png";
  a.href = scope.toDataURL("image/png"); a.click();
};
// TRLC-LINKS: REQ-SDS-069
$("eCSV").onclick = () => {
  if (!frame || !frame.c1) return;
  // dt_s is the true capture pitch; col_span_s is a display nominal on the
  // 1–200 ns/div bands (2 ns/sample shown at 1 ns — see frameReply.DtS).
  // Fall back for client-synthesized frames and captures saved before dt_s.
  const dt = frame.dt_s > 0 ? frame.dt_s : (frame.col_span_s || 0) / frame.c1.length;
  const vpc1 = frame.vpc1 || (1 / 25), vpc2 = frame.vpc2 || (1 / 25);
  const o1 = frame.off1_v || 0, o2 = frame.off2_v || 0;
  // TRLC-LINKS: REQ-SDS-069
  const toV = (code, vpc, off) => (code === undefined || code < 0 ? "" : ((code - 128) * vpc - off).toExponential(6));
  const c2 = frame.c2;
  // Decimate huge arrays: a superres stack's K× fine grid is interpolation,
  // so exporting every point (>1M) means ~0.5 s of number-formatting for
  // sub-sample rows that carry no new data. Cap at ~131072 rows — beyond the
  // raw record's real content — and note it in the header.
  const N = frame.c1.length, CAP = 131072;
  const step = N > CAP ? Math.ceil(N / CAP) : 1;
  const rowsN = Math.ceil(N / step);
  const rows = new Array(rowsN + 1);
  // Time is relative to the trigger (t = 0 at the trigger, pre-trigger
  // negative) when the frame places one; it started at the left edge.
  const t0 = frame.edge_frac >= 0 ? frame.edge_frac * N * dt : 0;
  rows[0] = "# open-sds1000cml capture seq=" + frame.seq +
    " tdiv_s=" + (frame.tdiv_s || 0) + " probe_c1=" + (st ? st.probe1 || 1 : 1) +
    " probe_c2=" + (st ? st.probe2 || 1 : 1) +
    (frame.edge_frac >= 0 ? " t0=trigger" : " t0=first_point") +
    (frame.peak_detect ? " peak_detect=1_rows_alternate_bucket_min_max" : "") +
    (step > 1 ? " decimated=" + step + "x_of_" + N + "_pts" : "") + "\nt_s,c1_v,c2_v";
  let r = 1;
  for (let i = 0; i < N; i += step)
    rows[r++] = (i * dt - t0).toExponential(6) + "," + toV(frame.c1[i], vpc1, o1) + "," +
      toV(c2 ? c2[i] : undefined, vpc2, o2);
  exportFile(exportName(frame.seq, "csv"), "text/csv", rows.join("\n") + "\n");
};
// ---- sigrok exports (encoders in sigrok_export.js) ----
// Same contract as eCSV: export the frame on screen — live, frozen, a capture
// under review, or a superres view — as calibrated volts. Envelope/roll frames
// have no per-sample record, so the click is a silent no-op like eCSV.
// exportName — superres view frames REUSE the live frame's seq (they have no
// publish seq), so their files get a "-superres" marker instead of silently
// overwriting the live frame's export of the same seq.
// TRLC-LINKS: REQ-SDS-069
function exportName(seq, ext) {
  return "scope-" + seq + (frame && frame.sr_view ? "-superres" : "") + "." + ext;
}
// TRLC-LINKS: REQ-SDS-069
function exportFile(name, type, data) {
  const a = document.createElement("a");
  a.download = name;
  a.href = URL.createObjectURL(new Blob([data], { type })); a.click();
  // Revoke once the download has surely started — otherwise every exported
  // blob (10 MB for a superres .sr) stays pinned until the page unloads.
  setTimeout(() => URL.revokeObjectURL(a.href), 60000);
}
// TRLC-LINKS: REQ-SDS-069
$("eSR").onclick = () => {
  const s = sigrokSeries(frame);
  if (s) exportFile(exportName(s.seq, "sr"), "application/zip", sigrokSR(s));
};
// TRLC-LINKS: REQ-SDS-069
$("eVCD").onclick = () => {
  const s = sigrokSeries(frame);
  if (s) exportFile(exportName(s.seq, "vcd"), "text/plain", sigrokVCD(s));
};
// TRLC-LINKS: REQ-SDS-069
$("eWAV").onclick = () => {
  const s = sigrokSeries(frame);
  const wav = s && sigrokWAV(s); // null when the rate exceeds WAV's uint32 Hz
  if (wav) exportFile(exportName(s.seq, "wav"), "audio/wav", wav);
};
// The device's own screenshot: the PRINT key saves the LCD on the scope.
// TRLC-LINKS: REQ-SDS-069
$("ePrint").onclick = async () => {
  popClose();
  const notice = async () => { try { return ((await (await fetch("/api/status")).json()).panel || {}).Notice || ""; } catch (e) { return null; } };
  const before = await notice();
  if (!(await panelPress("print"))) { toast("Device screenshot is not available on this server", "warn"); return; }
  toast("The scope is saving a screenshot of its screen (PRINT)");
  // The scope names the file it saved, or says why not, on its own screen:
  // say it here too (it only ever said "saving"; bug hunt #3).
  for (let i = 0; i < 24; i++) {
    await new Promise((r) => setTimeout(r, 250));
    const n = await notice();
    if (n === null || n === before) continue;
    if (/^SAVED /.test(n)) { toast("Saved on the scope's USB stick as " + n.slice(6)); return; }
    if (/^PRINT/.test(n)) { toast("The scope could not save its screenshot (" + n + ")", "warn"); return; }
  }
};
// TRLC-LINKS: REQ-SDS-069
$("exportBtn").onclick = () => popToggle($("exportpop"), $("exportBtn"));
for (const id of ["ePNG", "eCSV", "eSR", "eVCD", "eWAV"]) $(id).addEventListener("click", () => popClose());
// TRLC-LINKS: REQ-SDS-203
$("mathFn").onchange = () => { mathFn = $("mathFn").value; updateMathHint(); redraw(); };

// TRLC-LINKS: REQ-SDS-204
$("precisionRate").onchange = () => send("precisionrate", +$("precisionRate").value);

// Report the engine's qualifier settings after native edits and setup restore.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-072
function updateTriggerQualifiers(q) {
  if (qualifierPending || !q || !(q.pulse_lvl > 0)) return;
  const values = {
    'p-lvl': q.pulse_lvl * 100, 'p-min': q.pulse_min_ns / 1000, 'p-max': q.pulse_max_ns / 1000, 'p-cond': q.pulse_cond,
    's-lo': q.slope_lo * 100, 's-hi': q.slope_hi * 100, 's-min': q.slope_min_ns / 1000, 's-max': q.slope_max_ns / 1000, 's-cond': q.slope_cond,
    'v-std': q.video_std, 'v-line': q.video_line, 'v-neg': q.video_neg == null ? NaN : +q.video_neg,
  };
  for (const [id, value] of Object.entries(values)) {
    const el = $(id);
    if (el && document.activeElement !== el && !(el._userAt && performance.now() - el._userAt < 1500) && Number.isFinite(value)) el.value = value;
  }
}
