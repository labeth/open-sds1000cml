// ENGMODEL-OWNER-UNIT: FU-WEB-APP-SOUND
// app_sound_take.js — SOUND's takes on the web page (ADR-SOUND): RUN/STOP,
// SINGLE and the trigger mode as the rest of the scope has them; a take
// shown (STOP's last seconds, a NORMAL or SINGLE trigger's stretch with the
// trigger at the marker) in the Take view: the waveform of both channels,
// a spectrogram of the tuned one with its pitch over time, scrolled by a
// drag or a turned wheel with Shift and zoomed by the wheel; two cursors
// (a click, a Shift-click) read time, the volts of both channels, the
// frequency and its level, and Δt and Δf between them. Listen plays the
// take's window, looped, at full, half or quarter speed. The readouts
// (tuner, meters, the other views) read the take at the cursor or over the
// window. Classic script after app_sound.js.
"use strict";

const SND_TAKE_FMIN = 30;
const SND_TAKE_FMAX = 8000;
const SND_TAKE_ROWS = 160;

// sndTk is the take view's own state: the cursors (sample in the take and,
// placed on the spectrogram, a frequency), a drag, the analysis cache.
const sndTk = { cur: [null, null], drag: null, key: "", pendKey: "", pendAt: 0, spec: null, pitch: null, img: null, prevView: "cycle", wasOn: false, lanes: null };

// sndTakeTrigIdx is the trigger's sample in the take (-1 without).
// TRLC-LINKS: REQ-SDS-275
function sndTakeTrigIdx() { const t = snd.take; return t && t.hasTrig ? t.trig - t.first : -1; }

// sndTakeT is sample i's time: from the trigger, or from the take's start.
// TRLC-LINKS: REQ-SDS-275
function sndTakeT(i) { const ti = sndTakeTrigIdx(); return (i - (ti >= 0 ? ti : 0)) / snd.take.rate; }

// sndTakeY maps a frequency to the spectrogram lane's height (log axis).
// TRLC-LINKS: REQ-SDS-275
function sndTakeFy(f, y0, h) { return y0 + h * (1 - Math.log(f / SND_TAKE_FMIN) / Math.log(SND_TAKE_FMAX / SND_TAKE_FMIN)); }

// sndTakeYf is the frequency at a height of the spectrogram lane.
// TRLC-LINKS: REQ-SDS-275
function sndTakeYf(y, y0, h) { return SND_TAKE_FMIN * Math.pow(SND_TAKE_FMAX / SND_TAKE_FMIN, 1 - (y - y0) / h); }

// sndHeat is the spectrogram's colour of a level 0..1.
// TRLC-LINKS: REQ-SDS-275
function sndHeat(v) {
  v = Math.max(0, Math.min(1, v));
  const r = Math.min(255, Math.max(0, 255 * (1.6 * v - 0.3)));
  const g = Math.min(255, Math.max(0, 255 * (2 * v - 1)));
  const b = Math.min(255, Math.max(0, 255 * (v < 0.4 ? 2.2 * v : 1.6 - 2 * v)));
  return [r, g, b];
}

// sndTakeAnalyse makes the window's spectrogram and pitch curve once the
// window has held still for a moment (scrolling stays light).
// TRLC-LINKS: REQ-SDS-275
function sndTakeAnalyse(cols) {
  const t = snd.take, v = snd.tview, set = snd.settings;
  const key = `${t.id}:${Math.round(v.from)}:${Math.round(v.span)}:${cols}:${set.ch}:${set.range}`;
  if (key === sndTk.key) return;
  const now = performance.now();
  if (key !== sndTk.pendKey) { sndTk.pendKey = key; sndTk.pendAt = now; }
  if (sndTk.spec && now - sndTk.pendAt < 150) return;
  sndTk.key = key;
  const x = set.ch === 1 ? t.c2 : t.c1, secs = v.span / t.rate;
  const n = secs >= 0.5 ? 8192 : secs >= 0.1 ? 4096 : 2048;
  sndTk.spec = sndSpectrogram(x, t.rate, v.from, v.span, cols, n);
  sndTk.pitch = sndPitchTrack(x, t.rate, v.from, v.span, cols, set.range);
  // the image: rows on a log axis, each the strongest bin in its band
  let top = -Infinity;
  for (const col of sndTk.spec.cols) for (let i = 1; i < col.length; i++) top = Math.max(top, col[i]);
  const floor = top - 90, rows = SND_TAKE_ROWS, img = new Uint8Array(cols * rows * 4), bin = sndTk.spec.binHz;
  for (let r = 0; r < rows; r++) {
    const f1 = sndTakeYf(r, 0, rows), f0 = sndTakeYf(r + 1, 0, rows);
    const k0 = Math.max(1, Math.floor(f0 / bin)), k1 = Math.max(k0, Math.ceil(f1 / bin));
    for (let c = 0; c < cols; c++) {
      const col = sndTk.spec.cols[c];
      let best = -Infinity;
      for (let k = k0; k <= k1 && k < col.length; k++) best = Math.max(best, col[k]);
      const [cr, cg, cb] = sndHeat((best - floor) / (top - floor));
      const o = 4 * (r * cols + c);
      img[o] = cr; img[o + 1] = cg; img[o + 2] = cb; img[o + 3] = 255;
    }
  }
  sndTk.img = { rgba: img, w: cols, h: rows, top, floor };
}

