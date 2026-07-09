// app_zonemask.js — zone-trigger + mask editor UI (classic script; shares app.js globals).

// --- coordinate transforms (display point <-> edge-anchored zone coords) ---
"use strict";
function zmPointToZone(p) { // p = ptToNorm point -> {dtS, code}
  if (!frame || !frame.cols || !(frame.col_span_s > 0)) return null;
  const cols = frame.cols;
  const frac = view.win.a + p.x * (view.win.b - view.win.a);
  const c = frac * (cols - 1);
  const edgeCol = frame.edge_frac >= 0 ? frame.edge_frac * cols : cols / 2;
  return { dtS: (c - edgeCol) * (frame.col_span_s / cols), code: codeAtY(p.y, 1) };
}

function zmZoneToRect(z) { // zone -> on-screen rect {x0,x1,y0,y1} in px, or null
  if (!frame || !frame.cols || !(frame.col_span_s > 0)) return null;
  const cols = frame.cols;
  const edgeCol = frame.edge_frac >= 0 ? frame.edge_frac * cols : cols / 2;
  const spc = frame.col_span_s / cols;
  const span = view.win.b - view.win.a || 1;
  const xOf = dt => ((edgeCol + dt / spc) / (cols - 1) - view.win.a) / span * CW;
  return {
    x0: xOf(Math.min(z.dt_lo_s, z.dt_hi_s)), x1: xOf(Math.max(z.dt_lo_s, z.dt_hi_s)),
    y0: yFor(z.code_hi, 1), y1: yFor(z.code_lo, 1),
  };
}

// --- overlay rendering (called from redraw) ---
function drawZones(g) {
  if (view.mode !== "YT") return;
  for (const z of zm.zones) {
    const r = zmZoneToRect(z);
    if (!r) continue;
    g.fillStyle = z.avoid ? "rgba(240,80,80,0.13)" : "rgba(80,220,120,0.13)";
    g.strokeStyle = z.avoid ? "rgba(240,80,80,0.6)" : "rgba(80,220,120,0.6)";
    g.fillRect(r.x0, r.y0, r.x1 - r.x0, r.y1 - r.y0);
    g.strokeRect(r.x0, r.y0, r.x1 - r.x0, r.y1 - r.y0);
  }
  if (zm.drawArmed && zm.drawA && zm.drawB) { // live preview while dragging
    const x0 = Math.min(zm.drawA.x, zm.drawB.x) * CW, x1 = Math.max(zm.drawA.x, zm.drawB.x) * CW;
    const y0 = Math.min(zm.drawA.y, zm.drawB.y) * CH, y1 = Math.max(zm.drawA.y, zm.drawB.y) * CH;
    g.setLineDash([4, 4]);
    g.strokeStyle = "rgba(80,220,120,0.9)";
    g.strokeRect(x0, y0, x1 - x0, y1 - y0);
    g.setLineDash([]);
  }
  // mask envelope (standard windowed display only; deep serves skip the render)
  if (zm.mask && frame && frame.win_frac === 1 && st && st.win_cols === zm.mask.win) {
    const win = zm.mask.win, span = view.win.b - view.win.a || 1;
    g.strokeStyle = "rgba(242,166,59,0.55)";
    for (const env of [zm.mask.lo, zm.mask.hi]) {
      g.beginPath();
      let started = false;
      for (let x = 0; x < CW; x += 2) {
        const fr = view.win.a + (x / CW) * span;
        const j = Math.round(fr * (win - 1));
        if (j < 0 || j >= win) { started = false; continue; }
        const y = yFor(env[j], 1);
        if (!started) { g.moveTo(x, y); started = true; } else g.lineTo(x, y);
      }
      g.stroke();
    }
  }
  if (zm.failMark && frame) { // violation marker on a gallery frame
    const span = view.win.b - view.win.a || 1;
    const x = (zm.failMark.frac - view.win.a) / span * CW;
    const y = yFor(zm.failMark.code, 1);
    g.strokeStyle = "#f05050";
    g.lineWidth = 2 * dpr;
    g.beginPath(); g.arc(x, y, 9 * dpr, 0, 2 * Math.PI); g.stroke();
    g.lineWidth = dpr;
  }
}

// Zone/mask test RAW capture codes; AC/GND coupling is a display-only
// transform here, so what the user sees would not be what is tested.
// Refuse to create misaligned artifacts (review finding).
function zmCplOK(ch) {
  const cpl = ch === 1 ? (st && st.cpl2) : (st && st.cpl1);
  if (cpl) {
    zmStatus("zone/mask test the RAW capture — set C" + (ch + 1) + " coupling to DC first");
    return false;
  }
  return true;
}

