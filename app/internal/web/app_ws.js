// ENGMODEL-OWNER-UNIT: FU-WEB-APP-WS
// app_ws.js — task workspaces (ADR-TASK-WORKSPACES): the five workspaces SCOPE,
// BUS, SPECTRUM, MEASURE and COMPARE, each a split tree of panes over the fixed
// instrument strip. One instrument: every setting is shared; only the screen
// view is remembered per workspace. Owns the pane engine (resizable splits,
// collapse, maximize), workspace switching and "again", the keyboard and the
// help sheet. Classic script; shares app.js globals.
"use strict";

const WSDEF = [
  { id: "scope", name: "SCOPE", alt: "Alt+1", view: "YT",
    blurb: "The scope with its horizontal, vertical, trigger and acquisition settings and quick measurements.",
    again: "Y-T ⇄ X-Y" },
  { id: "bus", name: "BUS", alt: "Alt+2", view: "YT",
    blurb: "Decode a bus (entering it with decoding off auto-detects the bus), read its packets and trigger on bytes.",
    again: "packets → eye diagram → stream" },
  { id: "spectrum", name: "SPECTRUM", alt: "Alt+3", view: "FFT",
    blurb: "A large FFT with a peak table, harmonics and THD beside a small time trace.",
    again: "FFT → spectrogram → Bode" },
  { id: "measure", name: "MEASURE", alt: "Alt+4", view: "YT",
    blurb: "Every measurement on both channels with statistics, cursors, the mask test, acquisition, super-res and precision.",
    again: "statistics ⇄ all values" },
  { id: "compare", name: "COMPARE", alt: "Alt+5", view: "YT",
    blurb: "References A and B, math and the mask against the live trace, with a live difference readout.",
    again: "stack a master (average, REF A, mask, test)" },
];
const WSBY = Object.fromEntries(WSDEF.map((w) => [w.id, w]));

// Layout trees. In every split exactly one item grows (the main pane) and the
// others keep their pixel size, like an IDE's side bars.
// TRLC-LINKS: REQ-SDS-219
const lp = (pane, size, o) => Object.assign({ kind: "pane", pane, size }, o || {});
// TRLC-LINKS: REQ-SDS-219
const lr = (items, size, o) => Object.assign({ kind: "split", dir: "row", items, size }, o || {});
// TRLC-LINKS: REQ-SDS-219
const lc = (items, size, o) => Object.assign({ kind: "split", dir: "col", items, size }, o || {});
const SIDE = 372;
const WS_LAYOUT = {
  // TRLC-LINKS: REQ-SDS-219
  scope: () => lr([
    lc([lp("p-scope", 470, { grow: true, min: 200 }), lr([lp("p-horiz", 340, { minW: 270 }), lp("p-quick", 520, { grow: true, minW: 300 })], 168)], 900, { grow: true }),
    lc([lp("p-vert", 268), lp("p-trig", 300, { grow: true }), lp("p-acq", 196)], SIDE, { minW: 330 })]),
  // TRLC-LINKS: REQ-SDS-219
  bus: () => lr([
    lc([lp("p-bus-scope", 420, { grow: true, min: 200 }), lp("p-packets", 290, { minW: 420, min: 120 })], 900, { grow: true }),
    lc([lp("p-decoder", 340), lp("p-match", 360, { grow: true })], SIDE, { minW: 330 })]),
  // TRLC-LINKS: REQ-SDS-219
  spectrum: () => lr([
    lc([lp("p-analysis", 470, { grow: true, min: 200 }), lp("p-time", 190, { min: 110 })], 900, { grow: true }),
    lc([lp("p-peaks", 360, { grow: true }), lp("p-readout", 300)], SIDE, { minW: 330 })]),
  // TRLC-LINKS: REQ-SDS-219
  measure: () => lr([
    lc([lp("p-meas-scope", 400, { grow: true, min: 200 }), lp("p-stats", 300, { minW: 440, min: 120 })], 900, { grow: true }),
    lc([lp("p-cursors", 218), lp("p-mask", 300, { grow: true }), lp("p-acq", 196), lp("p-sr", 330, { collapsed: true }), lp("p-prec", 230, { collapsed: true })], SIDE, { minW: 330 })]),
  // TRLC-LINKS: REQ-SDS-219
  compare: () => lr([
    lc([lp("p-cmp-scope", 500, { grow: true, min: 200 }), lp("p-diff", 150, { minW: 380, min: 100 })], 900, { grow: true }),
    lc([lp("p-refs", 222), lp("p-math", 104), lp("p-mask", 300, { grow: true })], SIDE, { minW: 330 })]),
};
const SCOPE_PANES = { scope: "p-scope", bus: "p-bus-scope", spectrum: "p-analysis", measure: "p-meas-scope", compare: "p-cmp-scope" };

