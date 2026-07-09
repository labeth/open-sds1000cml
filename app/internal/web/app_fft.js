// app_fft.js — FFT/peak detection + FFT view rendering (classic script; shares app.js globals).

"use strict";
function peaksVisible() { return view.mode === "FFT" || view.mode === "YT"; }

function chOn(ch) { return ch === 1 ? view.c1 : view.c2; }

function chHas(ch) { return !!(frame && (ch === 1 ? frame.c1 : frame.c2)); }

// In FFT mode over a LIVE band the frequency source is the full-record RAW feed
// (fftRaw), not the display frame: on native-fast the display is an interpolated
// ~50 ns window whose FFT is a few real samples on a bogus multi-GHz axis. The
// raw record is the un-interpolated capture (e.g. 20480 samples @ its real rate
// → true Nyquist + fine resolution). Frozen/single and the super-res review keep
// their own shown frame.
function fftUseRaw() { return view.mode === "FFT" && fftRaw && fftRaw.c1 && !frozen && !(typeof sr !== "undefined" && sr.showing); }
function peakSrcCh(ch) {
  if (fftUseRaw()) { const s = ch === 2 ? fftRaw.c2 : fftRaw.c1; if (s) return s; }
  return frame ? (ch === 2 ? frame.c2 : frame.c1) : null;
}

function peakNyq() {
  if (fftUseRaw() && fftRaw.sample_s > 0) return 1 / (2 * fftRaw.sample_s); // real Nyquist from the raw rate
  return frame && frame.col_span_s > 0 ? frame.c1.length / (2 * frame.col_span_s) : 0;
}

// The palette slot a peak index occupies among a channel's (sorted) selection —
// keeps a peak's colour stable across the FFT markers, its list, and the overlay.
function selColorCh(ch, i) {
  const S = fftCh[ch], order = [...S.selIdx].sort((a, b) => a - b), k = order.indexOf(i);
  return k < 0 ? null : COMPCOLS[ch][k % COMPCOLS[ch].length];
}

// gapFill trims the -1 head/tail margins and linearly interpolates interior
// gaps so the FFT sees a UNIFORM time grid. Filtering the gaps out (the old
// behavior) compacts time and scales every reported frequency by the fill
// factor — badly wrong on a partially-filled superres stack, subtly wrong on
// a deep record's margins. Trimming doesn't change the sample interval, so
// peakNyq() (= 1/(2·dt)) stays correct.
function gapFill(src) {
  let a = 0, b = src.length - 1;
  while (a <= b && src[a] < 0) a++;
  while (b >= a && src[b] < 0) b--;
  if (b - a < 32) return null;
  const out = new Float64Array(b - a + 1);
  let last = -1;
  for (let i = a; i <= b; i++) {
    if (src[i] < 0) continue;
    if (last >= 0 && last < i - 1) {
      const v0 = out[last - a], v1 = src[i];
      for (let j = last + 1; j < i; j++) out[j - a] = v0 + (v1 - v0) * (j - last) / (i - last);
    }
    out[i - a] = src[i];
    last = i;
  }
  return out;
}

function fftStride() {
  const L = frame && frame.c1 ? frame.c1.length : 0;
  return Math.max(1, Math.ceil(L / FFT_MAX));
}

// displayNyq is the effective FFT Nyquist after the decimation cap — used by
// every FFT frequency mapping so the axis stays self-consistent.
function displayNyq() { return peakNyq() / fftStride(); }

function spectrumFor(ch) {
  const src = peakSrcCh(ch), m = specMemo[ch];
  if (m.src === src) return m.spec;
  m.src = src;
  let g = gapFill(src);
  if (g) {
    const stride = fftStride();
    if (stride > 1) {
      // Pure subsample (not box-average) so a tone's magnitude — hence
      // peakVolts — is preserved exactly; the interpolation ripple aliased
      // in is negligible next to the signal.
      const dg = new Float64Array(Math.floor(g.length / stride));
      for (let i = 0; i < dg.length; i++) dg[i] = g[i * stride];
      g = dg;
    }
    m.spec = spectrum(g, displayNyq());
  } else m.spec = null;
  return m.spec;
}