function zmPointerDown(p) { // true = consumed
  if (!zm.drawArmed || view.mode !== "YT") return false;
  zm.drawA = p; zm.drawB = p;
  return true;
}

function zmPointerMove(p) {
  if (!zm.drawArmed || !zm.drawA) return false;
  zm.drawB = p;
  redraw();
  return true;
}

function zmPointerUp() {
  if (!zm.drawArmed || !zm.drawA || !zm.drawB) return false;
  const a = zmPointToZone(zm.drawA), b = zmPointToZone(zm.drawB);
  zm.drawArmed = false;
  $("zmDraw").classList.remove("on");
  zm.drawA = zm.drawB = null;
  if (a && b && Math.abs(a.dtS - b.dtS) > 0) {
    const ch = +$("zmCh").value || 0;
    const z = {
      dt_lo_s: Math.min(a.dtS, b.dtS), dt_hi_s: Math.max(a.dtS, b.dtS),
      code_lo: Math.round(Math.min(a.code, b.code)), code_hi: Math.round(Math.max(a.code, b.code)),
      avoid: false, ch,
    };
    // A zone is physically VOLTS: freeze its source codes + vertical context
    // so V/div or offset changes re-map it instead of silently re-aiming it
    // (server ignores the underscore fields).
    const c = zmVctx(ch);
    if (c) { z._sclo = z.code_lo; z._schi = z.code_hi; z._svpc = c.vpc; z._soff = c.off; z._avpc = c.vpc; z._aoff = c.off; }
    zm.zones.push(z);
    zmPushZones();
  }
  redraw();
  return true;
}

function zmPushZones() {
  fetch("/api/zones", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(zm.zones) }).catch(() => {});
  zmZoneList();
}

function zmZoneList() {
  const rows = zm.zones.map((z, i) =>
    `<div>z${i + 1} <button class="btn-mini zmm" data-i="${i}">${z.avoid ? "avoid" : "hit"}</button> ` +
    `${eng(z.dt_lo_s, "s", 2)}..${eng(z.dt_hi_s, "s", 2)} · ${z.code_lo}-${z.code_hi} ` +
    `<button class="btn-mini zmx" data-i="${i}">✕</button></div>`).join("");
  $("zmZoneList").innerHTML = rows;
  for (const b of document.querySelectorAll("#zmZoneList .zmm")) {
    b.onclick = () => { zm.zones[+b.dataset.i].avoid = !zm.zones[+b.dataset.i].avoid; zmPushZones(); redraw(); };
  }
  for (const b of document.querySelectorAll("#zmZoneList .zmx")) {
    b.onclick = () => { zm.zones.splice(+b.dataset.i, 1); zmPushZones(); redraw(); };
  }
}

function zmStatus(m) { if (m !== undefined) $("zmStats").textContent = m || "—"; }

// --- vertical re-anchoring: masks and zones are physically VOLTS ---
// The engine tests raw codes and honestly SKIPS a mask whose vertical
// mapping changed (VdivKey/OffKey identity) — but a skipping test plus a
// stale overlay LOOKS happy. The client owns the volts<->code mapping, so on
// any V/div or offset change it re-maps the frozen volts-space source into
// the new code space and re-installs (same precedent as the REF rescale).
function zmVctx(ch) { // live vertical context; null while frozen/env (no authority)
  if (!frame || frozen || frame.is_env || !(frame.vpc1 > 0)) return null;
  return ch === 1 ? { vpc: frame.vpc2, off: frame.off2_v || 0 } : { vpc: frame.vpc1, off: frame.off1_v || 0 };
}

function zmRescale() {
  const near = (a, b) => Math.abs(a - b) <= Math.abs(b) * 1e-9 + 1e-12;
  let zchg = false;
  for (const z of zm.zones) {
    if (!(z._svpc > 0)) continue;
    const c = zmVctx(z.ch || 0);
    if (!c || (near(c.vpc, z._avpc) && near(c.off, z._aoff))) continue;
    const map = v => 128 + ((v - 128) * z._svpc - z._soff + c.off) / c.vpc;
    const a = Math.round(map(z._sclo)), b = Math.round(map(z._schi));
    z.code_lo = Math.max(0, Math.min(255, Math.min(a, b)));
    z.code_hi = Math.max(0, Math.min(255, Math.max(a, b)));
    z._avpc = c.vpc; z._aoff = c.off;
    zchg = true;
  }
  if (zchg) zmPushZones();
  const m = zm.mask;
  let mchg = false;
  if (m && m.srcLo && m.svpc > 0) {
    const c = zmVctx(m.ch || 0);
    if (c && !(near(c.vpc, m.avpc) && near(c.off, m.aoff))) {
      const map = v => 128 + ((v - 128) * m.svpc - m.soff + c.off) / c.vpc;
      for (let j = 0; j < m.win; j++) { // floor/ceil: never shrink the physical envelope
        m.lo[j] = Math.max(0, Math.min(255, Math.floor(map(m.srcLo[j]))));
        m.hi[j] = Math.max(0, Math.min(255, Math.ceil(map(m.srcHi[j]))));
      }
      m.avpc = c.vpc; m.aoff = c.off;
      mchg = true;
      fetch("/api/mask", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ lo: m.lo, hi: m.hi, win: m.win, ch: m.ch || 0 }) }).catch(() => {});
    }
  }
  if (zchg || mchg) { zmStatus("re-anchored to the new vertical scale"); redraw(); }
}