// The per-workspace screen view and sub-views. Only the view is per workspace:
// the instrument (and its settings) is one.
const WSV = { scope: "YT", bus: "YT", spectrum: "FFT", measure: "YT", compare: "YT" };
const ws = { cur: null, trees: {}, built: {}, maxed: {}, busView: "packets", measView: "stats", flashT: 0 };
// Workspace modules (app_bus.js, app_spectrum.js…) register enter/leave/view
// hooks here; setWs and setWsView call them in order. WS_TICKS run once per
// rendered frame (scheduleRender) and must stay cheap.
const WS_HOOKS = [], WS_TICKS = [];
// TRLC-LINKS: REQ-SDS-219
function wsTick(now) { for (const f of WS_TICKS) f(now); }

// ---- pane engine ----------------------------------------------------------------
const PHEAD = 30, PGAP = 6;
// TRLC-LINKS: REQ-SDS-219
const isOpen = (n) => n.kind === "split" || !n.collapsed;
// minOf: the smallest a node can be along dir — a split sums its children along
// its own axis and takes the largest across it, so nothing inside is clipped.
// TRLC-LINKS: REQ-SDS-219
function minOf(n, dir) {
  if (n.kind === "pane") return n.collapsed ? PHEAD : dir === "row" ? (n.minW || 200) : (n.min || 80);
  const v = n.items.map((it) => minOf(it, dir));
  const kids = n.dir === dir ? v.reduce((a, b) => a + b, 0) + PGAP * (v.length - 1) : Math.max(...v);
  return Math.max(kids, (dir === "row" ? n.minW : n.min) || 0); // a split's own minimum (the side column's)
}
// TRLC-LINKS: REQ-SDS-219
function growOf(node) {
  const g = node.items.find((x) => x.grow) || node.items[0];
  return isOpen(g) ? g : (node.items.find(isOpen) || g);
}
// TRLC-LINKS: REQ-SDS-219
function neighbours(node, gi) {
  let a = null, b = null;
  for (let j = gi - 1; j >= 0; j--) if (isOpen(node.items[j])) { a = node.items[j]; break; }
  for (let j = gi; j < node.items.length; j++) if (isOpen(node.items[j])) { b = node.items[j]; break; }
  return a && b ? [a, b] : null;
}
// TRLC-LINKS: REQ-SDS-219
function applySizes(node) {
  const grow = growOf(node);
  for (const it of node.items) {
    const shut = it.kind === "pane" && !!it.collapsed;
    it.el.style.flex = shut ? "0 0 " + PHEAD + "px" : it === grow ? "1 1 0px" : "0 1 " + Math.round(Math.max(minOf(it, node.dir), it.size)) + "px";
    it.el.style[node.dir === "row" ? "minWidth" : "minHeight"] = minOf(it, node.dir) + "px";
    if (it.kind === "pane") { it.el.classList.toggle("is-collapsed", shut); syncCaret(it); }
  }
  node.gutters.forEach((g, i) => {
    const ok = !!neighbours(node, i + 1);
    g.classList.toggle("is-idle", !ok);
    g.tabIndex = ok ? 0 : -1;
  });
}
// TRLC-LINKS: REQ-SDS-219
function syncCaret(n) {
  const pane = $(n.pane);
  if (!pane || pane.parentElement !== n.el) return;
  const c = pane.querySelector(":scope > .ph > .pc");
  if (!c) return;
  c.textContent = n.collapsed ? "▸" : "▾";
  c.setAttribute("aria-expanded", String(!n.collapsed));
  const t = pane.dataset.title || "pane";
  c.setAttribute("aria-label", (n.collapsed ? "Expand " : "Collapse ") + t);
  c.title = n.collapsed ? "Expand this pane" : "Collapse this pane";
}
// TRLC-LINKS: REQ-SDS-219
function resizePair(node, gi, delta, base) {
  const pair = neighbours(node, gi);
  if (!pair) return;
  const [a, b] = pair, dir = node.dir, grow = growOf(node);
  const lo = minOf(a, dir), hi = base.a0 + base.b0 - minOf(b, dir);
  if (hi < lo) return;
  const na = Math.max(lo, Math.min(hi, base.a0 + delta));
  if (a !== grow) a.size = na;
  if (b !== grow) b.size = base.a0 + base.b0 - na;
  applySizes(node);
}
// TRLC-LINKS: REQ-SDS-219
function pairBase(node, gi) {
  const p = neighbours(node, gi);
  if (!p) return null;
  // TRLC-LINKS: REQ-SDS-219
  const m = (el) => el.getBoundingClientRect()[node.dir === "row" ? "width" : "height"];
  return { a0: m(p[0].el), b0: m(p[1].el) };
}
// TRLC-LINKS: REQ-SDS-219
function resetSplit(node) {
  for (const it of node.items) { it.size = it.def; if (it.kind === "pane") it.collapsed = it.defCollapsed; }
  applySizes(node);
}
// TRLC-LINKS: REQ-SDS-219
function makeGutter(node, gi) {
  const vert = node.dir === "row";
  const g = document.createElement("div");
  g.className = "gut";
  g.setAttribute("role", "separator");
  g.setAttribute("aria-orientation", vert ? "vertical" : "horizontal");
  g.setAttribute("aria-label", "Resize panes");
  g.title = "Drag to resize, double-click to reset";
  g.addEventListener("pointerdown", (ev) => {
    if (ev.button !== 0) return;
    ev.preventDefault();
    const base = pairBase(node, gi);
    if (!base) return;
    const p0 = vert ? ev.clientX : ev.clientY;
    g.classList.add("is-drag");
    try { g.setPointerCapture(ev.pointerId); } catch (e) { /* synthetic pointer */ }
    // TRLC-LINKS: REQ-SDS-219
    const move = (e2) => { if (e2.pointerId === ev.pointerId) resizePair(node, gi, (vert ? e2.clientX : e2.clientY) - p0, base); };
    // TRLC-LINKS: REQ-SDS-219
    const end = (e2) => {
      if (e2.pointerId !== ev.pointerId) return;
      g.removeEventListener("pointermove", move); g.removeEventListener("pointerup", end); g.removeEventListener("pointercancel", end);
      g.classList.remove("is-drag");
    };
    g.addEventListener("pointermove", move); g.addEventListener("pointerup", end); g.addEventListener("pointercancel", end);
  });
  g.addEventListener("dblclick", () => resetSplit(node));
  g.addEventListener("keydown", (ev) => {
    let d = 0;
    if ((vert && ev.key === "ArrowLeft") || (!vert && ev.key === "ArrowUp")) d = -1;
    else if ((vert && ev.key === "ArrowRight") || (!vert && ev.key === "ArrowDown")) d = 1;
    else if (ev.key === "Enter" || ev.key === "Home") { ev.preventDefault(); ev.stopPropagation(); resetSplit(node); return; }
    if (!d) return;
    ev.preventDefault(); ev.stopPropagation();
    const base = pairBase(node, gi);
    if (base) resizePair(node, gi, d * (ev.shiftKey ? 64 : 16), base);
  });
  return g;
}
// TRLC-LINKS: REQ-SDS-219
function buildNode(wsId, node, parent) {
  node.parent = parent; node.def = node.size; node.ws = wsId;
  if (node.kind === "pane") {
    node.defCollapsed = !!node.collapsed;
    const slot = document.createElement("div");
    slot.className = "slot";
    slot.dataset.pane = node.pane;
    slot._node = node;
    node.el = slot;
    return slot;
  }
  const el = document.createElement("div");
  el.className = "split " + node.dir;
  node.el = el; node.gutters = [];
  node.items.forEach((it, i) => {
    if (i) { const g = makeGutter(node, i); node.gutters.push(g); el.appendChild(g); }
    el.appendChild(buildNode(wsId, it, node));
  });
  return el;
}
// TRLC-LINKS: REQ-SDS-219
function leaves(node, out) {
  out = out || [];
  if (node.kind === "pane") out.push(node); else node.items.forEach((n) => leaves(n, out));
  return out;
}
// TRLC-LINKS: REQ-SDS-219
function sizeTree(node) { if (node.kind !== "split") return; node.items.forEach(sizeTree); applySizes(node); }

