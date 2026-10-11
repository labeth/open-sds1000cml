// ENGMODEL-OWNER-UNIT: FU-WEB-APP-UI
// app_ui.js — the workspace layout's UI primitives: segmented controls that
// behave like a <select>, mirrors (a second live view of one control, so the
// strip, a pane and All settings can show the same setting without a second
// copy of its logic), popovers, the toast and canvas fitting. Classic script,
// loaded right after app.js; it only touches the DOM it is handed.
"use strict";

// ---- segmented controls -------------------------------------------------------
// <div class="seg" id="cpl1" data-value="0"><button data-value="0">DC</button>…</div>
// segInit gives the element a select-like `value` (string) and makes a click on
// an option set it and fire a bubbling "change", so `el.onchange = …` handlers
// written for a <select> keep working. Left/Right move the choice like a radio
// group. The options are radio buttons inside a radiogroup for assistive tech.
// TRLC-LINKS: REQ-SDS-219
function segInit(el) {
  if (!el || el._seg) return;
  el._seg = true;
  if (!el.getAttribute("role")) el.setAttribute("role", "radiogroup");
  let cur = el.dataset.value != null ? String(el.dataset.value) : null;
  const opts = () => el.querySelectorAll(":scope > button[data-value]");
  for (const b of opts()) { b.type = "button"; b.setAttribute("role", "radio"); }
  const paint = () => {
    for (const b of opts()) {
      const on = b.dataset.value === cur;
      b.classList.toggle("on", on);
      b.setAttribute("aria-checked", on ? "true" : "false");
      b.tabIndex = on || (cur === null && b === opts()[0]) ? 0 : -1;
    }
  };
  Object.defineProperty(el, "value", {
    configurable: true,
    get() { return cur; },
    set(v) { cur = v == null ? null : String(v); paint(); },
  });
  // TRLC-LINKS: REQ-SDS-219
  const pick = (b) => {
    if (!b || b.disabled) return;
    const changed = b.dataset.value !== cur;
    el.value = b.dataset.value;
    el._userAt = performance.now();
    if (changed || el.hasAttribute("data-retrigger")) el.dispatchEvent(new Event("change", { bubbles: true }));
  };
  el.addEventListener("click", (ev) => {
    const b = ev.target.closest("button[data-value]");
    if (b && b.parentElement === el) pick(b);
  });
  el.addEventListener("keydown", (ev) => {
    if (ev.key !== "ArrowLeft" && ev.key !== "ArrowRight") return;
    const list = [...opts()].filter((b) => !b.disabled);
    const i = list.findIndex((b) => b.dataset.value === cur);
    const j = i < 0 ? 0 : (i + (ev.key === "ArrowRight" ? 1 : -1) + list.length) % list.length;
    ev.preventDefault(); ev.stopPropagation();
    pick(list[j]); list[j].focus();
  });
  paint();
}

// segNext moves a seg to its next option (wrapping) as a user choice — the
// keyboard shortcuts that toggle a two-way setting (AUTO/NORM, C1/C2, slope).
// TRLC-LINKS: REQ-SDS-219
function segNext(el) {
  const list = [...el.querySelectorAll(":scope > button[data-value]")].filter((b) => !b.disabled);
  if (!list.length) return;
  const i = list.findIndex((b) => b.dataset.value === el.value);
  list[(i + 1) % list.length].click();
}

// setFromStatus writes an instrument-status value into a control unless the
// user is editing it (focused select/input) or changed it in the last 1.5 s: a
// status reply that left the device before the change would flip it back.
// TRLC-LINKS: REQ-SDS-219
function setFromStatus(el, v) {
  if (!el || v === undefined || v === null) return;
  if (document.activeElement === el) return;
  if (el._userAt && performance.now() - el._userAt < 1500) return;
  const s = String(v);
  if (el.value !== s) el.value = s;
}

// ---- mirrors ----------------------------------------------------------------------
// A mirror is a second, live view of a control: it shows the source's value and
// forwards a user edit to the source (setting its value and firing the source's
// own input/change/click), so the source's handler stays the one place a setting
// is applied. The strip holds the sources of the controls that also appear in a
// pane; All settings is built entirely from mirrors.
const MIRRORS = [];
let mirrorRaf = 0;