// ==== wiring ====

// ---- zone-trigger + mask wiring ----


// ---- zone trigger + mask testing (engine-side; this is the config surface) ----
// window property (not const): the scope draw path runs during load BEFORE
// this glue executes — a lexical binding would be a TDZ landmine there.
window.zm = {
  zones: [],      // client copy for rendering + editing: engine-format objects
  mask: null,     // {lo, hi, win} client copy for rendering
  drawArmed: false, drawA: null, drawB: null,
  failMark: null, // {frac, code} violation marker on a gallery frame
  lastRing: -1,
  lastSkip: -1, lastZoneSkip: -1, // skip-delta trackers (stale-test warnings)
};




// --- zone drawing (armed drag; wired into the scope pointer handlers) ---
$("zmDraw").onclick = () => {
  if (!zm.drawArmed && !zmCplOK(+$("zmCh").value || 0)) return;
  zm.drawArmed = !zm.drawArmed;
  zm.drawA = zm.drawB = null;
  $("zmDraw").classList.toggle("on", zm.drawArmed);
  zmStatus(zm.drawArmed ? "drag a rectangle on the scope to add a zone" : "");
};
$("zmClearZones").onclick = () => { zm.zones = []; zmPushZones(); redraw(); };
$("zmTrig").onclick = () => {
  const on = !$("zmTrig").classList.contains("on");
  $("zmTrig").classList.toggle("on", on);
  send("zonemode", on ? 1 : 0);
  zmStatus(on ? "zone trigger ON — only qualifying frames publish" : "zone trigger off");
};

// --- mask build from N raw frames (dilated client-side, uploaded) ---
$("zmBuild").onclick = async () => {
  if (!st || !st.win_cols) { zmStatus("no window info yet"); return; }
  const N = Math.max(4, Math.min(200, +$("zmN").value || 32));
  const tolT = Math.max(0, +$("zmTolT").value || 0), tolV = Math.max(0, +$("zmTolV").value || 0);
  const ch = +$("zmCh").value || 0;
  if (!zmCplOK(ch)) return;
  const win = st.win_cols, posFrac = st.trig_pos_frac > 0 ? st.trig_pos_frac : 0.5;
  const lo = new Array(win).fill(255), hi = new Array(win).fill(0);
  let got = 0, lastSeq = 0, tries = 0;
  zmStatus("building mask 0/" + N + "…");
  while (got < N && tries < N * 6) {
    tries++;
    try {
      const r = await fetch("/api/frame.bin?since=" + lastSeq + "&waitms=1000&raw=1");
      const f = decodeBinFrame(await r.arrayBuffer());
      if (!f || f.unchanged || f.seq === lastSeq) continue;
      lastSeq = f.seq;
      if (!(f.edge_x >= 0) || !(f.sample_s > 0)) continue;
      const sig = ch === 1 ? f.c2 : f.c1;
      if (!sig) continue;
      const left = Math.round(f.edge_x - posFrac * win);
      for (let j = 0; j < win; j++) {
        const s = left + j;
        if (s < 0 || s >= f.cols) continue;
        const v = sig[s];
        if (v < lo[j]) lo[j] = v;
        if (v > hi[j]) hi[j] = v;
      }
      got++;
      if (got % 8 === 0) zmStatus("building mask " + got + "/" + N + "…");
    } catch (e) { break; }
  }
  if (got < 4) { zmStatus("mask build failed — no locked frames (trigger on-signal?)"); return; }
  // unobserved columns (lo>hi) are UNTESTABLE — normalize to always-pass before
  // dilating, or they become inverted-garbage bounds that fail every sample
  for (let j = 0; j < win; j++) if (lo[j] > hi[j]) { lo[j] = 0; hi[j] = 255; }
  // dilation (same morphology as engine.BuildMaskFromEnvelope)
  const dLo = new Array(win), dHi = new Array(win);
  for (let j = 0; j < win; j++) {
    let mn = 255, mx = 0;
    for (let k = Math.max(0, j - tolT); k <= Math.min(win - 1, j + tolT); k++) {
      if (lo[k] < mn) mn = lo[k];
      if (hi[k] > mx) mx = hi[k];
    }
    dLo[j] = Math.max(0, mn - tolV);
    dHi[j] = Math.min(255, mx + tolV);
  }
  zm.mask = { lo: dLo, hi: dHi, win, ch };
  const c = zmVctx(ch);
  if (c) { // frozen source for exact re-mapping on later V/div / offset changes
    zm.mask.srcLo = dLo.slice(); zm.mask.srcHi = dHi.slice();
    zm.mask.svpc = c.vpc; zm.mask.soff = c.off; zm.mask.avpc = c.vpc; zm.mask.aoff = c.off;
  }
  await fetch("/api/mask", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ lo: dLo, hi: dHi, win, ch }) }).catch(() => {});
  zmStatus("mask built from " + got + " frames (±" + tolT + " samp, ±" + tolV + " codes) — set test mode");
  redraw();
};
$("zmMode").onchange = () => send("maskmode", +$("zmMode").value);
$("zmClearStats").onclick = () => { send("maskclear", 0); zm.failMark = null; redraw(); };