function computePeaksCh(ch) {
  const S = fftCh[ch]; S.peaks = []; S.selIdx = new Set();
  if (!chOn(ch) || !chHas(ch)) return null;
  const spec = spectrumFor(ch);
  if (!spec) return null;
  S.peaks = detectPeaks(spec, { floorDb: -50, maxPeaks });
  const idxs = new Set(), kept = [];
  for (const f of S.sel) {
    const i = nearestPeak(S.peaks, f);
    if (i < 0) { kept.push(f); continue; }
    if (!idxs.has(i)) { idxs.add(i); kept.push(S.peaks[i].freq); }
  }
  S.selIdx = idxs; S.sel = kept;
  return spec;
}

function togglePeakCh(ch, pf) {
  const S = fftCh[ch], i = nearestPeak(S.peaks, pf);
  if (i < 0) return;
  const before = S.sel.length;
  S.sel = S.sel.filter(f => nearestPeak(S.peaks, f) !== i);
  if (S.sel.length === before) S.sel.push(S.peaks[i].freq); // was not selected → add
  peakListLastT = 0; // selection changed: bypass the 1 Hz list throttle this redraw
  redraw();
}

function clearPeaksCh(ch) { fftCh[ch].sel = []; peakListLastT = 0; redraw(); }

function drawFFTHover() {
  if (!fftHover.on || boxZoom.moved) return;
  const nyq = displayNyq();
  const fw = view.fwin, fspan = fw.b - fw.a;
  const frac = fw.a + fftHover.x * fspan;
  const freq = frac * nyq;
  const ptrDb = -fftHover.y * 80;
  const px = fftHover.x * CW, py = fftHover.y * CH;
  ctx.save();
  ctx.strokeStyle = "rgba(255,210,63,.4)"; ctx.lineWidth = dpr; ctx.setLineDash([3 * dpr, 3 * dpr]);
  ctx.beginPath(); ctx.moveTo(px + .5, 0); ctx.lineTo(px + .5, CH); ctx.stroke();
  ctx.beginPath(); ctx.moveTo(0, py + .5); ctx.lineTo(CW, py + .5); ctx.stroke();
  ctx.setLineDash([]);
  const lines = ["f " + eng(freq, "Hz", 4) + " · ptr " + ptrDb.toFixed(1) + " dB"];
  for (const ch of [1, 2]) {
    if (!(ch === 1 ? view.c1 : view.c2)) continue;
    const spec = specMemo[ch].spec;
    if (!spec) continue;
    const k = Math.round(frac * (spec.half - 1));
    if (k < 0 || k >= spec.half) continue;
    const db = 20 * Math.log10(spec.mags[k] / spec.peak + 1e-12);
    lines.push("C" + ch + " " + Math.max(-99.9, db).toFixed(1) + " dB");
  }
  ctx.font = (11 * dpr) + "px system-ui";
  const w = Math.max(...lines.map(t => ctx.measureText(t).width)) + 12 * dpr;
  const h = lines.length * 14 * dpr + 8 * dpr;
  const bx = px + 12 * dpr + w > CW ? px - 12 * dpr - w : px + 12 * dpr;
  const by = Math.max(4 * dpr, Math.min(CH - h - 4 * dpr, py - h / 2));
  ctx.fillStyle = "rgba(5,8,12,.88)";
  ctx.fillRect(bx, by, w, h);
  ctx.strokeStyle = "rgba(255,210,63,.5)"; ctx.strokeRect(bx + .5, by + .5, w - 1, h - 1);
  const cols = { 0: "#ffd24f", 1: C1COL, 2: C2COL };
  lines.forEach((t, i) => {
    ctx.fillStyle = i === 0 ? cols[0] : (t.startsWith("C1") ? C1COL : C2COL);
    ctx.fillText(t, bx + 6 * dpr, by + (i + 1) * 14 * dpr - 2 * dpr);
  });
  ctx.restore();
}

