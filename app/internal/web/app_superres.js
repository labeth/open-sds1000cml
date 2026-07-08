// app_superres.js — super-res stacker UI glue (classic script; shares app.js globals).

"use strict";
function srStatus(msg) { $("srStats").textContent = msg; }

function srUpdateStats(final) {
  const now = performance.now();
  if (!final && now - sr.lastUi < 500) return;
  sr.lastUi = now;
  if (!sr.st || !sr.st.frames) { srStatus("waiting for frames…"); return; }
  // Strided stats-only reduction: a full one over 1.3M bins each tick janks.
  const res = srResult(sr.st, { statsOnly: true, stride: Math.max(1, Math.ceil(sr.st.nbins / 65536)) });
  if (res.sigmaStack > 0) sr.lastBits = res.bitsGained || 0; // for the +bits stop target
  const el = ((now - sr.t0) / 1000).toFixed(0);
  const rate = res.effRateSa >= 1e9 ? (res.effRateSa / 1e9).toFixed(2) + " GSa/s" : (res.effRateSa / 1e6).toFixed(0) + " MSa/s";
  // A deposit kernel (drizzle) puts only ~frames/K contributors in each fine
  // bin, too sparse to measure per-bin σ — the time-domain bits estimate needs
  // dense (resampled) bins. Rather than report a bogus +0.0, say so and point to
  // the per-tone FFT SNR (which is the right kernel-independent figure there).
  const noise = res.sigmaStack > 0
    ? `σ ${res.sigmaSingle.toFixed(2)}→${res.sigmaStack.toFixed(3)} codes · +${res.bitsGained.toFixed(1)} bits${res.sigmaMeasured ? "" : "~"} (eff ${res.effBits.toFixed(1)})`
    : "σ n/a on this grid — read per-tone SNR in FFT";
  // Gated reference-lock stacks OCCURRENCES (hits) — one frame yields many on a
  // repetitive signal — so report hits · frames; the auto path reports frames.
  const count = res.gated ? `${res.hits} hits · ${res.frames} fr` : `${res.frames} stacked`;
  // Honest no-repeat feedback: everything rejected for a while means the GATED
  // CONTENT does not recur (as a whole) — tell the user instead of spinning.
  const noRepeat = res.gated && res.hits <= 1 && res.rejected >= 12
    ? " · gate content doesn't repeat — move/narrow the markers onto the repeating part" : "";
  srStatus(`${count} · ${res.rejected} rej` + (res.clipped ? ` (${res.clipped} clip)` : "") + (res.reseeds ? ` · reseed ${res.reseeds}` : "") +
    ` · ${el}s · ${noise} · ${rate} grid · fill ${(res.fill * 100).toFixed(1)}%` + noRepeat);
}

// srTargetReached: has the selected stop target been met? bits + stacks are
// acquisition-rate independent (the device gets the same result crunching
// slower than the engine); time is the wall-clock fallback; manual never stops.
function srTargetReached() {
  if (!sr.st || sr.stopVal <= 0) return false;
  switch (sr.stopMode) {
    case "bits":   return sr.lastBits >= sr.stopVal;
    case "stacks": return (sr.st.gated ? sr.st.hits : sr.st.frames) >= sr.stopVal;
    case "time":   return (performance.now() - sr.t0) / 1000 >= sr.stopVal;
  }
  return false;
}

