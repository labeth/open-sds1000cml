// app_views.js — Bode + spectrogram view glue + small formatters (classic script; shares app.js globals).

"use strict";
function fmtTdiv(s) {
  const strip = x => x.replace(/(\.\d*?)0+$/, "$1").replace(/\.$/, "");
  if (s >= 1) return strip(s.toPrecision(3)) + " s";
  if (s >= 1e-3) return strip((s * 1e3).toPrecision(3)) + " ms";
  if (s >= 1e-6) return strip((s * 1e6).toPrecision(3)) + " µs";
  return Math.round(s * 1e9) + " ns";
}

function fmtVdiv(v) { return v >= 1 ? v + " V" : Math.round(v * 1e3) + " mV"; }

function hexA(hex, a) {
  const h = hex.replace("#", "");
  return `rgba(${parseInt(h.substr(0, 2), 16)},${parseInt(h.substr(2, 2), 16)},${parseInt(h.substr(4, 2), 16)},${a})`;
}

function fitLabel(g, text, maxW) {
  if (maxW <= 6 * dpr) return "";
  if (g.measureText(text).width <= maxW) return text;
  let t = text;
  while (t.length > 1 && g.measureText(t).width > maxW) t = t.slice(0, -1);
  return g.measureText(t).width <= maxW ? t : "";
}

// ---- measurements ----
function fmtMeas(key, m) {
  if (!m) return "—";
  switch (key) {
    case "Vpp": return eng(m.vpp, "V");
    case "Vmax": return eng(m.vmax, "V");
    case "Vmin": return eng(m.vmin, "V");
    case "Vmean": return eng(m.vmean, "V");
    case "Vrms": return eng(m.vrms, "V");
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
    case "Overshoot": return m.has_timing ? m.overshoot.toFixed(1) + " %" : "—";
  }
  return "—";
}

function bodeColors() {
  return { grid: GRIDCOL, axis: AXISCOL, mag: C1COL, phase: C2COL, text: DIMCOL };
}

function bodeStatus(m) { if (m !== undefined) $("bodeStats").textContent = m; }

function bodeDrawBig() {
  const cv = $("ejBig"); if (!cv) return;
  const r = cv.getBoundingClientRect(), dpr2 = window.devicePixelRatio || 1;
  if (cv.width !== Math.round(r.width * dpr2)) { cv.width = Math.round(r.width * dpr2); cv.height = Math.round(r.height * dpr2); }
  fetch("/api/bode").then(x => x.json()).then(pts => {
    bodeDraw(cv.getContext("2d"), cv.width, cv.height, pts.ok ? pts : { freq: [] }, bodeColors());
    $("ejBigInfo").textContent = "Bode / FRA — " + (pts.n || 0) + " points · click to close";
  }).catch(() => { });
}

function spgStatus(m) { if (m !== undefined) $("spgStats").textContent = m; }

function spgEnsure() { if (!spg.sg) spg.sg = sgNew(400, 200); return spg.sg; }

function spgPushCurrent() {
  if (!spg.armed || !frame || typeof spectrum !== "function") return;
  if (frame.seq === spg.lastSeq || frame.is_env) return;
  const src = spg.ch === 2 ? frame.c2 : frame.c1;
  if (!src || src.length < 32) return;
  spg.lastSeq = frame.seq;
  const s = spectrum(src, peakNyq());
  if (!s) return;
  const sg = spgEnsure();
  sgPushRow(sg, s.mags, s.half, s.peak, s.nyq || peakNyq());
}

function spgRender(cv) {
  cv = cv || $("spgCv");
  const rect = cv.getBoundingClientRect();
  const W = Math.max(200, Math.round(rect.width || cv.width));
  if (cv.width !== W) cv.width = W;
  sgBlit(cv.getContext("2d"), cv.width, cv.height, spg.sg, DIMCOL);
}

function spgDrawBig() {
  const cv = $("ejBig"); if (!cv) return;
  const r = cv.getBoundingClientRect(), dpr2 = window.devicePixelRatio || 1;
  if (cv.width !== Math.round(r.width * dpr2)) { cv.width = Math.round(r.width * dpr2); cv.height = Math.round(r.height * dpr2); }
  sgBlit(cv.getContext("2d"), cv.width, cv.height, spg.sg, DIMCOL);
  $("ejBigInfo").textContent = "Spectrogram — FFT over time · click to close";
}

// ==== wiring ====

// ---- Bode + spectrogram wiring ----