function drawFFT() {
  drawGrid(ctx);
  const nyq = displayNyq();
  const fw = view.fwin, fspan = fw.b - fw.a;
  const yAt = db => CH * Math.min(1, -db / 80); // 80 dB span
  for (const ch of [1, 2]) {
    const spec = computePeaksCh(ch);
    updateFFTListCh(ch);
    if (!spec) continue;
    const { mags, half, peak } = spec;
    const dbAt = k => 20 * Math.log10(mags[k] / peak + 1e-12);
    // bin (fractional) → screen x through the frequency window
    const xAt = frac => (frac / (half - 1) - fw.a) / fspan * (CW - 1);
    const base = ch === 1 ? C1COL : C2COL;
    const kLo = Math.max(0, Math.floor(fw.a * (half - 1)) - 1);
    const kHi = Math.min(half - 1, Math.ceil(fw.b * (half - 1)) + 1);
    ctx.strokeStyle = base; ctx.lineWidth = 1.3 * dpr; ctx.globalAlpha = 0.85; ctx.beginPath();
    if (kHi - kLo > 2 * CW) {
      // More bins than pixels: draw the per-pixel max envelope (peaks are
      // what matter in a spectrum) instead of one lineTo per bin.
      let started = false;
      for (let px = 0; px <= CW; px++) {
        const f0 = fw.a + (px / CW) * fspan, f1 = fw.a + ((px + 1) / CW) * fspan;
        let a = Math.floor(f0 * (half - 1)), b = Math.ceil(f1 * (half - 1));
        if (a < 0) a = 0; if (b >= half) b = half - 1;
        if (b < a) continue;
        let mx = 0; for (let k = a; k <= b; k++) if (mags[k] > mx) mx = mags[k];
        const y = yAt(20 * Math.log10(mx / peak + 1e-12));
        if (started) ctx.lineTo(px, y); else { ctx.moveTo(px, y); started = true; }
      }
    } else {
      for (let k = kLo; k <= kHi; k++) { const x = xAt(k), y = yAt(dbAt(k)); if (k === kLo) ctx.moveTo(x, y); else ctx.lineTo(x, y); }
    }
    ctx.stroke(); ctx.globalAlpha = 1;
    // peak markers; selected ones highlighted (palette) + labelled
    ctx.textBaseline = "bottom"; ctx.font = "bold " + (11 * dpr) + "px system-ui";
    for (let i = 0; i < fftCh[ch].peaks.length; i++) {
      const p = fftCh[ch].peaks[i], px = xAt(p.frac), py = yAt(p.db), col = selColorCh(ch, i), r = (col ? 6 : 3) * dpr;
      if (px < -20 || px > CW + 20) continue;
      ctx.fillStyle = col || base;
      ctx.beginPath(); ctx.moveTo(px, py - r * 2); ctx.lineTo(px - r, py - r * 0.5); ctx.lineTo(px + r, py - r * 0.5); ctx.closePath(); ctx.fill();
      if (col) {
        const tx = Math.min(px + 8 * dpr, CW - 96 * dpr);
        ctx.fillText("C" + ch + " " + eng(p.freq, "Hz", 4) + "  " + p.db.toFixed(1) + " dB", tx, Math.max(py - 10 * dpr, 30 * dpr));
      }
    }
    ctx.textBaseline = "alphabetic";
  }
  ctx.fillStyle = "#9fb0c0"; ctx.font = (12 * dpr) + "px system-ui";
  ctx.fillText("FFT  " + eng(fw.a * nyq, "Hz", 3) + " – " + eng(fw.b * nyq, "Hz", 3) +
    (fspan < 0.999 ? "  · drag/wheel to zoom, double-click resets" : "   (dB re each channel's own peak)"), 8 * dpr, 16 * dpr);
  // Physics markers on a superres stack: the fine grid extends the axis K×
  // past the RAW Nyquist (no real signal beyond it), and the MEASURED analog
  // rolloff (chain −3 dB ≈ 16 MHz on this bench — see superres_comp.js) bounds
  // trustworthy amplitude. With BW compensation on, the recovered −3 dB is
  // marked too, so the extended axis + boost can't mislead.
  if (sr.showing && sr.st && sr.st.sampleS > 0) {
    const measF3 = typeof SRCOMP_MEAS_F3_HZ !== "undefined" ? SRCOMP_MEAS_F3_HZ : 16.4e6;
    const marks = [
      { f: 1 / (2 * sr.st.sampleS), label: "raw Nyquist — no real content beyond", col: "rgba(232,96,76,.8)" },
      { f: measF3, label: "chain −3 dB (measured analog rolloff)", col: "rgba(245,162,76,.7)" },
    ];
    if (sr.comp && sr.compInfo && sr.compInfo.recoveredF3 > 0) {
      marks.push({ f: sr.compInfo.recoveredF3, label: "BW-comp −3 dB (recovered)", col: "rgba(120,220,140,.85)" });
    }
    ctx.font = (10 * dpr) + "px system-ui";
    for (const mk2 of marks) {
      const frac = mk2.f / nyq;
      if (frac <= fw.a || frac >= fw.b) continue;
      const x = (frac - fw.a) / fspan * (CW - 1);
      ctx.strokeStyle = mk2.col; ctx.lineWidth = dpr; ctx.setLineDash([8 * dpr, 5 * dpr]);
      ctx.beginPath(); ctx.moveTo(x + .5, 22 * dpr); ctx.lineTo(x + .5, CH); ctx.stroke();
      ctx.setLineDash([]);
      ctx.fillStyle = mk2.col;
      ctx.save();
      ctx.translate(x - 4 * dpr, CH * 0.45); ctx.rotate(-Math.PI / 2);
      ctx.fillText(mk2.label, 0, 0);
      ctx.restore();
    }
  }
  drawFFTHover();
}