window.zmRescale = zmRescale; // exercised directly by the browser e2e

// --- live meter + failure gallery (driven off the 1 Hz status poll) ---
setInterval(() => {
  if (!st) return;
  zmRescale();
  if (st.mask_mode > 0 || st.mask_fail > 0 || st.mask_pass > 0) {
    const total = (st.mask_pass || 0) + (st.mask_fail || 0);
    let line = `pass ${st.mask_pass || 0} · FAIL ${st.mask_fail || 0}` +
      (total ? ` · ${((st.mask_fail || 0) / total * 100).toFixed(3)}%` : "") +
      (st.mask_stopped ? " · STOPPED ON FAIL" : "");
    if (st.mask_skip > 0) line += ` · skip ${st.mask_skip}`;
    // a test that only skips is DEAD — say so instead of looking happy
    if (st.mask_mode > 0 && (st.mask_skip || 0) > zm.lastSkip && zm.lastSkip >= 0)
      line += " · MASK STALE (scale/timebase changed) — rebuild";
    zm.lastSkip = st.mask_skip || 0;
    $("zmMeter").textContent = line;
  } else { $("zmMeter").textContent = ""; zm.lastSkip = st.mask_skip || 0; }
  if (st.zone_mode > 0 && (st.zone_skip || 0) > zm.lastZoneSkip && zm.lastZoneSkip >= 0)
    zmStatus("zone trigger inactive at this timebase (env/roll frames can't qualify)");
  zm.lastZoneSkip = st.zone_skip || 0;
  if ((st.mask_ring || 0) !== zm.lastRing) {
    zm.lastRing = st.mask_ring || 0;
    let html = "";
    for (let i = 0; i < zm.lastRing; i++) html += `<button class="btn-mini zmf" data-i="${i}">fail ${i + 1}</button> `;
    $("zmGallery").innerHTML = html;
    for (const b of document.querySelectorAll("#zmGallery .zmf")) {
      b.onclick = async () => {
        try {
          const r = await (await fetch("/api/maskfail?i=" + b.dataset.i)).json();
          if (!r.ok) return;
          frame = {
            seq: r.seq, unchanged: false,
            c1: Int16Array.from(r.c1), c2: r.c2 ? Int16Array.from(r.c2) : null,
            is_env: false, cols: r.valid, col_span_s: r.valid * r.sample_s,
            tdiv_s: st.tdiv_s, displayed_sdiv_s: st.displayed_sdiv_s,
            vpc1: 1 / 25, vpc2: 1 / 25, off1_v: 0, off2_v: 0,
            edge_frac: r.edge_x >= 0 ? r.edge_x / r.valid : -1,
            win_frac: Math.min(1, r.win_cols / r.valid), depth: r.valid,
            trigd: true, interp: false, coherent: true, ptp: 0,
          };
          zm.failMark = { frac: r.fail_sample / (r.valid - 1), code: r.fail_code };
          frozen = true; $("freeze").classList.add("on");
          userZoomed = false; lastSig = "maskfail";
          const h = homeWindow(frame); view.win.a = h.a; view.win.b = h.b;
          redraw();
          zmStatus("failure " + (+b.dataset.i + 1) + " @ capture #" + r.seq + " — violation circled (unfreeze to resume)");
        } catch (e) { }
      };
    }
  }
}, 1000);
