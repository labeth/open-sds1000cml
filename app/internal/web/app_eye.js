// app_eye.js — eye-diagram + jitter view (classic script; shares app.js globals).

"use strict";
let ejOptBusy = false; // eye "optimize" search in progress (ejIngest guards re-learn instead of stopping)
function ejStop(why) {
  ej.armed = false;
  $("ejArm").textContent = "ARM";
  $("ejArm").classList.remove("on");
  if (why) ejStatus(($("ejStats").textContent || "") + " · " + why);
}

function ejIngest(f) {
  const ch = +$("ejCh").value === 2 ? 2 : 1;
  if (ej.ch && ch !== ej.ch) { ejStop("channel changed — re-ARM to analyze the other channel"); return; }
  ej.ch = ch;
  const sig = ch === 2 ? f.c2 : f.c1;
  if (!sig || f.is_env) { ejStop("band became unsupported"); return; }
  if (!(f.sample_s > 0)) return; // no timebase → zero-second TIE would be fabricated
  const vpc = (ch === 2 ? f.vpc2 : f.vpc1) || (1 / 25);
  ej.vpcReal = !!(ch === 2 ? f.vpc2 : f.vpc1); // mV readouts only with a real calibration
  // a V/div change rescales the codes mid-accumulation — the eye/levels would
  // mix scales; stop honestly (same policy as superres)
  if (ej.vpc0 && Math.abs(vpc - ej.vpc0) > 0.02 * ej.vpc0) {
    if (ejOptBusy) { ejFreshState(); ej.vpc0 = vpc; return; } // optimize changes V/div on purpose — re-learn, don't stop
    ejStop("vertical scale changed — data kept, re-ARM to continue"); return;
  }
  ej.vpc0 = vpc;
  ej.vpc = vpc;
  const disp = ejFeed(ej.st, sig, f.cols, f.sample_s);
  // a t/div change alters the UI in samples: every record rejects forever while
  // the panel looks live — stop honestly like the V/div and channel guards
  if (disp === "rejected:ui-inconsistent") {
    if (ejOptBusy) { ejFreshState(); return; } // optimize steps the timebase on purpose — re-learn the new band
    ej.incons = (ej.incons || 0) + 1;
    if (ej.incons >= 10) { ejStop("signal scale/timebase changed — data kept, re-ARM to continue"); return; }
  } else if (disp.startsWith("locked")) ej.incons = 0;
  ejRender(false);
}

function ejRender(force) {
  const now = performance.now();
  if (!force && now - ejLastUi < 500) return;
  ejLastUi = now;
  const st2 = ej.st;
  if (!st2) return;
  const res = ejResult(st2);
  // status line — optimize owns the status line while it runs (its transient
  // per-band accumulation must not clobber the search progress).
  if (!ejOptBusy) {
    if (res.records === 0) {
      ejStatus("no lock yet · " + st2.rejected + " rejected (" + (res.lastErr || "…") + ") — needs a clean NRZ stream");
    } else {
      ejStatus(res.records + " records · " + res.edges + " edges · " + st2.rejected + " rej" +
        (st2.rejected > 0 && res.lastErr ? " (" + res.lastErr + ")" : "") + " · " +
        eng(res.bitRate, "b/s", 4) + " · UI " + eng(res.uiSeconds, "s", 3));
    }
  }
  ejDrawEye(st2);
  ejDrawHist(st2, res);
  ejDrawSpec(res);
  ejMetricsTable(res);
  if (typeof ejBigVisible === "function" && ejBigVisible()) ejDrawBig();
}

// log-density heatmap: dark well -> blue -> cyan -> yellow -> white
function ejHeatColor(t) {
  const r = Math.min(255, Math.max(0, Math.round(t < 0.5 ? 0 : (t - 0.5) * 2 * 255)));
  const g = Math.min(255, Math.max(0, Math.round(t < 0.25 ? t * 4 * 130 : 130 + (t - 0.25) * 167)));
  const b = Math.min(255, Math.max(0, Math.round(t < 0.5 ? 120 + t * 270 : 255 - (t - 0.5) * 2 * 200)));
  return [r, g, b];
}

