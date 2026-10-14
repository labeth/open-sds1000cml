// ENGMODEL-OWNER-UNIT: FU-WEB-APP-VIEWS
// app_views.js — Bode + spectrogram view glue + small formatters (classic script; shares app.js globals).

"use strict";
// TRLC-LINKS: REQ-SDS-125
function fmtTdiv(s) {
  // TRLC-LINKS: REQ-SDS-125
  const strip = x => x.replace(/(\.\d*?)0+$/, "$1").replace(/\.$/, "");
  if (s >= 1) return strip(s.toPrecision(3)) + " s";
  if (s >= 1e-3) return strip((s * 1e3).toPrecision(3)) + " ms";
  if (s >= 1e-6) return strip((s * 1e6).toPrecision(3)) + " µs";
  return Math.round(s * 1e9) + " ns";
}

// TRLC-LINKS: REQ-SDS-125
function fmtVdiv(v) { return v >= 1 ? v + " V" : Math.round(v * 1e3) + " mV"; }

// TRLC-LINKS: REQ-SDS-125
function hexA(hex, a) {
  const h = hex.replace("#", "");
  return `rgba(${parseInt(h.substr(0, 2), 16)},${parseInt(h.substr(2, 2), 16)},${parseInt(h.substr(4, 2), 16)},${a})`;
}

// TRLC-LINKS: REQ-SDS-125
function fitLabel(g, text, maxW) {
  if (maxW <= 6 * dpr) return "";
  if (g.measureText(text).width <= maxW) return text;
  let t = text;
  while (t.length > 1 && g.measureText(t).width > maxW) t = t.slice(0, -1);
  return g.measureText(t).width <= maxW ? t : "";
}

// ---- measurements ----
// TRLC-LINKS: REQ-SDS-125
function fmtMeas(key, m) {
  if (!m) return "—";
  switch (key) {
    case "Vpp": return eng(m.vpp, "V");
    case "Vmax": return eng(m.vmax, "V");
    case "Vmin": return eng(m.vmin, "V");
    case "Vmean": return m.env_aliased ? "—" : eng(m.vmean, "V");
    case "Vrms": return m.env_aliased ? "—" : eng(m.vrms, "V");
    case "ACrms": return m.env_aliased ? "—" : eng(m.vrms_ac, "V");
    case "Vtop": return eng(m.vtop, "V");
    case "Vbase": return eng(m.vbase, "V");
    case "Vampl": return eng(m.vampl, "V");
    case "Freq": return m.has_timing ? eng(m.freq, "Hz") : "—";
    case "Period": return m.has_timing ? eng(m.period, "s") : "—";
    case "Duty": return m.has_timing ? m.duty.toFixed(1) + " %" : "—";
    case "Rise": return m.rise_s > 0 ? eng(m.rise_s, "s") : "—";
    case "Fall": return m.fall_s > 0 ? eng(m.fall_s, "s") : "—";
    case "+Width": return m.pos_width_s > 0 ? eng(m.pos_width_s, "s") : "—";
    case "-Width": return m.neg_width_s > 0 ? eng(m.neg_width_s, "s") : "—";
    case "Overshoot": return m.edge_shape || m.has_timing && !m.env_timing ? m.overshoot.toFixed(1) + " %" : "—";
    case "Preshoot": return m.edge_shape || m.has_timing && !m.env_timing ? m.preshoot.toFixed(1) + " %" : "—";
  }
  return "—";
}

// TRLC-LINKS: REQ-SDS-125
function bodeColors() {
  return { grid: GRIDCOL, axis: AXISCOL, mag: C1COL, phase: C2COL, text: DIMCOL };
}

// TRLC-LINKS: REQ-SDS-125
function bodeStatus(m) { if (m !== undefined) $("bodeStats").textContent = m; }

// TRLC-LINKS: REQ-SDS-068
function spgStatus(m) { if (m !== undefined) $("spgStats").textContent = m; }

// TRLC-LINKS: REQ-SDS-068
function spgEnsure() { if (!spg.sg) spg.sg = sgNew(400, 200); return spg.sg; }