async function bodeRenderNow() {
  if (typeof bodeDraw !== "function") return;
  const cv = $("bodeCv"); if (!cv) return;
  let pts = { freq: [], gain_db: [], phase_deg: [] };
  try { const r = await (await fetch("/api/bode")).json(); if (r.ok) pts = r; } catch (e) { }
  // crisp render at the element's CSS box × dpr
  const rect = cv.getBoundingClientRect();
  const W = Math.max(200, Math.round(rect.width || cv.width)), H = cv.height;
  if (cv.width !== W) cv.width = W;
  const g = cv.getContext("2d");
  bodeDraw(g, cv.width, H, pts, bodeColors());
  return pts.n || 0;
}

$("bodeArm").onclick = () => {
  bode.armed = !bode.armed;
  $("bodeArm").classList.toggle("on", bode.armed);
  $("bodeArm").textContent = bode.armed ? "STOP" : "ARM";
  const ref = +$("bodeRef").value || 0, dut = +$("bodeDut").value || 0;
  if (ref === dut) { bodeStatus("REF and DUT must be different channels"); }
  // /api/set bodemode carries ref/dut in lo/hi
  fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ control: "bodemode", value: bode.armed ? 1 : 0, lo: ref, hi: dut }) }).catch(() => {});
  bodeStatus(bode.armed ? "armed — sweep the source frequency to add points" : "stopped");
};
$("bodeClear").onclick = () => {
  send("bodeclear", 0);
  bode.lastN = -1;
  bodeRenderNow();
  bodeStatus(bode.armed ? "cleared — sweeping" : "cleared");
};
for (const id of ["bodeRef", "bodeDut"]) $(id).onchange = () => {
  if (bode.armed) {
    const ref = +$("bodeRef").value || 0, dut = +$("bodeDut").value || 0;
    fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ control: "bodemode", value: 1, lo: ref, hi: dut }) }).catch(() => {});
  }
};
// full-screen enlarge on click, reusing the eye big-view dialog shell
$("bodeCv").onclick = () => {
  if (typeof ejBigVisible !== "function") return;
  ejBigKind = "bode";
  $("ejBigWrap").classList.remove("hidden");
  bodeDrawBig();
};

// 1 Hz: refresh the curve + the live-point readout while armed (or non-empty).
setInterval(async () => {
  if (!st) return;
  const active = st.bode_mode > 0 || (st.bode_points || 0) > 0;
  if (!active) return;
  const n = await bodeRenderNow();
  if (typeof ejBigVisible === "function" && ejBigVisible() && ejBigKind === "bode") bodeDrawBig();
  let line = `${n || 0} points`;
  if (st.bode_mode > 0) {
    line = st.bode_valid
      ? `live: ${eng(st.bode_freq_hz, "Hz", 3)} · ${st.bode_gain_db.toFixed(2)} dB · ${st.bode_phase_deg.toFixed(1)}° — ${n || 0} pts`
      : `armed — no single-frequency lock yet (${n || 0} pts)`;
  }
  bodeStatus(line);
}, 1000);

$("spgArm").onclick = () => {
  spg.armed = !spg.armed;
  $("spgArm").classList.toggle("on", spg.armed);
  $("spgArm").textContent = spg.armed ? "STOP" : "ARM";
  if (spg.armed) spgEnsure();
  spgStatus(spg.armed ? "armed — building the waterfall from each capture" : "stopped");
};
$("spgClear").onclick = () => { if (spg.sg) sgClear(spg.sg); spgRender(); spgStatus(spg.armed ? "cleared — building" : "cleared"); };
$("spgCh").onchange = () => { spg.ch = +$("spgCh").value === 2 ? 2 : 1; };
$("spgFloor").onchange = () => { if (spg.sg) spg.sg.floorDb = +$("spgFloor").value || -60; };
$("spgCv").onclick = () => {
  if (typeof ejBigVisible !== "function") return;
  ejBigKind = "spg"; $("ejBigWrap").classList.remove("hidden"); spgDrawBig();
};
// push + render on a fast tick so the waterfall scrolls at ~frame rate
setInterval(() => {
  if (!spg.armed && (!spg.sg || spg.sg.rows === 0)) return;
  spgPushCurrent();
  spgRender();
  if (typeof ejBigVisible === "function" && ejBigVisible() && ejBigKind === "spg") spgDrawBig();
  if (spg.armed) spgStatus(`waterfall live · ${spg.sg ? spg.sg.rows : 0} rows · Nyquist ${eng(peakNyq(), "Hz", 3)}`);
}, 80);