function ejDrawEye(st2) {
  const W = st2.eyeW, H = st2.eyeH;
  // Build the RGBA density buffer once (kept in ejEyeCv for the enlarge view to
  // reuse — the small and big canvases are separate GL contexts, so each uploads
  // its own texture from this shared buffer). Row 0 = lowest code -> bottom.
  if (!ejEyeCv || ejEyeCv.w !== W || ejEyeCv.h !== H) ejEyeCv = { data: new Uint8Array(W * H * 4), w: W, h: H };
  const data = ejEyeCv.data;
  data.fill(0);
  let mx = 0;
  for (let i = 0; i < st2.eye.length; i++) if (st2.eye[i] > mx) mx = st2.eye[i];
  const lmax = Math.log1p(mx) || 1;
  for (let y = 0; y < H; y++) {
    for (let x = 0; x < W; x++) {
      const d = st2.eye[y * W + x];
      if (d <= 0) continue;
      const o = ((H - 1 - y) * W + x) * 4;
      const t = Math.log1p(d) / lmax;
      const [r, gg, b] = ejHeatColor(t);
      data[o] = r; data[o + 1] = gg; data[o + 2] = b; data[o + 3] = 255;
    }
  }
  const cv = $("ejEye"), g = glCardCtx(cv, "#05080c");
  if (!g) return;
  ejDrawEyeTo(g, cv, st2, false);
  glCardEnd(cv);
}

function ejDrawHist(st2, res) {
  const cv = $("ejHist"), g = glCardCtx(cv, "#05080c");
  if (!g) return;
  ejDrawHistTo(g, cv, st2, res, false);
  glCardEnd(cv);
}

function ejDrawHistTo(g, cv, st2, res, detailed) {
  const tie = st2.tie;
  if (tie.length < 50) return;
  let mn = Infinity, mx = -Infinity;
  for (const v of tie) { if (v < mn) mn = v; if (v > mx) mx = v; }
  if (!(mx > mn)) return;
  const NB = detailed ? 160 : 64, hist = new Float64Array(NB);
  for (const v of tie) hist[Math.min(NB - 1, Math.floor((v - mn) / (mx - mn) * NB))]++;
  let hmax = 0;
  for (const h of hist) if (h > hmax) hmax = h;
  const pad = detailed ? 26 : 0;
  g.fillStyle = "#35c8e8";
  const bw = (cv.width - pad) / NB;
  for (let i = 0; i < NB; i++) {
    const h = hist[i] / hmax * (cv.height - 14 - pad);
    g.fillRect(pad + i * bw, cv.height - pad - h, Math.max(1, bw - 1), h);
  }
  const fs = detailed ? 14 : 10;
  g.fillStyle = "#8899aa"; g.font = fs + "px sans-serif";
  g.fillText("TIE histogram — " + eng(mx - mn, "s", 2) + " pp · " + tie.length + " edges", 6 + pad, fs + 2);
  if (detailed) {
    // x-axis ticks (TIE in engineering units) + zero marker when in range
    g.strokeStyle = "rgba(255,255,255,0.2)";
    for (const t of [0, 0.25, 0.5, 0.75, 1]) {
      const x = pad + t * (cv.width - pad);
      g.beginPath(); g.moveTo(x, cv.height - pad); g.lineTo(x, cv.height - pad + 6); g.stroke();
      const val = mn + t * (mx - mn);
      g.fillText(eng(val, "s", 2), Math.min(x + 2, cv.width - 70), cv.height - 6);
    }
    if (mn < 0 && mx > 0) {
      const x0 = pad + (0 - mn) / (mx - mn) * (cv.width - pad);
      g.strokeStyle = "rgba(255,255,255,0.35)";
      g.setLineDash([4, 5]);
      g.beginPath(); g.moveTo(x0, 0); g.lineTo(x0, cv.height - pad); g.stroke();
      g.setLineDash([]);
    }
  }
}