function srIngest(f) {
  if (+$("srCh").value !== sr.ch) { srStop("channel changed — stack kept"); return; }
  const sig = sr.alignCh === 1 ? f.c2 : f.c1;
  if (!sig || f.is_env) { srStop("band became unsupported"); return; }
  if (!sr.st) {
    const K = +$("srK").value || 32;
    sr.st = srNew(f.cols, K);
    sr.st.kernel = $("srKernel").value || "interp"; // resample vs deposit (near-Nyquist)
    sr.st.align = sr.alignCh; // matching/alignment channel; BOTH channels stack
    sr.st.sampleS = f.sample_s || 0;
    sr.st.c[0].vpc = f.vpc1 || 1 / 32;
    sr.st.c[0].offV = f.off1_v || 0;
    sr.st.c[1].vpc = f.vpc2 || 1 / 32;
    sr.st.c[1].offV = f.off2_v || 0;
    sr.meta = { tdiv_s: f.tdiv_s, cols: f.cols, sample_s: f.sample_s, vpc1: f.vpc1, vpc2: f.vpc2 };
    if (sr.lockRef) {
      // Seed THIS frame as the match reference, then run so matching frames stack.
      // When the GATE markers are set, THEY are the region — exactly. sr.gateDt is
      // the marked span in SECONDS relative to the display's trigger edge (set at
      // ARM); anchor it on THIS raw frame's own edge. Otherwise the gate is auto.
      let gate = null;
      if (sr.gateDt) {
        if (!(f.sample_s > 0)) { srStop("no sample interval on the raw feed — can't place the gate"); return; }
        const anchor = f.edge_x != null && f.edge_x >= 0 ? f.edge_x : f.cols / 2;
        const s1 = Math.round(anchor + sr.gateDt.lo / f.sample_s);
        const s2 = Math.round(anchor + sr.gateDt.hi / f.sample_s);
        const lo = Math.max(0, s1), hi = Math.min(f.cols, s2);
        if (hi - lo < 8) { srStop("gate lies outside the captured record — move the markers nearer the trigger"); return; }
        gate = { lo, hi };
      }
      if (!srSeedRef(sr.st, f.c1, f.c2, f.edge_x != null ? f.edge_x : -1, gate)) {
        srStop("reference frame unusable (flat/clipped) — freeze a cleaner one"); return;
      }
      send("run", 1);
      srUpdateStats(false);
      return; // reference seeded; match+stack subsequent frames against it
    }
  } else if (f.cols !== sr.meta.cols || f.sample_s !== sr.meta.sample_s) {
    srStop("acquisition changed (t/div or depth) — stack kept");
    return;
  } else if (f.vpc1 !== sr.meta.vpc1 || f.vpc2 !== sr.meta.vpc2) {
    // NCC is gain-invariant and the drift fit clamps >10×, so a V/div change
    // would silently corrupt code-space stacking — stop instead.
    srStop("vertical scale changed — stack kept");
    return;
  }
  const dz = sr.dither;
  if (dz.on) {
    if (dz.pending > 0) { dz.pending--; return; } // offset still staging — skip
    // No commanded-value correction: iteration 2 of the lab showed the cal
    // volts mapping / DAC granularity mis-corrects (half-code clustering).
    // The ALIGNED drift fit in srFeed measures each frame's ACTUAL vertical
    // shift vs the base-offset reference (b-fit precision ~0.007 codes over
    // the window) and normalizes it out — whatever the DAC really did.
    // Advance the sweep every 2 ingested frames: `steps` phases spanning one
    // ADC LSB, cycling. The offset verb takes ELECTRICAL volts (the sliders
    // divide tip volts by the probe the same way); the next frame is skipped
    // (pending) while the staged DAC write lands.
    if (++dz.framesAtStep >= 2) {
      dz.framesAtStep = 0;
      dz.idx = (dz.idx + 1) % dz.steps;
      const target = dz.base - (dz.idx / dz.steps) * sr.st.c[sr.st.align].vpc; // tip volts, 0..−1 LSB
      const dch = sr.alignCh + 1; // dither sweeps the ALIGN channel's offset DAC
      send("offset" + dch, target / probeOf(dch));
      dz.pending = 1;
    }
  }
  // BOTH channels stack (the align channel drives alignment/lucky-select).
  srFeed(sr.st, f.c1, f.c2, { maxLag: 8, edgeX: f.edge_x });
  srUpdateStats(false);
  // Stop when the target is reached. stacks (exact) and bits are acquisition-rate
  // INDEPENDENT — the device, crunching slower than the engine, reaches the same
  // stack; time is the wall-clock fallback. For bits, recompute EVERY stacked
  // frame with a FIXED stride (not the throttled display cadence) so the stop
  // frame is a function of the stack, not wall-clock.
  if (sr.stopMode === "bits" && sr.stopVal > 0) {
    const r = srResult(sr.st, { statsOnly: true, stride: Math.max(1, Math.ceil(sr.st.nbins / 8192)) });
    if (r.sigmaStack > 0) sr.lastBits = r.bitsGained || 0;
  }
  if (sr.stopVal > 0 && srTargetReached()) { srStop("target reached"); return; }
}

function srStop(why) {
  sr.armed = false;
  if (sr.dither.on && sr.dither.idx !== 0) {
    const dch = (sr.alignCh || 0) + 1;
    send("offset" + dch, sr.dither.base / probeOf(dch)); // restore the pre-dither offset
    sr.dither.idx = 0;
  }
  $("srArm").textContent = "ARM";
  $("srArm").classList.remove("on");
  srUpdateStats(true);
  if (why && sr.st) $("srStats").textContent += " · " + why;
  else if (why) srStatus(why);
}

// The stack view is a first-class TOGGLE: everything a single shot can do —
// measurements, FFT, X-Y, math, decode, cursors, CSV/PNG — works on the
// synthetic frame, and you can flip between live and stack freely (the
// stack zoom is remembered across visits).
function srExitView() {
  sr.showing = false;
  $("srShow").classList.remove("on");
  sr.savedWin = { a: view.win.a, b: view.win.b, zoomed: userZoomed };
  frozen = false; $("freeze").classList.remove("on");
  // Live frames resume on the next poll; the poisoned acq signature re-homes.
}