// placePanes moves each pane of a workspace into its slot. Panes shared by two
// workspaces (Acquisition, Mask & zones) move to whichever is shown.
// TRLC-LINKS: REQ-SDS-219
function placePanes(wsId) {
  for (const n of leaves(ws.trees[wsId])) {
    const pane = $(n.pane);
    if (pane && pane.parentElement !== n.el) n.el.appendChild(pane);
  }
  sizeTree(ws.trees[wsId]);
}

// TRLC-LINKS: REQ-SDS-219
function paneNode(pane) { const s = pane && pane.parentElement; return s && s._node ? s._node : null; }

// TRLC-LINKS: REQ-SDS-219
function toggleCollapse(n, force) {
  const want = force === undefined ? !n.collapsed : !!force;
  if (want === !!n.collapsed) return;
  if (want && ws.maxed[n.ws] === n) toggleMax(n);
  if (want && !n.parent.items.some((x) => x !== n && isOpen(x))) { toast("The last open pane of a column stays open"); return; }
  if (!want) n.size = n.def;
  n.collapsed = want;
  applySizes(n.parent);
}
// toggleMax lays a pane over its whole workspace (double-click its title); again,
// or Esc, restores it.
// TRLC-LINKS: REQ-SDS-219
function toggleMax(n) {
  const cur = ws.maxed[n.ws];
  if (cur) {
    cur.el.classList.remove("is-max");
    ws.maxed[n.ws] = null;
    if (cur === n) return;
  }
  if (n.collapsed) toggleCollapse(n, false);
  n.el.classList.add("is-max");
  ws.maxed[n.ws] = n;
}

