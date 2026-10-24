// ENGMODEL-OWNER-UNIT: FU-WEB-APP-EYE
// app_eye.js — eye-diagram + jitter view (classic script; shares app.js globals).

"use strict";
// The LCD's BUS eye grid (2 px cells over its 768 x 332 plot, panel eyeCols and
// eyeRows): the same density cells, so the scope and the page draw one picture.
const EJ_EYE_W = 384, EJ_EYE_H = 166;
let ejOptBusy = false; // eye "optimize" search in progress (ejIngest guards re-learn instead of stopping)
// TRLC-LINKS: REQ-SDS-067
function ejStop(why) {
  ej.armed = false;
  $("ejArm").textContent = "ARM";
  $("ejArm").classList.remove("on");
  if (why) ejStatus(($("ejStats").textContent || "") + " · " + why);
}

// TRLC-LINKS: REQ-SDS-067
function ejIngest(f) {
  const ch = +$("ejCh").value === 2 ? 2 : 1;
  if (ej.ch && ch !== ej.ch) { ejStop("channel changed — re-ARM to analyze the other channel"); return; }
  ej.ch = ch;
  const raw = ch === 2 ? f.c2 : f.c1;
  if (!raw || !f.full_rate) return; // only full-rate raw records fold (ejLoop holds the eye meanwhile)
  // the codes as the scope draws them (inverted, magnified at 2 and 5 mV/div),
  // as the LCD's eye folds them; a change of either starts the eye over
  const z = (st && (ch === 2 ? st.zoom2 : st.zoom1)) || 1, inv = !!(st && (ch === 2 ? st.inv2 : st.inv1));
  if (ej.disp !== undefined && ej.disp !== z + "/" + inv) ej.st = ejNew({ eyeW: EJ_EYE_W, eyeH: EJ_EYE_H });
  ej.disp = z + "/" + inv;
  const sig = ejDisplayCodes(raw, z, inv);
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
  ej.foldAt = Date.now();
  const heldShown = ej.heldShown; // the held line goes at once
  ej.heldShown = false;
  // a t/div change alters the UI in samples: every record rejects forever while
  // the panel looks live — stop honestly like the V/div and channel guards
  if (disp === "rejected:ui-inconsistent") {
    if (ejOptBusy) { ejFreshState(); return; } // optimize steps the timebase on purpose — re-learn the new band
    ej.incons = (ej.incons || 0) + 1;
    if (ej.incons >= 10) { ejStop("signal scale/timebase changed — data kept, re-ARM to continue"); return; }
  } else if (disp.startsWith("locked")) ej.incons = 0;
  ejRender(heldShown);
}

// ejDisplayCodes is a channel's codes as the scope draws them, the LCD eye's
// displayCodes: inverted, then magnified about mid-screen at the 2 and 5 mV
// detents; a -1 margin stays -1.
// TRLC-LINKS: REQ-SDS-215
function ejDisplayCodes(sig, z, inv) {
  if (z === 1 && !inv) return sig;
  const out = new Float32Array(sig.length);
  for (let i = 0; i < sig.length; i++) {
    const v = sig[i];
    out[i] = v < 0 ? -1 : 128 + ((inv ? 255 - v : v) - 128) * z;
  }
  return out;
}

// TRLC-LINKS: REQ-SDS-200
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
        eng(res.bitRate, "bit/s", 5) + " · UI " + eng(res.uiSeconds, "s", 3));
    }
  }
  ejDrawEye(st2, res);
  ejDrawHist(st2, res);
  ejDrawSpec(res);
  ejMetricsTable(res);
  if (typeof ejBigVisible === "function" && ejBigVisible()) ejDrawBig();
}

// ejShades are the LCD eye's four density shades of the channel colour over
// the eye's background, dim to full (lcd eyeShades).
// TRLC-LINKS: REQ-SDS-215
function ejShades(ch) {
  const hex = (ch === 2 ? C2COL : C1COL) || "#f5d90a", bg = [5, 8, 12];
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
  return [0.3, 0.52, 0.76, 1].map((f) => bg.map((b, i) => Math.round(b + (c[i] - b) * f)));
}