function ejDrawSpec(res) {
  const cv = $("ejSpec"), g = glCardCtx(cv, "#05080c");
  if (!g) return;
  ejDrawSpecTo(g, cv, res, false);
  glCardEnd(cv);
}

function ejDrawSpecTo(g, cv, res, detailed) {
  const sp = res.spectrum;
  if (!sp || !res.specDf) return;
  let mx = 0;
  for (let k = 2; k < sp.length; k++) if (sp[k] > mx) mx = sp[k];
  if (!(mx > 0)) return;
  const pad = detailed ? 26 : 0;
  g.strokeStyle = "#f5d90a";
  g.beginPath();
  for (let k = 2; k < sp.length; k++) {
    const x = pad + (k - 2) / (sp.length - 2) * (cv.width - pad);
    const y = cv.height - pad - sp[k] / mx * (cv.height - 14 - pad);
    if (k === 2) g.moveTo(x, y); else g.lineTo(x, y);
  }
  g.stroke();
  const fs = detailed ? 14 : 10;
  g.fillStyle = "#8899aa"; g.font = fs + "px sans-serif";
  g.fillText("TIE spectrum — pk " + eng(res.specPeakHz, "Hz", 3) + " / " + eng(res.specPeakAmp, "s", 2), 6 + pad, fs + 2);
  if (detailed) {
    // frequency axis ticks + the measured CDR corner (below it the linear-fit
    // clock absorbs jitter — the honest measurement floor)
    const fMax = res.specDf * (sp.length - 2);
    g.strokeStyle = "rgba(255,255,255,0.2)";
    for (const t of [0, 0.25, 0.5, 0.75, 1]) {
      const x = pad + t * (cv.width - pad);
      g.beginPath(); g.moveTo(x, cv.height - pad); g.lineTo(x, cv.height - pad + 6); g.stroke();
      g.fillText(eng(t * fMax, "Hz", 2), Math.min(x + 2, cv.width - 80), cv.height - 6);
    }
    if (res.tieHpHz && res.tieHpHz < fMax) {
      const xc = pad + res.tieHpHz / fMax * (cv.width - pad);
      g.strokeStyle = "rgba(242,166,59,0.5)";
      g.setLineDash([4, 5]);
      g.beginPath(); g.moveTo(xc, 0); g.lineTo(xc, cv.height - pad); g.stroke();
      g.setLineDash([]);
      g.fillText("CDR corner", xc + 4, 30);
    }
    if (res.specPeakHz) {
      const xp = pad + res.specPeakHz / fMax * (cv.width - pad);
      g.fillStyle = "#f5d90a";
      g.fillText("▼", xp - 5, 16);
    }
  }
}

function ejMetricsTable(res) {
  const rows = [];
  const push = (k, v) => rows.push("<tr><th>" + k + "</th><td>" + v + "</td></tr>");
  if (res.bitRate) push("bit rate", eng(res.bitRate, "b/s", 5) + " (UI " + eng(res.uiSeconds, "s", 4) + ")");
  if (res.tieRms !== undefined) {
    push("TIE", eng(res.tieRms, "s", 3) + " rms · " + eng(res.tiePp, "s", 3) + " pp");
    // dual-Dirac-lite caveat: unimodal TIE (incl. periodic jitter) counts as RJ.
    // Flag when the spectrum's dominant tone explains most of the TIE power.
    const pjDominated = res.specPeakAmp && res.tieRms > 0 && (res.specPeakAmp / Math.SQRT2) > 0.6 * res.tieRms;
    push("RJ / DJ(δδ)", (res.rj !== undefined ? eng(res.rj, "s", 3) : "— (needs ≥200 edges)") + " / " +
      (res.dj ? eng(res.dj, "s", 3) : "—") + (pjDominated ? " · PJ-dominated" : ""));
    push("period / c2c", eng(res.periodJRms, "s", 3) + " / " + eng(res.c2cJRms, "s", 3) + " rms");
    // metrology honesty: the per-record linear-fit clock high-passes TIE — the
    // scope-world analogue of a golden PLL's loop bandwidth. Say so.
    if (res.tieHpHz) push("CDR corner", "&gt; " + eng(res.tieHpHz, "Hz", 2) + " measured");
  }
  const em = res.eyeMetrics;
  if (em && em.eyeHeightCodes > 0) {
    push("eye height", ej.vpcReal
      ? (em.eyeHeightCodes * ej.vpc * 1000).toFixed(1) + " mV (" + em.eyeHeightCodes.toFixed(0) + " codes)"
      : em.eyeHeightCodes.toFixed(0) + " codes");
    if (em.eyeWidthUI > 0) push("eye width", em.eyeWidthUI.toFixed(2) + " UI");
  }
  $("ejBody").innerHTML = rows.join("");
}