// Every pane header gets a caret (collapse), a double-click (maximize) and a
// click-to-expand while collapsed.
// TRLC-LINKS: REQ-SDS-219
function decoratePane(pane) {
  const ph = pane.querySelector(":scope > .ph");
  if (!ph || ph.querySelector(":scope > .pc")) return;
  const c = document.createElement("button");
  c.type = "button"; c.className = "pc"; c.textContent = "▾";
  c.setAttribute("aria-expanded", "true");
  ph.insertBefore(c, ph.firstChild);
  c.addEventListener("click", (ev) => { ev.stopPropagation(); const n = paneNode(pane); if (n) toggleCollapse(n); });
  ph.addEventListener("click", (ev) => {
    const n = paneNode(pane);
    if (n && n.collapsed && !ev.target.closest(".pa")) toggleCollapse(n, false);
  });
  ph.addEventListener("dblclick", (ev) => {
    if (ev.target.closest(".pa, .pc, button, select, input")) return;
    const n = paneNode(pane);
    if (n) toggleMax(n);
  });
  const t = ph.querySelector(".pt");
  if (t) t.title = "Double-click the title bar to maximize this pane (Esc restores)";
}

// ---- workspace switching ------------------------------------------------------------
// TRLC-LINKS: REQ-SDS-219
function wsBuild(id) {
  if (ws.built[id]) return;
  const tree = WS_LAYOUT[id]();
  ws.trees[id] = tree;
  $("ws-" + id).appendChild(buildNode(id, tree, null));
  ws.built[id] = true;
}