// TRLC-LINKS: REQ-SDS-200, REQ-SDS-215
function ejDrawEye(st2, res) {
  const W = st2.eyeW, H = st2.eyeH;
  // Build the RGBA density buffer once (kept in ejEyeCv for the enlarge view to
  // reuse — the small and big canvases are separate GL contexts, so each uploads
  // its own texture from this shared buffer). Row 0 = lowest code -> bottom.
  if (!ejEyeCv || ejEyeCv.w !== W || ejEyeCv.h !== H) ejEyeCv = { data: new Uint8Array(W * H * 4), w: W, h: H };
  const data = ejEyeCv.data;
  data.fill(0);
  const eye = st2.eye;
  let mx = 0;
  for (let i = 0; i < eye.length; i++) if (eye[i] > mx) mx = eye[i];
  // the LCD's shades at its thresholds: 1.5, 8 and 35 % of the densest cell,
  // as one packed RGBA word each, a cell written with one store
  const sh = ejShades(ej.ch), t1 = mx * 0.015, t2 = mx * 0.08, t3 = mx * 0.35;
  const px = new Uint32Array(data.buffer, data.byteOffset, W * H);
  const le = new Uint8Array(new Uint16Array([1]).buffer)[0] === 1;
  const word = sh.map((c) => (le ? ((255 << 24) | (c[2] << 16) | (c[1] << 8) | c[0]) : ((c[0] << 24) | (c[1] << 16) | (c[2] << 8) | 255)) >>> 0);
  for (let y = 0; y < H; y++) {
    const src = y * W, dst = (H - 1 - y) * W;
    for (let x = 0; x < W; x++) {
      const d = eye[src + x];
      if (d <= 0) continue;
      px[dst + x] = word[d >= t3 ? 3 : d >= t2 ? 2 : d >= t1 ? 1 : 0];
    }
  }
  const cv = $("ejEye");
  if (!fitCanvas(cv)) return; // the eye view is not shown: nothing to paint
  const g = glCardCtx(cv, "#05080c");
  if (!g) return;
  ejDrawEyeTo(g, cv, st2, false, res);
  glCardEnd(cv);
}

// TRLC-LINKS: REQ-SDS-200
function ejDrawHist(st2, res) {
  const cv = $("ejHist");
  if (!fitCanvas(cv)) return;
  const g = glCardCtx(cv, "#05080c");
  if (!g) return;
  ejDrawHistTo(g, cv, st2, res, false);
  glCardEnd(cv);
}

// TRLC-LINKS: REQ-SDS-200
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

// TRLC-LINKS: REQ-SDS-200
function ejDrawSpec(res) {
  const cv = $("ejSpec");
  if (!fitCanvas(cv)) return;
  const g = glCardCtx(cv, "#05080c");
  if (!g) return;
  ejDrawSpecTo(g, cv, res, false);
  glCardEnd(cv);
}

// TRLC-LINKS: REQ-SDS-200
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

