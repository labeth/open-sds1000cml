// ENGMODEL-OWNER-UNIT: FU-WEB-APP-SPECTRUM
// app_spectrum.js — the SPECTRUM workspace: a large FFT (the scope canvas in FFT
// view), a small time trace beside it, the peak table with harmonic numbers and
// THD (app_fft.js + peaks.js harmonics), and a readout. SPECTRUM again cycles
// FFT → spectrogram → Bode; the spectrogram runs while its view is shown.
// Classic script; shares app.js globals.
"use strict";

let specTraceT = 0, specSideT = 0, specArmedSpg = false;

// The spectrogram runs while SPECTRUM shows it (as on the scope); leaving the
// view stops it again (keeping the rows) unless the user armed it themselves.
// TRLC-LINKS: REQ-SDS-216
function specViewChanged(v) {
  anaTools(v);
  if (v === "SPG" && !spg.armed) { $("spgArm").click(); specArmedSpg = true; }
  if (v !== "SPG" && spg.armed && specArmedSpg) { $("spgArm").click(); }
  if (v !== "SPG") specArmedSpg = false;
  $("peakTable").hidden = v === "BODE";
  $("fftSel").hidden = v === "BODE";
  $("peaksNote").hidden = v === "BODE";
  $("bodeTable").hidden = v !== "BODE";
  $("peaksTitle").textContent = v === "BODE" ? "Bode points" : "Peaks";
  for (const id of ["pkCh", "fftClear"]) $(id).hidden = v === "BODE";
  $("fftN").closest("label").hidden = v === "BODE";
  if (v === "BODE") $("pkSub").textContent = "";
  $("tPersist").hidden = true; // no afterglow on a spectrum (shown again on leaving)
  $("timeSub").textContent = v === "BODE" ? "the live inputs (the sweep runs on its own)" : "the timebase sets the span and the resolution";
  $("roTdiv").hidden = v === "BODE";
  if (v === "BODE") bodeRenderNow();
  specSideT = 0;
}

WS_HOOKS.push({
  enter(id) { if (id === "spectrum") specViewChanged(WSV.spectrum); },
  leave(prev) {
    if (prev !== "spectrum") return;
    if (spg.armed && specArmedSpg) { $("spgArm").click(); specArmedSpg = false; }
    $("tPersist").hidden = false;
  },
  view(id, v) { if (id === "spectrum") specViewChanged(v); },
});

// drawMiniTrace paints SPECTRUM's small time trace: both shown channels over the
// home window (the hardware timebase), with a light graticule.
// TRLC-LINKS: REQ-SDS-216
function drawMiniTrace() {
  const cv = $("tracecv");
  if (!fitCanvas(cv)) return;
  const g = glCardCtx(cv, "#05080c");
  if (!g) return;
  const W = cv.width, H = cv.height;
  g.strokeStyle = "#182430"; g.lineWidth = 1;
  for (let i = 1; i < 10; i++) { const x = Math.round(i * W / 10) + 0.5; g.beginPath(); g.moveTo(x, 0); g.lineTo(x, H); g.stroke(); }
  for (let i = 1; i < 8; i++) { const y = Math.round(i * H / 8) + 0.5; g.beginPath(); g.moveTo(0, y); g.lineTo(W, y); g.stroke(); }
  const f = frame;
  if (f) {
    const h = homeWindow(f);
    // TRLC-LINKS: REQ-SDS-216
    const yOf = (code, zoom) => H * (1 - (0.5 + (code - 128) * zoom / 200));
    const chans = [[2, view.c2, C2COL, (st && st.zoom2) || 1], [1, view.c1, C1COL, (st && st.zoom1) || 1]];
    for (const [ch, on, col, zoom] of chans) {
      if (!on) continue;
      g.strokeStyle = col; g.lineWidth = 1.2 * (window.devicePixelRatio || 1);
      if (f.is_env) {
        const mn = ch === 2 ? f.e2min : f.e1min, mx = ch === 2 ? f.e2max : f.e1max;
        if (!mn || !mx) continue;
        g.fillStyle = col; g.globalAlpha = 0.8;
        for (let x = 0; x < W; x++) {
          const i = Math.min(mn.length - 1, Math.floor(x / W * mn.length));
          const yt = yOf(mx[i], zoom), yb = yOf(mn[i], zoom);
          g.fillRect(x, yt, 1, Math.max(1, yb - yt));
        }
        g.globalAlpha = 1;
        continue;
      }
      const src = ch === 2 ? f.c2 : f.c1;
      if (!src || src.length < 2) continue;
      const n = src.length;
      g.beginPath();
      let pen = false;
      for (let x = 0; x < W; x++) {
        const a = Math.max(0, Math.floor((h.a + (h.b - h.a) * x / W) * (n - 1)));
        const b = Math.min(n - 1, Math.max(a, Math.floor((h.a + (h.b - h.a) * (x + 1) / W) * (n - 1))));
        let lo = Infinity, hi = -Infinity;
        for (let i = a; i <= b; i++) { const v = src[i]; if (v < 0) continue; if (v < lo) lo = v; if (v > hi) hi = v; }
        if (hi < lo) { pen = false; continue; }
        const y1 = yOf(hi, zoom), y2 = yOf(lo, zoom);
        if (pen) g.lineTo(x, y1); else { g.moveTo(x, y1); pen = true; }
        if (y2 !== y1) g.lineTo(x, y2);
      }
      g.stroke();
    }
  }
  glCardEnd(cv);
}