// setWs shows a workspace: its panes, the scope display in its scope pane, the
// view it had, and its activity. Settings are untouched (one instrument).
// TRLC-LINKS: REQ-SDS-219
function setWs(id) {
  if (!WSBY[id] || id === ws.cur) return;
  const prev = ws.cur;
  wsBuild(id);
  const a = document.activeElement;
  const stranded = !!(prev && a && $("ws-" + prev).contains(a));
  if (prev) for (const h of WS_HOOKS) if (h.leave) h.leave(prev, id);
  ws.cur = id;
  for (const w of WSDEF) {
    $("ws-" + w.id).hidden = w.id !== id;
    const t = $("tab-" + w.id);
    t.setAttribute("aria-selected", String(w.id === id));
    t.tabIndex = w.id === id ? 0 : -1;
  }
  document.body.dataset.ws = id;
  placePanes(id);
  // the one scope display moves into this workspace's scope pane
  const sp = $(SCOPE_PANES[id]);
  const slot = sp.querySelector("[data-scope-slot]");
  if (slot && $("scopeunit").parentElement !== slot) slot.appendChild($("scopeunit"));
  const tools = sp.querySelector("[data-tools-slot]");
  if (tools && $("scopetools").parentElement !== tools) tools.appendChild($("scopetools"));
  if (stranded) { try { $("tab-" + id).focus({ preventScroll: true }); } catch (e) { /* ignore */ } }
  popClose();
  applyWsView(id);
  for (const h of WS_HOOKS) if (h.enter) h.enter(id, prev);
  allFit();
  wsDots();
  kickMirrors();
}

// applyWsView puts the screen into this workspace's remembered view.
// TRLC-LINKS: REQ-SDS-219
function applyWsView(id) {
  const v = WSV[id];
  if (id === "scope") $("viewScope").value = v;
  if (id === "spectrum") { $("viewSpec").value = v; anaTools(v); }
  if (view.mode !== v) setMode(v);
  else resize();
}

// setWsView changes the current workspace's view (the view controls, Y/X/F keys
// and "again" all come here) and remembers it for the workspace.
// TRLC-LINKS: REQ-SDS-219
function setWsView(v) {
  const id = ws.cur;
  WSV[id] = v;
  applyWsView(id);
  for (const h of WS_HOOKS) if (h.view) h.view(id, v);
  wsDots(); // a view can start or stop a feature (the spectrogram runs while shown)
}

// TRLC-LINKS: REQ-SDS-219
function anaTools(v) {
  for (const t of document.querySelectorAll(".anatools")) t.hidden = t.dataset.ana !== v;
  const sub = { FFT: "FFT of the shown channels (Hann window, dB re each channel's peak)", SPG: "the spectrum over time, newest on top", BODE: "gain and phase against log frequency" }[v] || "";
  $("anaSub").textContent = sub;
}

// "Again": pressing the shown workspace's key once more shows its next view,
// as F1-F5 do on the scope.
// TRLC-LINKS: REQ-SDS-219
function wsAgain(id) {
  if (id === "scope") setWsView(WSV.scope === "XY" ? "YT" : "XY");
  else if (id === "bus") busSetView({ packets: "eye", eye: "stream", stream: "packets" }[ws.busView] || "eye");
  else if (id === "spectrum") setWsView({ FFT: "SPG", SPG: "BODE", BODE: "FFT" }[WSV.spectrum] || "FFT");
  else if (id === "measure") measSetView(ws.measView === "stats" ? "values" : "stats");
  else if (id === "compare") stackMaster();
}

// TRLC-LINKS: REQ-SDS-219
function wsKey(id) {
  if (ws.cur === id) wsAgain(id); else setWs(id);
}

