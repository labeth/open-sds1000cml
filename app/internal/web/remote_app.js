// ENGMODEL-OWNER-UNIT: FU-WEB-REMOTE
// remote_app.js — the phone remote, "Tiles & Search" (the design the owner
// chose): Home is a grid of big tiles, each one setting with its value and
// two quick buttons that name the value they reach; the workspace chips sit on
// top of Home, the active one naming its "again"; a run dock and a tab bar
// (Home, Menus, History, Keys) stay at the bottom. Menus lists every sheet tab
// and row like phone Settings, with search; History lists every change, from
// this phone or the scope, with undo; Keys holds the keys and pushes the
// tiles do not. A bottom sheet edits one setting: big options, or steppers, a
// drag strip, presets and a keypad with units, or hex and text match bytes.
// Every change goes through the sheet model API (RN) and never opens, moves
// or closes anything on the scope's LCD. Control state only: no waveform, no
// readouts.
// TRLC-LINKS: REQ-SDS-220
"use strict";

(function () {
  const { h, icon, button, press, repeat, holdButton, drag, toast, haptic, Wake, Full, load, store } = window.RU;
  const RC = window.RC, RN = window.RN;
  const DOT = "·", LQ = "‹", RQ = "›", ARROW = "→", TIMES = "×", PM = "±";

  // ---- the scheme's tables ---------------------------------------------------------------

  const WS = [
    { id: "scope", name: "SCOPE", tag: "SCOP", icon: "wave", what: "The full-height trace: Y-T or X-Y.", words: "scope general y-t x-y yt xy trace" },
    { id: "bus", name: "BUS", tag: "BUS", icon: "bus", what: "Decoded packets, serial trigger, eye.", words: "bus decode packets packet hex string uart i2c spi serial eye diagram" },
    { id: "spectrum", name: "SPECTRUM", tag: "SPEC", icon: "spectrum", what: "FFT with peak marker, spectrogram, Bode.", words: "spectrum fft spectrogram bode frequency harmonics thd marker peak" },
    { id: "measure", name: "MEASURE", tag: "MEAS", icon: "measure", what: "Statistics; cursors on Adjust and Position.", words: "measure measurements statistics stats cursors values" },
    { id: "compare", name: "COMPARE", tag: "COMP", icon: "compare", what: "Master, references, mask test and math.", words: "compare master stack golden mask reference references refs math" },
  ];
  const WS_BY = Object.fromEntries(WS.map((w) => [w.id, w]));
  // each workspace's own sheet tabs (the device's wsDefs), shown first in Menus
  const OWN_TABS = { scope: ["trig", "ch1", "ch2", "time", "acq"], bus: ["bus", "trig", "time"], spectrum: ["view", "acq", "time"],
    measure: ["meas", "curs", "acq"], compare: ["cmp", "ch1", "ch2"] };
  // Home: the four big knobs first in every workspace (fixed places), then the workspace's own tiles
  const COMMON = ["time.tdiv", "trig.level", "ch1.vdiv", "ch2.vdiv"];
  const OWN = {
    scope: ["trig.edge", "time.trig_pos", "ch1.offset", "ch2.offset"],
    bus: ["tool", "bus.match", "bus.trigger_on", "bus.protocol"],
    spectrum: ["tool", "view.fft_source", "view.view", "trig.edge"],
    measure: ["curs.mode", "curs.a", "curs.b", "tool"],
    compare: ["tool", "cmp.mask", "cmp.math", "trig.edge"],
  };
  const defaultTiles = (ws) => COMMON.concat(OWN[ws] || []);
  const SHORT = { "Stop on fail": "Stop", "Hex+String": "Both", "Cursor A": "A", "Cursor B": "B", Positive: "Pos", Negative: "Neg" };
  const short = (t) => SHORT[t] || t;
  const JOB = { peak: "Peak marker", point: "Bode point", row: "Compare row", threshold: "Eye threshold", packet: "Packet" };
  const cap = (s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : "");
  const jobName = (j) => JOB[j] || cap(j || "");
  // the menu keys printed on the panel: each opens its tab here, never on the scope
  const MENU_KEYS = [["menu", "Menu On/Off", "", ""], ["trigmenu", "Trig Menu", "trig", ""], ["horizmenu", "Hori Menu", "time", ""],
    ["ch1", "1", "ch1", ""], ["ch2", "2", "ch2", ""], ["math", "Math", "cmp", "cmp.math"], ["ref", "Ref", "cmp", "cmp.store_a"],
    ["acquire", "Acquire", "acq", ""], ["display", "Display", "view", ""], ["measure", "Measure", "meas", ""], ["cursors", "Cursors", "curs", ""],
    ["utility", "Utility", "bus", ""], ["saverecall", "Save/Recall", "file", ""], ["help", "Help", "sys", "sys.help"]];
  // TRLC-LINKS: REQ-SDS-220
  function accent(id) {
    return /^ch1\./.test(id) ? "c1" : /^ch2\./.test(id) ? "c2" : /^(trig\.|bus\.match|bus\.trigger_on|bus\.i2c)/.test(id) ? "trigger"
      : /^time\./.test(id) ? "focus" : /^cmp\./.test(id) ? "math" : "on-fg";
  }

  // ---- reads ----------------------------------------------------------------------------------

  const row = (id) => RN.row(id);
  const panel = () => RN.S.panel || {};
  const wsNow = () => panel().WorkspaceID || "scope";
  const wsName = () => WS_BY[wsNow()].name;
  const tabs = () => RN.S.model.tabs;
  const tabInfo = (id) => tabs().find((t) => t.id === id) || null;
  const tabName = (id) => { const t = tabInfo(id); return t ? t.name : String(id || "").toUpperCase(); };
  // the LCD has a menu (sheet, picker, editor) open: the Adjust and Position knobs drive it there
  const scopeMenu = () => !!panel().Mode && panel().Mode !== "workspace";
  const tools = () => RC.tools(panel().Tool);
  const lit = (name) => (panel().LampsLit || []).indexOf(name) >= 0;
  const disp = (t) => RC.pretty(t);

  // TRLC-LINKS: REQ-SDS-220
  function labelFor(id) {
    if (id === "tool") return wsName() + " tools";
    const r = row(id);
    return r ? RC.label(r) : id;
  }

  // ---- binders: run on every change while their element is shown --------------------------

  const binds = new Set();
  // TRLC-LINKS: REQ-SDS-220
  function bind(el, fn) {
    const f = () => { if (!el.isConnected) { binds.delete(f); return; } fn(); };
    binds.add(f);
    fn();
    return f;
  }
  // TRLC-LINKS: REQ-SDS-220
  function refreshAll() {
    for (const f of Array.from(binds)) f();
    renderHome(false);
    renderBar();
    if (S.tab === "menus" && !S.page && !S.query.trim() && S.menuWs !== wsNow()) renderMenus();
    badge();
  }

  // ---- state and the phone's own settings -------------------------------------------------

  const S = { tab: "home", page: null, query: "", edit: false, sheet: null, menuWs: "" };
  // TRLC-LINKS: REQ-SDS-220
  function tilesOf(ws) {
    const v = load("tiles:" + ws, null), ids = Array.isArray(v) ? v : defaultTiles(ws);
    const have = RN.S.model.rows;
    return ids.filter((id, i) => typeof id === "string" && ids.indexOf(id) === i && (id === "tool" || !have.size || have.has(id)));
  }
  const saveTiles = (ws, ids) => store("tiles:" + ws, ids);
  const isPinned = (id) => tilesOf(wsNow()).indexOf(id) >= 0;

  // ---- changing the scope: through RN only; the answers speak ---------------------------------

  // TRLC-LINKS: REQ-SDS-220
  function answer(res, okText) {
    if (res && res.ok) { if (okText) toast(okText, "ok"); return res; }
    if (res && !res.end) { haptic("warn"); toast((res && res.error) || "The scope refused that", "err"); }
    return res || { ok: false };
  }
  const setRow = (id, input, okText) => RN.set(id, input).then((r) => answer(r, okText && r.ok ? okText(r) : ""));
  const stepRow = (id, cw) => RN.step(id, cw).then((r) => answer(r));
  // TRLC-LINKS: REQ-SDS-220
  function actRow(id, act, okText) {
    const r = row(id), a = r && (r.Actions || []).find((x) => x.ID === act);
    const label = (r ? tabName(r.tab) + " " + DOT + " " + RC.label(r) : id) + (a ? ": " + a.Text : "");
    const undoable = id === "sys.autoset" || id === "sys.default" || (RC.isSlot(r) && act === "recall");
    return RN.action(id, act, { label: id === "sys.autoset" ? "Autoset" : id === "sys.default" ? "Default setup" : label, undoable },
      { autoset: id === "sys.autoset" }).then((res) => answer(res, res.ok ? okText || "" : ""));
  }
  // TRLC-LINKS: REQ-SDS-220
  function pressKey(button, okText, label) {
    return RN.press(button, label ? { label } : {}).then((r) => answer(r, okText));
  }
  // TRLC-LINKS: REQ-SDS-220
  function turnKnob(name, dir) { return RN.knob(name, dir).then((r) => answer(r)); }

  // ---- the skeleton: bar, banner, views, run dock, tab bar, one sheet --------------------------

  const app = document.getElementById("app");
  const bar = h("header", { class: "bar", id: "bar" });
  const banner = h("div", { class: "banner", id: "banner", role: "alert", hidden: true });
  const views = h("main", { class: "views" });
  const V = {};
  for (const k of ["home", "menus", "hist", "keys"]) {
    V[k] = h("section", { class: "view scroll", id: "view-" + k, data: { view: k }, hidden: k !== "home", role: "tabpanel", "aria-labelledby": "tab-" + k });
    views.appendChild(V[k]);
  }
  const dock = h("div", { class: "dock", role: "group", "aria-label": "Run control" });
  const tabbar = h("nav", { class: "tabbar", role: "tablist", "aria-label": "Sections" });
  app.append(bar, banner, views, h("div", { class: "bottom" }, dock, tabbar));
  const sh = RU.sheet({ onClose: () => { S.sheet = null; sh.body.textContent = ""; KP.active = null; } });

  // the connection banner
  // TRLC-LINKS: REQ-SDS-220
  function renderBanner() {
    const s = RN.S;
    banner.hidden = s.online;
    if (!s.online) {
      banner.textContent = "";
      banner.append(h("span", { class: "dot", "aria-hidden": "true" }), s.ever ? "Connection lost - retrying" : "Connecting to the scope…");
    }
    document.body.classList.toggle("offline", !s.online);
  }

  // ---- the bar ------------------------------------------------------------------------------

  const ttlH = h("h1"), ttlP = h("p"), ttl = h("div", { class: "ttl" }, ttlH, ttlP);
  const awakeB = button({ icon: "eye", cls: "ghost sq ib", aria: "Keep the phone awake", onTap: () => toggleAwake() });
  awakeB.id = "awake";
  const fsB = button({ icon: "expand", cls: "ghost sq ib", aria: "Fullscreen", onTap: () => toggleFs() });
  fsB.id = "fs";
  const backB = button({ icon: "back", label: "Menus", cls: "ghost back", aria: "Back to Menus", onTap: () => goBack() });
  backB.id = "back";
  const doneB = button({ label: "Done", cls: "primary", onTap: () => exitEdit() });
  doneB.id = "edit-done";
  const resetB = button({ label: "Reset", cls: "ghost", aria: "Back to the default tiles", onTap: () => resetTiles() });
  const wsSeg = h("div", { class: "wss", role: "group", "aria-label": "Workspaces (tap the active one again: its next view)" });
  const wsAgainT = h("span", { id: "again" }), wsAgain = h("div", { class: "wsa", "aria-hidden": "true" }, wsAgainT);
  const wsWrap = h("div", { class: "wsw" }, wsSeg, wsAgain);
  const wsBtns = {};
  for (const w of WS) {
    const b = h("button", { type: "button", class: "wsb", "aria-pressed": "false", "aria-label": w.name, data: { ws: w.id } });
    b.innerHTML = icon(w.icon, 20);
    b.appendChild(h("span", { class: "wst", text: w.tag }));
    press(b, { onTap: () => goWs(w.id) });
    wsBtns[w.id] = b;
    wsSeg.appendChild(b);
  }
  // TRLC-LINKS: REQ-SDS-220
  function renderChips() {
    const P = panel(), cur = wsNow(), next = P.AgainNext || "";
    for (const w of WS) {
      const on = cur === w.id, b = wsBtns[w.id];
      b.setAttribute("aria-pressed", on ? "true" : "false");
      b.setAttribute("aria-label", w.name + (on ? " (now " + (P.View || "") + (next ? "; tap again: " + next : "") + ")" : ""));
    }
    const i = WS.findIndex((w) => w.id === cur), t = next ? "again: " + next : "";
    if (wsAgainT.textContent !== t) wsAgainT.textContent = t;
    wsAgainT.style.gridColumn = (i <= 2 ? i + 1 : i - 1) + " / span 3";
    wsAgainT.style.textAlign = i <= 2 ? "left" : "right";
  }
  // TRLC-LINKS: REQ-SDS-220
  function renderPhoneButtons() {
    const wk = Wake.state(), fs = Full.state();
    awakeB.classList.toggle("on", wk.on);
    awakeB.classList.toggle("bad", !wk.on && !!wk.note);
    awakeB.setAttribute("aria-pressed", wk.on ? "true" : "false");
    awakeB.setAttribute("aria-label", "Keep the phone awake: " + wk.text);
    fsB.classList.toggle("on", fs.on);
    fsB.classList.toggle("bad", !fs.can && !fs.on);
    fsB.setAttribute("aria-pressed", fs.on ? "true" : "false");
    fsB.setAttribute("aria-label", "Fullscreen: " + fs.text);
  }
  // TRLC-LINKS: REQ-SDS-220
  function setTitle(a, b) { if (ttlH.textContent !== a) ttlH.textContent = a; if (ttlP.textContent !== (b || "")) ttlP.textContent = b || ""; }
  let barSig = "";
  // TRLC-LINKS: REQ-SDS-220
  function renderBar() {
    renderChips();
    renderPhoneButtons();
    const n = RN.S.history.list.length;
    const sig = S.tab + "|" + (S.edit ? 1 : 0) + "|" + (S.page || "") + "|" + wsNow() + "|" + n;
    if (sig === barSig) return;
    barSig = sig;
    bar.textContent = "";
    bar.className = "bar";
    if (S.tab === "home" && S.edit) {
      setTitle("Edit tiles", wsName() + " Home " + DOT + " " + LQ + " " + RQ + " move, " + TIMES + " remove");
      bar.append(ttl, resetB, doneB);
      return;
    }
    if (S.tab === "home") { bar.classList.add("home"); bar.append(wsWrap, awakeB, fsB); return; }
    if (S.tab === "menus" && S.page) {
      const t = tabInfo(S.page);
      setTitle(t ? t.name : S.page, t ? t.title + ((OWN_TABS[wsNow()] || []).indexOf(S.page) >= 0 ? " " + DOT + " " + wsName() : "") : "");
      bar.append(backB, ttl, awakeB, fsB);
      return;
    }
    if (S.tab === "menus") setTitle("Menus", "Every setting " + DOT + " long press to pin it to Home");
    else if (S.tab === "hist") setTitle("History", n ? n + (n === 1 ? " change" : " changes") + ", kept on this phone" : "Every change, newest first");
    else setTitle("Keys", "Pushes, keys and the phone");
    bar.append(ttl, awakeB, fsB);
  }

  // ---- the run dock ---------------------------------------------------------------------------

  const runB = button({ label: "Run/Stop", sub: "running", cls: "dk", aria: "Run/Stop", onTap: () => pressKey("runstop") });
  runB.id = "runstop";
  const singleB = button({ label: "Single", sub: "one shot", cls: "dk", aria: "Single", onTap: () => { singleSeen = false; pressKey("single"); } });
  singleB.id = "single";
  const autoB = button({ label: "Auto", sub: "autoset", cls: "dk primary", aria: "Auto (autoset)", onTap: () => actRow("sys.autoset", "autoset", "Autoset") });
  autoB.id = "auto";
  dock.append(runB, singleB, autoB);
  let singleSeen = false;
  // TRLC-LINKS: REQ-SDS-220
  function renderDock() {
    const run = lit("RUN"), stop = lit("STOP"), armed = lit("SINGLE");
    runB.classList.toggle("run", run && !stop);
    runB.classList.toggle("stop", stop && !run);
    runB.classList.toggle("warn", run && stop);
    runB.setSub(run && stop ? "waiting" : run ? "running" : stop ? "stopped" : "—");
    if (armed) singleSeen = true;
    singleB.classList.toggle("warn", armed);
    singleB.setSub(armed ? "armed" : singleSeen && stop ? "done" : "one shot");
    autoB.setSub(panel().AutosetBusy ? "busy…" : "autoset");
  }

  // ---- the tab bar ----------------------------------------------------------------------------

  const tabBtns = {};
  for (const [id, label, ic] of [["home", "Home", "home"], ["menus", "Menus", "sliders"], ["hist", "History", "clock"], ["keys", "Keys", "keypad"]]) {
    const b = h("button", { type: "button", class: "tab", id: "tab-" + id, role: "tab", "aria-selected": id === "home" ? "true" : "false",
      "aria-controls": "view-" + id, data: { tabbar: id } });
    b.innerHTML = icon(ic, 24);
    b.appendChild(h("span", { text: label }));
    press(b, { onTap: () => goTab(id) });
    tabBtns[id] = b;
    tabbar.appendChild(b);
  }
  const histBadge = h("span", { class: "tbadge", id: "hist-badge", "aria-hidden": "true", hidden: true });
  tabBtns.hist.appendChild(histBadge);
  // TRLC-LINKS: REQ-SDS-220
  function goTab(id) {
    if (S.edit) { S.edit = false; renderHome(true); }
    if (id === S.tab) {
      if (id === "menus" && (S.page || S.query)) { S.page = null; S.query = ""; searchIn.value = ""; renderMenus(); }
      V[id].scrollTop = 0;
      renderBar();
      return;
    }
    S.tab = id;
    for (const k of Object.keys(V)) V[k].hidden = k !== id;
    for (const k of Object.keys(tabBtns)) tabBtns[k].setAttribute("aria-selected", k === id ? "true" : "false");
    if (id === "menus") renderMenus();
    if (id === "home") renderHome(true);
    if (id === "hist") renderHist();
    renderBar();
  }

  // =================================================================================================
  // Home: the tile grid
  // =================================================================================================

  let homeSig = "";
  const editLink = button({ icon: "pencil", label: "Edit tiles", cls: "ghost", aria: "Edit the tiles (or long press a tile)", onTap: () => enterEdit() });
  editLink.id = "edit-tiles";
  const addTileB = button({ icon: "plus", label: "Add a tile", onTap: () => openAdd() });
  addTileB.id = "add-tile";
  // TRLC-LINKS: REQ-SDS-220
  function renderHome(force) {
    if (!RN.S.model.rows.size) return;
    const ws = wsNow(), ids = tilesOf(ws), sig = ws + "|" + ids.join(",") + "|" + (S.edit ? 1 : 0);
    if (!force && sig === homeSig) return;
    homeSig = sig;
    V.home.textContent = "";
    const grid = h("div", { class: "grid" + (S.edit ? " editing" : ""), role: "list", "aria-label": WS_BY[ws].name + " tiles" });
    ids.forEach((id, i) => grid.appendChild(tile(id, i, ids.length)));
    V.home.append(grid, h("div", { class: "more" }, S.edit ? addTileB : editLink));
    for (const f of Array.from(binds)) f();   // the quick buttons fit their labels now that they are laid out
  }
  // TRLC-LINKS: REQ-SDS-220
  function tile(id, i, n) {
    const el = h("div", { class: "tile", role: "listitem", data: { tile: id }, style: "--acc:var(--" + (id === "tool" ? "on-fg" : accent(id)) + ")" });
    const lbl = h("span", { class: "tl" }), val = h("span", { class: "tv" });
    const main = h("button", { type: "button", class: "tmain" }, lbl, val);
    press(main, {
      onTap: () => {
        if (S.edit) { toast("Editing the tiles: " + LQ + " " + RQ + " move, " + TIMES + " remove; Done to finish"); return; }
        if (id === "tool") openTools(); else openEditor(id);
      },
      onLong: () => { if (!S.edit) enterEdit(); },
    });
    el.appendChild(main);
    el.appendChild(S.edit ? editBar(id, i, n) : id === "tool" ? toolQuick() : quick(id));
    bind(el, () => {
      let label, text, off = false;
      if (id === "tool") {
        const t = tools();
        label = wsName() + " " + DOT + " Adjust";
        text = scopeMenu() ? "scope menu open" : jobName(t.adjust.job) || "—";
      } else {
        const r = row(id);
        if (!r) return;
        label = labelFor(id); text = disp(r.Text); off = !r.Available;
        el.classList.toggle("pending", !!r.pending);
      }
      if (lbl.textContent !== label) lbl.textContent = label;
      if (val.textContent !== text) val.textContent = text;
      el.classList.toggle("off", off);
      el.classList.toggle("long", text.length > 10);
      main.setAttribute("aria-label", label + ": " + text + (S.edit ? "" : " (open)"));
    });
    return el;
  }

  // TRLC-LINKS: REQ-SDS-220
  function qbLabel(b, text, side) {
    const t = text === null ? "end" : RC.compact(text);
    if (!b.firstChild || !b.firstChild.classList || !b.firstChild.classList.contains("qc")) {
      b.textContent = "";
      const c = h("span", { class: "qc", "aria-hidden": "true", text: side < 0 ? LQ : RQ }), v = h("span", { class: "qv" });
      if (side < 0) b.append(c, v); else b.append(v, c);
    }
    const v = b.querySelector(".qv");
    if (v.dataset.full !== t) {
      // a value too wide for the button drops the space before its unit
      // ("-43.9mV"), then the chevron (the button's side says the way);
      // measured once the button is laid out
      v.textContent = t;
      b.classList.remove("tight");
      b.dataset.tight = b.dataset.squeeze = "";
      if (b.offsetParent) {
        v.dataset.full = t;
        if (v.scrollWidth > v.clientWidth + 1) {
          b.dataset.squeeze = "1";
          v.textContent = squeeze(t);
          if (v.scrollWidth > v.clientWidth + 1) b.dataset.tight = "1";
        }
      } else delete v.dataset.full;
    }
    b.disabled = text === null;
  }
  const squeeze = (t) => t.replace(/(\d) (?=[^\d\s])/, "$1");
  // a tile's two quick buttons share one form: both squeezed, both without chevrons
  // TRLC-LINKS: REQ-SDS-220
  function pairTight(a, b) {
    const t = a.dataset.tight === "1" || b.dataset.tight === "1", s = t || a.dataset.squeeze === "1" || b.dataset.squeeze === "1";
    for (const x of [a, b]) {
      const v = x.querySelector(".qv");
      if (v && v.dataset.full) { const want = s ? squeeze(v.dataset.full) : v.dataset.full; if (v.textContent !== want) v.textContent = want; }
      x.classList.toggle("tight", t);
    }
  }

  // the two quick buttons under a tile's value
  // TRLC-LINKS: REQ-SDS-220
  function quick(id) {
    const r0 = row(id), q = h("div", { class: "q" });
    const qb = (data, cls) => h("button", { type: "button", class: "qb" + (cls ? " " + cls : ""), data });
    if (!r0) return q;
    if (r0.Kind === "number") {
      const a = repeat(qb({ q: "ccw" }), () => stepRow(id, -1));
      const b = repeat(qb({ q: "cw" }), () => stepRow(id, 1));
      q.append(a, b);
      bind(q, () => {
        const r = row(id), lo = RC.turn(r, -1), hi = RC.turn(r, 1);
        qbLabel(a, lo ? lo.text : null, -1); qbLabel(b, hi ? hi.text : null, 1);
        pairTight(a, b);
        a.setAttribute("aria-label", labelFor(id) + " counter-clockwise" + (lo ? " to " + disp(lo.text) : ": at the end"));
        b.setAttribute("aria-label", labelFor(id) + " clockwise" + (hi ? " to " + disp(hi.text) : ": at the end"));
      });
    } else if (id === "trig.edge") {
      // the two things people flip: the source (C1/C2) and the slope (rise/fall)
      const a = qb({ q: "src" }, "sans"), b = qb({ q: "slope" }, "sans");
      press(a, { onTap: () => setRow(id, { value: row(id).Value ^ 2 }) });
      press(b, { onTap: () => setRow(id, { value: row(id).Value ^ 1 }) });
      q.append(a, b);
      bind(q, () => {
        const r = row(id), lab = (v) => ((r.Options || []).find((o) => o.Value === v) || { Text: String(v) }).Text;
        a.textContent = lab(r.Value ^ 2); b.textContent = lab(r.Value ^ 1);
        a.setAttribute("aria-label", "Trigger source: " + a.textContent); b.setAttribute("aria-label", "Trigger slope: " + b.textContent);
      });
    } else if (r0.Kind === "enum" && r0.Options.length <= 3 && r0.Options.every((o) => short(o.Text).length <= 6)) {
      q.setAttribute("role", "radiogroup");
      q.setAttribute("aria-label", labelFor(id));
      const bs = r0.Options.map((o) => {
        const b = qb({ value: String(o.Value) }, "sans");
        b.setAttribute("role", "radio");
        b.textContent = short(o.Text);
        press(b, { onTap: () => setRow(id, { value: o.Value }) });
        q.appendChild(b);
        return [b, o.Value];
      });
      bind(q, () => { const v = row(id).Value; for (const [b, ov] of bs) b.setAttribute("aria-checked", ov === v ? "true" : "false"); });
    } else if (r0.Kind === "enum") {
      const a = qb({ q: "ccw" }, "sans"), b = qb({ q: "cw" }, "sans");
      press(a, { onTap: () => { const o = RC.neighbour(row(id), -1); if (o) setRow(id, { value: o.value }); } });
      press(b, { onTap: () => { const o = RC.neighbour(row(id), 1); if (o) setRow(id, { value: o.value }); } });
      q.append(a, b);
      bind(q, () => {
        const p = RC.neighbour(row(id), -1), x = RC.neighbour(row(id), 1);
        qbLabel(a, p ? short(p.text) : null, -1); qbLabel(b, x ? short(x.text) : null, 1);
        pairTight(a, b);
        a.setAttribute("aria-label", labelFor(id) + (p ? ": " + p.text : ": first"));
        b.setAttribute("aria-label", labelFor(id) + (x ? ": " + x.text : ": last"));
      });
    } else if (r0.Kind === "bytes") {
      const a = qb({ q: "any" }, "sans"), b = qb({ q: "arm" }, "sans");
      a.textContent = "Any byte";
      press(a, { onTap: () => setRow(id, { text: "any" }, () => "Match: any byte") });
      press(b, { onTap: () => { const on = armed(); setRow("bus.trigger_on", { value: on ? 0 : 1 }, () => (on ? "Serial trigger off" : "Serial trigger on")); } });
      q.append(a, b);
      bind(q, () => { const on = armed(); b.textContent = on ? "Disarm" : "Arm"; b.classList.toggle("arm", on); b.setAttribute("aria-label", on ? "Serial trigger off" : "Serial trigger on"); });
    } else {
      const acts = (r0.Actions || []).slice(0, 2);
      acts.forEach((a) => {
        if (id === "sys.default") { q.appendChild(holdButton({ label: "Hold: " + a.Text, ms: 1000, onConfirm: () => actRow(id, a.ID, "Default setup restored") })); return; }
        const b = qb({ act: a.ID }, "sans");
        b.textContent = a.Text;
        press(b, { onTap: () => actRow(id, a.ID, labelFor(id) + ": " + a.Text) });
        q.appendChild(b);
        bind(b, () => { const x = (row(id).Actions || []).find((y) => y.ID === a.ID); b.classList.toggle("dimmed", !!x && !x.Available); });
      });
      if (!acts.length) q.appendChild(h("span", { class: "tl", text: "no actions" }));
    }
    return q;
  }
  const armed = () => { const r = row("bus.trigger_on"); return !!r && r.Value === 1; };

  // TRLC-LINKS: REQ-SDS-220
  function toolQuick() {
    const q = h("div", { class: "q" });
    const a = h("button", { type: "button", class: "qb sans", data: { q: "ccw" } }), b = h("button", { type: "button", class: "qb sans", data: { q: "cw" } });
    repeat(a, () => turnKnob("adjust", -1));
    repeat(b, () => turnKnob("adjust", 1));
    q.append(a, b);
    bind(q, () => {
      const t = tools(), w = RC.turnWords(t.adjust.job), off = scopeMenu() || !t.adjust.job;
      qbLabel(a, w[0], -1); qbLabel(b, w[1], 1);
      pairTight(a, b);
      a.disabled = b.disabled = off;
      a.setAttribute("aria-label", "Adjust " + (t.adjust.job || "") + " " + w[0]); b.setAttribute("aria-label", "Adjust " + (t.adjust.job || "") + " " + w[1]);
    });
    return q;
  }
  // TRLC-LINKS: REQ-SDS-220
  function editBar(id, i, n) {
    const q = h("div", { class: "q edq" }), lab = labelFor(id);
    const mk = (ic, aria, fn, dis) => { const b = button({ icon: ic, aria, cls: "sq", onTap: fn }); b.disabled = !!dis; return b; };
    q.append(mk("back", "Move " + lab + " earlier", () => moveTile(id, -1), i === 0), mk("close", "Remove " + lab, () => removeTile(id)),
      mk("chev", "Move " + lab + " later", () => moveTile(id, 1), i === n - 1));
    return q;
  }
  // TRLC-LINKS: REQ-SDS-220
  function enterEdit() {
    if (S.tab !== "home" || S.edit) return;
    S.edit = true;
    haptic("long");
    renderHome(true);
    renderBar();
  }
  // TRLC-LINKS: REQ-SDS-220
  function exitEdit() { S.edit = false; renderHome(true); renderBar(); }
  // TRLC-LINKS: REQ-SDS-220
  function moveTile(id, d) {
    const ws = wsNow(), ids = tilesOf(ws), i = ids.indexOf(id), j = i + d;
    if (i < 0 || j < 0 || j >= ids.length) return;
    ids.splice(i, 1); ids.splice(j, 0, id);
    saveTiles(ws, ids);
    renderHome(true);
  }
  // TRLC-LINKS: REQ-SDS-220
  function removeTile(id) {
    const ws = wsNow();
    saveTiles(ws, tilesOf(ws).filter((x) => x !== id));
    toast("Removed " + labelFor(id) + " (Add a tile brings it back)");
    renderHome(true);
  }
  // TRLC-LINKS: REQ-SDS-220
  function resetTiles() {
    store("tiles:" + wsNow(), undefined);
    toast("Default tiles for " + wsName(), "ok");
    renderHome(true);
  }
  // TRLC-LINKS: REQ-SDS-220
  function togglePin(id) {
    const ws = wsNow(), ids = tilesOf(ws), i = ids.indexOf(id);
    if (i >= 0) ids.splice(i, 1); else ids.push(id);
    saveTiles(ws, ids);
    haptic(i >= 0 ? "tap" : "confirm");
    toast((i >= 0 ? "Removed from " : "Pinned to ") + WS_BY[ws].name + " Home: " + labelFor(id), "ok");
    refreshAll();
  }

  // ---- workspaces ------------------------------------------------------------------------------

  // TRLC-LINKS: REQ-SDS-220
  function goWs(id) {
    if (wsNow() === id) { doAgain(); return; }
    RN.workspace(id).then((res) => answer(res, res.ok ? WS_BY[id].name + (res.notice ? ": " + res.notice : "") : ""));
  }
  // TRLC-LINKS: REQ-SDS-220
  function doAgain() {
    const name = wsName();
    RN.again().then((res) => answer(res, res.ok ? name + ": " + res.view : ""));
  }

  // what the Adjust push does here: on the scope, or, where it would open a
  // menu on the LCD (SCOPE: the Adjust row; BUS packets: the byte picker),
  // the same job on the phone
  // TRLC-LINKS: REQ-SDS-220
  function adjustPush() {
    const t = tools();
    if (/pick/i.test(t.adjust.push)) { openEditor("bus.match"); toast("Pick bytes here: type the match bytes", "ok"); return; }
    if (wsNow() === "scope" || /sheet/i.test(t.adjust.push)) {
      const job = String(t.adjust.job || "").toLowerCase();
      let found = null;
      for (const [id, r] of RN.S.model.rows) if (r.Kind === "number" && job && r.Label.toLowerCase() === job) { found = id; break; }
      if (found) openEditor(found); else toast("Adjust tunes the last value set in a sheet; nothing is set yet", "err");
      return;
    }
    pressKey("adjustpush");
  }

  // =================================================================================================
  // The bottom sheet: one setting, the tools, add a tile, help, a History entry
  // =================================================================================================

  const pinB = h("button", { type: "button", class: "btn ghost sq pin", "aria-label": "Pin to Home", "aria-pressed": "false", html: icon("pin") });
  press(pinB, { onTap: () => { if (S.sheet && S.sheet.pin) togglePin(S.sheet.pin); } });
  sh.tools.appendChild(pinB);
  bind(pinB, () => {
    const id = S.sheet && S.sheet.pin;
    pinB.hidden = !id;
    if (!id) return;
    const on = isPinned(id);
    pinB.setAttribute("aria-pressed", on ? "true" : "false");
    pinB.setAttribute("aria-label", (on ? "Unpin from " : "Pin to ") + wsName() + " Home");
  });
  // TRLC-LINKS: REQ-SDS-220
  function sheetOpen(kind, title, pin, acc) {
    S.sheet = { kind, pin: pin || null };
    KP.active = null;
    sh.body.textContent = "";
    sh.el.style.setProperty("--acc", "var(--" + (acc || "trigger") + ")");
    const box = h("div", { class: kind === "help" ? "help" : "ed", data: { sheet: kind } });
    sh.body.appendChild(box);
    sh.open(title);
    sh.body.scrollTop = 0;
    refreshAll();
    return box;
  }
  // TRLC-LINKS: REQ-SDS-220
  function finish(text) { haptic("confirm"); sh.close(); if (text) toast(text, "ok"); }

  // openEditor opens one setting's sheet.
  // TRLC-LINKS: REQ-SDS-220
  function openEditor(id) {
    const r = row(id);
    if (!r) return;
    const box = sheetOpen("row", labelFor(id), id, accent(id));
    box.dataset.editor = id;
    const sub = h("p", { class: "sub" });
    box.appendChild(sub);
    bind(sub, () => {
      const x = row(id);
      sub.textContent = "";
      const rng = x.Kind === "number" && x.Min != null && x.Max != null ? " " + DOT + " " + disp(RC.fmtValue(x, x.Min)) + " .. " + disp(RC.fmtValue(x, x.Max)) : "";
      sub.append(h("b", { text: tabName(x.tab) }), " " + DOT + " " + x.Label.replace(/\s+/g, " ") + (x.section ? " " + DOT + " " + x.section : "") + rng);
    });
    if (r.Kind === "number") numberEditor(id, box);
    else if (r.Kind === "enum") enumEditor(id, box);
    else if (r.Kind === "bytes") bytesEditor(id, box);
    else actionEditor(id, box);
    const why = h("p", { class: "why" }), help = h("p", { class: "sub", text: r.Help || "" });
    bind(why, () => { const x = row(id); why.textContent = x.Available ? "" : "Not in use now: needs " + (x.Why || "another setting") + "."; why.hidden = x.Available; });
    const all = button({ label: "All " + tabName(r.tab) + " settings", cls: "ghost", onTap: () => { sh.close(); goTab("menus"); openPage(r.tab, id); } });
    all.insertAdjacentHTML("beforeend", icon("chev", 18));
    all.dataset.all = r.tab;
    box.append(why, help, all);
  }

  // ---- numbers: steppers (hold repeats), a drag strip, presets, a keypad with units ----------

  // TRLC-LINKS: REQ-SDS-220
  function numberEditor(id, box) {
    const r0 = row(id);
    const side = (q) => {
      const b = h("button", { type: "button", class: "btn sb", data: { q } }, h("span", { class: "ch", text: q === "ccw" ? LQ : RQ }), h("span", { class: "sbv" }));
      return repeat(b, () => { stepRow(id, q === "ccw" ? -1 : 1); KP.reset(); });
    };
    const a = side("ccw"), b = side("cw");
    const dv = h("div", { class: "dv", "aria-live": "polite" }), msg = h("div", { class: "msg", id: "kp-msg", "aria-live": "polite" });
    // the controls, and the keypad beside them on a short landscape screen
    const left = h("div", { class: "ncol" }), cols = h("div", { class: "ncols" }, left);
    box.appendChild(cols);
    left.appendChild(h("div", { class: "stp" }, a, h("div", { class: "disp" }, dv), b));
    left.appendChild(msg);
    bind(a, () => {
      const r = row(id), lo = RC.turn(r, -1), hi = RC.turn(r, 1);
      a.querySelector(".sbv").textContent = lo ? RC.compact(lo.text) : "end";
      b.querySelector(".sbv").textContent = hi ? RC.compact(hi.text) : "end";
      a.disabled = !lo; b.disabled = !hi;
      a.setAttribute("aria-label", "Counter-clockwise" + (lo ? " to " + disp(lo.text) : ": at the end") + " (hold to repeat)");
      b.setAttribute("aria-label", "Clockwise" + (hi ? " to " + disp(hi.text) : ": at the end") + " (hold to repeat)");
    });
    left.appendChild(dragStrip(id));
    const pre = h("div", { class: "pre" });
    for (const p of r0.Presets || []) {
      const pb = button({ label: p, onTap: () => RN.preset(id, p).then((res) => { if (answer(res).ok) finish(labelFor(id) + ": " + disp(res.text)); }) });
      pb.dataset.preset = p;
      pre.appendChild(pb);
      bind(pb, () => pb.classList.toggle("on", disp(row(id).Text) === disp(p.replace(/\s+/g, ""))));
    }
    // the knob's printed push, where it belongs to this row
    if (id === "time.tdiv") {
      const zb = button({ label: "Zoom", onTap: () => pressKey("tdivpush") });
      zb.dataset.extra = "zoom";
      pre.appendChild(zb);
      bind(zb, () => { const z = row("time.zoom"); const on = !!z && z.Value !== 1; zb.classList.toggle("on", on); zb.setLabel(on ? "Zoom: " + z.Text : "Zoom: off"); });
    } else if (id === "trig.level") {
      const fb = button({ label: "Force", onTap: () => pressKey("force", "Forced a trigger") });
      fb.dataset.extra = "force";
      pre.appendChild(fb);
    }
    if (pre.children.length) left.appendChild(pre);
    cols.appendChild(numPad(id, dv, msg));
  }

  // the drag strip: 16 px per detent, right = clockwise; a tap on a half steps once
  // TRLC-LINKS: REQ-SDS-220
  function dragStrip(id) {
    const lt = h("span"), rt = h("span");
    const el = h("div", { class: "strip", role: "slider", tabindex: "0", data: { strip: id }, "aria-label": "Drag to change " + labelFor(id) + " (right = clockwise)" },
      lt, h("span", { class: "smid", text: "drag" }), rt);
    const STEP = 16;
    let did = 0, dragged = false;
    const turn = (n) => { for (let k = 0; k < Math.abs(n); k++) haptic("detent"); RN.step(id, Math.sign(n), Math.abs(n)).then((r) => answer(r)); KP.reset(); };
    drag(el, {
      buttons: true,
      onStart() { did = 0; dragged = true; el.classList.add("drag"); },
      onMove(d) {
        el.style.setProperty("--off", Math.round(d.dx) + "px");
        const n = Math.trunc(d.dx / STEP) - did;
        if (n) { did += n; turn(n); }
      },
      onEnd() { el.classList.remove("drag"); },
    });
    el.addEventListener("click", (e) => {
      if (dragged) { dragged = false; return; }
      const r = el.getBoundingClientRect();
      turn(e.clientX < r.left + r.width / 2 ? -1 : 1);
    });
    el.addEventListener("pointerdown", () => { dragged = false; });
    el.addEventListener("keydown", (e) => {
      const d = e.key === "ArrowRight" || e.key === "ArrowUp" ? 1 : e.key === "ArrowLeft" || e.key === "ArrowDown" ? -1 : 0;
      if (d) { e.preventDefault(); turn(d); }
    });
    bind(el, () => {
      const r = row(id), lo = RC.turn(r, -1), hi = RC.turn(r, 1);
      lt.textContent = LQ + " " + (lo ? RC.compact(lo.text) : "end");
      rt.textContent = (hi ? RC.compact(hi.text) : "end") + " " + RQ;
      el.setAttribute("aria-valuetext", disp(r.Text));
    });
    return el;
  }

  // ---- keypads (numbers with units; hex bytes) ------------------------------------------------

  const KP = { active: null, reset() { if (KP.active && KP.active.reset) KP.active.reset(); } };
  // TRLC-LINKS: REQ-SDS-220
  function keyBtn(k, label, cls, place, onKey) {
    const b = h("button", { type: "button", class: "key" + (cls ? " " + cls : ""), data: { key: k }, "aria-label": label === k ? null : label, style: place }, label);
    press(b, { onTap: () => onKey(k) });
    return b;
  }
  const at = (col, rw, cs, rs) => "grid-column:" + col + " / span " + (cs || 1) + ";grid-row:" + rw + " / span " + (rs || 1);
  // TRLC-LINKS: REQ-SDS-220
  function numPad(id, dv, msg) {
    const units = RC.unitKeys(row(id));
    let buf = "";
    const keys = h("div", { class: "keys" });
    const el = h("div", { class: "keypad", data: { keypad: id } }, keys);
    const add = (k, label, cls, place) => keys.appendChild(keyBtn(k, label, cls, place, key));
    [["7", "8", "9"], ["4", "5", "6"], ["1", "2", "3"], ["-", "0", "."]].forEach((rw, ri) => rw.forEach((k, ci) => add(k, k === "-" ? PM : k, k === "-" ? "op" : "", at(ci + 1, ri + 1))));
    if (units.length >= 3) {
      units.slice(0, 4).forEach((u, i) => add(u, u, "unit", at(4, 1 + i)));
      add("back", "Del", "op", at(1, 5));
      add("clear", "Clear", "op", at(2, 5));
      add("enter", "Set", "enter", at(3, 5, 2));
    } else if (units.length) {
      units.forEach((u, i) => add(u, u, "unit", units.length === 1 ? at(4, 1, 1, 2) : at(4, 1 + i)));
      add("back", "Del", "op", at(4, 3));
      add("clear", "Clear", "op", at(4, 4));
      add("enter", "Set", "enter", at(1, 5, 4));
    } else {
      add("back", "Del", "op", at(4, 1, 1, 2));
      add("clear", "Clear", "op", at(4, 3, 1, 2));
      add("enter", "Set", "enter", at(1, 5, 4));
    }
    // TRLC-LINKS: REQ-SDS-220
    function show() {
      const r = row(id);
      if (!buf) {
        dv.textContent = disp(r.Text); dv.classList.remove("typed");
        dv.classList.toggle("long", dv.textContent.length > 9);
        msg.className = "msg"; msg.textContent = units.length ? "Type a value and a unit, then Set" : "Type a value, then Set";
        return;
      }
      dv.textContent = buf; dv.classList.add("typed");
      dv.classList.toggle("long", buf.length > 9);
      const p = RC.numberHint(r, buf);
      msg.className = "msg " + (p.ok ? "ok" : "err");
      msg.textContent = p.ok ? "= " + disp(p.text) + (p.note ? " (" + p.note + ")" : "") : p.error;
    }
    // TRLC-LINKS: REQ-SDS-220
    function key(k) {
      if (k === "enter") { apply(); return; }
      buf = RC.numKey(buf, k, units);
      show();
    }
    // TRLC-LINKS: REQ-SDS-220
    function apply() {
      if (!buf) { haptic("warn"); msg.className = "msg err"; msg.textContent = "Type a value first"; return; }
      const typed = buf;
      RN.set(id, { text: typed }).then((res) => {
        if (res.ok) { buf = ""; finish(labelFor(id) + ": " + disp(res.text)); return; }
        haptic("warn");
        msg.className = "msg err"; msg.textContent = res.error || "The scope refused " + typed;
        toast(res.error || "The scope refused " + typed, "err");
      });
    }
    KP.active = { el, key, reset() { buf = ""; show(); } };
    bind(el, () => { if (!buf) show(); });
    return el;
  }
  // a keyboard types into the keypad on show (desktop; a phone's own keyboard is the text field)
  document.addEventListener("keydown", (e) => {
    const kp = KP.active;
    if (!kp || !kp.el.isConnected || !sh.isOpen() || e.ctrlKey || e.metaKey || e.altKey) return;
    const t = e.target;
    if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA")) return;
    if (e.key === "Enter" && t && t.tagName === "BUTTON" && t !== document.body) return;   // the button's own Enter
    if (e.key === "Enter") { e.preventDefault(); kp.key("enter"); return; }
    if (e.key === "Backspace") { e.preventDefault(); kp.key("back"); return; }
    if (e.key.length === 1 && /[0-9a-fA-F.?\s-]/.test(e.key)) {
      e.preventDefault();
      kp.key(kp.hex ? (e.key === "?" ? "??" : e.key.toUpperCase()) : e.key);
    }
  });
  // a turned phone re-lays the Settings lists (segmented options inline or stacked)
  let rz = 0;
  window.addEventListener("resize", () => {
    clearTimeout(rz);
    rz = setTimeout(() => { if (S.tab === "menus") renderMenus(); if (S.tab === "home") renderHome(true); }, 150);
  });

  // ---- options: big buttons; a pick sets it and closes ---------------------------------------

  // TRLC-LINKS: REQ-SDS-220
  function enumEditor(id, box) {
    const r0 = row(id);
    const grid = h("div", { class: "opts", role: "radiogroup", "aria-label": labelFor(id) });
    const bs = (r0.Options || []).map((o) => {
      const b = h("button", { type: "button", class: "opt", role: "radio", "aria-checked": "false", data: { value: String(o.Value) }, html: icon("check", 20) });
      b.appendChild(h("span", { text: o.Text }));
      press(b, { onTap: () => RN.set(id, { value: o.Value }).then((res) => { if (answer(res).ok) finish(labelFor(id) + ": " + disp(res.text)); }) });
      grid.appendChild(b);
      return [b, o.Value];
    });
    bind(grid, () => { const v = row(id).Value; for (const [b, ov] of bs) b.setAttribute("aria-checked", ov === v ? "true" : "false"); });
    box.appendChild(grid);
    // a rate the options do not list: typed (the device takes any baud)
    if (id === "bus.baud") {
      const inp = h("input", { class: "field-input", type: "text", inputmode: "decimal", enterkeyhint: "done", autocomplete: "off", spellcheck: "false",
        placeholder: "Other rate, e.g. 250000", "aria-label": "Type another baud rate", id: "baud-typed" });
      const setB = button({ label: "Set", onTap: () => typedBaud() });
      const msg = h("div", { class: "msg", "aria-live": "polite" });
      // TRLC-LINKS: REQ-SDS-220
      const typedBaud = () => {
        const t = inp.value.trim();
        if (!t) { inp.focus(); return; }
        RN.set(id, { text: t }).then((res) => {
          if (res.ok) { finish("Baud: " + disp(res.text)); return; }
          msg.className = "msg err"; msg.textContent = res.error; haptic("warn");
        });
      };
      inp.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); typedBaud(); } });
      box.append(h("div", { class: "frow" }, inp, setB), msg);
    }
  }

  // ---- actions and setup slots: labelled buttons; only an explicit tap runs one -----------------

  // TRLC-LINKS: REQ-SDS-220
  function actionButtons(id) {
    const wrap = h("div", { class: "acts" });
    for (const a of row(id).Actions || []) {
      let b;
      if (id === "sys.default") {
        b = holdButton({ label: a.Text, sub: "hold 1 s", ms: 1000, icon: "power", onConfirm: () => actRow(id, a.ID).then((res) => { if (res.ok) finish("Default setup restored"); }) });
      } else {
        b = button({ label: a.Text, sub: slotHint(id, a), cls: "primary", onTap: () => actRow(id, a.ID).then((res) => { if (res.ok) finish(labelFor(id) + ": " + a.Text); }) });
      }
      b.dataset.action = a.ID;
      bind(b, () => { const x = (row(id).Actions || []).find((y) => y.ID === a.ID); b.classList.toggle("dimmed", !!x && !x.Available); });
      wrap.appendChild(b);
    }
    return wrap;
  }
  // TRLC-LINKS: REQ-SDS-220
  function slotHint(id, a) {
    if (!RC.isSlot(row(id))) return undefined;
    const used = row(id).Text !== "empty";
    return a.ID === "save" ? (used ? "overwrite" : "store here") : used ? "restore" : "empty";
  }
  // TRLC-LINKS: REQ-SDS-220
  function actionEditor(id, box) {
    const st = h("p", { class: "sub" });
    bind(st, () => {
      const r = row(id);
      st.textContent = RC.isSlot(r) ? (r.Text === "empty" ? "Empty" : "Stored: " + r.Text) : r.Text ? "Now: " + r.Text : "";
      st.hidden = !st.textContent;
    });
    box.append(st, actionButtons(id));
  }

  // ---- match bytes: a hex keypad or text, then Set or Trigger on it -----------------------------

  // TRLC-LINKS: REQ-SDS-220
  function bytesEditor(id, box) {
    let mode = "hex", buf = "", err = "";   // err: the scope's last refusal, shown until the next key
    const modeSeg = h("div", { class: "seg", role: "radiogroup", "aria-label": "Type the bytes as" });
    const mHex = h("button", { type: "button", role: "radio", "aria-checked": "true", data: { mode: "hex" } }, "Hex bytes");
    const mTxt = h("button", { type: "button", role: "radio", "aria-checked": "false", data: { mode: "text" } }, "Text");
    modeSeg.append(mHex, mTxt);
    const dv = h("div", { class: "dv" }), msg = h("div", { class: "msg", id: "match-msg", "aria-live": "polite" });
    const keys = h("div", { class: "keys" });
    const pad = h("div", { class: "keypad", data: { keypad: id } }, keys);
    const txt = h("input", { class: "field-input", type: "text", inputmode: "text", enterkeyhint: "done", autocomplete: "off", autocapitalize: "off",
      autocorrect: "off", spellcheck: "false", placeholder: "Hi", "aria-label": "Match text", id: "match-text" });
    const txtBox = h("div", { class: "field", hidden: true }, h("label", { class: "field-label", for: "match-text", text: "Text, sent byte for byte (\\r \\n \\xNN escapes)" }), txt);
    const entry = h("div", { class: "bybox" }, pad, txtBox);   // one height in both modes: nothing moves under the finger
    const add = (k, label, cls, place) => keys.appendChild(keyBtn(k, label, cls, place, key));
    [["7", "8", "9", "F"], ["4", "5", "6", "E"], ["1", "2", "3", "D"], ["0", "A", "B", "C"]].forEach((rw, ri) =>
      rw.forEach((k, ci) => add(k, k, /[A-F]/.test(k) ? "unit" : "", at(ci + 1, ri + 1))));
    add("??", "??", "unit", at(1, 5));
    add(" ", "Space", "op", at(2, 5));
    add("back", "Del", "op", at(3, 5));
    add("clear", "Clear", "op", at(4, 5));
    const typed = () => (mode === "hex" ? buf.trim() : RC.quoteText(txt.value));
    // TRLC-LINKS: REQ-SDS-220
    function show() {
      const r = row(id), now = disp(r.Text);
      if (mode === "hex") { dv.textContent = buf || now; dv.classList.toggle("typed", !!buf); dv.classList.toggle("long", (buf || now).length > 11); }
      else { dv.textContent = txt.value ? '"' + txt.value + '"' : now; dv.classList.toggle("typed", !!txt.value); dv.classList.toggle("long", dv.textContent.length > 11); }
      const t = typed();
      if (err) { msg.className = "msg err"; msg.textContent = err; return; }
      if (!t) { msg.className = "msg"; msg.textContent = (mode === "hex" ? "hex pairs, ?? = any byte" : "type the text") + " " + DOT + " up to " + RC.MATCH_MAX + " bytes"; return; }
      const p = RC.parseMatch(t);
      msg.className = "msg " + (p.ok ? "ok" : "err");
      msg.textContent = p.ok ? "= " + (p.tokens.join(" ") || "any byte") + " (" + p.tokens.length + (p.tokens.length === 1 ? " byte)" : " bytes)") : p.error;
    }
    // TRLC-LINKS: REQ-SDS-220
    function key(k) {
      err = "";
      if (k === "enter") { setOnly(); return; }
      buf = RC.hexKey(buf, k);
      show();
    }
    KP.active = { el: pad, key, hex: true, reset() { show(); } };
    // TRLC-LINKS: REQ-SDS-220
    const setMode = (m) => {
      mode = m; err = "";
      mHex.setAttribute("aria-checked", m === "hex" ? "true" : "false"); mTxt.setAttribute("aria-checked", m === "text" ? "true" : "false");
      pad.hidden = m !== "hex"; txtBox.hidden = m !== "text";
      KP.active = m === "hex" ? { el: pad, key, hex: true, reset() { show(); } } : null;
      show();
    };
    press(mHex, { onTap: () => setMode("hex") });
    press(mTxt, { onTap: () => { setMode("text"); txt.focus({ preventScroll: true }); } });
    // keep the focus in the text field (a tapped button would take it)
    for (const b of [mHex, mTxt]) b.addEventListener("mousedown", (e) => e.preventDefault());
    txt.addEventListener("input", () => { err = ""; show(); });
    txt.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); arm(); } });
    // TRLC-LINKS: REQ-SDS-220
    async function setMatch() {
      const t = typed();
      if (!t) return { ok: true, text: row(id).Text };
      const res = await RN.set(id, { text: t });
      if (!res.ok) { haptic("warn"); err = res.error; show(); toast(res.error, "err"); }
      return res;
    }
    // TRLC-LINKS: REQ-SDS-220
    async function setOnly() { const res = await setMatch(); if (res.ok) finish("Match bytes: " + disp(res.text)); }
    // TRLC-LINKS: REQ-SDS-220
    async function arm() {
      const res = await setMatch();
      if (!res.ok) return;
      const a = await RN.set("bus.trigger_on", { value: 1 });
      if (a.ok) { finish("Serial trigger on " + disp(row(id).Text)); return; }
      haptic("warn"); err = a.error; show(); toast(a.error, "err");
    }
    const anyB = button({ label: "Any byte", onTap: () => { buf = ""; txt.value = ""; setRow(id, { text: "any" }).then((res) => { if (res.ok) finish("Match: any byte"); }); } });
    anyB.dataset.act = "any";
    const setB = button({ label: "Set", onTap: () => setOnly() });
    setB.dataset.act = "set";
    const armB = button({ label: "Trigger on it", cls: "primary", onTap: () => arm() });
    armB.dataset.act = "arm";
    const st = h("p", { class: "sub bst" });
    bind(st, () => { const a = row("bus.trigger_on"); st.textContent = "Serial trigger: " + (a ? a.Text : "?"); });
    // portrait: mode, display, keys, buttons; a short landscape screen: the keys beside the rest
    box.appendChild(h("div", { class: "ncols bcols" },
      h("div", { class: "ncol" }, modeSeg, h("div", { class: "disp bydisp" }, dv, msg), h("div", { class: "k3 bya" }, anyB, setB, armB), st), entry));
    bind(dv, () => { if (!buf && !txt.value) show(); });
    setMode("hex");
  }

  // ---- the workspace's tools: Adjust and Position jobs with their pushes, Zoom, again ------------

  // TRLC-LINKS: REQ-SDS-220
  function openTools() {
    const box = sheetOpen("tools", wsName() + " tools", "tool", "on-fg");
    const sub = h("p", { class: "sub" });
    box.appendChild(sub);
    bind(sub, () => {
      const P = panel();
      sub.textContent = scopeMenu() ? "A menu is open on the scope: there Adjust and Position move its rows, so they wait here until it closes."
        : "The knob jobs of " + (P.Workspace || "") + " (" + (P.View || "") + "), as the scope's tool line says.";
    });
    box.appendChild(knobRows("tools"));
    const zb = button({ label: "Zoom", sub: "S/DIV push", onTap: () => pressKey("tdivpush") });
    zb.dataset.tool = "zoom";
    const ab = button({ label: "Again", sub: "", cls: "primary", onTap: () => doAgain() });
    ab.dataset.tool = "again";
    box.appendChild(h("div", { class: "k2" }, zb, ab));
    bind(zb, () => { const t = tools(); zb.setLabel(cap(t.zoom || "zoom")); ab.setLabel(wsName() + " again"); ab.setSub(panel().AgainNext || ""); });
  }
  // Adjust and Position as rows of [< prev] [push: job] [next >] (the tools sheet and Keys)
  // TRLC-LINKS: REQ-SDS-220
  function knobRows(cls) {
    const wrap = h("div", { class: "col " + (cls || "") });
    for (const [knob, name, pushBtn] of [["adjust", "Adjust", "adjustpush"], ["horizpos", "Position", "horizpospush"]]) {
      const head = h("h3", { text: name });
      const a = h("button", { type: "button", class: "btn", data: { knob, q: "ccw" } }), b = h("button", { type: "button", class: "btn", data: { knob, q: "cw" } });
      repeat(a, () => turnKnob(knob, -1));
      repeat(b, () => turnKnob(knob, 1));
      const p = button({ label: "push", sub: "", onTap: () => (knob === "adjust" ? adjustPush() : pressKey(pushBtn)) });
      p.dataset.knob = knob;
      p.dataset.push = "1";
      wrap.append(head, h("div", { class: "k3" }, a, p, b));
      bind(head, () => {
        const t = tools(), j = knob === "adjust" ? t.adjust : t.position, w = RC.turnWords(j.job || ""), off = scopeMenu();
        // an Adjust push that would open a menu on the LCD does its job here instead
        const here = knob === "adjust" && (/pick|sheet/i.test(j.push) || wsNow() === "scope");
        head.textContent = name + " knob: " + (off ? "the scope's menu" : j.job || "—");
        a.textContent = LQ + " " + w[0]; b.textContent = w[1] + " " + RQ;
        a.disabled = b.disabled = off || !j.job;
        p.disabled = off || !j.push;
        a.setAttribute("aria-label", name + " " + w[0]); b.setAttribute("aria-label", name + " " + w[1]);
        p.setLabel(here ? (/pick/i.test(j.push) ? "Pick bytes" : "Open it here") : cap(j.push || "push"));
        p.setSub(here ? "on the phone" : name + " push");
      });
    }
    return wrap;
  }

  // ---- add a tile (edit mode) -------------------------------------------------------------------

  // TRLC-LINKS: REQ-SDS-220
  function openAdd() {
    const ws = wsNow(), box = sheetOpen("add", "Add a tile to " + WS_BY[ws].name, null, "on-fg");
    const inp = h("input", { class: "field-input", type: "search", inputmode: "search", enterkeyhint: "search", placeholder: "Find a setting",
      autocomplete: "off", autocapitalize: "off", autocorrect: "off", spellcheck: "false", "aria-label": "Find a setting to add", id: "add-find" });
    const list = h("div", { class: "list" });
    box.append(h("div", { class: "sbox" }, h("span", { html: icon("search", 20) }), inp), list);
    const draw = () => {
      list.textContent = "";
      const have = tilesOf(ws), q = inp.value.trim();
      const all = [];
      for (const t of tabs()) for (const id of t.rows) all.push(id);
      const ids = (q ? RC.search(RN.S.model, q, 80).map((x) => x.id) : all).filter((id) => have.indexOf(id) < 0 && id !== "acq.run");
      if (!q && have.indexOf("tool") < 0 && ws !== "scope") ids.unshift("tool");
      for (const id of ids.slice(0, 80)) {
        const r = id === "tool" ? null : row(id);
        const b = h("button", { type: "button", class: "cell drill", data: { add: id } },
          h("span", { class: "cl" }, h("b", { text: labelFor(id) }), h("small", { text: r ? tabName(r.tab) + " " + DOT + " " + r.Label.replace(/\s+/g, " ") : "Adjust and Position jobs of " + WS_BY[ws].name })),
          h("span", { class: "cv", text: r ? disp(r.Text) : "" }));
        b.insertAdjacentHTML("beforeend", icon("plus", 18));
        press(b, { onTap: () => { const now = tilesOf(ws); now.push(id); saveTiles(ws, now); finish("Added " + labelFor(id)); renderHome(true); } });
        list.appendChild(b);
      }
      if (!list.children.length) list.appendChild(h("p", { class: "empty", text: 'Nothing to add for "' + q + '".' }));
    };
    inp.addEventListener("input", draw);
    draw();
  }

  // ---- help -----------------------------------------------------------------------------------------

  // TRLC-LINKS: REQ-SDS-220
  function openHelp() {
    const box = sheetOpen("help", "How this remote works", null, "on-fg");
    const P = (t) => h("p", { text: t });
    box.append(
      h("h3", { text: "Home" }),
      P("Each tile is one setting with its value. Tap the tile to open it: options, or steppers, a drag strip and a keypad with units. The two buttons under the value go one step either way and say where they go; hold one to keep stepping. Right is clockwise, as on the scope's knobs, so for time/div and V/div right goes toward ns and mV."),
      P("The top two rows are the same in every workspace (Time/div, Trigger level, CH1 and CH2 V/div); the rows below belong to the workspace. Long press a tile (or Edit tiles) to move, remove or add tiles; each workspace keeps its own."),
      h("h3", { text: "Workspaces" }),
      P("The chips at the top of Home switch SCOPE, BUS, SPECTRUM, MEASURE and COMPARE, even while the scope shows a menu: its menu stays as it is. Tap the active chip again for its next view, as its F-key does on the scope; the line under it says which."),
      h("h3", { text: "Menus" }),
      P("Every tab and row of the scope's sheets, changed here and not on the scope; the scope confirms each change with a short REMOTE line. Search finds a setting by its name or by another word (holdoff, coupling, baud, averaging, screenshot). Long press a row to pin it to Home."),
      h("h3", { text: "History" }),
      P("Every change of a setting, made here or on the scope (its knobs, keys and menus), newest first, with where it came from. Undo puts the old value back, and the undo is itself a change you can undo. Steps on one setting in a row make one entry. Tap an entry for its details and to go back to before it (a 1 s hold). History is kept on this phone."),
      h("h3", { text: "Keys" }),
      P("Force, Set to 50 %, Print, the knob pushes (Zero, Zoom, Variable), the Adjust and Position jobs, the menu keys (they open the menu here) and Default Setup (hold it for 1 s)."),
      h("h3", { text: "The phone" }),
      P("The eye keeps the screen on: with a wake lock where the browser has one, else with a tiny silent video (the scope serves plain HTTP, where browsers have no wake lock). The corner button goes fullscreen on Android. An iPhone has no fullscreen for pages: Share, Add to Home Screen opens the remote without browser bars."));
    const sys = button({ label: "The scope's own help pages (SYS)", cls: "ghost", onTap: () => { sh.close(); goTab("menus"); openPage("sys"); } });
    sys.dataset.sys = "1";
    box.appendChild(sys);
  }

  // =================================================================================================
  // Menus: search, the tabs as a grid, then each tab as a Settings list
  // =================================================================================================

  const searchIn = h("input", { class: "field-input", type: "search", inputmode: "search", enterkeyhint: "search", placeholder: "Search settings",
    autocomplete: "off", autocapitalize: "off", autocorrect: "off", spellcheck: "false", "aria-label": "Search settings", id: "search" });
  const clearQ = button({ icon: "close", cls: "ghost sq", aria: "Clear the search", onTap: () => { searchIn.value = ""; S.query = ""; renderMenus(); } });
  const srow = h("div", { class: "srch" }, h("div", { class: "sbox" }, h("span", { html: icon("search", 20) }), searchIn), clearQ);
  const mBody = h("div", { class: "mbody" });
  V.menus.append(srow, mBody);
  searchIn.addEventListener("input", () => { S.query = searchIn.value; renderMenus(); });
  searchIn.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); searchIn.blur(); } });
  // the phone's keyboard covers the lower half: keep the results above it
  if (window.visualViewport) {
    const vv = window.visualViewport;
    const fit = () => document.documentElement.style.setProperty("--kb", Math.max(0, window.innerHeight - vv.height - vv.offsetTop) + "px");
    vv.addEventListener("resize", fit);
    vv.addEventListener("scroll", fit);
  }

  // TRLC-LINKS: REQ-SDS-220
  function renderMenus() {
    S.menuWs = wsNow();
    srow.hidden = !!S.page;
    clearQ.hidden = !S.query;
    mBody.textContent = "";
    if (!RN.S.model.rows.size) return;
    if (S.page) renderPage(S.page);
    else if (S.query.trim()) renderResults(S.query);
    else renderRoot();
  }
  // TRLC-LINKS: REQ-SDS-220
  function renderRoot() {
    const own = (OWN_TABS[wsNow()] || []).filter((t) => tabInfo(t)), rest = tabs().map((t) => t.id).filter((t) => own.indexOf(t) < 0);
    const grid = (ids, mine) => {
      const g = h("div", { class: "tabs" });
      for (const tid of ids) {
        const t = tabInfo(tid);
        const b = h("button", { type: "button", class: "tabc" + (mine ? " own" : ""), data: { tab: tid }, "aria-label": t.name + ": " + t.title }, h("b", { text: t.name }), h("span", { text: t.title }));
        press(b, { onTap: () => openPage(tid) });
        g.appendChild(b);
      }
      return g;
    };
    mBody.append(h("div", { class: "sec", text: wsName() + " menus" }), grid(own, true), h("div", { class: "sec", text: "More menus" }), grid(rest, false));
  }
  // TRLC-LINKS: REQ-SDS-220
  function openPage(tid, focusRow) {
    S.page = tid;
    if (S.tab !== "menus") goTab("menus");
    renderMenus();
    renderBar();
    V.menus.scrollTop = 0;
    if (focusRow) {
      const c = mBody.querySelector('[data-row="' + focusRow + '"]');
      if (c) {
        c.scrollIntoView({ block: "center" });
        c.classList.add("flash");
        setTimeout(() => c.classList.remove("flash"), 900);
      }
    }
  }
  // TRLC-LINKS: REQ-SDS-220
  function goBack() { S.page = null; renderMenus(); renderBar(); V.menus.scrollTop = 0; }
  // TRLC-LINKS: REQ-SDS-220
  function renderPage(tid) {
    const t = tabInfo(tid);
    if (!t) { S.page = null; renderRoot(); return; }
    const list = h("div", { class: "list", role: "list", "aria-label": t.title });
    for (const id of t.rows) {
      const r = row(id);
      if (r.sectionHead) list.appendChild(h("div", { class: "sec" }, r.section, r.sectionNote ? h("span", { class: "secn", text: r.sectionNote }) : null));
      list.appendChild(cell(id));
    }
    mBody.appendChild(list);
  }

  // ---- search: settings (with everyday words), then places --------------------------------------

  const PLACES = WS.map((w) => ({ id: "ws:" + w.id, icon: w.icon, label: w.name + " workspace", sub: w.what, words: w.words, run: () => goWs(w.id) })).concat([
    { id: "key:set50", icon: "zero", label: "Set to 50 %", sub: "Trigger key: the level to the middle of the signal", words: "set 50 % percent level middle half", run: () => pressKey("set50", "Level set to 50 %", "Set to 50 %") },
    { id: "key:zoom", icon: "expand", label: "Zoom (S/DIV push)", sub: "Zoom on or off around the trigger", words: "zoom push s/div magnify", run: () => pressKey("tdivpush") },
    { id: "key:tools", icon: "dial", label: "Workspace tools", sub: "Adjust and Position jobs and their pushes", words: "adjust position knob tools push statistics zero peak packet cursor row", run: () => openTools() },
    { id: "history", icon: "clock", label: "History", sub: "Every change, newest first, with undo", words: "history undo changes changed log recent back", run: () => goTab("hist") },
    { id: "help", icon: "hand", label: "Help: how this remote works", sub: "Tiles, sheets, search, pins", words: "help guide how tiles pin search remote", run: () => openHelp() },
    { id: "awake", icon: "eye", label: "Keep the phone awake", sub: "Wake lock, or a silent video on plain HTTP", words: "awake wake lock sleep screen dim", run: () => toggleAwake() },
    { id: "fs", icon: "expand", label: "Fullscreen", sub: "Hide the browser bars", words: "fullscreen full screen browser bars home", run: () => toggleFs() },
  ]);
  // TRLC-LINKS: REQ-SDS-220
  function renderResults(q) {
    const places = PLACES.filter((p) => RC.matchPlace(p, q));
    const found = RC.search(RN.S.model, q, 40);
    if (!places.length && !found.length) {
      mBody.appendChild(h("p", { class: "empty", text: 'Nothing found for "' + q.trim() + '". Try another word: holdoff, coupling, baud, average, cursor, setup.' }));
      return;
    }
    const list = h("div", { class: "list", role: "list", "aria-label": "Search results" });
    if (found.length) list.appendChild(h("div", { class: "sec", text: found.length + (found.length === 1 ? " setting" : " settings") }));
    for (const x of found.slice(0, 30)) list.appendChild(cell(x.id, { badge: true }));
    if (places.length) list.appendChild(h("div", { class: "sec", text: "Workspaces and keys" }));
    for (const p of places.slice(0, 6)) {
      const b = h("button", { type: "button", class: "cell drill place", data: { place: p.id } },
        h("span", { class: "pi", html: icon(p.icon, 22) }), h("span", { class: "cl" }, h("b", { text: p.label }), h("small", { text: p.sub })));
      b.insertAdjacentHTML("beforeend", icon("chev", 18));
      press(b, { onTap: () => p.run() });
      list.appendChild(b);
    }
    mBody.appendChild(list);
  }

  // ---- one row as a Settings cell: switch, segmented options, stepper, drill-in or buttons ------

  const textW = (s, px) => String(s).length * px * 0.6;
  const segW = (opts, pad, min) => opts.reduce((a, o) => a + Math.max(min, textW(o.Text, 15) + pad), 0) + 4 * (opts.length - 1);
  // TRLC-LINKS: REQ-SDS-220
  function cellLabel(id, badge) {
    const b = h("b"), s = h("small");
    return { el: h("span", { class: "cl" }, b, s), b, s, upd(r, extra) {
      const t = badge ? labelFor(id) : r.Label.replace(/\s+/g, " ");
      if (b.textContent !== t) b.textContent = t;
      const parts = [];
      if (badge) parts.push(tabName(r.tab) + (r.section ? " " + DOT + " " + r.section : ""));
      if (!r.Available) parts.push("needs " + (r.Why || "another setting"));
      if (extra) parts.push(extra);
      if (isPinned(id)) parts.push("on Home");
      const sub = parts.join(" " + DOT + " ");
      if (s.textContent !== sub) s.textContent = sub;
    } };
  }
  // TRLC-LINKS: REQ-SDS-220
  function cell(id, opts) {
    opts = opts || {};
    const r0 = row(id), lab = cellLabel(id, !!opts.badge), avail = (window.innerWidth || 360) - 28;
    const longPin = () => togglePin(id);
    let el;
    if (RC.isSwitch(r0)) {
      el = h("button", { type: "button", class: "cell", role: "switch", "aria-checked": "false", data: { row: id } }, lab.el, h("span", { class: "swt", "aria-hidden": "true" }));
      press(el, { onTap: () => { const r = row(id); setRow(id, { value: r.Value ? 0 : 1 }); }, onLong: longPin });
      bind(el, () => { const r = row(id); lab.upd(r); el.setAttribute("aria-checked", r.Value ? "true" : "false"); el.classList.toggle("off", !r.Available); el.setAttribute("aria-label", labelFor(id) + ": " + r.Text); });
      return el;
    }
    const sw = r0.Kind === "enum" ? segW(r0.Options, 14, 44) : 0;
    if (r0.Kind === "enum" && r0.Options.length <= 6 && sw <= avail) {
      const inline = textW(r0.Label, 15) + 16 + segW(r0.Options, 26, 48) <= avail - 8;
      el = h("div", { class: "cell" + (inline ? "" : " stack"), data: { row: id } });
      const seg = h("div", { class: "seg", role: "radiogroup", "aria-label": labelFor(id) });
      const bs = r0.Options.map((o) => {
        const b = h("button", { type: "button", role: "radio", "aria-checked": "false", data: { value: String(o.Value) } }, o.Text);
        press(b, { onTap: () => setRow(id, { value: o.Value }), onLong: longPin });
        seg.appendChild(b);
        return [b, o.Value];
      });
      el.append(lab.el, seg);
      bind(el, () => { const r = row(id); lab.upd(r); el.classList.toggle("off", !r.Available); for (const [b, v] of bs) b.setAttribute("aria-checked", v === r.Value ? "true" : "false"); });
      return el;
    }
    const val = h("span", { class: "cv" });
    if (r0.Kind === "number") {
      el = h("div", { class: "cell", data: { row: id } });
      const cm = h("button", { type: "button", class: "cm", data: { open: id } }, lab.el, val);
      cm.insertAdjacentHTML("beforeend", icon("chev", 18));
      press(cm, { onTap: () => openEditor(id), onLong: longPin });
      const a = repeat(h("button", { type: "button", class: "btn sq st", data: { q: "ccw" }, html: icon("back", 22) }), () => stepRow(id, -1));
      const b = repeat(h("button", { type: "button", class: "btn sq st", data: { q: "cw" }, html: icon("chev", 22) }), () => stepRow(id, 1));
      el.append(cm, a, b);
      bind(el, () => {
        const r = row(id), lo = RC.turn(r, -1), hi = RC.turn(r, 1);
        lab.upd(r); val.textContent = disp(r.Text); el.classList.toggle("off", !r.Available);
        a.disabled = !lo; b.disabled = !hi;
        a.setAttribute("aria-label", labelFor(id) + " counter-clockwise" + (lo ? " to " + disp(lo.text) : ": at the end"));
        b.setAttribute("aria-label", labelFor(id) + " clockwise" + (hi ? " to " + disp(hi.text) : ": at the end"));
        cm.setAttribute("aria-label", labelFor(id) + ": " + disp(r.Text) + " (open)");
      });
      return el;
    }
    const acts = r0.Actions || [];
    const inline = RC.isSlot(r0) ? acts : r0.Kind === "action" && acts.length === 1 && acts[0].Text.length <= 13 ? acts : [];
    if (inline.length) {
      el = h("div", { class: "cell", data: { row: id } });
      const cm = h("button", { type: "button", class: "cm", data: { open: id } }, lab.el);
      press(cm, { onTap: () => openEditor(id), onLong: longPin });
      el.appendChild(cm);
      const btns = inline.map((a) => {
        const b = id === "sys.default"
          ? holdButton({ label: a.Text, sub: "hold 1 s", ms: 1000, onConfirm: () => actRow(id, a.ID, "Default setup restored") })
          : button({ label: a.Text, cls: RC.isSlot(r0) ? "" : "primary", onTap: () => actRow(id, a.ID, labelFor(id) + ": " + a.Text) });
        b.dataset.act = a.ID;
        el.appendChild(b);
        return [b, a.ID];
      });
      bind(el, () => {
        const r = row(id), st = r.Text;
        lab.upd(r, st);
        for (const [b, aid] of btns) { const x = (r.Actions || []).find((y) => y.ID === aid); b.classList.toggle("dimmed", !!x && !x.Available); }
        el.classList.toggle("off", !r.Available);
        cm.setAttribute("aria-label", labelFor(id) + (st ? ": " + st : "") + " (open)");
      });
      return el;
    }
    el = h("button", { type: "button", class: "cell drill", data: { row: id, open: id } }, lab.el, val);
    el.insertAdjacentHTML("beforeend", icon("chev", 18));
    press(el, { onTap: () => openEditor(id), onLong: longPin });
    bind(el, () => { const r = row(id); lab.upd(r); val.textContent = disp(r.Text); el.classList.toggle("off", !r.Available); el.setAttribute("aria-label", labelFor(id) + ": " + disp(r.Text) + " (open)"); });
    return el;
  }

  // =================================================================================================
  // History: every change, newest first, with undo
  // =================================================================================================

  const histList = h("div", { class: "hist", role: "list", "aria-label": "Changes, newest first" });
  V.hist.appendChild(histList);
  let histMeta = [];
  const SRC = { phone: "phone", scope: "scope", remote: "other remote" };
  // TRLC-LINKS: REQ-SDS-220
  function entryTitle(e) {
    if (e.label) return e.label;
    if (e.ws) return "Workspace";
    if (e.rows.length === 1 || e.primary) return e.rows[0].tab + " " + DOT + " " + e.rows[0].label;
    return e.src === "phone" ? "Settings" : "On the scope";
  }
  const nd = (t) => RC.pretty(String(t == null ? "" : t)).replace(/\s*\/div$/, "");
  // TRLC-LINKS: REQ-SDS-220
  function valuesLine(e) {
    const v = h("span", { class: "hv" });
    const pair = (a, b) => v.append(nd(a) + " " + ARROW + " ", h("em", { text: nd(b) }));
    const more = (n) => (n ? " " + DOT + " " + n + (n === 1 ? " setting" : " settings") : "");
    if (e.ws) { pair(WS_BY[e.ws.from] ? WS_BY[e.ws.from].name : e.ws.from, WS_BY[e.ws.to] ? WS_BY[e.ws.to].name : e.ws.to); v.append(more(e.rows.length)); }
    else if (e.rows.length === 1 || e.primary) { pair(e.rows[0].old.text, e.rows[0].neu.text); if (e.rows.length > 1) v.append(" " + DOT + " +" + (e.rows.length - 1)); }
    else if (e.rows.length) v.append(e.rows.length + " settings: " + e.rows.slice(0, 3).map((r) => r.label + " " + nd(r.neu.text)).join(", "));
    else v.append(e.kind === "action" ? "done (no setting changed)" : "—");
    return v;
  }
  // TRLC-LINKS: REQ-SDS-220
  function badge() {
    const n = RN.S.history.list.length;
    histBadge.hidden = !n;
    histBadge.textContent = n > 99 ? "99+" : String(n);
    tabBtns.hist.setAttribute("aria-label", "History" + (n ? ", " + n + (n === 1 ? " change" : " changes") : ""));
  }
  // TRLC-LINKS: REQ-SDS-220
  function renderHist() {
    histList.textContent = "";
    histMeta = [];
    const L = RN.S.history.list;
    if (!L.length) {
      histList.appendChild(h("p", { class: "hempty", text: "No changes yet. Every setting changed here or on the scope (its knobs, keys and menus) lands here, newest first. Undo puts the old value back; tap an entry for its details." }));
      return;
    }
    for (const e of L) histList.appendChild(entryEl(e));
  }
  // TRLC-LINKS: REQ-SDS-220
  function entryEl(e) {
    const meta = h("small"), title = entryTitle(e);
    const main = h("button", { type: "button", class: "hmain", data: { hopen: String(e.id) }, "aria-label": title + " (details)" },
      h("span", { class: "hl" }, h("b", { text: title }), meta), valuesLine(e));
    press(main, { onTap: () => openEntry(e) });
    const el = h("div", { class: "hent " + e.src + (e.undone ? " undone" : ""), role: "listitem", data: { hist: String(e.id) } }, main);
    if (e.undoable && !e.undone) {
      const u = button({ label: "Undo", aria: "Undo: " + title, onTap: () => undoEntries([e], "Undo: " + title) });
      u.classList.add("hundo");
      u.dataset.hundo = String(e.id);
      el.appendChild(u);
    } else if (e.undone) el.appendChild(h("span", { class: "badge", text: "undone" }));
    const upd = () => { meta.textContent = (SRC[e.src] || e.src) + " " + DOT + " " + RC.ago(Date.now() - e.t); };
    upd();
    histMeta.push(upd);
    return el;
  }
  // TRLC-LINKS: REQ-SDS-220
  function undoEntries(list, title) {
    RN.undo(list, title).then((res) => {
      haptic(res.ok ? "confirm" : "warn");
      if (res.ok) toast(title, "ok");
      else toast(res.fails && res.fails.length ? "Undone, except " + res.fails[0] : res.error || "The scope refused the undo", "err", 4200);
      if (S.tab === "hist") renderHist();
    });
  }
  // TRLC-LINKS: REQ-SDS-220
  function openEntry(e) {
    const title = entryTitle(e), box = sheetOpen("hist", title, null, "on-fg");
    box.dataset.entry = String(e.id);
    box.appendChild(h("p", { class: "sub", text: (e.src === "phone" ? "Made on this phone" : e.src === "remote" ? "Made on another remote" : "Made on the scope") + " " + DOT + " " +
      RC.ago(Date.now() - e.t) + (e.n > 1 ? " " + DOT + " " + e.n + " steps in a row" : "") + (e.undone ? " " + DOT + " undone" : "") }));
    const list = h("div", { class: "list" });
    const line = (name, a, b, id) => {
      const el = h(id ? "button" : "div", { type: id ? "button" : null, class: "cell hrow" + (id ? " drill" : "") },
        h("span", { class: "cl" }, h("b", { text: name }), h("small", { text: nd(a) + " " + ARROW + " " + nd(b) })));
      if (id) { el.insertAdjacentHTML("beforeend", icon("chev", 18)); press(el, { onTap: () => openEditor(id) }); }
      return el;
    };
    if (e.ws) list.appendChild(line("Workspace", (WS_BY[e.ws.from] || {}).name || e.ws.from, (WS_BY[e.ws.to] || {}).name || e.ws.to, null));
    for (const r of e.rows) list.appendChild(line(r.tab + " " + DOT + " " + r.label, r.old.text, r.neu.text, r.kind === "slot" || !row(r.id) ? null : r.id));
    if (!e.rows.length && !e.ws) list.appendChild(h("p", { class: "sub", text: "No setting changed." }));
    box.appendChild(list);
    const acts = h("div", { class: "k1" });
    if (e.undoable && !e.undone) {
      const u = button({ label: "Undo this", cls: "primary", onTap: () => { sh.close(); undoEntries([e], "Undo: " + title); } });
      u.dataset.hundoOne = String(e.id);
      acts.appendChild(u);
    }
    const span = RN.S.history.since(e);
    if (RN.S.history.list.indexOf(e) > 0 && span.length >= 2) {
      const all = holdButton({ label: "Back to before this", sub: "undoes " + span.length + " changes since: hold 1 s", ms: 1000,
        onConfirm: () => { sh.close(); undoEntries(span, "Back to before: " + title); } });
      all.dataset.hundoAll = String(e.id);
      acts.appendChild(all);
    }
    if (!e.undoable) acts.appendChild(h("p", { class: "sub", text: e.kind === "action" ? "An action (save, store, build, reset) cannot be undone." : "Nothing here can be undone." }));
    box.appendChild(acts);
  }
  setInterval(() => { if (S.tab === "hist") for (const f of histMeta) f(); }, 5000);

  // =================================================================================================
  // Keys: pushes and keys the tiles do not cover, the knob jobs, the menu keys, the phone
  // =================================================================================================

  (function buildKeys() {
    const wrap = h("div", { class: "keyst" });
    const group = (title, cls, items) => { wrap.append(h("div", { class: "sec", text: title }), h("div", { class: cls }, items)); };
    const key = (k, label, sub, fn) => { const b = button({ label, sub, onTap: fn }); b.dataset.k = k; return b; };
    const v1 = key("ch1vdivpush", "CH1 V/DIV", "Variable", () => pressKey("ch1vdivpush"));
    const v2 = key("ch2vdivpush", "CH2 V/DIV", "Variable", () => pressKey("ch2vdivpush"));
    const zoomB = key("tdivpush", "S/DIV push", "Zoom", () => pressKey("tdivpush"));
    const hposB = key("horizpospush", "Position push", "centre", () => pressKey("horizpospush"));
    group("Trigger", "k3", [key("force", "Force", "trigger now", () => pressKey("force", "Forced a trigger")),
      key("set50", "Set to 50 %", "level", () => pressKey("set50", "Level set to 50 %", "Set to 50 %")),
      key("triglvlpush", "Level push", "Zero: 0 V", () => pressKey("triglvlpush", "Level 0 V", "Level push: 0 V"))]);
    group("Vertical", "k2", [v1, v2, key("ch1pospush", "CH1 Position", "Zero: 0 V", () => pressKey("ch1pospush", "CH1 offset 0 V", "CH1 Position push: 0 V")),
      key("ch2pospush", "CH2 Position", "Zero: 0 V", () => pressKey("ch2pospush", "CH2 offset 0 V", "CH2 Position push: 0 V"))]);
    group("Horizontal", "k2", [zoomB, hposB]);
    const helpB = button({ label: "Help", sub: "this remote", onTap: () => openHelp() });
    helpB.dataset.k = "help";
    group("System", "k2", [key("print", "Print", "screenshot", () => pressKey("print", "Print: the scope saves a screenshot")), helpB]);
    const defB = holdButton({ label: "Default Setup", sub: "factory settings: hold 1 s", ms: 1000, onConfirm: () => actRow("sys.default", "default", "Default setup restored") });
    defB.id = "default-setup";
    wrap.appendChild(h("div", { class: "k1" }, defB));
    wrap.append(h("div", { class: "sec", text: "Workspace knobs" }), knobRows());
    const TABNAME = { trig: "TRIG", time: "TIME", ch1: "CH1", ch2: "CH2", cmp: "MATH/REF", acq: "ACQ", view: "VIEW", meas: "MEAS", curs: "CURS", bus: "BUS", file: "FILE", sys: "SYS" };
    const mk = MENU_KEYS.map(([k, label, tab, focus]) => key("menu:" + k, label, tab ? TABNAME[tab] || tab.toUpperCase() : "all menus",
      () => { if (tab) openPage(tab, focus); else { S.page = null; goTab("menus"); renderMenus(); renderBar(); } }));
    group("Menu keys (they open the menu here, not on the scope)", "k3", mk);
    // the phone: keep awake and fullscreen, with their real state
    const awk = h("button", { type: "button", class: "cell", role: "switch", "aria-checked": "false", id: "keep-awake" },
      h("span", { class: "cl" }, h("b", { text: "Keep the phone awake" }), h("small")), h("span", { class: "swt", "aria-hidden": "true" }));
    const fsc = h("button", { type: "button", class: "cell", role: "switch", "aria-checked": "false", id: "fullscreen" },
      h("span", { class: "cl" }, h("b", { text: "Fullscreen" }), h("small")), h("span", { class: "swt", "aria-hidden": "true" }));
    press(awk, { onTap: () => toggleAwake() });
    press(fsc, { onTap: () => toggleFs() });
    wrap.append(h("div", { class: "sec", text: "This phone" }), awk, fsc,
      h("p", { class: "note", text: "The scope serves plain HTTP, where browsers have no Screen Wake Lock: the remote then keeps the screen on with a tiny silent video (on an iPhone that may pause music from other apps; turn keep-awake off to stop it). iPhone Safari has no fullscreen for pages: Share, Add to Home Screen opens the remote without browser bars. Android: Fullscreen above, or the browser menu's Add to Home screen." }));
    V.keys.appendChild(wrap);
    bind(wrap, () => {
      const t = tools();
      hposB.setSub(scopeMenu() ? "waits: scope menu open" : cap(t.position.push || "zero"));
      hposB.disabled = scopeMenu();
      const z = row("time.zoom");
      zoomB.setSub("Zoom: " + (!z || z.Value === 1 ? "off" : z.Text));
      const wk = Wake.state(), fs = Full.state();
      awk.setAttribute("aria-checked", wk.on ? "true" : "false");
      awk.querySelector("small").textContent = wk.text + (wk.note ? " " + DOT + " " + wk.note : "");
      fsc.setAttribute("aria-checked", fs.on ? "true" : "false");
      fsc.querySelector("small").textContent = fs.text;
    });
  })();

  // ---- the phone: keep awake and fullscreen ------------------------------------------------

  // TRLC-LINKS: REQ-SDS-220
  function toggleAwake() {
    if (Wake.state().on) { Wake.disable(); toast("Keep awake: off", "ok"); return; }
    Wake.enable().then((w) => toast(w.on ? w.text : w.note || w.text, w.on ? "ok" : "err", w.on ? 2200 : 4200));
  }
  // TRLC-LINKS: REQ-SDS-220
  function toggleFs() {
    const before = Full.state();
    if (!before.can && !before.on) { toast("Fullscreen " + before.text, "err", 4600); return; }
    Full.toggle().then((f) => toast("Fullscreen: " + f.text, f.note ? "err" : "ok"));
  }
  Wake.on(() => refreshAll());
  Full.on(() => refreshAll());

  // ---- start -----------------------------------------------------------------------------------

  RN.on((what) => {
    if (what === "online") { renderBanner(); return; }
    if (what.indexOf("notice:") === 0) {
      const t = document.querySelector(".toast");
      if (!(t && !t.hidden && t.textContent.indexOf(what.slice(7)) >= 0)) toast(what.slice(7));   // said already
      return;
    }
    if (what === "history") { badge(); if (S.tab === "hist") renderHist(); renderBar(); return; }
    renderDock();
    refreshAll();
    if (what === "model" && S.tab === "menus" && !V.menus.querySelector(".list,.tabs")) renderMenus();
  });
  renderBanner();
  badge();
  renderBar();
  RN.start();
  window.RA = { goTab, openEditor, openPage, S };
})();