// ejMetricsTable fills the eye readout with the LCD's BUS eye figures, in its
// order: bit rate, UI, crossing, UIs folded, eye height and width, rise, fall,
// TIE (rms, pk-pk), RJ / DJ and the period jitter; then the CDR corner.
// Below about 20 samples per UI the sample interval limits the edge times, eye
// width and jitter: those figures are upper/lower bounds, marked ≤ / ≥.
// TRLC-LINKS: REQ-SDS-200, REQ-SDS-215
function ejMetricsTable(res) {
  const rows = [];
  const b = !!res.bounded;
  // TRLC-LINKS: REQ-SDS-200
  const push = (k, v, bound) => rows.push("<tr><th>" + k + "</th><td" + (bound ? ' class="bnd" title="sample-limited: a bound, not a measurement"' : "") + ">" + v + "</td></tr>");
  const le = b ? "≤ " : "", ge = b ? "≥ " : "";
  push("bit rate", res.bitRate ? eng(res.bitRate, "bit/s", 5) : "—");
  push("UI", res.uiSeconds > 0 ? eng(res.uiSeconds, "s", 3) + (res.samplesPerUI ? " · " + res.samplesPerUI.toFixed(1) + " samples" : "") : "—", b);
  push("crossing", Number.isFinite(res.crossing) ? (res.crossing * 100).toFixed(1) + " %" : "—");
  push("folded", (res.uis || 0).toFixed(0) + " UI");
  const em = res.eyeMetrics;
  push("eye height", em && em.eyeHeightCodes > 0 ? (ej.vpcReal
    ? eng(em.eyeHeightCodes * ej.vpc, "V", 3)
    : em.eyeHeightCodes.toFixed(0) + " codes") : "—");
  push("eye width", em && em.eyeWidthUI > 0 ? ge + em.eyeWidthUI.toFixed(2) + " UI" : "—", b);
  push("rise", Number.isFinite(res.riseS) ? le + eng(res.riseS, "s", 3) : "—", b);
  push("fall", Number.isFinite(res.fallS) ? le + eng(res.fallS, "s", 3) : "—", b);
  if (res.tieRms !== undefined) {
    push("TIE rms", le + eng(res.tieRms, "s", 3), b);
    push("TIE pk-pk", le + eng(res.tiePp, "s", 3), b);
    // dual-Dirac-lite caveat: unimodal TIE (incl. periodic jitter) counts as RJ.
    // Flag when the spectrum's dominant tone explains most of the TIE power.
    const pjDominated = res.specPeakAmp && res.tieRms > 0 && (res.specPeakAmp / Math.SQRT2) > 0.6 * res.tieRms;
    push("RJ / DJ", (res.rj !== undefined ? le + eng(res.rj, "s", 3) : "— (needs ≥200 edges)") + " / " +
      (res.dj ? eng(res.dj, "s", 3) : "—") + (pjDominated ? " · PJ-dominated" : ""), b);
    push("period / c2c", le + eng(res.periodJRms, "s", 3) + " / " + eng(res.c2cJRms, "s", 3), b);
    // metrology honesty: the per-record linear-fit clock high-passes TIE — the
    // scope-world analogue of a golden PLL's loop bandwidth. Say so.
    if (res.tieHpHz) push("CDR corner", "&gt; " + eng(res.tieHpHz, "Hz", 2) + " measured");
  } else {
    push("TIE rms", "—"); push("TIE pk-pk", "—");
  }
  if (b) rows.push('<tr><td colspan="2" class="e">Below 20 samples per UI the sample interval limits these figures: ≤ / ≥ mark bounds. A faster timebase (Optimize) measures them.</td></tr>');
  $("ejBody").innerHTML = rows.join("");
}

// TRLC-LINKS: REQ-SDS-200
function ejOpenBig(kind) {
  if (!ej.st || ej.st.records === 0) return;
  ejBigKind = kind;
  $("ejBigWrap").classList.remove("hidden");
  ejDrawBig();
}

// TRLC-LINKS: REQ-SDS-200
function ejBigVisible() { return !$("ejBigWrap").classList.contains("hidden"); }

// TRLC-LINKS: REQ-SDS-200
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
    else ejDrawEyeTo(g, cv, st2, true, res);
    glCardEnd(cv);
  }
  const em = res.eyeMetrics;
  $("ejBigInfo").textContent =
    (res.bitRate ? eng(res.bitRate, "bit/s", 5) + " · UI " + eng(res.uiSeconds, "s", 3) : "") +
    (res.tieRms !== undefined ? " · TIE " + eng(res.tieRms, "s", 3) + " rms · RJ " + (res.rj !== undefined ? eng(res.rj, "s", 3) : "—") + " · DJ " + (res.dj ? eng(res.dj, "s", 3) : "—") : "") +
    (em && em.eyeHeightCodes > 0 ? " · eye " + (em.eyeHeightCodes * ej.vpc * 1000).toFixed(0) + " mV / " + em.eyeWidthUI.toFixed(2) + " UI" : "") +
    " · " + res.records + " records — click to close";
}

