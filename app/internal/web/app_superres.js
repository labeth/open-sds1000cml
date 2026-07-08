// app_superres.js — super-res stacker UI glue (classic script; shares app.js globals).

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

