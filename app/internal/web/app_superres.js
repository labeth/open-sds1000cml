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

// ==== ETS: phase-coherent equivalent-time reconstruction of a free-run clock ==
function srEtsInit(f, fref, dt) {
  const nbins = 4 * (+$("srK").value || 32); // K16→64, K32→128, K64→256 phase bins
  sr.etsSt = srEtsNew(nbins, dt);
  sr.etsSt.f = fref; sr.etsSt.align = sr.alignCh;
  sr.etsSt.c[0].vpc = f.vpc1 || 1 / 25; sr.etsSt.c[0].offV = f.off1_v || 0;
  sr.etsSt.c[1].vpc = f.vpc2 || 1 / 25; sr.etsSt.c[1].offV = f.off2_v || 0;
  sr.meta = { tdiv_s: f.tdiv_s, cols: f.cols, sample_s: dt };
}

function srEtsIngest(f) {
  const alignSig = sr.alignCh === 1 ? f.c2 : f.c1;
  if (!alignSig || f.is_env) { srStop("band unsupported for ETS — use a native/decimated t/div"); return; }
  const dt = f.sample_s;
  if (!(dt > 0)) { srStop("no sample interval on the raw feed"); return; }
  if (!sr.etsSt) {
    if (sr.etsF > 0) {
      srEtsInit(f, srEtsRefineFreq(Float64Array.from(alignSig), dt, sr.etsF), dt);
    } else {
      // AUTO detect: incoherent-average the spectrum over a few frames before
      // locking — one free-run frame's peak is unreliable (a square clock's
      // above-Nyquist harmonics ALIAS back in and can outweigh the attenuated
      // fundamental on any single frame; the average makes the true tone win).
      if (typeof spectrum !== "function") { srStop("ETS auto-detect needs the FFT — set the frequency"); return; }
      const spec = spectrum(alignSig, 1 / (2 * dt));
      if (!sr.etsDetect) sr.etsDetect = { sum: new Float64Array(spec.half), n: 0, half: spec.half, nyq: 1 / (2 * dt) };
      for (let k = 0; k < spec.half; k++) sr.etsDetect.sum[k] += spec.mags[k] * spec.mags[k];
      if (++sr.etsDetect.n < 8) { srStatus(`ETS: detecting clock… ${sr.etsDetect.n}/8`); return; }
      const { sum, half, nyq } = sr.etsDetect;
      const kMin = Math.max(1, Math.floor(20e6 / nyq * half));
      let best = 0, bk = 0; for (let k = kMin; k < half; k++) if (sum[k] > best) { best = sum[k]; bk = k; }
      sr.etsDetect = null;
      if (!bk) { srStop("ETS: no tone above 20 MHz — set the frequency"); return; }
      srEtsInit(f, srEtsRefineFreq(Float64Array.from(alignSig), dt, (bk / half) * nyq), dt);
    }
  } else if (f.sample_s !== sr.meta.sample_s) {
    srStop("acquisition changed (t/div or depth) — reconstruction kept"); return;
  }
  srEtsFeed(sr.etsSt, f.c1, f.c2);
  srEtsUpdateStats(false);
  if (sr.stopMode === "stacks" && sr.stopVal > 0 && sr.etsSt.frames >= sr.stopVal) { srStop("target reached"); return; }
  if (sr.stopMode === "time" && sr.stopVal > 0 && (performance.now() - sr.t0) / 1000 >= sr.stopVal) { srStop("target reached"); return; }
  if (sr.stopMode === "bits" && sr.stopVal > 0) {
    const r = srEtsResult(sr.etsSt);
    if ((r.bitsGained || 0) >= sr.stopVal) { srStop("target reached"); return; }
  }
}

function srEtsUpdateStats(final) {
  const now = performance.now();
  if (!final && now - sr.lastUi < 500) return;
  sr.lastUi = now;
  if (!sr.etsSt || !sr.etsSt.frames) { srStatus("waiting for free-run frames…"); return; }
  const r = srEtsResult(sr.etsSt);
  const el = ((now - sr.t0) / 1000).toFixed(0);
  const eqRate = r.periodS > 0 ? r.nbins / r.periodS : 0;
  const rate = eqRate >= 1e9 ? (eqRate / 1e9).toFixed(1) + " GSa/s" : (eqRate / 1e6).toFixed(0) + " MSa/s";
  srStatus(`ETS ${eng(r.f, "Hz", 5)} · ${r.frames} fr${r.rejected ? " · " + r.rejected + " rej" : ""} · ${el}s · ` +
    `σ ${r.sigmaSingle.toFixed(2)}→${r.sigmaStack.toFixed(3)} · +${(r.bitsGained || 0).toFixed(1)} bits (eff ${(r.effBits || 8).toFixed(1)}) · ` +
    `swing ${r.swing.toFixed(1)} codes · ${rate} equiv · fill ${(r.fill * 100).toFixed(0)}%`);
}