// ejDrawEyeTo paints the eye as the LCD's BUS eye does: the density, the
// graticule (0.5 UI columns, the centre the axis, and the 8 divisions), the
// -1 UI .. +1 UI labels, the dashed EYE OPENING box and the sample point (S)
// and threshold (T) cursors at the centre and the crossing level.
// TRLC-LINKS: REQ-SDS-200, REQ-SDS-215
function ejDrawEyeTo(g, cv, st2, detailed, res) {
  if (!ejEyeCv) return;
  // upload the density buffer as a texture, scaled to the canvas (detailed =
  // smooth/LINEAR upscale for the enlarge view; small view stays crisp/NEAREST)
  g.blit(ejEyeCv.data, ejEyeCv.w, ejEyeCv.h, 0, 0, cv.width, cv.height, detailed);
  const W = cv.width, H = cv.height, fs = detailed ? 14 : 10;
  // TRLC-LINKS: REQ-SDS-200, REQ-SDS-215
  const yOf = (code) => (EJ_CODE_HI - code) / (EJ_CODE_HI - EJ_CODE_LO) * (H - 1);
  // TRLC-LINKS: REQ-SDS-215
  const line = (x0, y0, x1, y1) => { g.beginPath(); g.moveTo(x0, y0); g.lineTo(x1, y1); g.stroke(); };
  for (let k = 0; k <= 4; k++) {
    g.strokeStyle = k === 2 ? "rgba(255,255,255,0.32)" : "rgba(255,255,255,0.13)";
    const x = Math.min(W - 1, k / 4 * W);
    line(x, 0, x, H);
  }
  for (let r = 0; r <= 8; r++) {
    g.strokeStyle = r === 4 ? "rgba(255,255,255,0.32)" : "rgba(255,255,255,0.13)";
    const y = Math.min(H - 1, r / 8 * H);
    line(0, y, W, y);
  }
  g.fillStyle = "#8899aa"; g.font = fs + "px sans-serif";
  ["-1 UI", "-0.5", "0", "+0.5", "+1 UI"].forEach((s, k) => {
    const w = g.measureText(s).width;
    g.fillText(s, Math.max(2, Math.min(W - w - 2, k / 4 * W - (k === 0 ? 0 : k === 4 ? w : w / 2))), H - 3);
  });
  res = res || ejResult(st2);
  const em = res.eyeMetrics, box = em && em.box;
  if (box) {
    const xa = box.left / 2 * W, xb = box.right / 2 * W, ya = yOf(box.hi), yb = yOf(box.lo);
    g.strokeStyle = "#f2a63b"; g.setLineDash([3, 2]);
    g.beginPath(); g.rect(xa, ya, xb - xa, yb - ya); g.stroke(); // a path: strokeRect draws no dashes
    g.setLineDash([]);
    g.fillStyle = "#f2a63b"; g.fillText("EYE OPENING", xa + 5, ya + fs + 3);
  }
  if (st2.hi > st2.lo) {
    const th = Number.isFinite(res.crossing) ? st2.lo + res.crossing * (st2.hi - st2.lo) : (st2.lo + st2.hi) / 2;
    g.strokeStyle = "rgba(230,230,230,0.7)"; g.setLineDash([2, 3]);
    line(W / 2, 0, W / 2, H);
    line(0, yOf(th), W, yOf(th));
    g.setLineDash([]);
    g.fillStyle = "rgba(230,230,230,0.85)";
    g.fillText("S", W / 2 + 3, fs + 2);
    g.fillText("T", 3, yOf(th) + fs + 1);
  }
}

