// app_nav.js — navigator strip drawing (classic script; shares app.js globals).

// drawSrGate overlays the super-res gate markers (magenta, matching the device)
// with a shaded region between them. Positions are EDGE-RELATIVE offsets mapped to
// this frame's record fraction (srGateRF) then into the view, so they stay pinned
// to the trigger-locked signal through zoom/pan and the edge's frame-to-frame wander.
"use strict";
function drawSrGate() {
  if (!srGate.on || view.mode !== "YT") return;
  const g = ctx, span = view.win.b - view.win.a || 1;
  const xf = f => (f - view.win.a) / span * CW;
  const xa = xf(srGateRF(Math.min(srGate.a, srGate.b))), xb = xf(srGateRF(Math.max(srGate.a, srGate.b)));
  g.save();
  g.fillStyle = "rgba(230,120,240,0.10)"; g.fillRect(xa, 0, xb - xa, CH);
  g.strokeStyle = "rgb(230,120,240)"; g.lineWidth = dpr; g.fillStyle = "rgb(230,120,240)";
  for (const x of [xa, xb]) {
    g.beginPath(); g.moveTo(x, 0); g.lineTo(x, CH); g.stroke();
    g.fillRect(x - 4 * dpr, 0, 8 * dpr, 10 * dpr); g.fillRect(x - 4 * dpr, CH - 10 * dpr, 8 * dpr, 10 * dpr);
  }
  g.font = `${11 * dpr}px sans-serif`; g.fillText("gate", (xa + xb) / 2 - 11 * dpr, 12 * dpr);
  g.restore();
}

// ---- navigator overview strip ----
function drawNavTrace(cols, color) {
  if (!cols || !cols.length) return;
  const n = cols.length;
  navCtx.fillStyle = color; navCtx.globalAlpha = .75;
  for (let px = 0; px < NW; px++) {
    let lo = 256, hi = -1;
    const a = Math.floor(px / NW * n), b = Math.max(a + 1, Math.floor((px + 1) / NW * n));
    for (let i = a; i < b && i < n; i++) { const v = cols[i]; if (v < 0) continue; if (v < lo) lo = v; if (v > hi) hi = v; }
    if (hi < 0) continue;
    const yt = navY(hi, 1), yb = navY(lo, 1);
    navCtx.fillRect(px, yt, 1, Math.max(1, yb - yt));
  }
  navCtx.globalAlpha = 1;
}

// The navigator overview redraws fully each frame on the GPU (no offscreen-canvas
// cache: a full-record downsample is a few hundred batched fillRects — cheap on
// WebGL, and a GL canvas can't be blit-cached the way a 2D one was).
function drawNavFFT() {
  for (const ch of [1, 2]) {
    if (!(ch === 1 ? view.c1 : view.c2)) continue;
    const spec = specMemo[ch].spec;
    if (!spec) continue;
    const { mags, half, peak } = spec;
    navCtx.fillStyle = ch === 1 ? C1COL : C2COL;
    navCtx.globalAlpha = 0.7;
    for (let px = 0; px < NW; px++) {
      const a = Math.floor(px / NW * half), b = Math.max(a + 1, Math.floor((px + 1) / NW * half));
      let mx = 0;
      for (let k = a; k < b && k < half; k++) if (mags[k] > mx) mx = mags[k];
      const db = 20 * Math.log10(mx / peak + 1e-12);
      const h = Math.max(1, NH * Math.max(0, 1 + db / 80)); // 80 dB floor
      navCtx.fillRect(px, NH - h, 1, h);
    }
    navCtx.globalAlpha = 1;
  }
  // viewport rectangle = the visible frequency window
  const x0 = view.fwin.a * NW, x1 = view.fwin.b * NW;
  navCtx.fillStyle = "rgba(255,159,46,.13)"; navCtx.fillRect(x0, 0, x1 - x0, NH);
  navCtx.strokeStyle = "rgba(255,159,46,.85)"; navCtx.lineWidth = 1;
  navCtx.strokeRect(x0 + .5, .5, Math.max(1, x1 - x0 - 1), NH - 1);
  navCtx.fillStyle = "rgba(255,159,46,.95)";
  navCtx.fillRect(x0 - 1, 0, 2, NH); navCtx.fillRect(x1 - 1, 0, 2, NH);
}

