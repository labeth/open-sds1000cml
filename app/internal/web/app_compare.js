// ENGMODEL-OWNER-UNIT: FU-WEB-APP-COMPARE
// app_compare.js — the COMPARE workspace: references A and B, math and the mask
// against the live trace, a live difference readout, and the stacked master
// (COMPARE again): average N captures of the trigger source in the device's
// average mode, refuse when the signal does not repeat, store the result as
// REF A, build the mask from it, restore the acquisition so live captures are
// tested raw, and start the mask test with zeroed counters. Classic script;
// shares app.js globals.
"use strict";

let cmpT = 0;

// refVoltsAt maps a reference onto the live record's sample grid by time from
// the trigger (references keep their own sample pitch and trigger index), or
// index-proportionally when either side has no trigger. Returns Float64Array
// volts per live sample (NaN outside the reference).
// TRLC-LINKS: REQ-SDS-218
function refOnLive(r, ch, n, stride) {
  const codes = ch === 2 ? r.c2 : r.c1;
  const vpc = ch === 2 ? r.vpc2 : r.vpc1, off = ch === 2 ? r.off2 : r.off1;
  const m = Math.ceil(n / stride), out = new Float64Array(m).fill(NaN);
  if (!codes || !codes.length) return out;
  const len = codes.length, dtL = frameDtS(frame, n), edgeL = frame.edge_frac >= 0 ? frame.edge_frac * (n - 1) : -1;
  const timed = r.dt > 0 && r.edge >= 0 && dtL > 0 && edgeL >= 0;
  for (let k = 0; k < m; k++) {
    const i = k * stride;
    const x = timed ? r.edge + (i - edgeL) * dtL / r.dt : i * (len - 1) / Math.max(1, n - 1);
    if (!(x >= 0 && x <= len - 1)) continue;
    const j = Math.floor(x), f = x - j;
    const a = codes[j], b = j + 1 < len ? codes[j + 1] : a;
    if (a < 0 || b < 0) continue;
    out[k] = ((a + (b - a) * f) - 128) * vpc - off;
  }
  return out;
}

// cmpRender fills the difference readout: for every shown reference and each
// channel it holds, the live trace against it (max |Δ|, RMS Δ, % of samples
// within half a division), or why it cannot compare.
// TRLC-LINKS: REQ-SDS-218
function cmpRender() {
  const rows = [];
  for (const slot of ["A", "B"]) {
    const r = refs[slot];
    if (!r || !r.show) continue;
    for (const ch of [1, 2]) {
      if (!(ch === 1 ? r.c1 : r.c2)) continue;
      const name = `REF ${slot} vs C${ch}`;
      const live = frame && !frame.is_env ? (ch === 1 ? frame.c1 : frame.c2) : null;
      if (!(ch === 1 ? view.c1 : view.c2) || !live) { rows.push([slot, name, "C" + ch + (live ? " is hidden — show it to compare" : " has no sampled record now")]); continue; }
      if (r.tdiv && frame.tdiv_s && Math.abs(r.tdiv - frame.tdiv_s) > r.tdiv * 1e-6) { rows.push([slot, name, "saved at " + fmtTdiv(r.tdiv) + "/div — set that timebase to compare"]); continue; }
      const n = live.length, stride = Math.max(1, Math.ceil(n / 8192));
      const vpc = (ch === 2 ? frame.vpc2 : frame.vpc1) || 1 / 25, off = (ch === 2 ? frame.off2_v : frame.off1_v) || 0;
      const lv = new Float64Array(Math.ceil(n / stride));
      for (let k = 0; k < lv.length; k++) { const c = live[k * stride]; lv[k] = c < 0 ? NaN : (c - 128) * vpc - off; }
      const div = st ? (ch === 2 ? st.vdiv2 * probeOf(2) : st.vdiv1 * probeOf(1)) : 25 * vpc;
      const d = refDiff(lv, refOnLive(r, ch, n, stride), 0.5 * div);
      if (!d.n) { rows.push([slot, name, "no overlap with the reference in time"]); continue; }
      // below a thousandth of a code is the arithmetic of the time mapping: 0
      const q = vpc * 1e-3, mx = d.maxAbs < q ? 0 : d.maxAbs, rms = d.rms < q ? 0 : d.rms;
      rows.push([slot, name, eng(mx, "V", 3) + " (" + (mx / div).toFixed(2) + " div)", eng(rms, "V", 3), d.pctWithin.toFixed(1) + " %"]);
    }
  }
  let html = rows.map((x) => x.length === 3
    ? `<tr data-ref="${x[0]}"><th class="l ref${x[0].toLowerCase()}">${x[1]}</th><td class="e" colspan="3">${escHTML(x[2])}</td></tr>`
    : `<tr data-ref="${x[0]}"><th class="l ref${x[0].toLowerCase()}">${x[1]}</th><td>${x[2]}</td><td>${x[3]}</td><td>${x[4]}</td></tr>`).join("");
  if (!rows.length) html = `<tr><td class="e" colspan="4">Save and show a reference (REF A / B) to compare the live trace with it — or stack a master (Alt+5 again).</td></tr>`;
  const b = $("diffBody");
  if (b.dataset.sig !== html) { b.dataset.sig = html; b.innerHTML = html; }
}
WS_TICKS.push((now) => { if (ws.cur === "compare" && now - cmpT > 300) { cmpT = now; cmpRender(); } });
// a held (stopped or frozen) capture sends no frames: keep the readout current anyway
setInterval(() => { if (ws.cur === "compare" && performance.now() - cmpT > 900) { cmpT = performance.now(); cmpRender(); } }, 1000);
WS_HOOKS.push({ enter(id) { if (id === "compare") { cmpT = 0; cmpRender(); } } });