function ejOpenBig(kind) {
  if (!ej.st || ej.st.records === 0) return;
  ejBigKind = kind;
  $("ejBigWrap").classList.remove("hidden");
  ejDrawBig();
}

function ejBigVisible() { return !$("ejBigWrap").classList.contains("hidden"); }

function ejDrawBig() {
  const st2 = ej.st;
  if (!st2) return;
  const cv = $("ejBig");
  // render at the element's CSS size × devicePixelRatio for a crisp upscale
  const r = cv.getBoundingClientRect();
  const dpr2 = window.devicePixelRatio || 1;
  if (cv.width !== Math.round(r.width * dpr2)) {
    cv.width = Math.round(r.width * dpr2);
    cv.height = Math.round(r.height * dpr2);
  }
  const res = ejResult(st2);
  const g = glCardCtx(cv, "#05080c");
  if (g) {
    if (ejBigKind === "hist") ejDrawHistTo(g, cv, st2, res, true);
    else if (ejBigKind === "spec") ejDrawSpecTo(g, cv, res, true);
    else ejDrawEyeTo(g, cv, st2, true);
    glCardEnd(cv);
  }
  const em = res.eyeMetrics;
  $("ejBigInfo").textContent =
    (res.bitRate ? eng(res.bitRate, "b/s", 4) + " · UI " + eng(res.uiSeconds, "s", 3) : "") +
    (res.tieRms !== undefined ? " · TIE " + eng(res.tieRms, "s", 3) + " rms · RJ " + (res.rj !== undefined ? eng(res.rj, "s", 3) : "—") + " · DJ " + (res.dj ? eng(res.dj, "s", 3) : "—") : "") +
    (em && em.eyeHeightCodes > 0 ? " · eye " + (em.eyeHeightCodes * ej.vpc * 1000).toFixed(0) + " mV / " + em.eyeWidthUI.toFixed(2) + " UI" : "") +
    " · " + res.records + " records — click to close";
}

function ejDrawEyeTo(g, cv, st2, detailed) {
  if (!ejEyeCv) return;
  // upload the density buffer as a texture, scaled to the canvas (detailed =
  // smooth/LINEAR upscale for the enlarge view; small view stays crisp/NEAREST)
  g.blit(ejEyeCv.data, ejEyeCv.w, ejEyeCv.h, 0, 0, cv.width, cv.height, detailed);
  // UI grid: the fold spans exactly 2 UI; mark the two bit boundaries
  g.strokeStyle = "rgba(255,255,255,0.25)";
  g.setLineDash(detailed ? [4, 6] : [3, 4]);
  for (const fx of [0.25, 0.5, 0.75]) {
    g.beginPath(); g.moveTo(fx * cv.width, 0); g.lineTo(fx * cv.width, cv.height); g.stroke();
  }
  g.setLineDash([]);
}