// ==== wiring ====

// ---- super-res stacker wiring ----
async function srLoop(gen) {
  if (!sr.armed || gen !== sr.gen) return;
  try {
    const r = await fetch("/api/frame.bin?since=" + sr.lastSeq + "&waitms=1000&raw=1");
    if (!r.ok) throw new Error("http " + r.status);
    const f = decodeBinFrame(await r.arrayBuffer());
    if (f === null) throw new Error("decode");
    // Re-check AFTER the await: the fetch parks server-side for up to 1 s
    // and a reset/stop click mid-flight must not resurrect the stack.
    if (!sr.armed || gen !== sr.gen) return;
    srFails = 0;
    if (!f.unchanged && f.seq !== sr.lastSeq) {
      sr.lastSeq = f.seq;
      srIngest(f);
    }
    setTimeout(() => srLoop(gen), 10);
  } catch (e) {
    // Failure (outage, protocol) — back off like the display loop instead of
    // hammering a struggling device at the 10 ms tick.
    srFails++;
    setTimeout(() => srLoop(gen), Math.min(2000, 250 * srFails) + 250 * Math.random());
  }
}


$("srArm").onclick = () => {
  if (sr.armed) { srStop("stopped"); return; }
  if (!st || (st.band !== "native-fast" && st.band !== "decimated")) {
    srStatus("unsupported band (" + (st ? st.band : "?") + ") — use a native or decimated t/div");
    return;
  }
  if (typeof decodeBinFrame !== "function" || typeof srNew !== "function") {
    srStatus("superres/binframe scripts missing");
    return;
  }
  if (typeof ej !== "undefined" && ej.armed) ejStop("stopped — superres armed (one raw consumer)");
  sr.st = null; sr.meta = null; sr.lastSeq = 0; sr.savedWin = null;
  sr.stopMode = $("srStopMode").value;
  sr.stopVal = +$("srStopVal").value || 0;
  sr.lastBits = 0;
  // GATE markers set → seed the first frame as the reference with THAT region
  // (works from SINGLE or free-run). Otherwise: frozen → lock the frozen frame;
  // running → auto-adopt.
  sr.lockRef = srGate.on || !!(st && !st.running);
  // Markers live on the DISPLAY record (a windowed/decimated, trigger-anchored
  // serve); the stacker feeds on the RAW record. The trigger edge is the common
  // anchor, so convert markers → SECONDS from the display's edge now, and map
  // onto the raw frame's own edge at seed time. Applying display fractions to
  // the raw record directly is wrong (it stacked ~10 waves for an edge-wide mark).
  sr.gateDt = null;
  if (srGate.on) {
    if (frame && frame.cols > 1 && frame.col_span_s > 0 && !frame.is_env) {
      const cols = frame.cols, spc = frame.col_span_s / cols; // seconds per display column
      const edgeCol = frame.edge_frac >= 0 ? frame.edge_frac * cols : cols / 2;
      const cLo = Math.min(srGate.a, srGate.b) * (cols - 1);
      const cHi = Math.max(srGate.a, srGate.b) * (cols - 1);
      sr.gateDt = { lo: (cLo - edgeCol) * spc, hi: (cHi - edgeCol) * spc };
    } else {
      srStatus("gate needs a live/frozen trace on screen"); return;
    }
  }
  sr.ch = +$("srCh").value || 0; // latched — a mid-capture change stops the stack
  // "both" (0) aligns on the trigger-source channel; C1/C2 lock the alignment.
  sr.alignCh = sr.ch === 2 ? 1 : sr.ch === 1 ? 0 : (st && st.trig_source === 1 ? 1 : 0);
  sr.dither.on = $("srDither").checked;
  sr.dither.base = (st && (sr.alignCh === 1 ? st.off2_v : st.off1_v)) || 0;
  sr.dither.idx = 0; sr.dither.pending = 0; sr.dither.framesAtStep = 0;
  sr.t0 = performance.now();
  sr.armed = true;
  $("srArm").textContent = "STOP";
  $("srArm").classList.add("on");
  srStatus(srGate.on ? "stacking… (gate = markers)" : "stacking…");
  srLoop(++sr.gen);
};

$("srReset").onclick = () => { if (sr.showing) srExitView(); srStop(); sr.st = null; sr.meta = null; sr.savedWin = null; srStatus("idle"); };