// ---- the stacked master ----------------------------------------------------------
// TRLC-LINKS: REQ-SDS-218
const master = { busy: false, abort: false };

// TRLC-LINKS: REQ-SDS-218
function masterSay(t, kind) {
  $("masterStat").textContent = t;
  $("masterStat").className = "sub" + (kind === "warn" ? " warn" : "");
}
// TRLC-LINKS: REQ-SDS-218
function masterUI() {
  const b = $("masterBtn");
  b.textContent = master.busy ? "Cancel master" : "Stack master";
  b.classList.toggle("on", master.busy);
  wsDots();
}
// TRLC-LINKS: REQ-SDS-218
function masterCancel() { if (master.busy) { master.abort = true; masterSay("cancelling…"); } }
// TRLC-LINKS: REQ-SDS-218
function trigAdvice(ch) {
  return "trigger on C" + (ch + 1) + " in Normal mode with the level at 50 % (the 50% key) so every capture starts at the same point on the signal, then try again.";
}
// TRLC-LINKS: REQ-SDS-218
const msleep = (ms) => new Promise((r) => setTimeout(r, ms));
// TRLC-LINKS: REQ-SDS-218
async function masterStatus() { try { st = await (await fetch("/api/status")).json(); applyStatus(); } catch (e) { /* keep the last */ } return st; }

// masterWindows reads `count` consecutive triggered raw captures of channel ch
// and returns their trigger-anchored windows (the mask build's window), or null.
// TRLC-LINKS: REQ-SDS-218
async function masterWindows(count, ch) {
  const out = [];
  let lastSeq = 0, tries = 0, geom = "";
  const posFrac = st && st.trig_pos_frac >= 0 && st.trig_pos_frac <= 1 ? st.trig_pos_frac : 0.5;
  while (out.length < count && tries < count * 8) {
    if (master.abort) return null;
    tries++;
    let f;
    try { f = decodeBinFrame(await (await fetch("/api/frame.bin?since=" + lastSeq + "&waitms=1000&raw=1")).arrayBuffer()); } catch (e) { continue; }
    if (!f || f.unchanged || f.seq === lastSeq) continue;
    lastSeq = f.seq;
    const sig = ch ? f.c2 : f.c1;
    if (!sig || f.is_env || !(f.edge_x >= 0) || !(f.sample_s > 0)) continue;
    const win = Math.min(f.win_cols || (st && st.win_cols) || f.cols, f.cols);
    const g = [win, f.tdiv_s, f.sample_s, !!f.peak_detect].join(":");
    if (g !== geom) { geom = g; out.length = 0; } // a capture of another geometry restarts the set
    const left = Math.round(f.edge_x - posFrac * win), w = new Float64Array(win);
    for (let j = 0; j < win; j++) { const s = left + j; w[j] = s >= 0 && s < f.cols && sig[s] >= 0 ? sig[s] : -1; }
    out.push(w);
  }
  return out.length >= count ? out : null;
}