// specReadout fills the readout: what the spectrum covers and what it found.
// TRLC-LINKS: REQ-SDS-216
function specReadout() {
  const rows = [];
  const v = view.mode;
  if (v === "BODE") {
    const p = bode.pts || { freq: [] }, n = p.freq ? p.freq.length : 0;
    rows.push(["Points", String(n) + (bode.armed ? " · armed" : "")]);
    if (n) {
      let pk = 0, f3 = -1;
      for (let i = 0; i < n; i++) if (p.gain_db[i] > p.gain_db[pk]) pk = i;
      for (let i = 1; i < n; i++) if (p.gain_db[i] < p.gain_db[0] - 3) { f3 = i; break; }
      rows.push(["Range", eng(p.freq[0], "Hz", 3) + " – " + eng(p.freq[n - 1], "Hz", 3)],
        ["Peak gain", p.gain_db[pk].toFixed(2) + " dB at " + eng(p.freq[pk], "Hz", 3)],
        ["−3 dB", f3 >= 0 ? "near " + eng(p.freq[f3], "Hz", 3) + ", " + p.phase_deg[f3].toFixed(0) + "°" : "—"]);
    }
    if (st && st.bode_mode > 0 && st.bode_valid) rows.push(["Live", eng(st.bode_freq_hz, "Hz", 3) + " · " + st.bode_gain_db.toFixed(2) + " dB · " + st.bode_phase_deg.toFixed(1) + "°"]);
  } else {
    const ch = pkChannel(), spec = specMemo[ch] && specMemo[ch].spec;
    const nyq = displayNyq();
    rows.push(["Source", "CH" + ch + (chOn(ch) ? "" : " (off)")]);
    rows.push(["Span", "0 – " + eng(nyq, "Hz", 3)]);
    if (spec) {
      rows.push(["Resolution", eng(nyq / spec.half, "Hz", 3) + " per bin · Hann · " + (spec.half * 2) + " points"]);
      const fl = specFloor(ch);
      if (fl > 0) rows.push(["Noise floor", (20 * Math.log10(fl / spec.peak)).toFixed(1) + " dB re the largest peak"]);
      const P = fftCh[ch].peaks;
      if (P.length) {
        const H = harmonics(P, spec);
        const top = H.fund >= 0 ? P[H.fund] : P.reduce((a, b) => (peakLvl(b) > peakLvl(a) ? b : a), P[0]);
        const lv = peakDBV(ch, top);
        rows.push(["Largest peak", eng(top.freq, "Hz", 4) + (Number.isFinite(lv) ? " · " + lv.toFixed(1) + " dBV" : "")]);
        rows.push(["THD", H.count ? (H.thd * 100).toFixed(2) + " % (" + H.count + (H.count === 1 ? " harmonic" : " harmonics") + " of the " + P.length + " peaks listed)" : "— (no harmonic among the listed peaks)"]);
      }
    }
    const dt = fftUseRaw() && fftRaw.sample_s > 0 ? fftRaw.sample_s : frame && (frame.c1 || frame.c2) ? frameDtS(frame, (frame.c1 || frame.c2).length) : 0;
    if (dt > 0) rows.push(["Sample rate", eng(1 / dt, "Sa/s", 3) + (fftUseRaw() ? " (full raw record)" : "")]);
    if (v === "SPG" && spg.sg) rows.push(["Waterfall", spg.sg.rows + " / " + spg.sg.h + " rows, newest on top"]);
  }
  const html = rows.map(([a, b]) => `<tr><th>${a}</th><td>${escHTML(b)}</td></tr>`).join("");
  const body = $("roBody");
  if (body.dataset.sig !== html) { body.dataset.sig = html; body.innerHTML = html; }
  const sub = { FFT: "FFT", SPG: "Spectrogram", BODE: "Bode" }[v] || "";
  if ($("roSub").textContent !== sub) $("roSub").textContent = sub;
}

// specOnBode lists the Bode points (newest sweep order: by frequency) in the
// peaks pane while the Bode view is shown, and the readout counts them with it.
// TRLC-LINKS: REQ-SDS-216
function specOnBode(p) {
  if ($("bodeTable").hidden) return;
  const n = p && p.freq ? p.freq.length : 0;
  let html = "";
  for (let i = 0; i < n; i++) html += `<tr><td class="l n">${i + 1}</td><td class="cc1">${eng(p.freq[i], "Hz", 4)}</td><td>${p.gain_db[i].toFixed(2)} dB</td><td class="cc2">${p.phase_deg[i].toFixed(1)}°</td></tr>`;
  if (!n) html = `<tr><td class="e" colspan="4">No points yet: put the DUT input on REF and its output on DUT, ARM, then sweep the source.</td></tr>`;
  const b = $("bodeBody");
  if (b.dataset.sig !== html) { b.dataset.sig = html; b.innerHTML = html; }
  specReadout();
}

WS_TICKS.push((now) => {
  if (ws.cur !== "spectrum") return;
  if (now - specTraceT > 60) { specTraceT = now; drawMiniTrace(); }
  if (now - specSideT > 400) {
    specSideT = now;
    // outside the FFT view drawFFT does not run: keep the peak table current here
    if (view.mode !== "FFT" && view.mode !== "BODE") { const ch = pkChannel(); computePeaksCh(ch); updateFFTListCh(ch); }
    specReadout();
  }
});