// SPECTRUM draws its spectrogram and Bode views on the scope canvas itself, so
// they get the large pane (and maximize) like the FFT. The plot routines take
// CSS pixels; a dpr scale keeps their text crisp and the right size on HiDPI.
// TRLC-LINKS: REQ-SDS-068, REQ-SDS-216
function drawSpgMain() {
  ctx.save(); ctx.scale(dpr, dpr);
  sgBlit(ctx, CW / dpr, CH / dpr, spg.sg, DIMCOL);
  ctx.restore();
}
// TRLC-LINKS: REQ-SDS-125, REQ-SDS-216
function drawBodeMain() {
  ctx.save(); ctx.scale(dpr, dpr);
  bodeDraw(ctx, CW / dpr, CH / dpr, bode.pts || { freq: [], gain_db: [], phase_deg: [] }, bodeColors());
  ctx.restore();
}

// TRLC-LINKS: REQ-SDS-068
function spgPushCurrent() {
  // the FFT's source and transform (fftReduce): the spectrum frame while the
  // spectrogram is shown, as the LCD's waterfall
  const f = fftUseRaw() ? fftRaw : frame;
  if (!spg.armed || !f || typeof spectrum !== "function") return;
  if (f.seq === spg.lastSeq || f.is_env) return;
  const src = spg.ch === 2 ? f.c2 : f.c1;
  if (!src || src.length < 32) return;
  spg.lastSeq = f.seq;
  const g = gapFill(f.peak_detect ? pairMids(src) : src);
  const s = g && fftReduce(g, peakNyq(), f.bandwidth_hz || 0, FFT_MAX);
  if (!s) return;
  const sg = spgEnsure();
  sgPushRow(sg, s.mags, s.half, s.peak, s.nyq);
}

// spgRender repaints the waterfall when SPECTRUM shows it (on the scope canvas).
// TRLC-LINKS: REQ-SDS-068
function spgRender() { if (view.mode === "SPG") scheduleRender(); }

// ==== wiring ====

// ---- Bode + spectrogram wiring ----


// bodeRenderNow fetches the accumulated response into bode.pts and repaints the
// Bode view when it is shown. Returns the point count.
// TRLC-LINKS: REQ-SDS-125
async function bodeRenderNow() {
  if (typeof bodeDraw !== "function") return 0;
  let pts = { freq: [], gain_db: [], phase_deg: [] };
  try { const r = await (await fetch("/api/bode")).json(); if (r.ok) pts = r; } catch (e) { }
  bode.pts = pts;
  if (view.mode === "BODE") scheduleRender();
  if (typeof specOnBode === "function") specOnBode(pts);
  return pts.n || 0;
}