// ==== decode-triggered super-res: stack a decoded protocol BYTE event ========
// Software trigger on a decoded value (like the serial trigger), then align +
// stack every occurrence — the "sw triggered on random events" generalization of
// the free-run clock ETS. Reuses the DECODE config from the Decode card and the
// gated multi-hit stacker (srSeedRef seeds the gate on the first occurrence's
// waveform; srGateFeed with hitCenters aligns+stacks the rest).
function srEvtParseByte(s) {
  s = (s || "").trim().replace(/^0x/i, "").replace(/^'|'$/g, "");
  if (s.length === 1 && !/^[0-9a-fA-F]$/.test(s)) return s.charCodeAt(0); // a single non-hex char, e.g. H
  if (/^[0-9a-fA-F]{1,2}$/.test(s)) return parseInt(s, 16);               // hex byte, e.g. 48
  if (s.length >= 1) return s.charCodeAt(0);
  return NaN;
}

// srEvtDecode decodes the align channel of a RAW frame and returns the spans of
// the target byte (data only). v1: UART (the Decode card must be set to UART).
function srEvtDecode(sig, sampleS, target) {
  if (typeof dcfg === "undefined" || dcfg.proto !== "uart" || typeof decodeUART !== "function") return null;
  const cfg = { baud: dcfg.baud > 0 ? dcfg.baud : null, bits: dcfg.bits || 8, parity: dcfg.parity || "none",
    threshold: dcfg.auto ? null : (+$("decThr").value || null), guard: 4 };
  const r = decodeUART(sig, sampleS, cfg);
  if (!r || !r.ok) return { spb: 0, occ: [] };
  return { spb: r.meta.samplesPerBit || 0, occ: r.spans.filter(s => s.kind === "data" && s.val === target) };
}

function srEvtIngest(f) {
  const alignSig = sr.alignCh === 1 ? f.c2 : f.c1;
  if (!alignSig || f.is_env) { srStop("band unsupported for decode-trig — use a native/decimated t/div"); return; }
  const dt = f.sample_s;
  if (!(dt > 0)) { srStop("no sample interval on the raw feed"); return; }
  if (!(sr.evtByte >= 0)) { srStop("decode-trig: enter a target byte (e.g. 48 or H)"); return; }
  const d = srEvtDecode(alignSig, dt, sr.evtByte);
  if (!d) { srStop("decode-trig needs the Decode card set to UART"); return; }
  let firstFrame = false;
  if (!sr.st) {
    if (!d.occ.length) { srStatus(`decode-trig: waiting for 0x${sr.evtByte.toString(16).toUpperCase()} (${d.spb ? d.spb.toFixed(0) + " samp/bit" : "no decode"})…`); return; }
    const K = +$("srK").value || 32;
    sr.st = srNew(f.cols, K);
    sr.st.kernel = $("srKernel").value || "interp";
    sr.st.align = sr.alignCh; sr.st.sampleS = dt;
    sr.st.c[0].vpc = f.vpc1 || 1 / 25; sr.st.c[0].offV = f.off1_v || 0;
    sr.st.c[1].vpc = f.vpc2 || 1 / 25; sr.st.c[1].offV = f.off2_v || 0;
    sr.meta = { tdiv_s: f.tdiv_s, cols: f.cols, sample_s: dt, vpc1: f.vpc1, vpc2: f.vpc2 };
    const h = d.occ[0];
    sr.evtMargin = Math.max(4, Math.round(d.spb || (h.i1 - h.i0) / 10));
    const gLo = Math.max(0, h.i0 - sr.evtMargin), gHi = Math.min(f.cols, h.i1 + sr.evtMargin);
    if (gHi - gLo < 8 || !srSeedRef(sr.st, f.c1, f.c2, -1, { lo: gLo, hi: gHi })) { sr.st = null; srStatus("decode-trig: reference byte unusable (raise V/div for headroom)"); return; }
    firstFrame = true; // srSeedRef already stacked occ[0]
  } else if (f.sample_s !== sr.meta.sample_s) {
    srStop("acquisition changed (t/div or depth) — stack kept"); return;
  }
  const list = firstFrame ? d.occ.slice(1) : d.occ;
  if (list.length) {
    const centers = list.map(o => Math.max(0, o.i0 - sr.evtMargin));
    srGateFeed(sr.st, f.c1, f.c2, { hitCenters: centers, centerR: sr.evtMargin });
  }
  srUpdateStats(false);
  if (sr.stopVal > 0 && srTargetReached()) { srStop("target reached"); return; }
}

