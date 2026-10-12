// ENGMODEL-OWNER-UNIT: FU-WEB-REMOTE
// remote_net.js — the phone remote's link to the scope (window.RN): the
// device API, one long-poll per phone (GET /api/panel/wait) that re-reads the
// sheet model when its values changed (SheetsSeq), never more often than
// every 300 ms, and backs off while the scope is away, the
// model with this phone's optimistic values, phone calls kept in order (steps
// on one row while a call is out coalesce into one), and History: a change is
// booked as this phone's only where the scope's answer to one of its calls
// lists the row, else as the scope's (or another remote's, while the scope's
// REMOTE line names it). Nothing here polls in a loop: the long-poll is the
// only standing request.
// TRLC-LINKS: REQ-SDS-220
"use strict";

(function () {
  const RC = window.RC, RU = window.RU;

  // ---- the device API ---------------------------------------------------------------

  // TRLC-LINKS: REQ-SDS-220
  async function http(method, path, body, ms) {
    const ctl = new AbortController(), t = setTimeout(() => ctl.abort(), ms || 8000);
    try {
      const r = await fetch(path, {
        method, cache: "no-store", signal: ctl.signal,
        headers: body ? { "Content-Type": "application/json" } : undefined,
        body: body ? JSON.stringify(body) : undefined,
      });
      if (!r.ok) throw Object.assign(new Error("HTTP " + r.status), { status: r.status });
      return await r.json();
    } finally {
      clearTimeout(t);
    }
  }

  // panel input answers {ok} when the key or knob was queued on the scope
  // TRLC-LINKS: REQ-SDS-220
  const busy = (r) => (r && r.ok ? r : { ok: false, error: (r && (r.error || r.err)) || "the scope is busy: try again" });
  const api = {
    wait: (seq, ms) => http("GET", "/api/panel/wait?seq=" + seq + "&timeout_ms=" + ms, null, ms + 6000),
    sheets: () => http("GET", "/api/sheets", null, 10000),
    set: (row, input) => http("POST", "/api/sheets/set", Object.assign({ row }, input)),
    action: (row, action) => http("POST", "/api/sheets/action", { row, action: action || "" }),
    press: async (button) => busy(await http("POST", "/api/panel", { button })),
    knob: async (knob, dir, steps) => busy(await http("POST", "/api/panel", { knob, dir, steps: steps || 1 })),
    workspace: (ws) => http("POST", "/api/panel", { workspace: ws }),
    again: () => http("POST", "/api/panel", { again: true }),
  };

  // ---- state ----------------------------------------------------------------------------

  const HIST_KEY = "history";
  const S = {
    model: { rows: new Map(), tabs: [] }, // RC.index(/api/sheets)
    panel: null,                          // the panel status (/api/panel/wait)
    seq: 0, sheetsSeq: -1,                // the status's Seq, and the SheetsSeq of the model read last
    online: false, ever: false, error: "",
    overlay: new Map(),                   // row id -> {value, text}: set here, not answered yet
    stamps: new Map(),                    // row id -> when a call's answer set it (an older read does not undo it)
    history: new RC.History(RU.load(HIST_KEY, [])),
    base: null,                           // the snapshot History last booked
    calls: [],                            // this phone's calls whose changes may still show
    queued: new Map(),                    // row id -> the queued call that sends its latest value
    lastCall: -Infinity, notice: "",
    fns: [],
  };
  const now = () => performance.now();

  // TRLC-LINKS: REQ-SDS-220
  function emit(what) { for (const f of S.fns) { try { f(what); } catch (e) { console.error(e); } } }

  // TRLC-LINKS: REQ-SDS-220
  function setOnline(on, err) {
    const was = S.online;
    S.online = on;
    S.error = on ? "" : String((err && err.message) || err || "no answer");
    if (on) S.ever = true;
    if (was !== on) emit("online");
  }

  const wsId = () => (S.panel && S.panel.WorkspaceID) || "";

  // row is a row as the phone shows it: the scope's value, or the value this
  // phone just set while the scope has not answered yet.
  // TRLC-LINKS: REQ-SDS-220
  function row(id) {
    const r = S.model.rows.get(id);
    if (!r) return null;
    const o = S.overlay.get(id);
    return o ? Object.assign({}, r, { Value: o.value, Text: o.text, pending: true }) : r;
  }

  // TRLC-LINKS: REQ-SDS-220
  function setModelRow(id, value, text) {
    const r = S.model.rows.get(id);
    if (!r) return;
    const x = Object.assign({}, r, { Text: text === undefined ? r.Text : text });
    if (value !== undefined) x.Value = value;
    S.model.rows.set(id, x);
  }

  // ---- the long-poll -----------------------------------------------------------------------

  let started = false;

  // shown resolves when the page is shown: a hidden phone (screen off,
  // another app) asks the scope nothing.
  // TRLC-LINKS: REQ-SDS-220
  function shown() {
    if (!document.hidden) return Promise.resolve(false);
    return new Promise((r) => {
      const f = () => { if (!document.hidden) { document.removeEventListener("visibilitychange", f); r(true); } };
      document.addEventListener("visibilitychange", f);
    });
  }

  // GAP_MS is the least time between two long-polls: a scope whose snapshot
  // keeps changing is followed a few times a second, not as fast as the
  // network allows.
  const GAP_MS = 300;

  // start runs the one long-poll of this phone: on every Seq change it takes
  // the panel status, and reads the sheets again when their values changed
  // (SheetsSeq); while the scope does not answer it retries at 0.5, 1, 2, 4
  // and then every 8 s, and reads everything again when it is back. It rests
  // while the page is hidden and catches up when it shows.
  // TRLC-LINKS: REQ-SDS-220
  async function start() {
    if (started) return;
    started = true;
    let backoff = 0, last = -Infinity;
    for (;;) {
      if (await shown()) S.seq = 0;   // back from hidden: read everything again
      const gap = GAP_MS - (now() - last);
      if (gap > 0) await new Promise((r) => setTimeout(r, gap));
      last = now();
      try {
        const st = await api.wait(S.seq, S.online ? 9000 : 0);
        const back = !S.online;
        setOnline(true);
        backoff = 0;
        if (st.Seq !== S.seq || back) {
          S.seq = st.Seq;
          applyPanel(st);
          if (back || st.SheetsSeq === undefined || st.SheetsSeq !== S.sheetsSeq) await refresh(back ? 0 : st.SheetsSeq);
          else book();   // the workspace, the lamps: the status alone
        }
      } catch (e) {
        setOnline(false, e);
        backoff = Math.min(backoff ? backoff * 2 : 500, 8000);
        await new Promise((r) => setTimeout(r, backoff));
      }
    }
  }

  // TRLC-LINKS: REQ-SDS-220
  function applyPanel(st) {
    S.panel = st;
    // a notice the scope shows in answer to this phone (AUTO: UART ... decode on)
    if (st.Notice && st.Notice !== S.notice && now() - S.lastCall < 4000) emit("notice:" + st.Notice);
    S.notice = st.Notice || "";
    emit("panel");
  }

  let reading = null, readAgain = false, readUpTo = 0;

  // refresh reads the sheet model and books what changed: one read at a
  // time. Without a target (a call of this phone) a read must start after
  // the request, so one comes after a read under way; with the long-poll's
  // SheetsSeq as the target, a read under way that brings it is enough.
  // TRLC-LINKS: REQ-SDS-220
  function refresh(target) {
    if (reading) {
      if (target) readUpTo = Math.max(readUpTo, target); else readAgain = true;
      return reading;
    }
    reading = (async () => {
      do {
        readAgain = false;
        const t0 = now();
        const sh = await api.sheets();
        applySheets(sh, t0);
        book();
        emit("model");
      } while (readAgain || (readUpTo && S.sheetsSeq < readUpTo));
    })().finally(() => { reading = null; readUpTo = 0; });
    return reading;
  }

  // TRLC-LINKS: REQ-SDS-220
  function applySheets(sh, t0) {
    if (typeof sh.SheetsSeq === "number") S.sheetsSeq = sh.SheetsSeq;
    const m = RC.index(sh);
    for (const [id, t] of S.stamps) {
      const mine = S.model.rows.get(id);
      if (t > t0 && mine && m.rows.has(id)) m.rows.set(id, Object.assign({}, m.rows.get(id), { Value: mine.Value, Text: mine.Text }));
      else if (t <= t0) S.stamps.delete(id);
    }
    S.model = m;
  }

  // ---- History --------------------------------------------------------------------------------

  // A change the scope shows is this phone's only where one of its calls
  // claims the row: the scope lists the rows each call changed, measured on
  // its panel goroutine (a knob turned there or another remote's call
  // meanwhile is not in the list), and an Autoset this phone started lists
  // its sweep's rows when it ends. Every other change is the scope's, or
  // another remote's where the scope's REMOTE line names the row. A claim is
  // used up by the read that shows the row changed, or lapses after 10 s (a
  // timebase the engine applies at its next capture shows a moment after
  // the answer).
  const CLAIM_MS = 10000, AUTOSET_MS = 30000;

  // TRLC-LINKS: REQ-SDS-220
  function saveHistory() { RU.store(HIST_KEY, S.history.list); }

  // took keeps what an answer of the scope says a call did: the rows it
  // changed, the workspace it switched to, the Autoset it started, and the
  // sheet values' count that shows it.
  // TRLC-LINKS: REQ-SDS-220
  function took(c, res) {
    if (!res) return;
    for (const id of res.changed || []) c.claims.set(id, Infinity);
    if (res.ok && res.workspace) c.wsTo = res.workspace;
    if (res.autoset) c.autoset = res.autoset;
    if (res.sheetsSeq) c.upTo = Math.max(c.upTo, res.sheetsSeq);
  }

  // settle starts the claims' clocks of the calls that answered, and takes
  // the rows of an Autoset sweep this phone started once the scope says it
  // ended (or gives up on it after 30 s).
  // TRLC-LINKS: REQ-SDS-220
  function settle() {
    const P = S.panel || {}, t = now();
    for (const c of S.calls) {
      if (!c.doneAt) continue;
      if (c.autoset && ((P.AutosetDone || 0) >= c.autoset || t - c.doneAt > AUTOSET_MS)) {
        if (P.AutosetDone === c.autoset) for (const id of P.AutosetChanged || []) c.claims.set(id, Infinity);
        c.autoset = 0;
      }
      if (!c.autoset) for (const [id, until] of c.claims) if (until === Infinity) c.claims.set(id, t + CLAIM_MS);
    }
  }

  // claimer is the newest call that claims row id; the claim is used up.
  // TRLC-LINKS: REQ-SDS-220
  function claimer(id) {
    const t = now();
    for (let i = S.calls.length - 1; i >= 0; i--) {
      const c = S.calls[i], until = c.claims.get(id);
      if (until !== undefined && until >= t) { c.claims.delete(id); return c; }
    }
    return null;
  }

  // book compares the model with what History saw last and books each
  // change to the call of this phone that claims it, else to the scope or
  // another remote. While a call has not answered (or its Autoset sweeps),
  // nothing is booked: its rows are not known yet.
  // TRLC-LINKS: REQ-SDS-220
  function book() {
    const snap = RC.snapshot(S.model, wsId());
    if (!S.base || !S.base.rows.size) { S.base = snap; return; }
    settle();
    if (S.calls.some((c) => !c.doneAt || c.autoset)) return;
    const rows = RC.diff(S.base, snap);
    const ws = S.base.ws && snap.ws && S.base.ws !== snap.ws ? { from: S.base.ws, to: snap.ws } : null;
    S.base = snap;
    if (rows.length || ws) {
      const mine = new Map(), remote = [], scope = [];
      for (const r of rows) {
        const c = claimer(r.id);
        if (c) { if (!mine.has(c)) mine.set(c, []); mine.get(c).push(r); }
        else (otherRemote(r) ? remote : scope).push(r);
      }
      let wsCall = null;
      for (let i = S.calls.length - 1; ws && i >= 0 && !wsCall; i--) if (S.calls[i].wsTo === ws.to) wsCall = S.calls[i];
      if (wsCall) { wsCall.wsTo = ""; if (!mine.has(wsCall)) mine.set(wsCall, []); }
      for (const [c, rs] of mine) attach(c, rs, c === wsCall ? ws : null);
      const theirWs = ws && !wsCall ? ws : null, remoteWs = !!theirWs && otherRemoteWs();
      if (remote.length || remoteWs) S.history.add("remote", remote, { ws: remoteWs ? theirWs : undefined }, Date.now());
      if (scope.length || (theirWs && !remoteWs)) S.history.add("scope", scope, { ws: theirWs && !remoteWs ? theirWs : undefined }, Date.now());
      saveHistory();
      emit("history");
    }
    const t = now();
    S.calls = S.calls.filter((c) => !c.doneAt || c.autoset || c.wsTo || [...c.claims.values()].some((until) => until >= t));
  }

  // otherRemote: the scope's REMOTE line names the row while this phone
  // changed nothing lately: another remote made the change.
  // TRLC-LINKS: REQ-SDS-220
  function otherRemote(r) {
    const line = (S.panel && S.panel.Remote) || "";
    return !!line && now() - S.lastCall >= 2000 && line.indexOf("REMOTE  " + r.tab + " " + r.label.toLowerCase() + " ") === 0;
  }

  // TRLC-LINKS: REQ-SDS-220
  function otherRemoteWs() {
    const line = (S.panel && S.panel.Remote) || "";
    return now() - S.lastCall >= 2000 && line.indexOf("REMOTE  workspace ") === 0;
  }

  // TRLC-LINKS: REQ-SDS-220
  function attach(c, rows, ws) {
    const live = c.entry && S.history.list.indexOf(c.entry) >= 0;
    if (live) S.history.extend(c.entry, rows, ws);
    else c.entry = S.history.add("phone", rows, Object.assign({}, c.meta.history, { ws: ws || undefined }), Date.now());
  }

  // ---- this phone's calls ---------------------------------------------------------------------

  let tail = Promise.resolve(), callSeq = 0;

  // noAnswer says why a call got no answer, in the remote's words (not the
  // browser's: "Failed to fetch", "Load failed").
  // TRLC-LINKS: REQ-SDS-220
  function noAnswer(e) {
    if (e && e.name === "AbortError") return "The scope did not answer in time";
    if (e && e.status) return "The scope answered HTTP " + e.status;
    return "No answer from the scope";
  }

  // call runs fn as this phone's change, in order after the calls before it.
  // meta: {row (the row it sets), history ({label, kind, undoable, primary})}.
  // fn gets the call c and hands every answer of the scope it gets to
  // took(c, answer) (call() does so for the one it returns). Answers
  // {ok, value, text, error, changed, notice} as soon as the scope does,
  // with after: a promise of the read that shows the change; the next call
  // waits for that read.
  // TRLC-LINKS: REQ-SDS-220
  function call(meta, fn) {
    if (!S.online) return Promise.resolve({ ok: false, error: "Not connected to the scope" });
    let answered;
    const answer = new Promise((r) => { answered = r; });
    const run = async () => {
      book();                                         // what came before this call is not this call's
      const c = { id: ++callSeq, meta, doneAt: 0, claims: new Map(), wsTo: "", autoset: 0, upTo: 0, entry: null };
      S.calls.push(c);
      S.lastCall = now();
      let res, lost = false;
      try {
        res = await fn(c);
      } catch (e) {
        res = { ok: false, error: noAnswer(e) };
        lost = true;
      }
      res = res || { ok: false, error: "no answer" };
      took(c, res);
      c.doneAt = now();
      S.lastCall = c.doneAt;
      if (res.ok && meta.row && !res.skipped) {
        setModelRow(meta.row, res.value, res.text);
        S.stamps.set(meta.row, c.doneAt);
      }
      if (res.ok && res.workspace) applyWorkspace(res);
      if (meta.row && !S.queued.has(meta.row)) S.overlay.delete(meta.row);
      book();
      emit("model");
      let readBack;
      res.after = new Promise((r) => { readBack = r; });
      answered(res);
      // the read that shows the change (one under way that brings the
      // answer's sheet count is enough); a refused call changed nothing
      try { if ((res.ok && !res.skipped) || lost) await refresh(lost ? 0 : c.upTo); } catch (e) { /* the long-poll reports the link */ }
      readBack();
    };
    tail = tail.then(run, run).catch(() => {});
    return answer;
  }

  // TRLC-LINKS: REQ-SDS-220
  function applyWorkspace(res) {
    if (!S.panel) return;
    S.panel = Object.assign({}, S.panel, { WorkspaceID: res.workspace, Workspace: res.name, View: res.view, AgainNext: res.next });
    emit("panel");
  }

  // optimistic shows a value at once, before the scope answers.
  // TRLC-LINKS: REQ-SDS-220
  function optimistic(id, value, text) {
    S.overlay.set(id, { value, text });
    emit("model");
  }

  // set sets a row ({value} or {text}); with a value the phone shows it at once.
  // TRLC-LINKS: REQ-SDS-220
  function set(id, input, history) {
    if (input.value !== undefined && S.online) {
      const r = S.model.rows.get(id);
      const o = r && r.Kind === "enum" ? (r.Options || []).find((x) => x.Value === input.value) : null;
      optimistic(id, input.value, o ? o.Text : r && r.Kind === "number" ? RC.fmtValue(r, input.value) : r ? r.Text : "");
    }
    return call({ row: id, history: Object.assign({ primary: id }, history) }, () => api.set(id, input));
  }

  // step turns a row n detents clockwise (cw +1) or back (-1): the phone
  // shows the value at once; detents while a call is out join the next one.
  // TRLC-LINKS: REQ-SDS-220
  function step(id, cw, n) {
    const r = row(id);
    const t = r && RC.turn(r, cw, n || 1);
    if (!t) return Promise.resolve({ ok: false, end: true });
    if (!S.online) return Promise.resolve({ ok: false, error: "Not connected to the scope" });
    optimistic(id, t.value, t.text);
    if (S.queued.has(id)) return S.queued.get(id);
    const p = call({ row: id, history: { primary: id } }, async () => {
      S.queued.delete(id);
      const o = S.overlay.get(id);
      if (!o) return { ok: true, skipped: true };
      return api.set(id, { value: o.value });
    });
    S.queued.set(id, p);
    p.then((res) => { if (!res.ok && !S.queued.has(id)) { S.overlay.delete(id); emit("model"); } });
    return p;
  }

  // action runs an action of a row (a setup slot's Save, a preset, Autoset:
  // the rows its sweep changes are this phone's once the scope says it ended).
  // TRLC-LINKS: REQ-SDS-220
  function action(id, act, history) {
    return call({ row: id, history: Object.assign({ kind: "action" }, history) }, () => api.action(id, act));
  }

  // preset runs a number row's labelled value: a change like a set.
  // TRLC-LINKS: REQ-SDS-220
  function preset(id, label) {
    return call({ row: id, history: { primary: id } }, () => api.action(id, label));
  }

  // press is a key of the panel; knob a detent of a knob.
  // TRLC-LINKS: REQ-SDS-220
  function press(button, history, extra) {
    return call(Object.assign({ history: history || {} }, extra), () => api.press(button));
  }
  // TRLC-LINKS: REQ-SDS-220
  function knob(name, dir) { return call({ history: {} }, () => api.knob(name, dir, 1)); }

  // workspace switches the scope's workspace (an open sheet on the LCD stays
  // as it is); again runs the active workspace's again. BUS may then search
  // for the bus: the decode it turns on a moment later is the scope's doing.
  // TRLC-LINKS: REQ-SDS-220
  function workspace(id) {
    return call({ history: {} }, async () => {
      const prev = S.panel;
      if (prev) { S.panel = Object.assign({}, prev, { WorkspaceID: id, Workspace: id.toUpperCase(), AgainNext: "" }); emit("panel"); }
      const res = await api.workspace(id);
      if (!res.ok && prev) {
        S.panel = Object.assign({}, S.panel, { WorkspaceID: prev.WorkspaceID, Workspace: prev.Workspace, AgainNext: prev.AgainNext });
        emit("panel");
      }
      return res;
    });
  }
  // TRLC-LINKS: REQ-SDS-220
  function again() {
    const name = S.panel ? S.panel.Workspace : "";
    return call({ history: { label: name + " again" } }, () => api.again());
  }

  // undo sets the rows of these entries back to their old values (and the
  // workspace they started in), in up to three passes so that rows depending
  // on others settle, and books that as one entry. Rows the scope would not
  // set back are named in the answer.
  // TRLC-LINKS: REQ-SDS-220
  function undo(entries, title) {
    const t = S.history.targets(entries);
    return call({ history: { kind: "undo", label: title } }, async (c) => {
      const fails = [];
      if (t.ws && wsId() !== t.ws) {
        const r = await api.workspace(t.ws);
        took(c, r);
        if (r.ok) applyWorkspace(r); else fails.push(r.error);
      }
      let todo = t.rows;
      for (let pass = 0; pass < 3 && todo.length; pass++) {
        for (const x of todo.slice().reverse()) {
          const input = x.kind === "bytes" ? { text: x.value ? String(x.value) : "any" } : { value: x.value };
          const r = await api.set(x.id, input);
          took(c, r);
          if (r.ok) setModelRow(x.id, r.value, r.text);
        }
        await refresh();
        todo = todo.filter((x) => { const cur = S.model.rows.get(x.id); return cur && JSON.stringify(cur.Value === undefined ? null : cur.Value) !== x.key; });
      }
      for (const x of todo) fails.push(x.tab + " " + x.label + " stayed at " + RC.pretty((S.model.rows.get(x.id) || {}).Text || "?"));
      for (const e of entries) if (e.undoable) e.undone = true;
      saveHistory();
      return { ok: !fails.length, fails, error: fails[0] };
    });
  }

  // TRLC-LINKS: REQ-SDS-220
  function on(fn) { S.fns.push(fn); }

  window.RN = { api, S, start, refresh, row, set, step, action, preset, press, knob, workspace, again, undo, on, saveHistory };
})();