// TRLC-LINKS: REQ-SDS-125
$("bodeArm").onclick = () => {
  bode.armed = !bode.armed;
  bode.pending = { armed: bode.armed, until: Date.now() + 3000 };
  $("bodeArm").classList.toggle("on", bode.armed);
  $("bodeArm").textContent = bode.armed ? "STOP" : "ARM";
  const ref = +$("bodeRef").value || 0, dut = +$("bodeDut").value || 0;
  if (ref === dut) { bodeStatus("REF and DUT must be different channels"); }
  // /api/set bodemode carries ref/dut in lo/hi
  fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ control: "bodemode", value: bode.armed ? 1 : 0, lo: ref, hi: dut }) }).catch(() => {});
  bodeStatus(bode.armed ? "armed — sweep the source frequency to add points" : "stopped");
};
// TRLC-LINKS: REQ-SDS-125
$("bodeClear").onclick = () => {
  send("bodeclear", 0);
  bode.lastN = -1;
  bodeRenderNow();
  bodeStatus(bode.armed ? "cleared — sweeping" : "cleared");
};
// The channels are the scope's (its VIEW tab's Bode REF / DUT rows show them):
// a change goes there armed or not, and picking the other's channel swaps the
// two, as on the scope.
// TRLC-LINKS: REQ-SDS-125
for (const id of ["bodeRef", "bodeDut"]) $(id).onchange = () => {
  let ref = +$("bodeRef").value || 0, dut = +$("bodeDut").value || 0;
  if (ref === dut) {
    if (id === "bodeRef") dut = 1 - ref; else ref = 1 - dut;
    $("bodeRef").value = String(ref); $("bodeDut").value = String(dut);
  }
  bode.pendingCh = { ref, dut, until: Date.now() + 3000 };
  fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ control: "bodemode", value: bode.armed ? 1 : 0, lo: ref, hi: dut }) }).catch(() => {});
};
// bodeFollow shows the scope's own Bode state: armed (its Bode view arms it
// too) and the channels set on the scope or the phone.
// TRLC-LINKS: REQ-SDS-125
function bodeFollow() {
  const armed = st.bode_mode > 0;
  // a click is shown until the scope's status agrees with it: the status read
  // before it would undo it for a poll
  if (bode.pending) {
    if (armed !== bode.pending.armed && Date.now() < bode.pending.until) return;
    bode.pending = null;
  }
  if (armed !== bode.armed) {
    bode.armed = armed;
    $("bodeArm").classList.toggle("on", armed);
    $("bodeArm").textContent = armed ? "STOP" : "ARM";
  }
  // the channels picked here alike (the other selector, swapped, had no
  // focus, and ARM would send what it shows)
  const pc = bode.pendingCh;
  if (pc) {
    if (!((st.bode_ref & 1) === pc.ref && (st.bode_dut & 1) === pc.dut) && Date.now() < pc.until) return;
    bode.pendingCh = null;
  }
  for (const [id, v] of [["bodeRef", st.bode_ref], ["bodeDut", st.bode_dut]]) {
    if (Number.isInteger(v) && document.activeElement !== $(id) && $(id).value !== String(v & 1)) $(id).value = String(v & 1);
  }
}
// 1 Hz: refresh the curve + the live-point readout while armed (or non-empty),
// and whenever the Bode view is shown.
setInterval(async () => {
  if (!st) return;
  bodeFollow();
  const active = st.bode_mode > 0 || (st.bode_points || 0) > 0 || bode.armed;
  if (!active && view.mode !== "BODE") return;
  const n = await bodeRenderNow();
  if (!active) return;
  let line = `${n || 0} points`;
  if (st.bode_mode > 0) {
    line = st.bode_valid
      ? `live: ${eng(st.bode_freq_hz, "Hz", 3)} · ${st.bode_gain_db.toFixed(2)} dB · ${st.bode_phase_deg.toFixed(1)}° — ${n || 0} pts`
      : `armed — ${st.bode_why || "no single-frequency lock yet"} (${n || 0} pts)`;
  }
  bodeStatus(line);
}, 1000);

// TRLC-LINKS: REQ-SDS-068
$("spgArm").onclick = () => {
  spg.armed = !spg.armed;
  $("spgArm").classList.toggle("on", spg.armed);
  $("spgArm").textContent = spg.armed ? "STOP" : "ARM";
  if (spg.armed) spgEnsure();
  spgStatus(spg.armed ? "armed — building the waterfall from each capture" : "stopped");
};
// TRLC-LINKS: REQ-SDS-068
$("spgClear").onclick = () => { if (spg.sg) sgClear(spg.sg); spgRender(); spgStatus(spg.armed ? "cleared — building" : "cleared"); };
// TRLC-LINKS: REQ-SDS-068
$("spgCh").onchange = () => { spg.ch = +$("spgCh").value === 2 ? 2 : 1; };
// TRLC-LINKS: REQ-SDS-068
$("spgFloor").onchange = () => { if (spg.sg) spg.sg.floorDb = +$("spgFloor").value || -60; };
// push + render on a fast tick so the waterfall scrolls at ~frame rate
setInterval(() => {
  if (!spg.armed && (!spg.sg || spg.sg.rows === 0)) return;
  spgPushCurrent();
  spgRender();
  if (spg.armed) spgStatus(`waterfall live · ${spg.sg ? spg.sg.rows : 0} rows · span 0 – ${eng(displayNyq(), "Hz", 3)}`);
}, 80);
