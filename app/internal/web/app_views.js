// app_views.js — Bode + spectrogram view glue + small formatters (classic script; shares app.js globals).

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