// TRLC-LINKS: REQ-SDS-219
function mirrorOf(src, opts) {
  opts = opts || {};
  if (typeof src === "string") src = $(src);
  if (!src) return document.createElement("span");
  let m;
  const kind = src.classList.contains("seg") ? "seg" : src.tagName === "SELECT" ? "select" : src.tagName === "INPUT" ? "input:" + (src.type || "text") :
    src.tagName === "BUTTON" ? "button" : "text";
  if (kind === "text") { // a readout: its text, live
    m = document.createElement("span");
    m.className = src.className;
    const rec = { m, src, kind, label: null };
    MIRRORS.push(rec);
    mirrorSync(rec);
    return m;
  }
  if (kind === "seg") {
    m = document.createElement("div");
    m.className = src.className;
    for (const b of src.querySelectorAll(":scope > button[data-value]")) {
      const c = document.createElement("button");
      c.dataset.value = b.dataset.value;
      c.innerHTML = b.innerHTML;
      if (b.className) c.className = b.className.replace(/\bon\b/, "").trim();
      for (const a of ["title", "aria-label"]) if (b.getAttribute(a)) c.setAttribute(a, b.getAttribute(a));
      m.appendChild(c);
    }
    m.dataset.value = src.value == null ? "" : src.value;
    segInit(m);
    m.addEventListener("change", (ev) => {
      ev.stopPropagation();
      const b = src.querySelector(`:scope > button[data-value="${CSS.escape(m.value)}"]`);
      if (b) b.click();
    });
  } else if (kind === "select") {
    m = document.createElement("select");
    m.className = src.className;
    m.addEventListener("change", () => {
      src.value = m.value;
      src.dispatchEvent(new Event("change", { bubbles: true }));
    });
    if (src._knob) knobWheel(m, src._knob); // the time/div and V/div wheel rule follows the copy
  } else if (kind.startsWith("input:")) {
    m = document.createElement("input");
    m.type = src.type;
    m.className = src.className;
    for (const a of ["min", "max", "step", "placeholder", "inputMode"]) if (src[a] !== undefined && src[a] !== "") m[a] = src[a];
    if (src.type === "checkbox") {
      m.addEventListener("change", () => { src.checked = m.checked; src.dispatchEvent(new Event("change", { bubbles: true })); });
    } else {
      m.addEventListener("input", () => { src.value = m.value; src.dispatchEvent(new Event("input", { bubbles: true })); });
      m.addEventListener("change", () => { src.value = m.value; src.dispatchEvent(new Event("change", { bubbles: true })); });
      m.addEventListener("keydown", (ev) => {
        if (ev.key !== "Enter") return;
        src.value = m.value; src.dispatchEvent(new Event("change", { bubbles: true }));
      });
    }
  } else {
    m = document.createElement("button");
    m.type = "button";
    m.className = src.className;
    m.addEventListener("click", () => src.click());
  }
  if (opts.label) m.textContent = opts.label;
  const lab = opts.ariaLabel || src.getAttribute("aria-label") || src.getAttribute("title") || src.textContent.trim();
  if (lab && kind !== "seg") m.setAttribute("aria-label", lab);
  else if (lab) m.setAttribute("aria-label", lab);
  if (src.title && !m.title) m.title = src.title;
  m.classList.add("mirror-of");
  m.dataset.mirrorOf = src.id;
  const rec = { m, src, kind, label: opts.label || null };
  MIRRORS.push(rec);
  mirrorSync(rec);
  return m;
}

// TRLC-LINKS: REQ-SDS-219
function mirrorSync(r) {
  const { m, src, kind } = r;
  if (!src.isConnected) return;
  if (kind === "text") { if (m.textContent !== src.textContent) m.textContent = src.textContent; return; }
  if (kind === "seg") {
    if (m.value !== src.value) m.value = src.value;
    for (const b of m.querySelectorAll(":scope > button[data-value]")) {
      const sb = src.querySelector(`:scope > button[data-value="${CSS.escape(b.dataset.value)}"]`);
      if (sb) b.disabled = sb.disabled;
    }
  } else if (kind === "select") {
    if (m.options.length !== src.options.length || (src.options.length && m.options[0].value !== src.options[0].value)) {
      m.innerHTML = "";
      for (const o of src.options) { const c = document.createElement("option"); c.value = o.value; c.textContent = o.textContent; m.appendChild(c); }
    }
    if (document.activeElement !== m && m.value !== src.value) m.value = src.value;
    m.disabled = src.disabled;
  } else if (kind.startsWith("input:")) {
    if (kind === "input:checkbox") m.checked = src.checked;
    else {
      for (const a of ["min", "max", "step"]) if (src[a] !== m[a]) m[a] = src[a];
      if (document.activeElement !== m && m.value !== src.value) m.value = src.value;
    }
    m.disabled = src.disabled;
  } else {
    const on = src.classList.contains("on");
    m.classList.toggle("on", on);
    if (src.hasAttribute("aria-pressed")) m.setAttribute("aria-pressed", src.getAttribute("aria-pressed"));
    m.disabled = src.disabled;
    // a copy labelled "On" is an on/off switch: it says which
    const t = r.label === "On" ? (on ? "On" : "Off") : r.label || src.textContent;
    if (m.textContent !== t) m.textContent = t;
  }
}