// masterMask builds the mask in the geometry of a LIVE raw capture (the one the
// test will judge) from the averaged master, by time from the trigger: each
// live sample's bound is the master's min/max over that sample's span, then the
// mask tolerances dilate it (wsmath.js maskDilate, as the mask build does).
// TRLC-LINKS: REQ-SDS-218
function masterMask(m, live, tolT, tolV, posFrac) {
  const win = Math.min(live.win_cols || (st && st.win_cols) || live.cols, live.cols);
  const lo = new Array(win).fill(255), hi = new Array(win).fill(0);
  const r = Math.max(0.5, live.sample_s / m.sampleS / 2), len = m.sig.length;
  for (let j = 0; j < win; j++) {
    const t = (j - posFrac * win) * live.sample_s;
    const x = m.edge + t / m.sampleS;
    if (!(x >= 0 && x <= len - 1)) continue;
    let mn = Infinity, mx = -Infinity;
    for (let k = Math.max(0, Math.floor(x - r)); k <= Math.min(len - 1, Math.ceil(x + r)); k++) {
      const v = m.sig[k];
      if (!(v >= 0)) continue;
      if (v < mn) mn = v;
      if (v > mx) mx = v;
    }
    if (mn > mx) continue;
    lo[j] = Math.floor(mn); hi[j] = Math.ceil(mx);
  }
  const d = maskDilate(lo, hi, tolT, tolV);
  return { lo: d.lo, hi: d.hi, win, geometry: { tdiv_s: live.tdiv_s, sample_s: live.sample_s, peak_detect: !!live.peak_detect } };
}

