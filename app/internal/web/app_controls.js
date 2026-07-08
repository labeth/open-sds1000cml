// app_controls.js — control wiring: acquisition/trigger/timebase/vertical + view/channel/export (classic script; loaded after app.js state).

// ---- acquisition, trigger, timebase, vertical + view/channel/export controls ----
"use strict";
$("autoset").onclick = autoset;
$("run").onclick = () => { const on = !(st && st.running); send("run", on ? 1 : 0); if (st) { st.running = on; applyStatus(); } };
$("single").onclick = () => {
  send("single", 1);
  if (st) { st.norm = st.running = st.single = true; applyStatus(); }
  // A one-shot self-stops on capture. Poll fast until it does so the RUN button
  // — which toggles off st.running — sees the stop immediately; otherwise a
  // post-capture RUN click reads a stale "running" and sends STOP (scope would
  // not resume). Bounded; the steady 1 s poll takes over after.
  let n = 0;
  const chk = async () => {
    try { st = await (await fetch("/api/status")).json(); applyStatus(); } catch (e) {}
    if (st && st.running && st.single && n++ < 50) setTimeout(chk, 200);
  };
  setTimeout(chk, 200);
};
$("tpos").oninput = () => send("trigpos", +$("tpos").value);
$("mode").onclick = () => { const on = !(st && st.norm); send("norm", on ? 1 : 0); if (st) { st.norm = on; applyStatus(); } };
$("slope").onclick = () => { const r = !(st && st.trig_rising); send("trigslope", r ? 1 : 0); if (st) { st.trig_rising = r; applyStatus(); } };
$("source").onclick = () => { const c = st && st.trig_source === 1 ? 0 : 1; send("trigsource", c); if (st) { st.trig_source = c; applyStatus(); } };
$("ets").onclick = () => { const on = !(st && st.ets); send("ets", on ? 1 : 0); if (st) { st.ets = on; applyStatus(); } };
$("tdiv").onchange = () => send("tdiv", +$("tdiv").value);
$("vdiv1").onchange = () => send("vdiv1", +$("vdiv1").value);
$("vdiv2").onchange = () => send("vdiv2", +$("vdiv2").value);
$("probe1").onchange = () => send("probe1", +$("probe1").value);
$("probe2").onchange = () => send("probe2", +$("probe2").value);
$("cpl1").onchange = () => send("coupling1", +$("cpl1").value);
$("cpl2").onchange = () => send("coupling2", +$("cpl2").value);
$("refSaveA").onclick = () => saveRef("A");
$("refSaveB").onclick = () => saveRef("B");
$("holdoff").onchange = () => send("holdoff", +$("holdoff").value);
for (const [rng, lbl, ctl, ch] of [["off1", "off1v", "offset1", 1], ["off2", "off2v", "offset2", 2]]) {
  $(rng).oninput = () => { offDragging = true; $(lbl).textContent = (+$(rng).value).toFixed(2) + " V"; };
  $(rng).onchange = () => { offDragging = false; send(ctl, +$(rng).value / probeOf(ch)); };
}
$("lvl").oninput = () => { lvlDragging = true; $("lvlv").textContent = (+$("lvl").value).toFixed(2) + " V"; };
$("lvl").onchange = () => { lvlDragging = false; send("triglevelcode", Math.round(31434 - 938 * (+$("lvl").value) / trigProbe())); };

$("ttype").onchange = () => { send("trigtype", +$("ttype").value); if (st) st.trig_type = +$("ttype").value; updateQualRow(); };
async function sendParams(control, extra) {
  try { await fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(Object.assign({ control, value: 0 }, extra)) }); } catch (e) {}
}
for (const id of ["p-lvl", "p-min", "p-max", "p-cond"]) $(id).onchange = sendPulse;
for (const id of ["s-lo", "s-hi", "s-min", "s-max", "s-cond"]) $(id).onchange = sendSlope;
for (const id of ["v-std", "v-line", "v-neg"]) $(id).onchange = sendVideo;