// ---- OPTIMIZE: find the best acquisition settings for the eye on the current
// signal. The eye folds every sample at (t−t0) mod 2UI and pools edge intervals
// across records, so it needs NO trigger lock — it works free-run above the
// comparator ceiling. Quality is governed by SAMPLES PER UI (CDR sub-sample
// resolution + phase bins): more is better until the record spans too few UIs.
// So: fit the vertical (autoset), then step the timebase to drive samples/UI
// (ej.st.ui, measured) into a sweet spot — for a fast signal that means the
// FASTEST band that still LOCKS, which is exactly the rate limit.
const ejSleep = ms => new Promise(r => setTimeout(r, ms));
// fresh accumulation for a NEW band during the search: also clear the guard
// state (vpc0/incons) so the intentional V/div + timebase changes we make don't
// trip the "vertical/timebase changed — re-ARM" guards mid-optimize.
function ejFreshState() { ej.st = ejNew({}); ej.lastUi = 0; ej.vpc0 = 0; ej.incons = 0; }

// wait for the eye to lock at the current band; resolve to samples/UI or null.
function ejWaitLock(timeoutMs) {
  return new Promise(resolve => {
    const t0 = Date.now();
    const poll = () => {
      if (ej.st && ej.st.uiN >= 3 && ej.st.records >= 2) return resolve(ej.st.ui);
      if (Date.now() - t0 > timeoutMs) return resolve(ej.st && ej.st.uiN > 0 ? ej.st.ui : null);
      setTimeout(poll, 120);
    };
    poll();
  });
}
// step the timebase one detent (dir<0 faster / more samples/UI, dir>0 slower),
// staying on an eye-usable band (native-fast/decimated, tdiv < 5 ms). false at edge.
function ejStepTdiv(dir) {
  if (!st || !st.tdivs || !st.tdiv_s) return false;
  const tds = st.tdivs.filter(t => t < 5e-3).sort((a, b) => a - b);
  let i = tds.findIndex(t => Math.abs(t - st.tdiv_s) <= t * 1e-6);
  if (i < 0) { let best = Infinity; tds.forEach((t, k) => { const d = Math.abs(t - st.tdiv_s); if (d < best) { best = d; i = k; } }); }
  const ni = i + dir;
  if (ni < 0 || ni >= tds.length) return false;
  send("tdiv", tds[ni]);
  return true;
}
// set the timebase to the nearest eye-usable detent to `want`.
function ejStepToNearest(want) {
  if (!st || !st.tdivs) return;
  const tds = st.tdivs.filter(t => t < 5e-3);
  let best = tds[0], bd = Infinity;
  for (const t of tds) { const d = Math.abs(t - want); if (d < bd) { bd = d; best = t; } }
  if (best) send("tdiv", best);
}
// wait for the band change to land in the status poll, then settle.
function ejSettleBand(prevTdiv) {
  return new Promise(resolve => {
    const t0 = Date.now();
    const poll = () => {
      if (st && st.tdiv_s && Math.abs(st.tdiv_s - prevTdiv) > prevTdiv * 1e-6) return setTimeout(resolve, 450);
      if (Date.now() - t0 > 3500) return resolve();
      setTimeout(poll, 100);
    };
    poll();
  });
}