// sndTakeRender draws the take view into the Sound view's canvas.
// TRLC-LINKS: REQ-SDS-275
function sndTakeRender(g, W, H, d) {
  const t = snd.take, v = snd.tview, set = snd.settings;
  if (!t) { g.fillStyle = DIMCOL; g.textAlign = "center"; g.fillText(snd.mode === "armed" ? "Armed: waiting for the FPGA's edge" : snd.mode === "triggered" ? "Triggered: streaming the post-trigger time" : "No take: STOP, SINGLE or NORMAL", W / 2, H / 2); g.textAlign = "left"; return; }
  const waveH = Math.round(H * 0.4), specY = waveH + 14 * d, specH = H - specY - 14 * d;
  sndTk.lanes = { waveH, specY, specH, W, H };
  // TRLC-LINKS: REQ-SDS-275
  const xOf = (i) => (i - v.from) / v.span * W;
  // the waveform: both channels, a band of each column's lowest and highest
  const cols = Math.max(16, Math.min(Math.round(W), 1600));
  let lo = Infinity, hi = -Infinity;
  const mm = [0, 1].map((ch) => sndMinMaxColumns(sndTakeWindow(snd, ch), cols));
  for (const m of mm) for (let i = 0; i < m.lo.length; i++) { lo = Math.min(lo, m.lo[i]); hi = Math.max(hi, m.hi[i]); }
  if (!(hi > lo)) { lo = -1; hi = 1; }
  const pad = 0.06 * (hi - lo);
  lo -= pad; hi += pad;
  // TRLC-LINKS: REQ-SDS-275
  const yOf = (val) => waveH - (val - lo) / (hi - lo) * waveH;
  g.strokeStyle = "#182430"; g.lineWidth = 1;
  for (let k = 1; k < 10; k++) { g.beginPath(); g.moveTo(k * W / 10, 0); g.lineTo(k * W / 10, waveH); g.stroke(); }
  if (lo < 0 && hi > 0) { g.beginPath(); g.moveTo(0, yOf(0)); g.lineTo(W, yOf(0)); g.stroke(); }
  for (const ch of [1, 0]) {
    const m = mm[ch], cw = W / m.lo.length;
    g.fillStyle = ch === 1 ? C2COL : C1COL;
    g.globalAlpha = ch === set.ch ? 1 : 0.55;
    for (let i = 0; i < m.lo.length; i++) { const y1 = yOf(m.hi[i]), y2 = yOf(m.lo[i]); g.fillRect(i * cw, y1, Math.max(1, cw), Math.max(d, y2 - y1)); }
  }
  g.globalAlpha = 1;
  g.fillStyle = DIMCOL; g.font = (10 * d) + "px monospace";
  g.fillText(eng(hi, "V"), W - 64 * d, 12 * d); g.fillText(eng(lo, "V"), W - 64 * d, waveH - 4 * d);
  // the spectrogram and the pitch curve
  sndTakeAnalyse(Math.max(40, Math.min(240, Math.round(W / (4 * d)))));
  if (sndTk.img) g.blit(sndTk.img.rgba, sndTk.img.w, sndTk.img.h, 0, specY, W, specH, true);
  for (const nt of sndNoteGrid(SND_TAKE_FMIN, SND_TAKE_FMAX, set.a4)) {
    if (!/^C\d|^A\d/.test(nt.name)) continue;
    const y = sndTakeFy(nt.f, specY, specH);
    g.strokeStyle = "rgba(200,210,220,0.18)"; g.beginPath(); g.moveTo(0, y); g.lineTo(W, y); g.stroke();
    g.fillStyle = "rgba(200,210,220,0.6)"; g.fillText(nt.name, 2 * d, y - 2 * d);
  }
  if (sndTk.pitch) {
    const p = sndTk.pitch, n = p.length;
    g.strokeStyle = "#ffffff"; g.lineWidth = 1.6 * d; g.beginPath();
    let pen = false;
    for (let c = 0; c < n; c++) {
      if (!(p[c] > SND_TAKE_FMIN && p[c] < SND_TAKE_FMAX)) { pen = false; continue; }
      const x = (c + 0.5) * W / n, y = sndTakeFy(p[c], specY, specH);
      if (pen) g.lineTo(x, y); else { g.moveTo(x, y); pen = true; }
    }
    g.stroke();
  }
  // time axis: seconds from the trigger (or the take's start)
  g.fillStyle = DIMCOL;
  for (let k = 0; k <= 10; k += 2) { const i = v.from + k * v.span / 10; g.fillText(sndTakeTxt(sndTakeT(i)), Math.min(W - 50 * d, k * W / 10 + 2 * d), waveH + 11 * d); }
  g.fillText(`C${set.ch + 1} spectrogram, ${eng(SND_TAKE_FMIN, "Hz")}–${eng(SND_TAKE_FMAX, "Hz")} log · white: pitch`, 4 * d, H - 3 * d);
  // the trigger: a dashed line with its marker on top
  const ti = sndTakeTrigIdx();
  if (ti >= 0 && ti >= v.from && ti <= v.from + v.span) {
    const x = xOf(ti);
    g.strokeStyle = TRIGCOL; g.lineWidth = 1.2 * d; g.setLineDash([5 * d, 4 * d]); g.beginPath(); g.moveTo(x, 0); g.lineTo(x, H); g.stroke(); g.setLineDash([]);
    g.fillStyle = TRIGCOL; g.beginPath(); g.moveTo(x - 6 * d, 0); g.lineTo(x + 6 * d, 0); g.lineTo(x, 9 * d); g.closePath(); g.fill();
    g.fillText("T", x + 7 * d, 10 * d);
  }
  // the cursors
  sndTk.cur.forEach((cu, k) => {
    if (!cu) return;
    const x = xOf(cu.i);
    g.strokeStyle = CURCOL; g.lineWidth = d; g.setLineDash(k ? [3 * d, 3 * d] : []);
    g.beginPath(); g.moveTo(x, 0); g.lineTo(x, H); g.stroke();
    if (cu.f) { const y = sndTakeFy(cu.f, specY, specH); g.beginPath(); g.moveTo(0, y); g.lineTo(W, y); g.stroke(); }
    g.setLineDash([]);
    g.fillStyle = CURCOL; g.fillText(k ? "B" : "A", x + 3 * d, waveH - 16 * d);
  });
  // Listen's place in the take
  if (snd.audio.on && snd.play.on) { const x = xOf(snd.play.pos); g.fillStyle = "#ffffff"; g.fillRect(x, 0, d, H); }
  // where the window sits in the take
  g.fillStyle = "#24364a"; g.fillRect(0, H - 2 * d, W, 2 * d);
  g.fillStyle = "#7c8894"; g.fillRect(v.from / t.n * W, H - 2 * d, Math.max(2 * d, v.span / t.n * W), 2 * d);
  $("sndViewSub").textContent = `${{ stop: "STOP", single: "SINGLE", normal: "NORMAL" }[t.why] || t.why} take ${(t.n / t.rate).toFixed(2)} s` +
    (t.hasTrig ? ` · trigger C${t.trigCh + 1} ${t.trigRising ? "rising" : "falling"} ${eng(t.trigV, "V")} (FPGA edge)` : "") +
    ` · view ${eng(v.span / t.rate, "s")} · drag/Shift+wheel scroll, wheel zoom, click A, Shift-click B, double-click all`;
}