// wsDots marks a workspace whose feature is active (decoding, a running mask
// test, a shown reference…): a dot on its tab, red when the mask test fails.
// TRLC-LINKS: REQ-SDS-219
function wsDots() {
  // TRLC-LINKS: REQ-SDS-219
  const set = (id, on, bad, why) => {
    const t = $("tab-" + id);
    if (!t) return;
    const dot = t.querySelector(".tdot");
    dot.hidden = !on; dot.classList.toggle("is-bad", !!bad);
    const w = WSBY[id];
    t.title = w.blurb + " Again: " + w.again + "." + (on && why ? " Now: " + why + "." : "") + " Key: " + w.alt + ".";
  };
  const decoding = typeof dcfg !== "undefined" && dcfg.proto !== "off";
  const serial = typeof stArmed === "function" && stArmed();
  set("scope", false);
  set("bus", decoding || serial, false, serial ? "serial trigger armed" : "decoding " + (decoding ? dcfg.proto.toUpperCase() : ""));
  const sgOn = typeof spg !== "undefined" && spg.armed, bdOn = typeof bode !== "undefined" && bode.armed;
  set("spectrum", sgOn || bdOn, false, bdOn ? "Bode armed" : "spectrogram running");
  const maskOn = !!(st && st.mask_mode > 0), fails = !!(st && st.mask_fail > 0);
  const srOn = typeof sr !== "undefined" && sr.armed, srScope = !!(st && st.sr_active);
  set("measure", maskOn || srOn || srScope, maskOn && fails, maskOn ? "mask test running" + (fails ? ", " + st.mask_fail + " failures" : "") :
    srOn ? "super-res stacking" : "super-res stacking on the scope");
  const refOn = !!((refs.A && refs.A.show) || (refs.B && refs.B.show)), mathOn = mathFn !== "off";
  const mst = typeof master !== "undefined" && master.busy;
  set("compare", refOn || mathOn || mst, false, mst ? "stacking a master" : refOn ? "references shown" : "math on");
}
setInterval(wsDots, 1000);

// revealControl shows the workspace, view and pane where an element lives, so
// it is visible and can take focus (All settings' "go" arrows use it).
// TRLC-LINKS: REQ-SDS-219
function revealControl(el) {
  if (!el) return false;
  if (el.closest("#strip, #top")) return true;
  // a menu entry (Export, Setups): open its menu under its top-bar button
  const pop = el.closest(".pop");
  if (pop) {
    const btn = $(pop.id === "exportpop" ? "exportBtn" : pop.id === "setpop" ? "setupsBtn" : "");
    if (btn && pop.hidden) btn.click(); // as its button opens it (Setups reads the slots)
    return !pop.hidden;
  }
  const pane = el.closest(".pane");
  if (!pane) return false;
  let wsId = null;
  const host = pane.closest(".ws");
  if (host) wsId = host.id.replace(/^ws-/, "");
  else {
    // a pane still in the library or shared: find a workspace that lays it out
    for (const w of WSDEF) if (leaves(WS_LAYOUT[w.id]()).some((n) => n.pane === pane.id)) { wsId = w.id; break; }
  }
  if (!wsId) return false;
  // a shared pane already shown in the current workspace stays put
  if (ws.cur !== wsId && !leaves(ws.trees[ws.cur] || { kind: "pane" }).some((n) => n.pane === pane.id)) setWs(wsId);
  const bv = el.closest(".bview");
  if (bv && bv.hidden) busSetView(bv.dataset.view);
  const at = el.closest(".anatools");
  if (at && at.hidden) setWsView(at.dataset.ana);
  if (el.closest("#valuesTable") && ws.measView !== "values") measSetView("values");
  // controls a view hides: the peak controls under Bode, Persist on a spectrum
  if (!el.getClientRects().length) {
    if (el.closest("#p-peaks") && !el.closest("#bodeTable") && WSV.spectrum === "BODE") setWsView("FFT");
    else if (el.closest("#scopetools") && ws.cur === "spectrum") { setWs("scope"); return revealControl(el); }
  }
  const n = paneNode(pane);
  if (n && n.collapsed) toggleCollapse(n, false);
  const mx = ws.maxed[ws.cur];
  if (mx && mx !== n) toggleMax(mx);
  const q = el.closest(".qb");
  if (q && getComputedStyle(q).display === "none") return false;
  return true;
}