function drawNavYT() {
  navCtx.strokeStyle = "#182430"; navCtx.lineWidth = 1;
  navCtx.beginPath(); navCtx.moveTo(0, NH / 2 + .5); navCtx.lineTo(NW, NH / 2 + .5); navCtx.stroke();
  if (frame && !frame.is_env) {
    if (view.c2) drawNavTrace(frame.c2, C2COL);
    if (view.c1) drawNavTrace(frame.c1, C1COL);
  }
  drawNavDecode(); // decode tokens across the WHOLE record (small-window view)
  // viewport rectangle = the visible [a,b] window.
  const x0 = view.win.a * NW, x1 = view.win.b * NW;
  navCtx.fillStyle = "rgba(255,159,46,.13)"; navCtx.fillRect(x0, 0, x1 - x0, NH);
  navCtx.strokeStyle = "rgba(255,159,46,.85)"; navCtx.lineWidth = 1;
  navCtx.strokeRect(x0 + .5, .5, Math.max(1, x1 - x0 - 1), NH - 1);
  navCtx.fillStyle = "rgba(255,159,46,.95)";
  navCtx.fillRect(x0 - 1, 0, 2, NH); navCtx.fillRect(x1 - 1, 0, 2, NH);
}

function drawNav() {
  if (nav.style.display === "none" || !NAVR || NAVR.lost()) return;
  NAVR.begin("#05080c");            // clears to the navigator background
  if (view.mode === "FFT") drawNavFFT();
  else drawNavYT();
  NAVR.end();
}

// Decode lane in the NAVIGATOR: every token across the whole record, so you see
// what goes where in the small window; the main window shows the detail. Uses
// full-record nav coords (not the zoom window), colour-coded by kind, with the
// byte text drawn wherever a token is wide enough.
function drawNavDecode() {
  if (dcfg.proto === "off" || !frame || frame.is_env || !frame.c1) return;
  const r = dcfg.result;
  if (!r || !r.ok || !r.spans.length) return;
  const n = frame.c1.length, laneH = 13 * dpr, laneY = NH - laneH;
  navCtx.fillStyle = "rgba(5,8,12,0.66)"; navCtx.fillRect(0, laneY, NW, laneH);
  navCtx.textBaseline = "middle"; navCtx.textAlign = "center"; navCtx.font = "bold " + (9 * dpr) + "px system-ui";
  const nx = i => i / (n - 1) * NW, spans = r.spans, cy = laneY + laneH / 2;
  for (let k = 0; k < spans.length; k++) {
    const s = spans[k], col = DECCOL[s.kind] || "#7c8894";
    let x0 = nx(s.i0), x1 = Math.max(nx(s.i1), x0 + 1.5 * dpr);
    if (s.kind === "start" || s.kind === "stop") { navCtx.fillStyle = col; navCtx.fillRect(x0 - dpr, laneY, 1.6 * dpr, laneH); continue; }
    navCtx.fillStyle = hexA(col, 0.85); navCtx.fillRect(x0, laneY + dpr, x1 - x0, laneH - 2 * dpr);
    // Fit inside the token box, else spill right into the gap to the next token.
    if (navCtx.measureText(s.text).width <= x1 - x0 - 2 * dpr) {
      navCtx.fillStyle = "#05080c"; navCtx.textAlign = "center"; navCtx.fillText(s.text, (x0 + x1) / 2, cy);
    } else {
      const nextX0 = k + 1 < spans.length ? nx(spans[k + 1].i0) : NW;
      const label = fitLabel(navCtx, s.text, nextX0 - x0 - 2 * dpr);
      if (label) { navCtx.fillStyle = col; navCtx.textAlign = "left"; navCtx.fillText(label, x0 + 2 * dpr, cy); }
    }
  }
  navCtx.textAlign = "left"; navCtx.textBaseline = "alphabetic";
}