// sndTakeTxt writes a time from the trigger.
// TRLC-LINKS: REQ-SDS-275
function sndTakeTxt(s) { return (s >= 0 ? "+" : "") + eng(s, "s"); }

// sndTakeCursorRows are the cursors' readings and their differences.
// TRLC-LINKS: REQ-SDS-275
function sndTakeCursorRows() {
  const t = snd.take, set = snd.settings, rows = [];
  if (!t) return rows;
  const read = sndTk.cur.map((cu) => {
    if (!cu) return null;
    const i = Math.max(0, Math.min(t.n - 1, Math.round(cu.i)));
    let f = cu.f, src = "spectrogram";
    if (!f) {
      const x = set.ch === 1 ? t.c2 : t.c1, n = 4096, a = Math.max(0, Math.min(t.n - n, i - n / 2));
      const p = t.n >= 256 ? sndPitch(x.subarray(a, Math.min(t.n, a + n)), t.rate, sndPitchRange(set.range)) : { ok: false };
      f = p.ok ? p.hz : 0; src = "pitch";
    }
    let db = NaN;
    if (f && sndTk.spec) {
      const v = snd.tview, c = Math.floor((i - v.from) / v.span * sndTk.spec.cols.length), col = sndTk.spec.cols[c];
      if (col) db = col[Math.min(col.length - 1, Math.round(f / sndTk.spec.binHz))];
    }
    return { i, t: sndTakeT(i), v1: t.c1[i], v2: t.c2[i], f, src, db };
  });
  read.forEach((r, k) => {
    if (!r) return;
    rows.push(sndRow(k ? "B" : "A", sndTakeTxt(r.t), `C1 ${eng(r.v1, "V")} · C2 ${eng(r.v2, "V")}`));
    rows.push(sndRow("", r.f ? `${eng(r.f, "Hz", 5)} ${sndNote(r.f, set.a4).text}` : "no pitch", r.f ? `${r.src}${Number.isFinite(r.db) ? " · " + r.db.toFixed(1) + " dBV" : ""}` : ""));
  });
  if (read[0] && read[1]) {
    const dt = read[1].t - read[0].t, df = read[1].f && read[0].f ? read[1].f - read[0].f : NaN;
    rows.push(sndRow("Δt", sndTakeTxt(dt), dt ? "1/Δt " + eng(1 / Math.abs(dt), "Hz") : ""));
    if (Number.isFinite(df)) rows.push(sndRow("Δf", (df >= 0 ? "+" : "") + eng(df, "Hz"), `${sndCents(read[1].f, read[0].f).toFixed(1)} c`));
  }
  const v = snd.tview;
  rows.push(sndRow("window", `${sndTakeTxt(sndTakeT(v.from))} … ${sndTakeTxt(sndTakeT(v.from + v.span))}`, "readouts over it" + (sndTk.cur[0] ? " (tuner at A)" : "")));
  if (!sndTk.cur[0]) rows.push(sndRow("cursors", "click: A", "Shift-click: B · on the spectrogram: a frequency too"));
  return rows;
}