async function ejOptimize() {
  if (ejOptBusy) return;
  ejOptBusy = true;
  const btn = $("ejOpt"); if (btn) btn.classList.add("on");
  try {
    // 1. fit the vertical + find the signal via the device autoset. Then anchor
    //    on a FAST native band before the search: a slow start lets a fast signal
    //    ALIAS into a false low-frequency lock (huge samples/UI) that would drive
    //    the search the wrong way; starting fast shows the true rate and the
    //    search only slows down for a genuinely slow signal.
    ejStatus("optimize: fitting the signal…");
    try { await autoset(); } catch (e) { }
    ejStepToNearest(5e-8);
    await ejSleep(500);
    // 2. arm the eye if it isn't already.
    if (!ej.armed) { $("ejArm").click(); await ejSleep(300); }
    if (!ej.armed) { ejStatus("optimize needs a native/decimated band with a signal"); return; }
    // 3. 1-D search on samples/UI (monotone in band: faster band → more samples/UI).
    const LO = 12, HI = 32;
    let lastGood = null;
    for (let step = 0; step < 8; step++) {
      ejFreshState();
      const spu = await ejWaitLock(4500);
      if (spu == null) { // this band won't lock — fall back to the last that did
        if (lastGood != null) {
          const prev = st.tdiv_s; send("tdiv", lastGood); await ejSettleBand(prev);
          ejFreshState(); await ejWaitLock(3000);
          ejStatus(`optimized at the lock limit · ${eng(ejResult(ej.st).bitRate, "b/s", 3)} · ${ej.st.ui.toFixed(1)} samp/UI`);
        } else ejStatus("no lock — no repetitive serial signal found on this channel");
        return;
      }
      const rate = ejResult(ej.st).bitRate;
      lastGood = st.tdiv_s;
      if (spu >= LO && spu <= HI) { ejStatus(`optimized · ${spu.toFixed(1)} samp/UI · ${eng(rate, "b/s", 3)}`); return; }
      const dir = spu < LO ? -1 : +1; // too coarse → faster; too fine → slower
      const prev = st.tdiv_s;
      if (!ejStepTdiv(dir)) { ejStatus(`optimized (band ${dir < 0 ? "floor" : "ceiling"}) · ${spu.toFixed(1)} samp/UI · ${eng(rate, "b/s", 3)}`); return; }
      ejStatus(`optimize: ${spu.toFixed(1)} samp/UI → ${dir < 0 ? "faster" : "slower"}…`);
      await ejSettleBand(prev);
    }
    ejStatus("optimize: converged");
  } finally {
    // keep the accumulation from the settled band (a fresh wipe here re-locks and
    // catches the last band transition, tripping the ui-inconsistent stop); just
    // clear the transition counter so the eye keeps accumulating cleanly.
    ejOptBusy = false;
    ej.incons = 0;
    if ($("ejOpt")) $("ejOpt").classList.remove("on");
  }
}

// ==== wiring ====

// ---- eye / jitter wiring ----

$("ejOpt").onclick = ejOptimize;
$("ejArm").onclick = () => {
  if (ej.armed) { ejStop("stopped"); return; }
  if (!st || (st.band !== "native-fast" && st.band !== "decimated")) {
    ejStatus("unsupported band (" + (st ? st.band : "?") + ") — use a native/decimated t/div");
    return;
  }
  if (typeof ejNew !== "function" || typeof decodeBinFrame !== "function") { ejStatus("eyejitter/binframe scripts missing"); return; }
  if (sr.armed) srStop("stopped — eye/jitter armed (one raw consumer)");
  ej.st = ejNew({});
  ej.lastSeq = 0; ej.gen++; ej.fails = 0;
  ej.ch = 0; ej.vpc0 = 0; ej.incons = 0;
  ej.armed = true;
  $("ejArm").textContent = "STOP";
  $("ejArm").classList.add("on");
  ejStatus("locking…");
  ejLoop(ej.gen);
};
$("ejReset").onclick = () => { ej.st = ejNew({}); ej.lastUi = 0; ejRender(true); ejStatus(ej.armed ? "reset — locking…" : "idle"); };

async function ejLoop(gen) {
  if (!ej.armed || gen !== ej.gen) return;
  try {
    const r = await fetch("/api/frame.bin?since=" + ej.lastSeq + "&waitms=1000&raw=1");
    if (!r.ok) throw new Error("http " + r.status);
    const f = decodeBinFrame(await r.arrayBuffer());
    if (f === null) throw new Error("decode");
    if (!ej.armed || gen !== ej.gen) return; // stop clicked mid-flight
    ej.fails = 0;
    if (!f.unchanged && f.seq !== ej.lastSeq) {
      ej.lastSeq = f.seq;
      ejIngest(f);
    }
    setTimeout(() => ejLoop(gen), 10);
  } catch (e) {
    ej.fails++;
    setTimeout(() => ejLoop(gen), Math.min(2000, 250 * ej.fails) + 250 * Math.random());
  }
}
$("ejEye").onclick = () => ejOpenBig("eye");
$("ejHist").onclick = () => ejOpenBig("hist");
$("ejSpec").onclick = () => ejOpenBig("spec");
$("ejBigWrap").onclick = () => $("ejBigWrap").classList.add("hidden");