// ---- OPTIMIZE: find the best acquisition settings for the eye on the current
// signal. The eye folds every sample at (t−t0) mod 2UI and pools edge intervals
// across records, so it needs NO trigger lock — it works free-run above the
// comparator ceiling. Quality is governed by SAMPLES PER UI (CDR sub-sample
// resolution + phase bins): more is better until the record spans too few UIs.
// So: fit the vertical (autoset), then step the timebase to drive samples/UI
// (ej.st.ui, measured) into a sweet spot — for a fast signal that means the
// FASTEST band that still LOCKS, which is exactly the rate limit.
// TRLC-LINKS: REQ-SDS-201
const ejSleep = ms => new Promise(r => setTimeout(r, ms));
// fresh accumulation for a NEW band during the search: also clear the guard
// state (vpc0/incons) so the intentional V/div + timebase changes we make don't
// trip the "vertical/timebase changed — re-ARM" guards mid-optimize.
// TRLC-LINKS: REQ-SDS-201, REQ-SDS-067
function ejFreshState() { ej.st = ejNew({ eyeW: EJ_EYE_W, eyeH: EJ_EYE_H }); ej.lastUi = 0; ej.vpc0 = 0; ej.incons = 0; }

// wait for the eye to lock at the current band; resolve to samples/UI or null.
// TRLC-LINKS: REQ-SDS-201
function ejWaitLock(timeoutMs) {
  return new Promise(resolve => {
    const t0 = Date.now();
    // TRLC-LINKS: REQ-SDS-201
    const poll = () => {
      if (ej.st && ej.st.uiN >= 3 && ej.st.records >= 2) return resolve(ej.st.ui);
      if (Date.now() - t0 > timeoutMs) return resolve(ej.st && ej.st.uiN > 0 ? ej.st.ui : null);
      setTimeout(poll, 120);
    };
    poll();
  });
}
// ejTdivOK: a timebase whose live records are full-rate (the eye folds no
// other), or under 5 ms/div where the scope does not say.
// TRLC-LINKS: REQ-SDS-201, REQ-SDS-215
function ejTdivOK(t) { return st && st.full_rate_tdiv_s > 0 ? t <= st.full_rate_tdiv_s * 1.001 : t < 5e-3; }
// step the timebase one detent (dir<0 faster / more samples/UI, dir>0 slower),
// staying on an eye-usable band (ejTdivOK). false at edge.
// TRLC-LINKS: REQ-SDS-201
function ejStepTdiv(dir) {
  if (!st || !st.tdivs || !st.tdiv_s) return false;
  const tds = st.tdivs.filter(ejTdivOK).sort((a, b) => a - b);
  let i = tds.findIndex(t => Math.abs(t - st.tdiv_s) <= t * 1e-6);
  if (i < 0) { let best = Infinity; tds.forEach((t, k) => { const d = Math.abs(t - st.tdiv_s); if (d < best) { best = d; i = k; } }); }
  const ni = i + dir;
  if (ni < 0 || ni >= tds.length) return false;
  send("tdiv", tds[ni]);
  return true;
}
// set the timebase to the nearest eye-usable detent to `want`.
// TRLC-LINKS: REQ-SDS-201
async function ejStepToNearest(want) {
  if (!st || !st.tdivs) return;
  const tds = st.tdivs.filter(ejTdivOK);
  let best = tds[0], bd = Infinity;
  for (const t of tds) { const d = Math.abs(t - want); if (d < bd) { bd = d; best = t; } }
  if (best) await send("tdiv", best);
  return best;
}
// wait for the band change to land in the status poll, then settle.
// TRLC-LINKS: REQ-SDS-201
function ejSettleBand(prevTdiv) {
  return new Promise(resolve => {
    const t0 = Date.now();
    // TRLC-LINKS: REQ-SDS-201
    const poll = () => {
      if (st && st.tdiv_s && Math.abs(st.tdiv_s - prevTdiv) > prevTdiv * 1e-6) return setTimeout(resolve, 450);
      if (Date.now() - t0 > 3500) return resolve();
      setTimeout(poll, 100);
    };
    poll();
  });
}