$("acq").onchange = () => { send("acqmode", +$("acq").value); if (st) st.acq_mode = +$("acq").value; updateAcqN(); };
$("acqn").onchange = () => send(+$("acq").value === 1 ? "avgcount" : "eres", +$("acqn").value);
$("memdepth").onchange = () => { send("memdepth", +$("memdepth").value); userZoomed = false; }; // deeper = more to scroll, fewer fps

$("mYT").onclick = () => setMode("YT");
$("mXY").onclick = () => setMode("XY");
$("mFFT").onclick = () => setMode("FFT");
$("tPersist").onclick = () => { view.persist = !view.persist; $("tPersist").classList.toggle("on", view.persist); clearPersist(); redraw(); };
$("tCursors").onclick = () => { view.cursors = !view.cursors; $("tCursors").classList.toggle("on", view.cursors); updateCursors(); redraw(); };
$("tC1").onclick = () => { view.c1 = !view.c1; $("tC1").classList.toggle("on", view.c1); redraw(); };
$("tC2").onclick = () => { view.c2 = !view.c2; $("tC2").classList.toggle("on", view.c2); redraw(); };
$("freeze").onclick = () => { frozen = !frozen; $("freeze").classList.toggle("on", frozen); };

$("ePNG").onclick = () => {
  // The scope is a WebGL canvas without preserveDrawingBuffer, so its buffer is
  // cleared after compositing — repaint synchronously in THIS tick so toDataURL
  // reads a live frame instead of a blank buffer.
  redraw();
  const a = document.createElement("a");
  a.download = "scope-" + (frame ? frame.seq : 0) + ".png";
  a.href = scope.toDataURL("image/png"); a.click();
};
$("eCSV").onclick = () => {
  if (!frame || !frame.c1) return;
  const dt = (frame.col_span_s || 0) / frame.c1.length;
  const vpc1 = frame.vpc1 || (1 / 32), vpc2 = frame.vpc2 || (1 / 32);
  const o1 = frame.off1_v || 0, o2 = frame.off2_v || 0;
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
  rows[0] = "# open-sds1000cml capture seq=" + frame.seq +
    " tdiv_s=" + (frame.tdiv_s || 0) + " probe_c1=" + (st ? st.probe1 || 1 : 1) +
    " probe_c2=" + (st ? st.probe2 || 1 : 1) +
    (step > 1 ? " decimated=" + step + "x_of_" + N + "_pts" : "") + "\nt_s,c1_v,c2_v";
  let r = 1;
  for (let i = 0; i < N; i += step)
    rows[r++] = (i * dt).toExponential(6) + "," + toV(frame.c1[i], vpc1, o1) + "," +
      toV(c2 ? c2[i] : undefined, vpc2, o2);
  const a = document.createElement("a");
  a.download = "scope-" + frame.seq + ".csv";
  a.href = URL.createObjectURL(new Blob([rows.join("\n") + "\n"], { type: "text/csv" })); a.click();
};
window.addEventListener("keydown", (e) => {
  if (e.key === "Escape") { $("help").classList.remove("show"); return; }
  if (editableFocused() || e.ctrlKey || e.metaKey || e.altKey) return;
  const k = e.key.length === 1 ? e.key.toLowerCase() : e.key;
  const m = KEYMAP.find(x => x.key === k || x.key === e.key);
  if (m) { e.preventDefault(); m.run(); }
});
$("help").onclick = (e) => { if (e.target.id === "help") $("help").classList.remove("show"); }; // click backdrop closes
$("mathFn").onchange = () => { mathFn = $("mathFn").value; updateMathHint(); redraw(); };
$("panelToggle").onclick = () => { document.body.classList.toggle("dock-toggled"); resize(); }; // collapse/expand dock
// Minimise/expand an individual card by clicking its title (ignore header controls).
$("dock").addEventListener("click", ev => {
  // ignore header controls, the action cluster, and hint text (e.g. "click to toggle")
  if (ev.target.closest("button, input, select, label, textarea, a, .subtle, .card-actions")) return;
  const h3 = ev.target.closest("h3");
  if (h3 && h3.parentElement.classList.contains("card")) h3.parentElement.classList.toggle("collapsed");
});