function drawYTPeaks(g) {
  if (view.mode !== "YT" || !frame || frame.is_env) return;
  // Nothing selected and no residual math → the spectra only feed the pick
  // lists. Refresh those at ~1 Hz instead of paying two full-record FFTs per
  // frame (the dominant avoidable per-frame client cost at 20 fps).
  const needFFT = fftCh[1].sel.length || fftCh[2].sel.length || mathFn === "res1" || mathFn === "res2";
  if (!needFFT) {
    const now = performance.now();
    if (now - peakListLastT > 1000) {
      peakListLastT = now;
      for (const ch of [1, 2]) { computePeaksCh(ch); updateFFTListCh(ch); }
    }
    return;
  }
  for (const ch of [1, 2]) { computePeaksCh(ch); updateFFTListCh(ch); }
  g.save();
  g.font = "bold " + (12 * dpr) + "px system-ui";
  let row = 0;
  for (const ch of [1, 2]) {
    const S = fftCh[ch];
    if (!S.selIdx.size) continue;
    const src = peakSrcCh(ch), zoom = ch === 1 ? (st ? st.zoom1 : 1) : (st ? st.zoom2 : 1), pal = COMPCOLS[ch];
    let k = 0;
    for (const i of [...S.selIdx].sort((a, b) => a - b)) {
      const f = S.peaks[i].freq, comp = componentMemo(src, f * frame.col_span_s);
      if (comp) {
        const col = pal[k % pal.length];
        drawTrace(g, comp, col, zoom);
        g.fillStyle = col;
        g.fillText("C" + ch + " f = " + eng(f, "Hz", 4), CW - 165 * dpr, (18 + row * 15) * dpr);
        row++;
      }
      k++;
    }
  }
  g.restore();
}

// peakVolts: absolute amplitude (volts, peak) of a spectral line. |X| of a
// sine of amplitude A through a Hann window is A·N·cg/2 with coherent gain
// cg = 0.5, so A = 4·|X|/N codes; ×vpc for volts. Parabolic-refined peaks
// under-read by up to ~15% (scalloping) — labelled ≈ for that reason.
function peakVolts(ch, p) {
  const spec = specMemo[ch].spec;
  if (!spec || !frame) return 0;
  const N = spec.half * 2;
  const mag = spec.peak * Math.pow(10, p.db / 20);
  const ampCodes = 4 * mag / N;
  return ampCodes * ((ch === 2 ? frame.vpc2 : frame.vpc1) || 1 / 25);
}

// Noise-floor magnitude of a channel's current spectrum = the MEDIAN AC-bin
// magnitude (the handful of real peaks are sparse, so they don't move the
// median). Memoized on the spectrum object, so the sort runs once per new
// spectrum, not per redraw. Lets each selected peak report its SNR above the
// floor — on a stack that's the improved (crunched) per-frequency figure.
function specFloor(ch) {
  const spec = specMemo[ch] && specMemo[ch].spec;
  if (!spec) return 0;
  if (spec._floor !== undefined) return spec._floor;
  const lo = Math.max(1, Math.floor(spec.half * 0.02)); // skip DC / near-DC
  const tmp = Array.prototype.slice.call(spec.mags, lo, spec.half).sort((a, b) => a - b);
  spec._floor = tmp.length ? tmp[tmp.length >> 1] : 0;
  return spec._floor;
}