// TRLC-LINKS: REQ-SDS-201
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
    const knownRate = ej.st && ej.st.records > 0 ? ejResult(ej.st).bitRate : 0;
    ejStatus("optimize: fitting the signal…");
    try { await autoset("scope"); } catch (e) { } // SCOPE's framing: an edge trigger, not BUS's frame start
    const sampledSRAM = st && st.band === "sram";
    await ejStepToNearest(sampledSRAM ? (knownRate > 0 ? 128 / (10 * knownRate) : 2e-6) : 5e-8);
    await ejSleep(500);
    // 2. arm the eye if it isn't already.
    if (!ej.armed) { $("ejArm").click(); await ejSleep(300); }
    if (!ej.armed) { ejStatus("optimize needs sampled waveform data with a signal"); return; }
    // 3. 1-D search on samples/UI (monotone in band: faster band → more samples/UI).
    const LO = 12, HI = 32;
    let lastGood = null;
    for (let step = 0; step < (sampledSRAM ? 12 : 8); step++) {
      ejFreshState();
      const spu = await ejWaitLock(4500);
      if (spu == null) { // short SRAM windows may contain too few transitions
        if (sampledSRAM) {
          const prev = st.tdiv_s;
          if (ejStepTdiv(1)) { await ejSettleBand(prev); continue; }
        }
        // Fall back to the last band that locked.
        if (lastGood != null) {
          const prev = st.tdiv_s; send("tdiv", lastGood); await ejSettleBand(prev);
          ejFreshState(); await ejWaitLock(3000);
          ejStatus(`optimized at the lock limit · ${eng(ejResult(ej.st).bitRate, "b/s", 3)} · ${ej.st.ui.toFixed(1)} samp/UI`);
        } else ejStatus("no lock — no repetitive serial signal found on this channel");
        return;
      }
      const rate = ejResult(ej.st).bitRate;
      if (sampledSRAM) {
        // SRAM timebase changes record length before sample spacing changes.
        // Capture enough bits for the CDR instead of chasing 12–32 samples/UI.
        const prev = st.tdiv_s;
        const target = await ejStepToNearest(128 / (10 * rate));
        if (Math.abs(target - prev) > prev * 1e-6) await ejSettleBand(prev);
        ejFreshState();
        const settled = await ejWaitLock(4500);
        ejStatus(settled == null ? "no lock after changing the capture span — re-ARM at the previous timebase" :
          `optimized SRAM · ${settled.toFixed(1)} samp/UI · ${eng(ejResult(ej.st).bitRate, "b/s", 3)}`);
        return;
      }
      lastGood = st.tdiv_s;
      if (spu >= LO && spu <= HI) { ejStatus(`optimized · ${spu.toFixed(1)} samp/UI · ${eng(rate, "b/s", 3)}`); return; }
      const dir = spu < LO ? -1 : +1; // too coarse → faster; too fine → slower
      const prev = st.tdiv_s;
      if (!ejStepTdiv(dir)) { ejStatus(`optimized (band ${dir < 0 ? "floor" : "ceiling"}) · ${spu.toFixed(1)} samp/UI · ${eng(rate, "b/s", 3)}`); return; }
      ejStatus(`optimize: ${spu.toFixed(1)} samp/UI → ${dir < 0 ? "faster" : "slower"}…`);
      await ejSettleBand(prev);
    }
    ejStatus("optimize: search limit reached — check the lock and capture span");
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
// TRLC-LINKS: REQ-SDS-201
$("ejArm").onclick = () => {
  if (ej.armed) { ejStop("stopped"); return; }
  if (!st || !["native-fast", "decimated", "sram"].includes(st.band)) {
    ejStatus("unsupported band (" + (st ? st.band : "?") + ") — select a sampled waveform timebase");
    return;
  }
  if (typeof ejNew !== "function" || typeof decodeBinFrame !== "function") { ejStatus("eyejitter/binframe scripts missing"); return; }
  if (sr.armed) srStop("stopped — eye/jitter armed (one raw consumer)");
  ej.st = ejNew({ eyeW: EJ_EYE_W, eyeH: EJ_EYE_H });
  ej.lastSeq = 0; ej.gen++; ej.fails = 0;
  ej.foldAt = 0;
  ej.ch = 0; ej.vpc0 = 0; ej.incons = 0;
  ej.armed = true;
  $("ejArm").textContent = "STOP";
  $("ejArm").classList.add("on");
  ejStatus("locking…");
  ejLoop(ej.gen);
};
// TRLC-LINKS: REQ-SDS-201
$("ejReset").onclick = () => { ej.st = ejNew({ eyeW: EJ_EYE_W, eyeH: EJ_EYE_H }); ej.lastUi = 0; ejRender(true); ejStatus(ej.armed ? "reset — locking…" : "idle"); };

