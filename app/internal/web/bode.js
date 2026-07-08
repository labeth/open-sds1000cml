// Bode / Frequency-Response-Analysis plot renderer. The engine accumulates the
// (frequency, gain_dB, phase_deg) points as an external source sweeps; this
// draws them as a classic Bode plot — magnitude (dB) on top, phase (deg) below,
// both against a LOG frequency axis with decade gridlines. Pure rendering; the
// points come from /api/bode. Shared helpers are exported for node tests.

// bodeNiceRange returns [lo, hi] padded to round numbers that bracket the data.
function bodeNiceRange(vals, fallbackLo, fallbackHi, step) {
  if (!vals.length) return [fallbackLo, fallbackHi];
  let lo = Infinity, hi = -Infinity;
  for (const v of vals) { if (v < lo) lo = v; if (v > hi) hi = v; }
  if (!(hi > lo)) { lo -= step; hi += step; }
  const pad = Math.max(step, (hi - lo) * 0.1);
  lo = Math.floor((lo - pad) / step) * step;
  hi = Math.ceil((hi + pad) / step) * step;
  return [lo, hi];
}

// bodeLogTicks returns the decade + 1-2-5 minor tick frequencies within [f0,f1].
function bodeLogTicks(f0, f1) {
  const ticks = [];
  if (!(f0 > 0) || !(f1 > f0)) return ticks;
  const d0 = Math.floor(Math.log10(f0)), d1 = Math.ceil(Math.log10(f1));
  for (let d = d0; d <= d1; d++) {
    for (const m of [1, 2, 5]) {
      const f = m * Math.pow(10, d);
      if (f >= f0 * 0.999 && f <= f1 * 1.001) ticks.push({ f, major: m === 1 });
    }
  }
  return ticks;
}

function bodeFmtHz(f) {
  if (f >= 1e6) return (f / 1e6).toPrecision(3).replace(/\.?0+$/, "") + "M";
  if (f >= 1e3) return (f / 1e3).toPrecision(3).replace(/\.?0+$/, "") + "k";
  return f.toPrecision(3).replace(/\.?0+$/, "");
}

// bodeDraw renders the plot onto a 2D context sized w×h. `pts` = {freq, gain_db,
// phase_deg} parallel arrays (ascending freq). colors: {grid, axis, mag, phase,
// text}. Splits the height: top ~55% magnitude, bottom ~45% phase.
function bodeDraw(g, w, h, pts, colors) {
  const C = colors || {};
  const cGrid = C.grid || "#243", cAxis = C.axis || "#456", cMag = C.mag || "#f5d90a", cPh = C.phase || "#35c8e8", cText = C.text || "#9ab";
  g.clearRect(0, 0, w, h);
  const n = pts.freq ? pts.freq.length : 0;
  const padL = 42, padR = 40, padT = 12, padB = 22;
  const magH = Math.round((h - padT - padB) * 0.56);
  const gap = 16;
  const phY0 = padT + magH + gap;
  const phH = h - padB - phY0;
  const plotL = padL, plotR = w - padR, plotW = plotR - plotL;

  g.font = "10px system-ui, sans-serif";
  g.textBaseline = "middle";

  if (n < 1) {
    g.fillStyle = cText; g.textAlign = "center";
    g.fillText("arm FRA and sweep the source frequency to build the curve", w / 2, h / 2);
    return;
  }

  const f0 = pts.freq[0], f1 = pts.freq[n - 1];
  const lf0 = Math.log10(f0 <= 0 ? 1 : f0), lf1 = Math.log10(f1 <= f0 ? f0 * 10 : f1);
  const span = lf1 - lf0 || 1;
  const xOf = (f) => plotL + (Math.log10(f) - lf0) / span * plotW;

  const [gLo, gHi] = bodeNiceRange(Array.from(pts.gain_db), -20, 20, 10);
  const [pLo, pHi] = bodeNiceRange(Array.from(pts.phase_deg), -180, 180, 45);
  const magYOf = (db) => padT + (gHi - db) / (gHi - gLo || 1) * magH;
  const phYOf = (d) => phY0 + (pHi - d) / (pHi - pLo || 1) * phH;

  // frequency gridlines (shared by both panels)
  const ticks = bodeLogTicks(f0, f1);
  g.textAlign = "center";
  for (const t of ticks) {
    const x = xOf(t.f);
    g.strokeStyle = t.major ? cAxis : cGrid;
    g.beginPath(); g.moveTo(x, padT); g.lineTo(x, padT + magH); g.moveTo(x, phY0); g.lineTo(x, phY0 + phH); g.stroke();
    if (t.major) { g.fillStyle = cText; g.fillText(bodeFmtHz(t.f), x, h - padB + 8); }
  }

  // magnitude horizontal gridlines + labels (dB)
  g.textAlign = "right";
  for (let db = gLo; db <= gHi + 1e-6; db += (gHi - gLo) / 4) {
    const y = magYOf(db);
    g.strokeStyle = Math.abs(db) < 1e-6 ? cAxis : cGrid;
    g.beginPath(); g.moveTo(plotL, y); g.lineTo(plotR, y); g.stroke();
    g.fillStyle = cText; g.fillText(db.toFixed(0), plotL - 4, y);
  }
  // phase horizontal gridlines + labels (deg)
  for (let d = pLo; d <= pHi + 1e-6; d += (pHi - pLo) / 4) {
    const y = phYOf(d);
    g.strokeStyle = Math.abs(d) < 1e-6 ? cAxis : cGrid;
    g.beginPath(); g.moveTo(plotL, y); g.lineTo(plotR, y); g.stroke();
    g.fillStyle = cText; g.fillText(d.toFixed(0) + "°", plotL - 4, y);
  }

  // magnitude trace
  g.strokeStyle = cMag; g.lineWidth = 1.5; g.beginPath();
  for (let i = 0; i < n; i++) { const x = xOf(pts.freq[i]), y = magYOf(pts.gain_db[i]); i ? g.lineTo(x, y) : g.moveTo(x, y); }
  g.stroke();
  g.fillStyle = cMag;
  for (let i = 0; i < n; i++) { g.beginPath(); g.arc(xOf(pts.freq[i]), magYOf(pts.gain_db[i]), 1.6, 0, 7); g.fill(); }
  // phase trace
  g.strokeStyle = cPh; g.beginPath();
  for (let i = 0; i < n; i++) { const x = xOf(pts.freq[i]), y = phYOf(pts.phase_deg[i]); i ? g.lineTo(x, y) : g.moveTo(x, y); }
  g.stroke();
  g.fillStyle = cPh;
  for (let i = 0; i < n; i++) { g.beginPath(); g.arc(xOf(pts.freq[i]), phYOf(pts.phase_deg[i]), 1.6, 0, 7); g.fill(); }

  // panel labels
  g.textAlign = "left"; g.fillStyle = cMag; g.fillText("dB", plotL + 2, padT + 6);
  g.fillStyle = cPh; g.fillText("phase", plotL + 2, phY0 + 6);
  g.lineWidth = 1;
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { bodeNiceRange, bodeLogTicks, bodeFmtHz, bodeDraw };
}