// TRLC-LINKS: REQ-SDS-219
function flash(el) {
  if (!el) return;
  el.classList.remove("is-flash"); void el.offsetWidth; el.classList.add("is-flash");
  clearTimeout(ws.flashT);
  ws.flashT = setTimeout(() => el.classList.remove("is-flash"), 1500);
}

// ---- keyboard ---------------------------------------------------------------------
// One keydown handler for the whole app. Alt+1..5 are matched on the key CODE so
// macOS Option+digit (which types a symbol) works too; F1-F5 are deliberately
// not bound (F5 is the browser's reload). Single letters are ignored while
// typing, and Space/Enter keep their meaning on a focused control.
// TRLC-LINKS: REQ-SDS-219, REQ-SDS-204
function wsKeydown(e) {
  const k = e.key;
  if (k === "Escape") {
    if ($("help").classList.contains("show")) { toggleHelp(false); e.preventDefault(); return; }
    if (popOpenEl) { const b = popClose(); if (b) b.focus(); e.preventDefault(); return; }
    if (!$("ejBigWrap").classList.contains("hidden")) { $("ejBigWrap").classList.add("hidden"); e.preventDefault(); return; }
    if (typeof master !== "undefined" && master.busy) { masterCancel(); e.preventDefault(); return; }
    if (ws.maxed[ws.cur] && !editableFocused()) { toggleMax(ws.maxed[ws.cur]); e.preventDefault(); return; }
    return;
  }
  const dg = /^(?:Digit|Numpad)([1-5])$/.exec(e.code || "");
  if (dg && e.altKey && !e.ctrlKey && !e.metaKey && !e.shiftKey) {
    // while typing, an Option+digit that produces a character belongs to the field
    if (editableFocused() && k.length === 1 && !/^[1-5]$/.test(k)) return;
    e.preventDefault();
    wsKey(WSDEF[+dg[1] - 1].id);
    return;
  }
  if (e.ctrlKey && !e.altKey && !e.metaKey && k === ",") { e.preventDefault(); allToggle(); return; }
  if ($("help").classList.contains("show")) return;
  if (editableFocused() || e.ctrlKey || e.metaKey || e.altKey) return;
  const t = e.target;
  if ((k === " " || k === "Enter") && t && t.closest && t.closest("button, [role=tab], [role=radio], [role=separator], a, summary")) return;
  const key = k.length === 1 ? k.toLowerCase() : k;
  const m = KEYMAP.find((x) => x.key === key || x.key === k);
  if (m) { e.preventDefault(); m.run(); }
}
window.addEventListener("keydown", wsKeydown);

// Roving focus on the tab list: arrows, Home and End switch workspace.
$("wstabs").addEventListener("keydown", (ev) => {
  const i = WSDEF.findIndex((w) => $("tab-" + w.id) === ev.target);
  if (i < 0) return;
  let j = -1;
  if (ev.key === "ArrowRight") j = (i + 1) % WSDEF.length;
  else if (ev.key === "ArrowLeft") j = (i + WSDEF.length - 1) % WSDEF.length;
  else if (ev.key === "Home") j = 0;
  else if (ev.key === "End") j = WSDEF.length - 1;
  if (j < 0) return;
  ev.preventDefault(); ev.stopPropagation();
  setWs(WSDEF[j].id);
  $("tab-" + WSDEF[j].id).focus();
});
for (const w of WSDEF) $("tab-" + w.id).addEventListener("click", () => setWs(w.id));