function srIngest(f) {
  if (sr.ets) { srEtsIngest(f); return; }
  if (sr.evt) { srEvtIngest(f); return; }
  if (+$("srCh").value !== sr.ch) { srStop("channel changed — stack kept"); return; }
  const sig = sr.alignCh === 1 ? f.c2 : f.c1;
  if (!sig || f.is_env) { srStop("band became unsupported"); return; }
  if (!sr.st) {
    const K = +$("srK").value || 32;
    sr.st = srNew(f.cols, K);
    sr.st.kernel = $("srKernel").value || "interp"; // resample vs deposit (near-Nyquist)
    sr.st.align = sr.alignCh; // matching/alignment channel; BOTH channels stack
    sr.st.sampleS = f.sample_s || 0;
    sr.st.c[0].vpc = f.vpc1 || 1 / 25;
    sr.st.c[0].offV = f.off1_v || 0;
    sr.st.c[1].vpc = f.vpc2 || 1 / 25;
    sr.st.c[1].offV = f.off2_v || 0;
    sr.meta = { tdiv_s: f.tdiv_s, cols: f.cols, sample_s: f.sample_s, vpc1: f.vpc1, vpc2: f.vpc2 };
    if (sr.lockRef) {
      // Seed THIS frame as the match reference, then run so matching frames stack.
      // When the GATE markers are set, THEY are the region. sr.gateDt is the marked
      // span in SECONDS from the display's trigger edge (set at ARM); anchor it on
      // THIS raw frame's own edge — edge-relative, so a periodic feature is gated
      // the same whichever period's crossing anchors either frame. Otherwise auto.
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
  sr.st = null; sr.etsSt = null; sr.etsDetect = null; sr.meta = null; sr.lastSeq = 0; sr.savedWin = null;
  sr.stopMode = $("srStopMode").value;
  sr.stopVal = +$("srStopVal").value || 0;
  sr.lastBits = 0;
  // ETS (free-run clock reconstruction) — a distinct feed path; skip the
  // gate/reference machinery, it folds every free-run frame by measured phase.
  sr.ets = $("srEts").checked;
  sr.etsF = (parseFloat($("srEtsFreq").value) || 0) * 1e6;
  sr.evt = $("srEvt").checked && !sr.ets; // decode-triggered byte-event stack (ETS wins if both)
  sr.evtByte = srEvtParseByte($("srEvtByte").value);
  if ((sr.ets || sr.evt) && st && st.norm) send("norm", 0); // free-run so every event/cycle is captured
  // GATE markers set → seed the first frame as the reference with THAT region
  // (works from SINGLE or free-run). Otherwise: frozen → lock the frozen frame;
  // running → auto-adopt.
  sr.lockRef = srGate.on || !!(st && !st.running);
  // The markers are EDGE-RELATIVE (record-fraction offsets from the trigger edge):
  // the raw record is not phase-stable, so the crossing — not a fixed sample — is
  // the anchor. The span in SECONDS from the edge is just the offset times the
  // record's time span; the raw-fed seed re-anchors that on each frame's own edge,
  // so a periodic feature is gated identically whichever period's crossing anchors
  // either frame.
  sr.gateDt = null;
  if (srGate.on && !sr.ets) {
    if (frame && frame.cols > 1 && frame.col_span_s > 0 && !frame.is_env) {
      const gLo = Math.min(srGate.a, srGate.b), gHi = Math.max(srGate.a, srGate.b);
      sr.gateDt = { lo: gLo * frame.col_span_s, hi: gHi * frame.col_span_s };
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
  srStatus(sr.ets ? "reconstructing (equivalent-time)… use AUTO trigger" : sr.evt ? "decode-trig stacking… (Decode = UART, free-run)" : srGate.on ? "stacking… (gate = markers)" : "stacking…");
  srLoop(++sr.gen);
};

$("srReset").onclick = () => { if (sr.showing) srExitView(); srStop(); sr.st = null; sr.etsSt = null; sr.etsDetect = null; sr.meta = null; sr.savedWin = null; srStatus("idle"); };

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

// Build the synthetic review frame from the current stack into the global
// `frame`, optionally analog-falloff compensated. Factored out so the BW-comp
// toggle can re-render the already-showing view in place.
function srMakeViewFrame() {
  if (sr.ets) { srMakeEtsViewFrame(); return; }
  const res = srResult(sr.st);
  const n = sr.st.n;
  const dt = sr.st.sampleS / sr.st.K; // seconds per fine bin
  // res.mean is the ALIGN channel's stack, res.mean2 the other — map them back to
  // the PHYSICAL channels so the review honors the selection (align C2 must show
  // as the cyan C2 trace, not swap into C1).
  let c1m = sr.st.align === 0 ? res.mean : res.mean2;
  let c2m = sr.st.align === 0 ? res.mean2 : res.mean;
  // Analog-falloff compensation: de-embed the measured chain rolloff on the
  // crunched fine grid so EVERY downstream view (Y-T/FFT/X-Y/measure) shows the
  // recovered signal. The stack's extra ENOB is the SNR headroom the boost
  // spends. Applied per channel; the filter figures are the same for both.
  sr.compInfo = null;
  if (sr.comp && typeof srCompensate === "function" && dt > 0) {
    // auto: pick the target from the stack's MEASURED bit budget (a longer,
    // quieter stack recovers a higher −3 dB). Fixed: force the target.
    const rawNyq = sr.st.sampleS > 0 ? 1 / (2 * sr.st.sampleS) : 250e6;
    const opts = sr.compFbw === "auto"
      ? srCompAuto(res.bitsGained, rawNyq, sr.compSpend)
      : { fbw: +sr.compFbw };
    if (c1m) c1m = srCompensate(c1m, dt, opts).comp;
    if (c2m) { const r = srCompensate(c2m, dt, opts); c2m = r.comp; sr.compInfo = r; }
    if (!sr.compInfo) sr.compInfo = srCompInfo(opts);
    sr.compInfo.auto = !!opts.auto; sr.compInfo.budgetDb = opts.budgetDb; sr.compInfo.bitsGained = opts.bitsGained;
  }
  srUpdateCompInfo();
  const meas = (mean, ch) => mean ? srMeasure(mean, sr.st.c[ch].vpc, sr.st.c[ch].offV, dt) : null;
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
}

// srMakeEtsViewFrame builds the ONE-PERIOD equivalent-time reconstruction into
// the global `frame` (optionally BW-compensated to recover the attenuated
// high-frequency amplitude). Two periods are tiled so a full cycle is easy to
// see. dtFine = period / phase-bins.
function srMakeEtsViewFrame() {
  const st = sr.etsSt, r = srEtsResult(st);
  const nb = st.nbins, dtFine = r.periodS / nb;
  let c1m = r.c1 ? r.c1.mean : null;
  let c2m = r.c2 && r.c2.mean ? r.c2.mean : null;
  sr.compInfo = null;
  if (sr.comp && typeof srCompensate === "function" && dtFine > 0) {
    const rawNyq = 0.5 / dtFine; // fine-grid Nyquist (huge); comp cal caps itself
    const opts = sr.compFbw === "auto" ? srCompAuto(r.bitsGained, rawNyq, sr.compSpend) : { fbw: +sr.compFbw };
    if (c1m) c1m = srCompensate(c1m, dtFine, opts).comp;
    if (c2m) { const cr = srCompensate(c2m, dtFine, opts); c2m = cr.comp; sr.compInfo = cr; }
    if (!sr.compInfo) sr.compInfo = srCompInfo(opts);
    sr.compInfo.auto = !!opts.auto; sr.compInfo.budgetDb = opts.budgetDb; sr.compInfo.bitsGained = opts.bitsGained;
  }
  srUpdateCompInfo();
  // tile two periods so a whole cycle reads clearly
  const tile = (m) => { if (!m) return null; const out = new Float32Array(nb * 2); for (let i = 0; i < nb * 2; i++) out[i] = m[i % nb]; return out; };
  const c1t = tile(c1m), c2t = tile(c2m);
  const meas = (m, ch) => m ? srMeasure(m, st.c[ch].vpc, st.c[ch].offV, dtFine) : null;
  frame = {
    seq: frame ? frame.seq : 0, unchanged: false,
    c1: c1t, c2: c2t, is_env: false,
    cols: nb * 2, col_span_s: 2 * r.periodS,
    tdiv_s: sr.meta.tdiv_s, displayed_sdiv_s: sr.meta.tdiv_s,
    vpc1: st.c[0].vpc, vpc2: st.c[1].vpc, off1_v: st.c[0].offV, off2_v: st.c[1].offV,
    edge_frac: -1, win_frac: 1, depth: 0,
    m1: meas(c1t, 0), m2: meas(c2t, 1),
    clip1: false, clip2: false, trigd: true, interp: false, coherent: true, ptp: 0,
  };
}

// srUpdateCompInfo writes the compensation readout (measured → recovered −3 dB
// and the peak boost) into the panel.
function srUpdateCompInfo() {
  const el = $("srCompInfo");
  if (!el) return;
  if (!sr.comp) { el.textContent = ""; return; }
  const info = sr.compInfo;
  if (!info) { el.textContent = sr.compFbw === "auto" ? "auto — view the stack to size the boost" : ""; return; }
  const body = `−3 dB ${eng(info.measF3, "Hz", 2)} → ${eng(info.recoveredF3, "Hz", 2)} · +${info.peakBoostDb.toFixed(1)} dB boost`;
  el.textContent = info.auto ? `auto (+${(info.bitsGained || 0).toFixed(1)} bit budget) · ${body}` : body;
}

// Goal presets — configure EVERY optimization knob (grid/kernel/stop/dither/
// BW-comp/target/spend), leaving only the signal choices (ch, gate) to the
// user. Applied to the controls + state; grid/kernel/stop take effect on ARM.
const SR_PRESETS = {
  // Accumulate bits and spend them as boost: highest recovered −3 dB.
  hibw: { K: "64", kernel: "interp", stopMode: "time", stopVal: 60, dither: false, comp: true, fbw: "auto", spend: 0.9 },
  // Accumulate with NO boost: lowest noise floor / most effective bits.
  enob: { K: "32", kernel: "interp", stopMode: "bits", stopVal: 4, dither: false, comp: false, fbw: "auto", spend: 0.8 },
  // Coarse grid fills fast + short stop: a quick usable stack, modest boost.
  fast: { K: "16", kernel: "interp", stopMode: "time", stopVal: 8, dither: false, comp: true, fbw: "auto", spend: 0.65 },
};
function srApplyPreset(name) {
  const p = SR_PRESETS[name];
  if (!p) return; // "custom" — leave the controls as the user set them
  $("srK").value = p.K;
  $("srKernel").value = p.kernel;
  $("srStopMode").value = p.stopMode;
  if ($("srStopMode").onchange) $("srStopMode").onchange();
  $("srStopVal").value = p.stopVal;
  $("srDither").checked = p.dither;
  $("srComp").checked = p.comp; sr.comp = p.comp;
  $("srCompBw").value = p.fbw; sr.compFbw = p.fbw; sr.compSpend = p.spend;
  if (sr.showing) { srMakeViewFrame(); computeDecode(); redraw(); updateMeas(); updateCursors(); }
  else srUpdateCompInfo();
}

$("srShow").onclick = () => {
  if (sr.showing) { srExitView(); return; }
  const has = sr.ets ? (sr.etsSt && sr.etsSt.frames) : (sr.st && sr.st.frames);
  if (!has) { srStatus(sr.ets ? "nothing reconstructed yet" : "nothing stacked yet"); return; }
  srMakeViewFrame();
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
  if (sr.ets) srEtsUpdateStats(true); else srUpdateStats(true);
};

// BW-compensation toggle + target: re-render the stack view in place when it's
// already showing (a pure post-process of the crunched grid, no re-stacking).
$("srComp").onchange = () => {
  sr.comp = $("srComp").checked;
  if (sr.showing) { srMakeViewFrame(); computeDecode(); redraw(); updateMeas(); updateCursors(); }
  else srUpdateCompInfo();
};
$("srCompBw").onchange = () => {
  const v = $("srCompBw").value;
  sr.compFbw = v === "auto" ? "auto" : (+v || 70e6);
  if (sr.comp && sr.showing) { srMakeViewFrame(); computeDecode(); redraw(); updateMeas(); updateCursors(); }
  else if (sr.comp) srUpdateCompInfo();
};
$("srPreset").onchange = () => {
  srApplyPreset($("srPreset").value);
  srStatus(`preset: ${$("srPreset").selectedOptions[0].textContent} — ARM to apply`);
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
    c2: null, vpc1: ac.vpc, vpc2: 1 / 25, off1: ac.offV, off2: 0, show: true,
    // only overlay on a matching time base; a gated stack spans just the gate
    srSpanS: (sr.st.gated ? sr.st.gridL : sr.st.n) * sr.st.sampleS,
  };
  updateRefRows(); redraw();
  srStatus("model → REF B: " + fit.freqs.map(f => eng(f, "Hz", 3)).join(", "));
};
