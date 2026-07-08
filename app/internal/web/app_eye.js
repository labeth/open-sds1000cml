// app_eye.js — eye-diagram + jitter view (classic script; shares app.js globals).

"use strict";
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
  const vpc = (ch === 2 ? f.vpc2 : f.vpc1) || (1 / 32);
  ej.vpcReal = !!(ch === 2 ? f.vpc2 : f.vpc1); // mV readouts only with a real calibration
  // a V/div change rescales the codes mid-accumulation — the eye/levels would
  // mix scales; stop honestly (same policy as superres)
  if (ej.vpc0 && Math.abs(vpc - ej.vpc0) > 0.02 * ej.vpc0) { ejStop("vertical scale changed — data kept, re-ARM to continue"); return; }
  ej.vpc0 = vpc;
  ej.vpc = vpc;
  const disp = ejFeed(ej.st, sig, f.cols, f.sample_s);
  // a t/div change alters the UI in samples: every record rejects forever while
  // the panel looks live — stop honestly like the V/div and channel guards
  if (disp === "rejected:ui-inconsistent") {
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
  // status line
  if (res.records === 0) {
    ejStatus("no lock yet · " + st2.rejected + " rejected (" + (res.lastErr || "…") + ") — needs a clean NRZ stream");
  } else {
    ejStatus(res.records + " records · " + res.edges + " edges · " + st2.rejected + " rej" +
      (st2.rejected > 0 && res.lastErr ? " (" + res.lastErr + ")" : "") + " · " +
      eng(res.bitRate, "b/s", 4) + " · UI " + eng(res.uiSeconds, "s", 3));
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
  const cv = $("ejEye"), g = cv.getContext("2d");
  const W = st2.eyeW, H = st2.eyeH;
  if (!ejEyeCv) { ejEyeCv = document.createElement("canvas"); ejEyeCv.width = W; ejEyeCv.height = H; }
  const og = ejEyeCv.getContext("2d");
  const img = og.createImageData(W, H);
  let mx = 0;
  for (let i = 0; i < st2.eye.length; i++) if (st2.eye[i] > mx) mx = st2.eye[i];
  const lmax = Math.log1p(mx) || 1;
  for (let y = 0; y < H; y++) {
    for (let x = 0; x < W; x++) {
      const d = st2.eye[y * W + x];
      const o = ((H - 1 - y) * W + x) * 4; // row 0 = lowest code -> bottom of canvas
      if (d <= 0) { img.data[o + 3] = 0; continue; }
      const t = Math.log1p(d) / lmax;
      const [r, gg, b] = ejHeatColor(t);
      img.data[o] = r; img.data[o + 1] = gg; img.data[o + 2] = b; img.data[o + 3] = 255;
    }
  }
  og.putImageData(img, 0, 0);
  g.fillStyle = "#05080c";
  g.fillRect(0, 0, cv.width, cv.height);
  g.imageSmoothingEnabled = false;
  g.drawImage(ejEyeCv, 0, 0, cv.width, cv.height);
  // UI grid: the fold spans exactly 2 UI; mark the two bit boundaries
  g.strokeStyle = "rgba(255,255,255,0.25)";
  g.setLineDash([3, 4]);
  for (const fx of [0.25, 0.5, 0.75]) {
    g.beginPath(); g.moveTo(fx * cv.width, 0); g.lineTo(fx * cv.width, cv.height); g.stroke();
  }
  g.setLineDash([]);
}

function ejDrawHist(st2, res) { ejDrawHistTo($("ejHist"), st2, res, false); }

function ejDrawHistTo(cv, st2, res, detailed) {
  const g = cv.getContext("2d");
  g.fillStyle = "#05080c"; g.fillRect(0, 0, cv.width, cv.height);
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

function ejDrawSpec(res) { ejDrawSpecTo($("ejSpec"), res, false); }

function ejDrawSpecTo(cv, res, detailed) {
  const g = cv.getContext("2d");
  g.fillStyle = "#05080c"; g.fillRect(0, 0, cv.width, cv.height);
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
  if (ejBigKind === "hist") ejDrawHistTo(cv, st2, res, true);
  else if (ejBigKind === "spec") ejDrawSpecTo(cv, res, true);
  else ejDrawEyeTo(cv, st2, true);
  const em = res.eyeMetrics;
  $("ejBigInfo").textContent =
    (res.bitRate ? eng(res.bitRate, "b/s", 4) + " · UI " + eng(res.uiSeconds, "s", 3) : "") +
    (res.tieRms !== undefined ? " · TIE " + eng(res.tieRms, "s", 3) + " rms · RJ " + (res.rj !== undefined ? eng(res.rj, "s", 3) : "—") + " · DJ " + (res.dj ? eng(res.dj, "s", 3) : "—") : "") +
    (em && em.eyeHeightCodes > 0 ? " · eye " + (em.eyeHeightCodes * ej.vpc * 1000).toFixed(0) + " mV / " + em.eyeWidthUI.toFixed(2) + " UI" : "") +
    " · " + res.records + " records — click to close";
}

function ejDrawEyeTo(cv, st2, detailed) {
  if (!ejEyeCv) return;
  const g = cv.getContext("2d");
  g.fillStyle = "#05080c";
  g.fillRect(0, 0, cv.width, cv.height);
  g.imageSmoothingEnabled = detailed; // large view: smooth the density upscale
  g.drawImage(ejEyeCv, 0, 0, cv.width, cv.height);
  g.strokeStyle = "rgba(255,255,255,0.25)";
  g.setLineDash([4, 6]);
  for (const fx of [0.25, 0.5, 0.75]) {
    g.beginPath(); g.moveTo(fx * cv.width, 0); g.lineTo(fx * cv.width, cv.height); g.stroke();
  }
  g.setLineDash([]);
}

// ==== wiring ====

// ---- eye / jitter wiring ----

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