// ---- help sheet ---------------------------------------------------------------------
let helpBack = null;
// TRLC-LINKS: REQ-SDS-219, REQ-SDS-204
function toggleHelp(force) {
  const el = $("help");
  const show = force === undefined ? !el.classList.contains("show") : !!force;
  if (show === el.classList.contains("show")) return;
  if (show) {
    // TRLC-LINKS: REQ-SDS-219, REQ-SDS-204
    const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;");
    // TRLC-LINKS: REQ-SDS-219, REQ-SDS-204
    const rows = (arr) => arr.map((x) => `<tr><td><kbd>${esc(x.label)}</kbd></td><td>${esc(x.desc)}</td></tr>`).join("");
    // three columns: the workspaces, the keys, the mouse
    $("helpBody").innerHTML =
      `<tr><th colspan="2" class="cap">Workspaces</th></tr>` +
      WSDEF.map((w) => `<tr><td><kbd>${w.alt}</kbd></td><td><b>${w.name}</b> <span class="dim">${esc(w.blurb)} Again: ${esc(w.again)}.</span></td></tr>`).join("");
    $("helpKeys").innerHTML = `<tr><th colspan="2" class="cap">Keys</th></tr>` +
      rows([{ label: "Alt+1 – 5", desc: "Workspace; the shown workspace's key again shows its next view" },
        { label: "Ctrl+,", desc: "All settings" }]) + rows(KEYMAP) +
      rows([{ label: "Esc", desc: "Close help, menus, a maximized pane; cancel a master" }]);
    $("helpMouse").innerHTML = `<tr><th colspan="2" class="cap">Mouse</th></tr>` +
      MOUSEMAP.map((x) => `<tr><td class="ml">${esc(x.label)}</td><td>${esc(x.desc)}</td></tr>`).join("");
    helpBack = document.activeElement;
    el.classList.add("show");
    $("helpClose").focus();
  } else {
    el.classList.remove("show");
    if (helpBack && helpBack.isConnected) { try { helpBack.focus({ preventScroll: true }); } catch (e) { /* ignore */ } }
    helpBack = null;
  }
}
$("help").addEventListener("click", (e) => { if (e.target.id === "help") toggleHelp(false); }); // backdrop closes
// TRLC-LINKS: REQ-SDS-219, REQ-SDS-204
$("helpClose").onclick = () => toggleHelp(false);
// TRLC-LINKS: REQ-SDS-219, REQ-SDS-204
$("helpBtn").onclick = () => toggleHelp();
// a modal: Tab stays on the sheet's control instead of wandering behind it
$("help").addEventListener("keydown", (e) => { if (e.key === "Tab") { e.preventDefault(); $("helpClose").focus(); } });

// ---- view controls ---------------------------------------------------------------------
// TRLC-LINKS: REQ-SDS-219
$("viewScope").onchange = () => { if (ws.cur === "scope") setWsView($("viewScope").value); else { WSV.scope = $("viewScope").value; setWs("scope"); } };
// TRLC-LINKS: REQ-SDS-219
$("viewSpec").onchange = () => { if (ws.cur === "spectrum") setWsView($("viewSpec").value); else { WSV.spectrum = $("viewSpec").value; setWs("spectrum"); } };

// Y / X / F (and the old view buttons) reach the workspace that owns the view.
// TRLC-LINKS: REQ-SDS-219, REQ-SDS-204
function keyView(v) {
  if (v === "FFT") { WSV.spectrum = "FFT"; if (ws.cur === "spectrum") setWsView("FFT"); else setWs("spectrum"); return; }
  if (v === "XY") { WSV.scope = "XY"; if (ws.cur === "scope") setWsView("XY"); else setWs("scope"); return; }
  // Y-T: every time-domain workspace already shows it; only SCOPE has X-Y, and
  // SPECTRUM is the frequency domain, so leave it for SCOPE.
  if (ws.cur === "scope") setWsView("YT");
  else if (ws.cur === "spectrum") { WSV.scope = "YT"; setWs("scope"); }
}

// TRLC-LINKS: REQ-SDS-219
function wsInit() {
  for (const p of document.querySelectorAll("#panelib > .pane")) decoratePane(p);
  setWs("scope");
}