// stackMaster runs the master: refuses (saying how to fix the trigger) when the
// signal does not repeat, else averages, stores REF A, builds the mask, restores
// the acquisition and starts the test. Esc (or the button) cancels; the
// acquisition is restored whatever happens.
// TRLC-LINKS: REQ-SDS-218
async function stackMaster() {
  if (master.busy) { masterCancel(); return; }
  if (ws.cur !== "compare") setWs("compare");
  const N = Math.max(2, +$("masterN").value || 64);
  if (!st) { masterSay("No instrument status yet.", "warn"); return; }
  const ch = st.trig_source === 1 ? 1 : 0;
  const refuse = (why) => { masterSay("Master refused: " + why, "warn"); toast("Master refused: " + why, "warn"); };
  if (st.band === "envelope" || st.band === "roll" || st.roll) { refuse("this timebase shows a min/max envelope — choose 5 ms/div or faster for sampled captures."); return; }
  if (ch ? st.cpl2 : st.cpl1) { refuse("set C" + (ch + 1) + " coupling to DC: the mask tests the raw capture."); return; }
  const prev = { mode: st.acq_mode || 0, avg: st.avg_count || 16, eres: st.eres_len || 1, norm: !!st.norm };
  master.busy = true; master.abort = false; masterUI();
  let switched = false;
  const restore = async () => {
    if (!switched) return;
    switched = false;
    await send("acqmode", prev.mode);
    if (prev.mode === 1 || prev.mode === 4) await send("avgcount", prev.avg);
    if (prev.mode === 2) await send("eres", prev.eres);
    await send("norm", prev.norm ? 1 : 0); // SINGLE leaves the trigger in Normal
    await send("run", 1);
    await masterStatus(); // the panes show the restored acquisition at once
  };
  try {
    if (!st.running) { await send("run", 1); await msleep(300); }
    // 1. the signal must repeat (the mask build's rule) on consecutive raw captures
    masterSay("Checking that C" + (ch + 1) + " repeats from capture to capture…");
    const tolT = Math.max(0, Math.floor(+$("zmTolT").value || 0)), tolV = Math.max(0, +$("zmTolV").value || 0);
    const w = await masterWindows(3, ch);
    if (master.abort) { masterSay("Master cancelled."); return; }
    if (!w) { refuse("no triggered captures of C" + (ch + 1) + " — " + trigAdvice(ch)); return; }
    if (!repeats(w[0], w[1], tolV, tolT) || !repeats(w[1], w[2], tolV, tolT)) { refuse("C" + (ch + 1) + " does not repeat from capture to capture — " + trigAdvice(ch)); return; }
    // 2. average N captures with the device's average mode. The deep-memory
    //    (SRAM) path averages in blocks and names its progress ("block average
    //    k/N"); SINGLE stops on the completed block, so the held capture is the
    //    master. The fast path keeps a running mean of the last N captures:
    //    after N more captures it holds the master, and STOP keeps it.
    masterSay("Averaging " + N + " captures of C" + (ch + 1) + "…");
    await send("acqmode", 1); switched = true;
    await send("avgcount", N);
    await masterStatus(); // the Acquisition pane shows Average while it stacks
    await msleep(250);
    const seq0 = frame ? frame.seq : 0;
    for (let k = 0; k < 20 && !(frame && frame.seq > seq0); k++) await msleep(100);
    const block = !!(frame && /block average/.test(frame.filter || ""));
    const seqA = frame ? frame.seq : 0;
    if (block) await send("single", 1);
    const deadline = Date.now() + 20000 + N * 500;
    for (;;) {
      if (master.abort) { masterSay("Master cancelled — acquisition restored."); return; }
      if (Date.now() > deadline) { refuse("the average did not complete — " + trigAdvice(ch)); return; }
      await msleep(250);
      if (!block) {
        const k = frame ? Math.min(N, Math.max(0, frame.seq - seqA)) : 0;
        masterSay("Averaging " + N + " captures of C" + (ch + 1) + "… " + k + "/" + N);
        if (frame && frame.seq - seqA > N) { await send("run", 0); await masterStatus(); break; }
        continue;
      }
      const s = await masterStatus();
      const m = frame && /block average (\d+)\/(\d+)/.exec(frame.filter || "");
      if (m) masterSay("Averaging " + N + " captures of C" + (ch + 1) + "… " + m[1] + "/" + m[2]);
      if (s && !s.running) break;
    }
    // the held capture: the completed average
    let held = null;
    for (let k = 0; k < 6 && !held; k++) {
      try {
        const f = decodeBinFrame(await (await fetch("/api/frame.bin?raw=1&waitms=200")).arrayBuffer());
        if (f && !f.unchanged && (ch ? f.c2 : f.c1) && f.edge_x >= 0 && f.sample_s > 0) held = f;
      } catch (e) { /* retry */ }
      if (!held) await msleep(150);
    }
    if (!held) { refuse("the averaged capture has no trigger — " + trigAdvice(ch)); return; }
    const am = /block average (\d+)\/(\d+)/.exec(held.filter || "");
    const got = am ? +am[1] : N;
    // the display frame of the same capture becomes REF A (it overlays the live trace)
    for (let k = 0; k < 20 && !(frame && frame.seq >= held.seq); k++) await msleep(100);
    saveRef("A");
    refs.A.label = "master ×" + got;
    updateRefRows();
    const mst = { sig: ch ? held.c2 : held.c1, sampleS: held.sample_s, edge: held.edge_x };
    // 3. restore the acquisition so live captures are tested raw
    await restore();
    masterSay("Building the mask from the master…");
    // 4. the mask, in the geometry of the live captures it will judge
    let live = null;
    for (let k = 0; k < 10 && !live; k++) {
      try {
        const f = decodeBinFrame(await (await fetch("/api/frame.bin?raw=1&waitms=1000")).arrayBuffer());
        if (f && !f.unchanged && f.sample_s > 0 && f.edge_x >= 0 && (ch ? f.c2 : f.c1) && !/block average/.test(f.filter || "")) live = f;
      } catch (e) { /* retry */ }
    }
    if (!live) { refuse("no live capture after restoring the acquisition — REF A is kept; build the mask in the Mask pane."); return; }
    const posFrac = st && st.trig_pos_frac >= 0 && st.trig_pos_frac <= 1 ? st.trig_pos_frac : 0.5;
    const mk = masterMask(mst, live, tolT, tolV, posFrac);
    const resp = await fetch("/api/mask", { method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ lo_b64: zmPackMask(mk.lo), hi_b64: zmPackMask(mk.hi), win: mk.win, ch, ...mk.geometry }) });
    const res = await resp.json().catch(() => ({}));
    if (!resp.ok || !res.ok) { refuse("the mask was not accepted (" + (res.err || resp.status) + ")."); return; }
    // gen: this install's number, so the page lets the mask go once the scope clears or replaces it
    zm.mask = { lo: mk.lo, hi: mk.hi, win: mk.win, ch, posFrac, geometry: mk.geometry, gen: typeof res.gen === "number" ? res.gen : null, genAt: Date.now() };
    const c = zmVctx(ch);
    if (c) { zm.mask.srcLo = mk.lo.slice(); zm.mask.srcHi = mk.hi.slice(); zm.mask.svpc = c.vpc; zm.mask.soff = c.off; zm.mask.avpc = c.vpc; zm.mask.aoff = c.off; }
    $("zmCh").value = String(ch);
    // 5. the test runs on raw captures with zeroed counters
    await send("maskclear", 0);
    zm.failMark = null;
    const cnt = $("zmMode").querySelector('button[data-value="1"]');
    if ($("zmMode").value !== "1") cnt.click(); else send("maskmode", 1);
    zmStatus("mask built from the master (×" + got + ", ±" + tolT + " samples, ±" + tolV + " codes) — testing");
    masterSay("Master ×" + got + " stored as REF A; the mask is built from it and the test counts live captures (counters zeroed).");
    toast("Master ×" + got + ": REF A stored, mask built, test running");
    redraw();
  } catch (e) {
    masterSay("Master failed: " + (e && e.message ? e.message : e), "warn");
  } finally {
    try { await restore(); } catch (e) { /* the status poll will show the state */ }
    master.busy = false; master.abort = false;
    masterUI();
  }
}
$("masterBtn").onclick = () => stackMaster();
