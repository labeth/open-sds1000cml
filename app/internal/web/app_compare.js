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
const msleep = (ms) => new Promise((r) => setTimeout(r, ms));
// TRLC-LINKS: REQ-SDS-218
async function masterStatus() { try { st = await (await fetch("/api/status")).json(); applyStatus(); } catch (e) { /* keep the last */ } return st; }

// masterAct runs the scope's Stack master row (stack, or cancel while stacking).
// TRLC-LINKS: REQ-SDS-218
async function masterAct(action) {
  try {
    return await (await fetch("/api/sheets/action", { method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ row: "cmp.stack_master", action }) })).json();
  } catch (e) { return { ok: false, error: "no answer from the scope" }; }
}

// masterRow is the scope's Stack master row: "stacking", "REF A: 64 x C1" or
// "none"; null when the scope did not answer.
// TRLC-LINKS: REQ-SDS-218
async function masterRow() {
  try {
    const m = await (await fetch("/api/sheets")).json();
    for (const t of m.Tabs) for (const x of t.Rows) if (x.ID === "cmp.stack_master") return x.Text || "";
  } catch (e) { /* the next look */ }
  return null;
}

// loadScopeRef makes the scope's saved reference a (GET /api/refs: its screen
// columns, the tip volts it was saved at, the timebase and the trigger's place
// across the screen) this page's reference slot.
// TRLC-LINKS: REQ-SDS-218, REQ-SDS-203
function loadScopeRef(slot, a, label) {
  const n = a.cols || (a.c1 || a.c2 || []).length;
  refs[slot] = {
    c1: a.c1 || null, c2: a.c2 || null,
    vpc1: (a.vdiv[0] || 1) / 25, vpc2: (a.vdiv[1] || 1) / 25,
    off1: a.off[0] || 0, off2: a.off[1] || 0,
    show: true,
    dt: n ? 10 * a.tdiv_s / n : 0,
    edge: n ? a.pos_frac * (n - 1) : -1,
    tdiv: a.tdiv_s || 0,
    vdiv1: a.vdiv[0] || 0, vdiv2: a.vdiv[1] || 0,
    at: Date.now(), label: label || "",
  };
  updateRefRows(); redraw();
}

// stackMaster has the scope stack its master, as COMPARE's key again does on
// the LCD: the scope averages the trigger source while running (or refuses,
// saying how to fix the trigger), stores REF A, builds the mask in the live
// captures' geometry and starts the test. This page takes the scope's REF A
// as its own, and its mask pane follows the scope's mask. The page averaged
// through SINGLE before, a full record per capture on the SRAM image: 64
// captures took about 15 minutes and always timed out (bug hunt #3). The
// button again, or Esc, cancels.
// TRLC-LINKS: REQ-SDS-218
async function stackMaster() {
  if (master.busy) { masterCancel(); return; }
  if (ws.cur !== "compare") setWs("compare");
  const N = Math.max(2, +$("masterN").value || 64);
  const refuse = (why) => { masterSay("Master refused: " + why, "warn"); toast("Master refused: " + why, "warn"); };
  master.busy = true; master.abort = false; masterUI();
  try {
    await fetch("/api/sheets/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ row: "cmp.master_depth", value: N }) });
    const r = await masterAct("stack");
    if (!r.ok) { refuse(r.error || r.notice || "the scope did not start it"); return; }
    const t0 = Date.now();
    let text = "stacking";
    while (text === "stacking" || text === null) {
      if (master.abort) { await masterAct("cancel"); masterSay("Master cancelled."); return; }
      if (Date.now() - t0 > 120000) { await masterAct("cancel"); refuse("the scope's average did not complete in two minutes: the trigger source must repeat."); return; }
      masterSay("The scope is averaging " + N + " captures for the master… " + Math.round((Date.now() - t0) / 1000) + " s");
      await msleep(400);
      text = await masterRow();
    }
    const m = /^REF A: (\d+) x C(\d)/.exec(text);
    if (!m) { // the scope stopped the stack: its notice says why
      const s = await masterStatus();
      refuse((s && s.panel && s.panel.Notice) || "the scope stopped the stack");
      return;
    }
    const j = await (await fetch("/api/refs")).json().catch(() => null);
    const a = j && j.ok && j.refs && j.refs[0];
    if (a && a.has) loadScopeRef("A", a, "master ×" + m[1]);
    masterSay("Master ×" + m[1] + " of C" + m[2] + " stored as REF A on the scope and here; the scope builds the mask from it and tests live captures.");
    toast("Master ×" + m[1] + ": REF A stored, the scope's mask test runs");
    await masterStatus();
  } catch (e) {
    masterSay("Master failed: " + (e && e.message ? e.message : e), "warn");
  } finally {
    master.busy = false; master.abort = false;
    masterUI();
  }
}
$("masterBtn").onclick = () => stackMaster();