// sndTakeEventX is an event's place across the canvas in device pixels.
// TRLC-LINKS: REQ-SDS-275
function sndTakeEventXY(cv, e) {
  const r = cv.getBoundingClientRect();
  return [(e.clientX - r.left) * cv.width / r.width, (e.clientY - r.top) * cv.height / r.height];
}

// sndTakeWire connects the take's controls and the canvas's mouse.
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-274
function sndTakeWire() {
  const cv = $("sndViewCv");
  segInit($("sndTrigMode"));
  segInit($("sndSpeed"));
  // TRLC-LINKS: REQ-SDS-275
  const active = () => snd.view === "take" && snd.take;
  // TRLC-LINKS: REQ-SDS-275
  cv.addEventListener("wheel", (e) => {
    if (!active()) return;
    e.preventDefault();
    const [x] = sndTakeEventXY(cv, e), v = snd.tview, at = v.from + x / cv.width * v.span;
    if (e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY)) sndTakeScroll(snd, Math.sign(e.deltaX || e.deltaY) * v.span / 10);
    else sndTakeZoom(snd, e.deltaY > 0 ? 1.3 : 1 / 1.3, at);
  }, { passive: false });
  // TRLC-LINKS: REQ-SDS-275
  cv.addEventListener("mousedown", (e) => {
    if (!active()) return;
    const [x, y] = sndTakeEventXY(cv, e);
    sndTk.drag = { x0: x, from0: snd.tview.from, moved: false, y, shift: e.shiftKey || e.button === 2 };
  });
  // TRLC-LINKS: REQ-SDS-275
  window.addEventListener("mousemove", (e) => {
    const dr = sndTk.drag;
    if (!dr || !active()) return;
    const [x] = sndTakeEventXY(cv, e);
    if (Math.abs(x - dr.x0) > 4) dr.moved = true;
    if (dr.moved) { snd.tview.from = dr.from0; sndTakeScroll(snd, -(x - dr.x0) / cv.width * snd.tview.span); }
  });
  // TRLC-LINKS: REQ-SDS-275
  window.addEventListener("mouseup", (e) => {
    const dr = sndTk.drag;
    sndTk.drag = null;
    if (!dr || dr.moved || !active()) return;
    const [x] = sndTakeEventXY(cv, e), v = snd.tview, L = sndTk.lanes;
    const cu = { i: v.from + x / cv.width * v.span, f: 0 };
    if (L && dr.y >= L.specY && dr.y <= L.specY + L.specH) cu.f = sndTakeYf(dr.y, L.specY, L.specH);
    sndTk.cur[dr.shift ? 1 : 0] = cu;
    if (!dr.shift) snd.focus = cu.i;
  });
  // TRLC-LINKS: REQ-SDS-275
  cv.addEventListener("contextmenu", (e) => { if (active()) e.preventDefault(); });
  // TRLC-LINKS: REQ-SDS-275
  cv.addEventListener("dblclick", () => { if (active()) { snd.tview = { from: 0, span: snd.take.n }; sndTk.cur = [null, null]; snd.focus = -1; } });
  // TRLC-LINKS: REQ-SDS-275
  $("sndRun").onclick = () => send("runtoggle", 0);
  // TRLC-LINKS: REQ-SDS-275
  $("sndSingle").onclick = () => send("single", 1);
  // TRLC-LINKS: REQ-SDS-275
  $("sndTrigMode").onchange = () => send("norm", $("sndTrigMode").value === "norm" ? 1 : 0);
  // TRLC-LINKS: REQ-SDS-275
  $("sndPre").onchange = () => sndSet({ pre_s: +$("sndPre").value });
  // TRLC-LINKS: REQ-SDS-275
  $("sndPost").onchange = () => sndSet({ post_s: +$("sndPost").value });
  // TRLC-LINKS: REQ-SDS-275
  $("sndTrigFrom").onchange = () => sndSet({ trig_gate: $("sndTrigFrom").value === "1" });
  // TRLC-LINKS: REQ-SDS-275
  $("sndLoop").onclick = () => { snd.play.loop = !snd.play.loop; $("sndLoop").classList.toggle("on", snd.play.loop); };
  // TRLC-LINKS: REQ-SDS-275
  $("sndSpeed").onchange = () => { snd.play.speed = +$("sndSpeed").value || 1; };
  // TRLC-LINKS: REQ-SDS-275
  $("sndTakeAll").onclick = () => { if (snd.take) { snd.tview = { from: 0, span: snd.take.n }; } };
  // TRLC-LINKS: REQ-SDS-275
  $("sndTakeTrig").onclick = () => {
    const ti = sndTakeTrigIdx();
    if (ti < 0) return;
    const v = snd.tview;
    sndTakeScroll(snd, ti - (v.from + v.span / 2));
  };
}