// Selected-peak measurement lines under each FFT list: exact frequency,
// level re the channel's strongest line, absolute amplitude, and the tone's SNR
// above the noise floor (with the equivalent bits of resolution, 6.02 dB/bit).
function updateFFTSel(ch) {
  const el = $("fftSel" + ch);
  const S = fftCh[ch];
  if (!peaksVisible() || !S.selIdx.size) { el.textContent = ""; return; }
  const spec = specMemo[ch] && specMemo[ch].spec, floor = specFloor(ch);
  const rows = [];
  for (const i of [...S.selIdx].sort((a, b) => a - b)) {
    const p = S.peaks[i];
    if (!p) continue;
    let snrStr = "";
    if (spec && floor > 0) {
      const snr = 20 * Math.log10((spec.peak * Math.pow(10, p.db / 20)) / floor);
      snrStr = " · " + snr.toFixed(0) + " dB SNR (~" + Math.max(0, snr / 6.02).toFixed(1) + " bit)";
    }
    rows.push(eng(p.freq, "Hz", 4) + " · " + p.db.toFixed(1) + " dBc · ≈" + eng(peakVolts(ch, p), "Vpk", 3) + snrStr);
  }
  // Harmonic/delta line when 2+ peaks are picked: spacing and ratio.
  if (S.selIdx.size >= 2) {
    const fs = [...S.selIdx].map(i => S.peaks[i] && S.peaks[i].freq).filter(Boolean).sort((a, b) => a - b);
    if (fs.length >= 2) rows.push("Δf " + eng(fs[1] - fs[0], "Hz", 3) + " · f2/f1 " + (fs[1] / fs[0]).toFixed(3));
  }
  el.textContent = rows.join("\n");
  el.style.whiteSpace = "pre-line";
}

function updateFFTListCh(ch) {
  const card = $("fftCardC" + ch);
  if (!(peaksVisible() && chOn(ch) && chHas(ch))) { card.style.display = "none"; return; }
  card.style.display = "";
  const S = fftCh[ch], body = $("fftBody" + ch), need = S.peaks.length;
  let rows = body.querySelectorAll("tr.pk");
  // Rebuild row STRUCTURE only when the count changes — otherwise update cells in
  // place so the row elements (and your click) are never dropped.
  if (rows.length !== need) {
    let html = "<tr><th>#</th><th>Freq</th><th>dB</th></tr>";
    for (let i = 0; i < need; i++) html += `<tr class="pk" data-i="${i}" style="cursor:pointer"><th>${i + 1}</th><td class="pf"></td><td class="pd"></td></tr>`;
    if (!need) html += "<tr><td colspan='3' style='color:var(--dim)'>no peaks</td></tr>";
    body.innerHTML = html;
    rows = body.querySelectorAll("tr.pk");
  }
  for (let i = 0; i < need; i++) {
    const p = S.peaks[i], tr = rows[i], col = selColorCh(ch, i);
    tr.dataset.freq = p.freq;                 // selection carries the FREQUENCY
    tr.style.background = col ? "rgba(255,210,63,.14)" : "";
    tr.style.boxShadow = col ? `inset 3px 0 0 ${col}` : "";
    tr.querySelector(".pf").textContent = eng(p.freq, "Hz", 4);
    tr.querySelector(".pd").textContent = p.db.toFixed(1);
  }
  updateFFTSel(ch);
}

function updateFFTLists() { updateFFTListCh(1); updateFFTListCh(2); }

// ==== wiring ====

// ---- FFT peak-row selection wiring ----




// One delegated handler per channel's (never-replaced) tbody. Toggling by the
// row's own frequency is robust to the list re-sorting between render and click.
for (const ch of [1, 2]) {
  $("fftBody" + ch).addEventListener("click", ev => {
    const tr = ev.target.closest(".pk");
    if (!tr || tr.dataset.freq == null) return;
    togglePeakCh(ch, +tr.dataset.freq);
  });
  $("fftClear" + ch).onclick = () => clearPeaksCh(ch);
  $("fftN" + ch).oninput = () => {
    const v = Math.round(+$("fftN" + ch).value);
    maxPeaks = Number.isFinite(v) && v >= 1 ? Math.min(64, v) : 8;
    $("fftN1").value = maxPeaks; $("fftN2").value = maxPeaks; // shared count
    redraw();
  };
}
