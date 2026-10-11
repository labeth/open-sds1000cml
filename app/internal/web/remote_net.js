// ENGMODEL-OWNER-UNIT: FU-WEB-REMOTE
// remote_net.js — the phone remote's link to the scope (window.RN): the
// device API, one long-poll per phone (GET /api/panel/wait) that re-reads the
// sheet model on every change and backs off while the scope is away, the
// model with this phone's optimistic values, phone calls kept in order (steps
// on one row while a call is out coalesce into one), and History: every
// change is booked as this phone's when a call of this phone can have made
// it, else as the scope's (or another remote's, while the scope shows its
// REMOTE line). Nothing here polls in a loop: the long-poll is the only
// standing request.
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
      if (!r.ok) throw new Error("HTTP " + r.status);
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

  const S = {
    model: { rows: new Map(), tabs: [] }, // RC.index(/api/sheets)
    panel: null,                          // the panel status (/api/panel/wait)
    seq: 0, online: false, ever: false, error: "",
    overlay: new Map(),                   // row id -> {value, text}: set here, not answered yet
    stamps: new Map(),                    // row id -> when a call's answer set it (an older read does not undo it)
    history: new RC.History(RU.load("history", [])),
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

  // start runs the one long-poll of this phone: on every Seq change it reads
  // the sheets again; while the scope does not answer it retries at 0.5, 1,
  // 2, 4 and then every 8 s, and reads everything again when it is back. It
  // rests while the page is hidden and catches up when it shows.
  // TRLC-LINKS: REQ-SDS-220
  async function start() {
    if (started) return;
    started = true;
    let backoff = 0;
    for (;;) {
      if (await shown()) S.seq = 0;   // back from hidden: read everything again
      try {
        const st = await api.wait(S.seq, S.online ? 9000 : 0);
        const back = !S.online;
        setOnline(true);
        backoff = 0;
        if (st.Seq !== S.seq || back) {
          S.seq = st.Seq;
          applyPanel(st);
          await refresh();
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

  let reading = null, readAgain = false;

  // refresh reads the sheet model (one read at a time; a request during a
  // read makes one more after it) and books what changed.
  // TRLC-LINKS: REQ-SDS-220
  function refresh() {
    if (reading) { readAgain = true; return reading; }
    reading = (async () => {
      do {
        readAgain = false;
        const t0 = now();
        const sh = await api.sheets();
        applySheets(sh, t0);
        book(t0);
        emit("model");
      } while (readAgain);
    })().finally(() => { reading = null; });
    return reading;
  }

  // TRLC-LINKS: REQ-SDS-220
  function applySheets(sh, t0) {
    const m = RC.index(sh);
    for (const [id, t] of S.stamps) {
      const mine = S.model.rows.get(id);
      if (t > t0 && mine && m.rows.has(id)) m.rows.set(id, Object.assign({}, m.rows.get(id), { Value: mine.Value, Text: mine.Text }));
      else if (t <= t0) S.stamps.delete(id);
    }
    S.model = m;
  }

  // ---- History --------------------------------------------------------------------------------

  // held: a call whose effects keep coming after its answer (Autoset while
  // it sweeps, BUS while it searches for the bus)
  // TRLC-LINKS: REQ-SDS-220
  function held(c) {
    if (!c.doneAt) return false;
    return (c.meta.autoset && ((S.panel && S.panel.AutosetBusy) || now() - c.doneAt < 1500)) || (c.meta.windowMs > 0 && now() - c.doneAt < c.meta.windowMs);
  }

  // openCall is the call that can have made what a read started at t0 shows:
  // one still out, one done after the read began, one whose own read-back has
  // not been booked yet, or one held open; the newest of them.
  // TRLC-LINKS: REQ-SDS-220
  function openCall(t0) {
    for (let i = S.calls.length - 1; i >= 0; i--) {
      const c = S.calls[i];
      if (!c.doneAt || !c.seen || held(c) || (t0 !== undefined && c.doneAt > t0)) return c;
    }
    return null;
  }

  // closeCalls: a read that started after a call's answer was that call's
  // read-back; calls read back and no longer held are done with.
  // TRLC-LINKS: REQ-SDS-220
  function closeCalls(t0) {
    for (const c of S.calls) if (c.doneAt && c.doneAt <= t0) c.seen = true;
    S.calls = S.calls.filter((c) => !c.doneAt || !c.seen || held(c));
  }

  // TRLC-LINKS: REQ-SDS-220
  function saveHistory() { RU.store("history", S.history.list); }

  // book compares the model with what History saw last: a change goes to
  // the phone call that can have made it, else to the scope.
  // TRLC-LINKS: REQ-SDS-220
  function book(t0) {
    const snap = RC.snapshot(S.model, wsId());
    if (!S.base || !S.base.rows.size) { S.base = snap; return; }
    const rows = RC.diff(S.base, snap);
    const ws = S.base.ws && snap.ws && S.base.ws !== snap.ws ? { from: S.base.ws, to: snap.ws } : null;
    S.base = snap;
    if (rows.length || ws) {
      const c = openCall(t0);
      if (c) attach(c, rows, ws);
      else S.history.add(otherRemote(rows) ? "remote" : "scope", rows, { ws }, Date.now());
      saveHistory();
      emit("history");
    }
    if (t0 !== undefined) closeCalls(t0);
  }

  // otherRemote: the scope's REMOTE line names one of these rows while this
  // phone changed nothing lately: another remote made the change.
  // TRLC-LINKS: REQ-SDS-220
  function otherRemote(rows) {
    const line = (S.panel && S.panel.Remote) || "";
    if (!line || now() - S.lastCall < 2000) return false;
    return rows.some((r) => line.indexOf("REMOTE  " + r.tab + " " + r.label.toLowerCase() + " ") === 0);
  }

  // TRLC-LINKS: REQ-SDS-220
  function attach(c, rows, ws) {
    const live = c.entry && S.history.list.indexOf(c.entry) >= 0;
    if (live) S.history.extend(c.entry, rows, ws);
    else c.entry = S.history.add("phone", rows, Object.assign({}, c.meta.history, { ws: ws || undefined }), Date.now());
  }

  // ---- this phone's calls ---------------------------------------------------------------------

  let tail = Promise.resolve(), callSeq = 0;

  // call runs fn as this phone's change, in order after the calls before it.
  // meta: {row (the row it sets), history ({label, kind, undoable, primary}),
  // autoset, windowMs (how long its effects keep coming)}. Answers
  // {ok, value, text, error} as soon as the scope does; the next call waits
  // until this one's effects have been read back, so each change is booked
  // to the call that made it.
  // TRLC-LINKS: REQ-SDS-220
  function call(meta, fn) {
    if (!S.online) return Promise.resolve({ ok: false, error: "Not connected to the scope" });
    let answered;
    const answer = new Promise((r) => { answered = r; });
    const run = async () => {
      book();                                         // what came before this call is not this call's
      const c = { id: ++callSeq, meta, doneAt: 0, seen: false, entry: null };
      S.calls.push(c);
      S.lastCall = now();
      let res;
      try {
        res = await fn(c);
      } catch (e) {
        res = { ok: false, error: "No answer from the scope (" + ((e && e.message) || e) + ")" };
      }
      res = res || { ok: false, error: "no answer" };
      c.doneAt = now();
      c.seen = !res.ok;   // a refused or unanswered call has nothing to read back
      S.lastCall = c.doneAt;
      if (res.ok && meta.row && !res.skipped) {
        setModelRow(meta.row, res.value, res.text);
        S.stamps.set(meta.row, c.doneAt);
      }
      if (res.ok && res.workspace) applyWorkspace(res);
      if (meta.row && !S.queued.has(meta.row)) S.overlay.delete(meta.row);
      book();
      emit("model");
      answered(res);
      try { await refresh(); } catch (e) { /* the long-poll reports the link */ }
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

  // action runs an action of a row (a setup slot's Save, a preset, Autoset).
  // TRLC-LINKS: REQ-SDS-220
  function action(id, act, history, extra) {
    return call(Object.assign({ row: id, history: Object.assign({ kind: "action" }, history) }, extra), () => api.action(id, act));
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
  // as it is); again runs the active workspace's again.
  // TRLC-LINKS: REQ-SDS-220
  function workspace(id) {
    // BUS may search for the bus after the switch: its decode turns on a moment later
    return call({ history: {}, windowMs: id === "bus" ? 3000 : 0 }, async () => {
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
    return call({ history: { kind: "undo", label: title } }, async () => {
      const fails = [];
      if (t.ws && wsId() !== t.ws) {
        const r = await api.workspace(t.ws);
        if (r.ok) applyWorkspace(r); else fails.push(r.error);
      }
      let todo = t.rows;
      for (let pass = 0; pass < 3 && todo.length; pass++) {
        for (const x of todo.slice().reverse()) {
          const input = x.kind === "bytes" ? { text: x.value ? String(x.value) : "any" } : { value: x.value };
          const r = await api.set(x.id, input);
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