// sndTakeTick runs each analysis tick: Listen on the take, the Take view
// in and out as takes come and go, and the bar's state.
// TRLC-LINKS: REQ-SDS-275
function sndTakeTick() {
  sndTakeFeed(snd);
  if (snd.takeOn && !sndTk.wasOn) {
    sndTk.prevView = snd.view === "take" ? sndTk.prevView : snd.view;
    sndTk.cur = [null, null];
    snd.focus = -1;
    sndSetView("take");
  } else if (!snd.takeOn && sndTk.wasOn && snd.view === "take") sndSetView(sndTk.prevView);
  if (snd.take && sndTk.lastId !== snd.take.id) {
    // a new take: the cursors stay where they are on a NORMAL refresh of the same length
    sndTk.lastId = snd.take.id;
    if (snd.take.why === "stop" || sndTk.cur.some((cu) => cu && cu.i >= snd.take.n)) { sndTk.cur = [null, null]; snd.focus = -1; }
  }
  sndTk.wasOn = snd.takeOn;
  const running = st ? !!st.running : true, norm = st ? !!st.norm : false, single = st ? !!st.single : false;
  $("sndRun").textContent = running ? "STOP ■" : "RUN ▶";
  $("sndRun").classList.toggle("on", running);
  $("sndSingle").classList.toggle("on", single);
  if (document.activeElement !== $("sndTrigMode")) $("sndTrigMode").value = norm ? "norm" : "auto";
  const set = snd.settings;
  if (document.activeElement !== $("sndPre") && set.pre_s !== undefined) $("sndPre").value = set.pre_s;
  if (document.activeElement !== $("sndPost") && set.post_s !== undefined) $("sndPost").value = set.post_s;
  $("sndTrigFrom").value = set.trig_gate ? "1" : "0";
  const m = snd.mode, t = snd.take;
  $("sndTakeSt").textContent = m === "live" ? "live" : m === "stopped" ? "stopped" + (t ? ` · take ${(t.n / t.rate).toFixed(1)} s` : "") :
    m === "armed" ? "armed: waiting for the FPGA's edge" : m === "triggered" ? "triggered" : m;
}
sndTakeWire();