// AUTOGATE: always (re-)place the markers on the best feature in the current
// view, then show them. GATE: show/hide toggle — auto-places only the first time;
// after that the markers are wherever you dragged them (the only truth).
$("srAutoGate").onclick = () => {
  srGateDefaultFromView();
  srGate.placed = true;
  srGate.on = true;
  $("srGate").classList.add("on");
  srStatus("gate placed — drag to adjust, then ARM");
  redraw();
};
$("srGate").onclick = () => {
  srGate.on = !srGate.on;
  if (srGate.on && !srGate.placed) { srGateDefaultFromView(); srGate.placed = true; }
  $("srGate").classList.toggle("on", srGate.on);
  if (srGate.on) srStatus("gate on — drag the markers over the feature, then ARM");
  redraw();
};

// Stop-mode selector: adapt the target field's default + step to the units.
$("srStopMode").onchange = () => {
  const m = $("srStopMode").value, v = $("srStopVal");
  const d = { bits: [4, 0.5], stacks: [500, 50], time: [60, 10] }[m];
  v.disabled = !d;
  if (d) { v.value = d[0]; v.step = d[1]; }
};

$("srShow").onclick = () => {
  if (sr.showing) { srExitView(); return; }
  if (!sr.st || !sr.st.frames) { srStatus("nothing stacked yet"); return; }
  const res = srResult(sr.st);
  const n = sr.st.n;
  const dt = sr.st.sampleS / sr.st.K;
  const meas = (mean, ch) => mean ? srMeasure(mean, sr.st.c[ch].vpc, sr.st.c[ch].offV, dt) : null;
  // res.mean is the ALIGN channel's stack, res.mean2 the other — map them back to
  // the PHYSICAL channels so the review honors the selection (align C2 must show
  // as the cyan C2 trace, not swap into C1).
  const c1m = sr.st.align === 0 ? res.mean : res.mean2;
  const c2m = sr.st.align === 0 ? res.mean2 : res.mean;
  // A gated stack's mean spans only the gate (gridL raw samples), not the whole
  // record — size the time axis and edge anchor to the grid actually served.
  const spanCols = sr.st.gated ? sr.st.gridL : n;
  const edgeFrac = sr.st.gated
    ? (sr.st.refEdgeX >= sr.st.gateLo && sr.st.refEdgeX < sr.st.gateHi ? (sr.st.refEdgeX - sr.st.gateLo) / sr.st.gridL : -1)
    : (sr.st.edgeX >= 0 ? sr.st.edgeX / n : -1);
  frame = {
    seq: frame ? frame.seq : 0, unchanged: false,
    c1: c1m, c2: c2m, is_env: false,
    cols: res.mean.length, col_span_s: spanCols * sr.st.sampleS,
    tdiv_s: sr.meta.tdiv_s, displayed_sdiv_s: sr.meta.tdiv_s,
    vpc1: sr.st.c[0].vpc, vpc2: sr.st.c[1].vpc,
    off1_v: sr.st.c[0].offV, off2_v: sr.st.c[1].offV,
    edge_frac: edgeFrac,
    win_frac: 1, depth: 0,
    m1: meas(c1m, 0), m2: meas(c2m, 1),
    clip1: false, clip2: false,
    trigd: true, interp: false, coherent: true, ptp: 0,
  };
  sr.showing = true;
  $("srShow").classList.add("on");
  frozen = true; $("freeze").classList.add("on");
  if (sr.savedWin) {
    view.win.a = sr.savedWin.a; view.win.b = sr.savedWin.b; userZoomed = sr.savedWin.zoomed;
  } else {
    view.win.a = 0; view.win.b = 1; userZoomed = true; // whole stack; wheel-zoom into the detail
  }
  lastSig = "superres"; // poison the acq signature so returning to live re-homes
  computeDecode(); redraw(); updateMeas(); updateCursors();
  srUpdateStats(true);
};

$("srFit").onclick = () => {
  if (!sr.st || !sr.st.frames) { srStatus("nothing stacked yet"); return; }
  const res = srResult(sr.st);
  const am = res.mean; // res.mean IS the align channel's stack
  const ac = sr.st.c[sr.st.align];
  const fit = srModelFit(am, sr.st.K, sr.st.sampleS, { spectrum, detectPeaks }, 6);
  if (!fit) { srStatus("model fit failed (need a fuller stack)"); return; }
  refs.B = {
    c1: Array.from(fit.synth(Math.min(am.length, 16384))),
    c2: null, vpc1: ac.vpc, vpc2: 1 / 32, off1: ac.offV, off2: 0, show: true,
    // only overlay on a matching time base; a gated stack spans just the gate
    srSpanS: (sr.st.gated ? sr.st.gridL : sr.st.n) * sr.st.sampleS,
  };
  updateRefRows(); redraw();
  srStatus("model → REF B: " + fit.freqs.map(f => eng(f, "Hz", 3)).join(", "));
};
