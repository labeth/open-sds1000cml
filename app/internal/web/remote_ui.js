// ENGMODEL-OWNER-UNIT: FU-WEB-REMOTE
// remote_ui.js — the phone remote's touch primitives (window.RU): a DOM
// helper and icons; buttons that act on the click a tap makes (so a sheet
// opening under the finger never takes a ghost tap), with a pressed look, a
// long press, hold-to-repeat and hold-to-confirm; the bottom sheet; the
// toast; haptics (vibration where the phone has it, a visible tick always);
// keeping the phone awake (Screen Wake Lock, else a muted video behind the
// page, or one with a silent sound track) and fullscreen, each with its real
// state.
"use strict";

(function () {
  const doc = document;

  // ---- DOM --------------------------------------------------------------------------

  // h builds an element: h("button", {class, text, html, data:{k:v}, on:{click}, ...attrs}, ...children).
  // TRLC-LINKS: REQ-SDS-220
  function h(tag, attrs) {
    const e = doc.createElement(tag);
    if (attrs) {
      for (const k of Object.keys(attrs)) {
        const v = attrs[k];
        if (v === null || v === undefined || v === false) continue;
        if (k === "class") e.className = v;
        else if (k === "text") e.textContent = String(v);
        else if (k === "html") e.innerHTML = v;
        else if (k === "data") for (const d of Object.keys(v)) e.dataset[d] = String(v[d]);
        else if (k === "on") for (const ev of Object.keys(v)) e.addEventListener(ev, v[ev]);
        else e.setAttribute(k, v === true ? "" : String(v));
      }
    }
    for (let i = 2; i < arguments.length; i++) add(e, arguments[i]);
    return e;
  }
  // TRLC-LINKS: REQ-SDS-220
  function add(e, k) {
    if (k === null || k === undefined || k === false) return;
    if (Array.isArray(k)) { for (const x of k) add(e, x); return; }
    e.appendChild(typeof k === "object" && k.nodeType ? k : doc.createTextNode(String(k)));
  }

  const ICONS = {
    stop: '<rect x="6" y="6" width="12" height="12" rx="1.5" fill="currentColor" stroke="none"/>',
    search: '<circle cx="11" cy="11" r="6"/><path d="M20 20l-4.5-4.5"/>',
    close: '<path d="M6 6l12 12M18 6L6 18"/>',
    back: '<path d="M15 5l-7 7 7 7"/>',
    chev: '<path d="M9 5l7 7-7 7"/>',
    check: '<path d="M5 12.5l4.5 4.5L19 7"/>',
    keypad: '<circle cx="6" cy="6" r="1.3"/><circle cx="12" cy="6" r="1.3"/><circle cx="18" cy="6" r="1.3"/><circle cx="6" cy="12" r="1.3"/><circle cx="12" cy="12" r="1.3"/><circle cx="18" cy="12" r="1.3"/><circle cx="6" cy="18" r="1.3"/><circle cx="12" cy="18" r="1.3"/><circle cx="18" cy="18" r="1.3"/>',
    home: '<path d="M4 11l8-7 8 7v9H4z"/><path d="M10 20v-6h4v6"/>',
    sliders: '<path d="M4 7h10M18 7h2M4 17h4M12 17h8"/><circle cx="16" cy="7" r="2"/><circle cx="10" cy="17" r="2"/>',
    clock: '<path d="M3.5 12a8.5 8.5 0 1 0 2.5-6"/><path d="M3 4v4.5h4.5"/><path d="M12 7.5V12l3 2"/>',
    power: '<path d="M12 3v8"/><path d="M6.3 7.3a8 8 0 1 0 11.4 0"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    wave: '<path d="M3 12c2-6 4-6 6 0s4 6 6 0 4-6 6 0"/>',
    bus: '<rect x="3" y="9" width="5" height="6" rx="1"/><rect x="10" y="9" width="5" height="6" rx="1"/><rect x="17" y="9" width="4" height="6" rx="1"/>',
    spectrum: '<path d="M5 20V6M9 20v-9M13 20v-5M17 20v-3M21 20v-1"/>',
    measure: '<path d="M7 4v16M17 4v16M7 12h10"/><path d="M10 9l-3 3 3 3M14 9l3 3-3 3"/>',
    compare: '<path d="M3 13c2-5 4-5 6 0s4 5 6 0 4-5 6 0"/><path d="M3 7h18M3 19h18" stroke-dasharray="2 3"/>',
    expand: '<path d="M4 9V4h5M15 4h5v5M20 15v5h-5M9 20H4v-5"/>',
    eye: '<path d="M2 12s4-7 10-7 10 7 10 7-4 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>',
    dial: '<circle cx="12" cy="12" r="8"/><path d="M12 4v5"/>',
    zero: '<circle cx="12" cy="12" r="7"/><path d="M12 3v4M12 17v4M3 12h4M17 12h4"/>',
    hand: '<path d="M8 13V5.5a1.5 1.5 0 0 1 3 0V12M11 11V4a1.5 1.5 0 0 1 3 0v7M14 11V5.5a1.5 1.5 0 0 1 3 0V14c0 4-2.5 7-6 7-2.5 0-4-1-5.5-3L3 14.5a1.5 1.5 0 0 1 2.4-1.8L8 15"/>',
    pin: '<path class="pinhead" d="M9 3h6l-1 6 4 4H6l4-4z"/><path d="M12 13v8"/>',
    pencil: '<path d="M4 20h4L19 9l-4-4L4 16z"/><path d="M13 7l4 4"/>',
    note: '<path d="M9 18V5l11-2v13"/><circle cx="6.5" cy="18" r="2.5"/><circle cx="17.5" cy="16" r="2.5"/>',
    wifi: '<path d="M2 8.5a15 15 0 0 1 20 0M5 12a10 10 0 0 1 14 0M8.5 15.5a5 5 0 0 1 7 0"/><circle cx="12" cy="19" r="1.2" fill="currentColor"/>',
  };

  // icon is an inline stroke icon (24x24, currentColor).
  // TRLC-LINKS: REQ-SDS-220
  function icon(name, size) {
    const s = size || 22;
    return '<svg viewBox="0 0 24 24" width="' + s + '" height="' + s + '" fill="none" stroke="currentColor" stroke-width="2" ' +
      'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">' + (ICONS[name] || "") + "</svg>";
  }

  // ---- haptics --------------------------------------------------------------------------

  const VIBRATE = { tap: 8, detent: 4, long: 18, confirm: [10, 40, 10], warn: [30, 50, 30] };
  let tickEl = null;

  // haptic gives a vibration where the phone has one (after a real touch:
  // browsers block it before), and always a visible tick in the corner.
  // TRLC-LINKS: REQ-SDS-220
  function haptic(kind) {
    kind = VIBRATE[kind] !== undefined ? kind : "tap";
    const nav = navigator, ua = nav.userActivation;
    if (typeof nav.vibrate === "function" && (!ua || ua.hasBeenActive)) {
      try { nav.vibrate(VIBRATE[kind]); } catch (e) { /* no vibration */ }
    }
    if (!tickEl) {
      tickEl = h("div", { class: "tick", "aria-hidden": "true" });
      doc.body.appendChild(tickEl);
    }
    tickEl.className = "tick " + kind;
    void tickEl.offsetWidth;
    tickEl.className = "tick " + kind + " flash";
  }

  // ---- pressing ----------------------------------------------------------------------------

  const LONG_MS = 500, SLOP = 10;

  // After a long press the lifted finger's click is swallowed wherever it
  // lands: the long press may have re-drawn what was under the finger.
  let swallow = 0;
  doc.addEventListener("pointerup", () => { if (swallow === Infinity) swallow = performance.now() + 500; }, true);
  doc.addEventListener("click", (e) => {
    if (performance.now() < swallow) { e.preventDefault(); e.stopPropagation(); swallow = 0; }
  }, true);

  // press makes a button act on the click its tap (or Enter / Space) makes:
  // a pressed look while the finger is down, a tap tick, and an optional long
  // press (that click is then swallowed). o: {onTap, onLong, haptic}.
  // TRLC-LINKS: REQ-SDS-220
  function press(el, o) {
    let timer = 0, long = false, x0 = 0, y0 = 0;
    // TRLC-LINKS: REQ-SDS-220
    const stop = () => { if (timer) { clearTimeout(timer); timer = 0; } el.classList.remove("down"); };
    el.addEventListener("pointerdown", (e) => {
      if (e.pointerType === "mouse" && e.button !== 0) return;
      long = false;
      x0 = e.clientX; y0 = e.clientY;
      el.classList.add("down");
      if (o.onLong) {
        timer = setTimeout(() => {
          timer = 0;
          if (!el.isConnected || el.disabled) return;
          long = true;
          swallow = Infinity;
          el.classList.remove("down");
          haptic("long");
          o.onLong(e);
        }, LONG_MS);
      }
    });
    el.addEventListener("pointermove", (e) => { if (Math.hypot(e.clientX - x0, e.clientY - y0) > SLOP) stop(); });
    for (const ev of ["pointerup", "pointercancel", "pointerleave"]) el.addEventListener(ev, stop);
    el.addEventListener("click", (e) => {
      if (long) { long = false; e.preventDefault(); e.stopPropagation(); return; }
      if (el.disabled || el.getAttribute("aria-disabled") === "true") return;
      haptic(o.haptic || "tap");
      if (o.onTap) o.onTap(e);
    });
    if (o.onLong) el.addEventListener("contextmenu", (e) => e.preventDefault());
    return el;
  }

  // button builds <button class="btn">: {label, sub, icon, cls, aria, data, onTap, onLong}.
  // setLabel(t) and setSub(t) change its texts.
  // TRLC-LINKS: REQ-SDS-220
  function button(o) {
    const b = h("button", { type: "button", class: "btn" + (o.cls ? " " + o.cls : "") + (o.sub !== undefined ? " col" : ""),
      "aria-label": o.aria || null, data: o.data || null });
    if (o.icon) b.insertAdjacentHTML("beforeend", icon(o.icon, o.iconSize));
    if (o.label !== undefined) b.appendChild(h("span", { class: "lbl", text: o.label }));
    if (o.sub !== undefined) b.appendChild(h("span", { class: "sub", text: o.sub }));
    // TRLC-LINKS: REQ-SDS-220
    b.setLabel = (t) => { const s = b.querySelector(".lbl"); if (s && s.textContent !== t) s.textContent = t; };
    // TRLC-LINKS: REQ-SDS-220
    b.setSub = (t) => { const s = b.querySelector(".sub"); if (s && s.textContent !== t) s.textContent = t; };
    return press(b, o);
  }

  // repeat makes a button step once on a tap and keep stepping while held:
  // the first repeat after 450 ms, then every 110 ms, each with a detent tick.
  // fn(held) runs per step.
  // TRLC-LINKS: REQ-SDS-220
  function repeat(b, fn) {
    let timer = 0, reps = 0;
    // TRLC-LINKS: REQ-SDS-220
    const stop = () => { if (timer) { clearTimeout(timer); timer = 0; } b.classList.remove("down"); };
    b.addEventListener("pointerdown", (e) => {
      if (e.pointerType === "mouse" && e.button !== 0) return;
      reps = 0;
      b.classList.add("down");
      // TRLC-LINKS: REQ-SDS-220
      const again = () => {
        if (!b.isConnected || b.disabled) { stop(); return; }
        reps++;
        haptic("detent");
        fn(true);
        timer = setTimeout(again, 110);
      };
      timer = setTimeout(again, 450);
    });
    for (const ev of ["pointerup", "pointercancel", "pointerleave"]) b.addEventListener(ev, stop);
    b.addEventListener("click", (e) => {
      if (reps) { reps = 0; e.preventDefault(); return; }   // the hold already stepped
      if (b.disabled) return;
      haptic("tap");
      fn(false);
    });
    b.addEventListener("contextmenu", (e) => e.preventDefault());
    return b;
  }

  // holdButton fires only after a press held for ms (a finger, or Space /
  // Enter held); a tap only says how. {label, sub, ms, icon, cls, onConfirm}.
  // TRLC-LINKS: REQ-SDS-220
  function holdButton(o) {
    const ms = o.ms || 1000;
    const b = h("button", { type: "button", class: "btn hold " + (o.cls || "warn") + (o.sub !== undefined ? " col" : ""), style: "--hold-ms:" + ms + "ms",
      "aria-label": o.aria || o.label + " (hold " + ms / 1000 + " s)", data: o.data || null });
    if (o.icon) b.insertAdjacentHTML("beforeend", icon(o.icon));
    b.appendChild(h("span", { class: "lbl", text: o.label }));
    if (o.sub !== undefined) b.appendChild(h("span", { class: "sub", text: o.sub }));
    let timer = 0, done = false, active = false;
    // TRLC-LINKS: REQ-SDS-220
    const start = () => {
      if (b.disabled || active) return;
      active = true; done = false;
      b.classList.add("down", "holding");
      timer = setTimeout(() => {
        timer = 0; done = true;
        b.classList.remove("holding", "down");
        haptic("confirm");
        o.onConfirm();
      }, ms);
    };
    // TRLC-LINKS: REQ-SDS-220
    const end = (explain) => {
      if (!active) return;
      active = false;
      if (timer) { clearTimeout(timer); timer = 0; }
      b.classList.remove("holding", "down");
      if (!done && explain) { haptic("warn"); toast("Hold to confirm: " + o.label + " (" + ms / 1000 + " s)", "err"); }
    };
    b.addEventListener("pointerdown", (e) => { if (e.pointerType === "mouse" && e.button !== 0) return; start(); });
    b.addEventListener("pointerup", () => end(true));
    b.addEventListener("pointercancel", () => end(false));
    b.addEventListener("pointerleave", () => end(false));
    b.addEventListener("keydown", (e) => { if ((e.key === " " || e.key === "Enter") && !e.repeat) { e.preventDefault(); start(); } });
    b.addEventListener("keyup", (e) => { if (e.key === " " || e.key === "Enter") { e.preventDefault(); end(true); } });
    b.addEventListener("click", (e) => e.preventDefault());
    b.addEventListener("contextmenu", (e) => e.preventDefault());
    // TRLC-LINKS: REQ-SDS-220
    b.setLabel = (t) => { b.querySelector(".lbl").textContent = t; };
    return b;
  }

  // drag follows one finger on el (touch-action none): onStart, onMove({dx,
  // dy}), onEnd({dx, dy, moved}). A press on a button inside el is left to
  // the button; the pointer is captured only once a drag has started.
  // TRLC-LINKS: REQ-SDS-220
  function drag(el, o) {
    let id = null, x0 = 0, y0 = 0, on = false;
    el.addEventListener("pointerdown", (e) => {
      if (id !== null || (e.pointerType === "mouse" && e.button !== 0)) return;
      if (!o.buttons && e.target.closest && e.target.closest("button,input") && e.target.closest("button,input") !== el) return;
      id = e.pointerId; x0 = e.clientX; y0 = e.clientY; on = false;
    });
    el.addEventListener("pointermove", (e) => {
      if (e.pointerId !== id) return;
      const dx = e.clientX - x0, dy = e.clientY - y0;
      if (!on && Math.hypot(dx, dy) > (o.slop || SLOP)) {
        on = true;
        try { el.setPointerCapture(id); } catch (err) { /* gone */ }
        if (o.onStart) o.onStart();
      }
      if (on && o.onMove) o.onMove({ dx, dy });
    });
    // TRLC-LINKS: REQ-SDS-220
    const end = (e, cancel) => {
      if (e.pointerId !== id) return;
      const dx = e.clientX - x0, dy = e.clientY - y0, moved = on;
      id = null; on = false;
      if (o.onEnd) o.onEnd({ dx, dy, moved, cancel });
    };
    el.addEventListener("pointerup", (e) => end(e, false));
    el.addEventListener("pointercancel", (e) => end(e, true));
  }

  // ---- the toast --------------------------------------------------------------------------

  let toastEl = null, toastTimer = 0;

  // toast shows a short message (kind "ok" or "err") above the dock, or at
  // the top while a sheet is open.
  // TRLC-LINKS: REQ-SDS-220
  function toast(text, kind, ms) {
    if (!toastEl) {
      toastEl = h("div", { class: "toast", role: "status", "aria-live": "polite", hidden: true });
      doc.body.appendChild(toastEl);
    }
    toastEl.textContent = String(text);
    toastEl.className = "toast" + (kind ? " " + kind : "");
    toastEl.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { toastEl.hidden = true; }, ms || (kind === "err" ? 3600 : 2200));
  }

  // ---- the bottom sheet -------------------------------------------------------------------

  // sheet is the one bottom sheet: a scrim, a grab handle, a title, extra
  // head buttons, a close button; drag the head down or tap the scrim to
  // close, Escape on a keyboard. {el, head, body, open(title), close(),
  // isOpen(), setTitle(t)}. Focus goes into it and back to where it was;
  // o.inert (the page behind) is inert while it is open, so focus and a
  // screen reader stay in the sheet.
  // TRLC-LINKS: REQ-SDS-220
  function sheet(o) {
    const scrim = h("div", { class: "scrim", hidden: true });
    const title = h("h2", { id: "sheet-title" });
    const close = h("button", { type: "button", class: "btn ghost sq sheet-close", "aria-label": "Close", html: icon("close") });
    const tools = h("span", { class: "sheet-tools" });
    const head = h("div", { class: "sheet-head" }, h("div", { class: "grab", "aria-hidden": "true" }), title, tools, close);
    const body = h("div", { class: "sheet-body scroll" });
    const el = h("section", { class: "sheet", role: "dialog", "aria-modal": "true", "aria-labelledby": "sheet-title", hidden: true, tabindex: "-1" }, head, body);
    doc.body.append(scrim, el);
    let open = false, back = null;
    const api = {
      el, head, body, tools,
      // TRLC-LINKS: REQ-SDS-220
      open(t) {
        api.setTitle(t || "");
        if (open) return api;
        open = true;
        back = doc.activeElement;
        scrim.hidden = false; el.hidden = false;
        void el.offsetWidth;
        scrim.classList.add("open"); el.classList.add("open");
        doc.body.classList.add("sheet-on");
        if (o && o.inert) o.inert.inert = true;
        el.focus({ preventScroll: true });
        return api;
      },
      // TRLC-LINKS: REQ-SDS-220
      close() {
        if (!open) return api;
        open = false;
        scrim.classList.remove("open"); el.classList.remove("open");
        el.style.transform = "";
        scrim.hidden = true; el.hidden = true;
        doc.body.classList.remove("sheet-on");
        if (o && o.inert) o.inert.inert = false;
        if (o && o.onClose) o.onClose();
        if (back && back.isConnected && back.focus) back.focus({ preventScroll: true });
        return api;
      },
      // TRLC-LINKS: REQ-SDS-220
      isOpen: () => open,
      // TRLC-LINKS: REQ-SDS-220
      setTitle(t) { title.textContent = t; },
    };
    // TRLC-LINKS: REQ-SDS-220
    press(close, { onTap: () => api.close() });
    scrim.addEventListener("click", () => api.close());
    drag(head, {
      // TRLC-LINKS: REQ-SDS-220
      onStart() { el.classList.add("dragging"); },
      // TRLC-LINKS: REQ-SDS-220
      onMove(d) { el.style.transform = "translateY(" + Math.max(0, d.dy) + "px)"; },
      // TRLC-LINKS: REQ-SDS-220
      onEnd(d) { el.classList.remove("dragging"); el.style.transform = ""; if (d.moved && !d.cancel && d.dy > 80) api.close(); },
    });
    el.addEventListener("keydown", (e) => { if (e.key === "Escape") { e.preventDefault(); api.close(); } });
    return api;
  }

  // ---- keeping the phone awake ------------------------------------------------------------

  // Wake keeps the screen on, in one of three ways, and says which holds:
  //  - the Screen Wake Lock, where the browser has one (secure contexts
  //    only; the scope serves plain HTTP, so mostly not);
  //  - "muted video" (Chromium, Android): a muted video playing behind the
  //    controls. Chromium holds the screen for a muted video only while it is
  //    on screen and covers at least a fifth of it (its VideoWakeLock: 75 %
  //    in the viewport, 20 % of the viewport's area; opacity and what covers
  //    it do not count), so it fills the screen behind the page;
  //  - "silent sound" (WebKit, the iPhone, which holds the screen only for a
  //    video with an audio track that is not muted and not looping; or any
  //    phone with Stronger keep-awake on): the same video unmuted, its sound
  //    track silent; navigator.audioSession "ambient" (where the browser has
  //    it) mixes it with other apps' music instead of stopping it.
  // The video seeks back before its end instead of looping (WebKit). The
  // first time a video with sound starts, a notice says so, once.
  const Wake = {
    want: false, how: "", note: "", sentinel: null, video: null, fns: [], pending: null,
    // TRLC-LINKS: REQ-SDS-220
    on(fn) { this.fns.push(fn); },
    // TRLC-LINKS: REQ-SDS-220
    emit() { for (const f of this.fns) f(this.state()); },
    // state: {on, how ("wake lock" | "muted video" | "silent sound" | ""), text, note}
    // TRLC-LINKS: REQ-SDS-220
    state() {
      const how = this.how;
      return { on: !!how, how, text: how ? "awake: " + how : "off: tap to keep awake", note: this.note };
    },
    // wanted: on unless the user turned it off (the owner's phone must not lock)
    // TRLC-LINKS: REQ-SDS-220
    wanted() { return this.want || load("wake", true); },
    // strong: Stronger keep-awake, the silent sound track on any phone
    // TRLC-LINKS: REQ-SDS-220
    strong() { return load("wake-strong", false) === true; },
    // sound: the video plays with its (silent) sound track
    // TRLC-LINKS: REQ-SDS-220
    sound() { return isWebKit() || this.strong(); },
    // TRLC-LINKS: REQ-SDS-220
    lockAvailable() { return "wakeLock" in navigator && !!window.isSecureContext && navigator.wakeLock && typeof navigator.wakeLock.request === "function"; },
    // enable starts keeping awake; a start under way is the one answered.
    // The state it answers has told: true when it showed the notice.
    // TRLC-LINKS: REQ-SDS-220
    enable() {
      if (!this.pending) this.pending = this.start().finally(() => { this.pending = null; });
      return this.pending;
    },
    // TRLC-LINKS: REQ-SDS-220
    async start() {
      this.want = true;
      store("wake", true);
      this.note = "";
      if (this.lockAvailable()) {
        try {
          const s = await navigator.wakeLock.request("screen");
          this.sentinel = s;
          s.addEventListener("release", () => {
            if (this.sentinel === s) this.sentinel = null;
            if (this.how === "wake lock") { this.how = ""; this.note = "The browser released the lock; it is asked for again when the page shows."; this.emit(); }
          });
          this.how = "wake lock";
          this.emit();
          return this.state();
        } catch (e) {
          this.note = "Wake lock refused (" + ((e && e.message) || e) + "); using the video instead.";
        }
      }
      const v = this.ensureVideo(), sound = this.sound();
      let told = false;
      v.muted = !sound;
      if (sound && navigator.audioSession) {
        try { navigator.audioSession.type = "ambient"; } catch (e) { /* the browser's own choice */ }
      }
      try {
        await v.play();
        this.how = sound ? "silent sound" : "muted video";
        if (sound && !load("wake-sound-told", false)) {
          store("wake-sound-told", true);
          told = true;
          toast("Keep-awake now plays a video with a silent sound track: nothing is heard, but some phones pause other apps' music for it. Keys, This phone, has the choices.", "", 9000);
        }
      } catch (e) {
        this.how = "";
        this.note = "The browser would not play the keep-awake video (" + ((e && e.name) || e) + "): tap to try again.";
      }
      this.emit();
      return Object.assign(this.state(), { told });
    },
    // TRLC-LINKS: REQ-SDS-220
    disable() {
      this.want = false;
      store("wake", false);
      if (this.sentinel) { const s = this.sentinel; this.sentinel = null; s.release().catch(() => {}); }
      if (this.video) this.video.pause();
      this.how = ""; this.note = "";
      this.emit();
    },
    // setStrong turns Stronger keep-awake on or off; a video keeping the
    // phone awake the other way starts again the new way (also one the same
    // tap was starting)
    // TRLC-LINKS: REQ-SDS-220
    async setStrong(on) {
      store("wake-strong", !!on);
      if (this.pending) await this.pending;
      if (this.how && this.how !== "wake lock" && this.video && this.video.muted === this.sound()) {
        this.how = "";   // the pause this makes is not the browser's
        this.video.pause();
        return this.enable();
      }
      this.emit();
      return this.state();
    },
    // TRLC-LINKS: REQ-SDS-220
    ensureVideo() {
      if (this.video) return this.video;
      const v = h("video", { playsinline: "", "webkit-playsinline": "", preload: "auto", title: "Keep awake", "aria-hidden": "true", tabindex: "-1", class: "awake-video" });
      v.appendChild(h("source", { src: "remote-awake.webm", type: "video/webm" }));
      v.appendChild(h("source", { src: "remote-awake.mp4", type: "video/mp4" }));
      // seek back before the end: a looping video does not keep the screen on everywhere
      v.addEventListener("timeupdate", () => { if (v.currentTime > 0.5) v.currentTime = 0.05 + Math.random() * 0.1; });
      v.addEventListener("ended", () => { if (this.want) v.play().catch(() => {}); });
      v.addEventListener("pause", () => {
        if (this.how && this.how !== "wake lock") { this.how = ""; this.note = "The browser paused the keep-awake video: tap to resume."; this.emit(); }
      });
      doc.body.appendChild(v);
      this.video = v;
      return v;
    },
    // resume after the page shows again (the lock is released and the
    // video paused while it is hidden); the video may need the next tap
    // TRLC-LINKS: REQ-SDS-220
    async resume() {
      if (!this.want || this.how || this.pending) return;
      if (this.lockAvailable()) { await this.enable(); return; }
      if (this.video) {
        try { await this.video.play(); this.how = this.video.muted ? "muted video" : "silent sound"; this.note = ""; this.emit(); } catch (e) { /* the next tap */ }
      }
    },
  };
  doc.addEventListener("visibilitychange", () => { if (doc.visibilityState === "visible") Wake.resume(); });
  // the first tap (and any tap after the browser stopped it) starts keep-awake
  // unless the user turned it off: play() and the lock need a user gesture
  // TRLC-LINKS: REQ-SDS-220
  const kick = () => { if (Wake.wanted() && !Wake.how && !Wake.pending) Wake.enable(); };
  doc.addEventListener("touchend", kick, true);
  doc.addEventListener("click", kick, true);

  // ---- fullscreen --------------------------------------------------------------------------

  // TRLC-LINKS: REQ-SDS-220
  function isIOS() {
    const n = navigator;
    return /iPhone|iPad|iPod/.test(n.userAgent || "") || (n.platform === "MacIntel" && n.maxTouchPoints > 1);
  }

  // isWebKit: Safari, and every browser on an iPhone (they all use WebKit)
  // TRLC-LINKS: REQ-SDS-220
  function isWebKit() {
    const ua = navigator.userAgent || "";
    return isIOS() || (/Safari\//.test(ua) && !/Chrome|Chromium|Android|Edg\//.test(ua));
  }

  // Full is fullscreen from a tap (Android and desktop browsers). An iPhone
  // has no Fullscreen API for pages: Add to Home Screen opens the remote
  // without browser bars (the page carries the manifest and the meta tags).
  const Full = {
    note: "", fns: [],
    // TRLC-LINKS: REQ-SDS-220
    on(fn) { this.fns.push(fn); },
    // TRLC-LINKS: REQ-SDS-220
    emit() { for (const f of this.fns) f(this.state()); },
    // TRLC-LINKS: REQ-SDS-220
    supported() {
      const e = doc.documentElement;
      return !!(e.requestFullscreen || e.webkitRequestFullscreen) && (doc.fullscreenEnabled === true || doc.webkitFullscreenEnabled === true);
    },
    // TRLC-LINKS: REQ-SDS-220
    standalone() { return !!(window.matchMedia && window.matchMedia("(display-mode: standalone)").matches) || navigator.standalone === true; },
    // TRLC-LINKS: REQ-SDS-220
    active() { return !!(doc.fullscreenElement || doc.webkitFullscreenElement); },
    // state: {on, can, text}
    // TRLC-LINKS: REQ-SDS-220
    state() {
      if (this.active()) return { on: true, can: true, text: "on" };
      if (this.standalone()) return { on: true, can: false, text: "on: opened from the home screen" };
      if (!this.supported()) {
        return { on: false, can: false, text: isIOS() ? "not on iPhone: Share, Add to Home Screen opens it without bars" : "not in this browser" };
      }
      return { on: false, can: true, text: this.note ? "refused: " + this.note : "off" };
    },
    // TRLC-LINKS: REQ-SDS-220
    async toggle() {
      this.note = "";
      if (!this.supported()) { this.emit(); return this.state(); }
      try {
        if (this.active()) {
          const ex = doc.exitFullscreen || doc.webkitExitFullscreen;
          await ex.call(doc);
        } else {
          const e = doc.documentElement, req = e.requestFullscreen || e.webkitRequestFullscreen;
          await req.call(e, { navigationUI: "hide" });
        }
      } catch (err) {
        this.note = (err && err.message) || String(err);
      }
      this.emit();
      return this.state();
    },
  };
  doc.addEventListener("fullscreenchange", () => Full.emit());
  doc.addEventListener("webkitfullscreenchange", () => Full.emit());

  // ---- small storage (a private window may have none) -------------------------------------

  // TRLC-LINKS: REQ-SDS-220
  function load(k, def) {
    try { const v = localStorage.getItem("sds-remote:" + k); return v === null ? def : JSON.parse(v); } catch (e) { return def; }
  }
  // TRLC-LINKS: REQ-SDS-220
  function store(k, v) {
    try { if (v === undefined) localStorage.removeItem("sds-remote:" + k); else localStorage.setItem("sds-remote:" + k, JSON.stringify(v)); } catch (e) { /* none */ }
  }

  window.RU = { h, add, icon, haptic, press, button, repeat, holdButton, drag, toast, sheet, Wake, Full, isIOS, isWebKit, load, store };
})();