// TRLC-LINKS: REQ-SDS-219
function syncMirrors() { mirrorRaf = 0; for (const r of MIRRORS) mirrorSync(r); }
// TRLC-LINKS: REQ-SDS-219
function kickMirrors() { if (!mirrorRaf) mirrorRaf = requestAnimationFrame(syncMirrors); }

// mirrorInit replaces every <span class="mirror" data-src="id" [data-label]>
// placeholder in the page with a mirror of that control.
// TRLC-LINKS: REQ-SDS-219
function mirrorInit(root) {
  for (const ph of (root || document).querySelectorAll("span.mirror[data-src]")) {
    const m = mirrorOf(ph.dataset.src, { label: ph.dataset.label, ariaLabel: ph.dataset.aria });
    if (ph.dataset.cls) m.classList.add(...ph.dataset.cls.split(" "));
    ph.replaceWith(m);
  }
}

// Any user edit anywhere can change a mirrored source: refresh on the next frame.
for (const t of ["click", "change", "input", "keyup"]) document.addEventListener(t, kickMirrors, true);

// ---- popovers -------------------------------------------------------------------
let popOpenEl = null, popAnchor = null;
// TRLC-LINKS: REQ-SDS-219
function popClose() {
  if (!popOpenEl) return;
  popOpenEl.hidden = true;
  if (popAnchor) popAnchor.setAttribute("aria-expanded", "false");
  const back = popAnchor;
  popOpenEl = null; popAnchor = null;
  return back;
}
// TRLC-LINKS: REQ-SDS-219
function popToggle(pop, anchor) {
  if (popOpenEl === pop) { popClose(); return; }
  popClose();
  pop.hidden = false;
  const r = anchor.getBoundingClientRect();
  const w = pop.offsetWidth;
  pop.style.top = Math.round(r.bottom + 6) + "px";
  pop.style.left = Math.round(Math.max(8, Math.min(window.innerWidth - w - 8, r.right - w))) + "px";
  anchor.setAttribute("aria-expanded", "true");
  popOpenEl = pop; popAnchor = anchor;
  const f = pop.querySelector("button:not([disabled]), select, input");
  if (f) f.focus();
}
document.addEventListener("pointerdown", (ev) => {
  if (popOpenEl && !popOpenEl.contains(ev.target) && !(popAnchor && popAnchor.contains(ev.target))) popClose();
}, true);

// ---- toast ------------------------------------------------------------------------
let toastT = 0;
// toast shows a one-line notice at the bottom of the workspace for a few seconds
// (kind "warn" or "err" tints it). It is also an aria-live announcement.
// TRLC-LINKS: REQ-SDS-219
function toast(msg, kind) {
  const el = $("toast");
  if (!el) return;
  el.textContent = msg;
  el.className = "toast" + (kind ? " is-" + kind : "");
  el.hidden = false;
  clearTimeout(toastT);
  toastT = setTimeout(() => { el.hidden = true; }, Math.max(2600, Math.min(7000, msg.length * 70)));
}

// ---- canvases -------------------------------------------------------------------
// fitCanvas sizes a card canvas's backing store to its CSS box × devicePixelRatio
// (pane resizes change the box). Returns false while it is not laid out (hidden),
// so the caller skips drawing instead of rendering into a 0×0 store.
// TRLC-LINKS: REQ-SDS-219
function fitCanvas(cv) {
  if (!cv) return false;
  const r = cv.getBoundingClientRect();
  if (r.width < 4 || r.height < 4) return false;
  const d = window.devicePixelRatio || 1;
  const w = Math.round(r.width * d), h = Math.round(r.height * d);
  if (cv.width !== w) cv.width = w;
  if (cv.height !== h) cv.height = h;
  return true;
}

// The time/div and V/div selects step like the scope's knobs and the plot's
// wheel: wheel up = a bigger trace (faster timebase, more sensitive scale).
// TRLC-LINKS: REQ-SDS-219
function knobWheel(el, step) {
  el.addEventListener("wheel", (ev) => {
    const d = ev.deltaY || ev.deltaX;
    if (!d) return;
    ev.preventDefault();
    step(d < 0 ? -1 : 1);
  }, { passive: false });
}

// TRLC-LINKS: REQ-SDS-219
function stepSelect(sel, dir, control) {
  if (!sel || !sel.options.length) return;
  const i = Math.max(0, Math.min(sel.options.length - 1, sel.selectedIndex + dir));
  if (i === sel.selectedIndex) return;
  sel.selectedIndex = i;
  sel._userAt = performance.now();
  send(control, +sel.value);
  kickMirrors();
}