// TRLC-LINKS: REQ-SDS-201
async function ejLoop(gen) {
  if (!ej.armed || gen !== ej.gen) return;
  try {
    const r = await fetch("/api/frame.bin?since=" + ej.lastSeq + "&waitms=1000&raw=1&lease=raw");
    if (!r.ok) throw new Error("http " + r.status);
    const f = decodeBinFrame(await r.arrayBuffer());
    if (f === null) throw new Error("decode");
    if (!ej.armed || gen !== ej.gen) return; // stop clicked mid-flight
    ej.fails = 0;
    if (!f.unchanged && f.seq !== ej.lastSeq) {
      ej.lastSeq = f.seq;
      // Only full-rate raw records fold: the live envelope, a decimated record
      // or a filtered acquisition between them would fold another picture.
      // Any other frame leaves the eye as it is.
      if (f.full_rate) ejIngest(f);
    }
    ejHeld();
    setTimeout(() => ejLoop(gen), 10);
  } catch (e) {
    ej.fails++;
    setTimeout(() => ejLoop(gen), Math.min(2000, 250 * ej.fails) + 250 * Math.random());
  }
}
// EJ_HELD_MS: running this long with no full-rate capture folded, the eye
// says it is held (a raw 200us/div record takes about 3 s), as the LCD's.
const EJ_HELD_MS = 6000;
// ejRawFix is the setting that keeps full-rate captures from the eye, as what
// to set ("" when none does): the LCD eye's eyeRawFix.
// TRLC-LINKS: REQ-SDS-215
function ejRawFix() {
  if (!st) return "";
  if ([1, 2, 4].includes(st.acq_mode)) return "set ACQ to Normal";
  if (st.full_rate_tdiv_s > 0 && st.tdiv_s > st.full_rate_tdiv_s * 1.001) return "T/div " + eng(st.full_rate_tdiv_s, "s", 3) + " or faster";
  return "";
}
// ejHeld says why the eye has nothing yet, or that it holds what it folded
// while no full-rate capture comes.
// TRLC-LINKS: REQ-SDS-215
function ejHeld() {
  if (ejOptBusy) return;
  const fix = ejRawFix();
  if (!ej.foldAt) {
    ejStatus(fix ? "the eye folds full-rate captures: " + fix : "waiting for a full-rate capture…");
    return;
  }
  const idle = Date.now() - ej.foldAt;
  if (st && st.running && idle > EJ_HELD_MS) {
    ej.heldShown = true;
    ejStatus("held: no full-rate capture for " + Math.round(idle / 1000) + " s" + (fix ? " — " + fix : "") +
      (ej.st && ej.st.records ? " · " + ej.st.records + " records" : ""));
  }
}
// TRLC-LINKS: REQ-SDS-200
$("ejEye").onclick = () => ejOpenBig("eye");
// TRLC-LINKS: REQ-SDS-200
$("ejHist").onclick = () => ejOpenBig("hist");
// TRLC-LINKS: REQ-SDS-200
$("ejSpec").onclick = () => ejOpenBig("spec");
// TRLC-LINKS: REQ-SDS-200
$("ejBigWrap").onclick = () => $("ejBigWrap").classList.add("hidden");
