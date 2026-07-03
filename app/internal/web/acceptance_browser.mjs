// Acceptance suite for the scope web-UI paths NOT already covered by the
// decode/fft/deepmem drivers: boot/liveness, acquire, trigger, vertical/
// horizontal, cursors, X-Y, export, and mode-driven panel visibility. Driven
// through the shared page-object (scope_po.mjs) against httptest+fakeScope.
// Captures TODAY's behavior so the design-system refactor can't regress it.
// SKIP/exit 0 when the browser is unavailable; ALL PASS/exit 1 otherwise.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2]);
  t.browser = browser;
  // selects (tdiv/vdiv) are filled by the status poll — wait for it once.
  await po.waitFor(() => document.getElementById("tdiv").options.length > 0);

  // --- boot / liveness -------------------------------------------------------
  t.ok((await po.eval(() => getComputedStyle(document.body).getPropertyValue("--c1").trim())).length > 0,
    "tokens.css loaded (palette custom properties resolve)");
  t.ok((await po.statusLine() || "").length > 0, "status line shows a connection state");
  t.ok(await po.eval(() => typeof frame !== "undefined" && !!frame.c1), "a live frame is present");
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("stopped")).display) === "none",
    "STOPPED overlay hidden while running");

  // --- acquire (RUN / SINGLE) ------------------------------------------------
  // the button shows the ACTION you can take, not the current state
  t.ok((await po.text("run")).includes("STOP") && await po.hasClass("run", "is-stop"),
    "while running, the button offers STOP");
  await po.click("run"); // stop
  t.ok((await po.text("run")).includes("RUN") && await po.hasClass("run", "is-run"),
    "after stopping, the button offers RUN (the correct way round)");
  await po.click("run"); // run again
  t.ok((await po.text("run")).includes("STOP") && await po.hasClass("run", "is-stop"),
    "running again offers STOP");
  await po.click("single");
  t.ok(await po.hasClass("single", "on"), "SINGLE arms (on state)");
  // AUTOSET: pick a timebase from the measured frequency + EDGE/AUTO/running
  const as = await po.eval(() => {
    const f = frame.m1 ? frame.m1.freq : 0;
    autoset();
    return { f, tdiv: st.tdiv_s, tdivs: st.tdivs, trigType: st.trig_type, norm: st.norm, running: st.running };
  });
  t.ok(as.f > 0, `signal has a measured frequency for autoset (${Math.round(as.f)} Hz)`);
  const target = 0.3 / as.f, exp = as.tdivs.reduce((a, b) => Math.abs(b - target) < Math.abs(a - target) ? b : a);
  t.ok(Math.abs(as.tdiv - exp) < 1e-15, `autoset sets time/div to ~3 cycles (${as.tdiv}s)`);
  t.ok(as.trigType === 0 && as.norm === false && as.running === true, "autoset selects EDGE / AUTO / running");

  // --- trigger ---------------------------------------------------------------
  const mode0 = await po.text("mode");
  await po.click("mode");
  t.ok(await po.text("mode") !== mode0, `trig mode toggles (${mode0} -> ${await po.text("mode")})`);
  const src0 = await po.text("source");
  await po.click("source");
  t.ok(await po.text("source") !== src0, `trig source toggles (${src0} -> ${await po.text("source")})`);
  await po.setSelect("ttype", "1"); // PULSE
  t.ok(await po.isCardVisible("qualrow") && await po.isCardVisible("qp-pulse"),
    "PULSE type reveals the pulse qualifier panel");
  await po.setSelect("ttype", "2"); // SLOPE
  t.ok(await po.isCardVisible("qp-slope") && !(await po.isCardVisible("qp-pulse")),
    "switching to SLOPE reveals slope, hides pulse");
  await po.setSelect("ttype", "0"); // EDGE
  t.ok(!(await po.isCardVisible("qualrow")), "EDGE hides the qualifier panel");
  await po.setRange("lvl", 1.5);
  t.ok((await po.text("lvlv") || "").includes("1.5"), "trigger level readout tracks the slider");

  // --- vertical / horizontal -------------------------------------------------
  await po.click("tC1");
  t.ok(!(await po.hasClass("tC1", "on")), "C1 enable toggles off");
  await po.click("tC1");
  t.ok(await po.hasClass("tC1", "on"), "C1 enable toggles back on");
  const tdivOpts = await po.eval(() => document.getElementById("tdiv").options.length);
  t.ok(tdivOpts > 3, `time/div select is populated (${tdivOpts} detents)`);

  // --- cursors ---------------------------------------------------------------
  await po.click("tCursors");
  t.ok(await po.isCardVisible("curCard") && await po.hasClass("tCursors", "on"),
    "cursors on reveals the Cursors card");
  await po.click("tCursors");
  t.ok(!(await po.isCardVisible("curCard")), "cursors off hides the Cursors card");

  // --- view modes + panel visibility ----------------------------------------
  await po.setMode("FFT");
  t.ok(await po.isCardVisible("fftCardC1") && await po.isCardVisible("fftCardC2"),
    "FFT mode shows both per-channel FFT boxes");
  t.ok(await po.hasClass("mFFT", "on"), "FFT mode button active");
  await po.setMode("XY");
  t.ok(!(await po.isCardVisible("fftCardC1")) && await po.hasClass("mXY", "on"),
    "X-Y mode hides FFT boxes, activates X-Y");
  await po.setMode("YT");
  t.ok(await po.hasClass("mYT", "on") && await po.isCardVisible("fftCardC1"),
    "Y-T mode active; FFT boxes also shown in the time view (peaks-in-time feature)");

  // --- export (PNG data URL + CSV) ------------------------------------------
  t.ok((await po.eval(() => document.getElementById("scope").toDataURL("image/png"))).startsWith("data:image/png"),
    "canvas exports a PNG data URL");
  await po.click("eCSV"); // must not throw
  t.ok(true, "CSV export click completes without error");

  // --- accessibility ---------------------------------------------------------
  t.ok(await po.eval(() => document.getElementById("hstat").getAttribute("aria-live")) === "polite",
    "status region is an aria-live=polite announcer");
  t.ok(await po.eval(() => document.getElementById("scope").getAttribute("role")) === "img"
    && (await po.eval(() => document.getElementById("scope").getAttribute("aria-label")) || "").includes("trigger"),
    "canvas is role=img with a live trigger-state label");
  await po.click("tCursors");
  t.ok(await po.eval(() => document.getElementById("tCursors").getAttribute("aria-pressed")) === "true",
    "toggling a control updates its aria-pressed");
  await po.click("tCursors");
  t.ok(await po.eval(() => document.getElementById("tCursors").getAttribute("aria-pressed")) === "false",
    "aria-pressed clears when toggled off");
  t.ok((await po.eval(() => document.getElementById("lvl").getAttribute("aria-label")) || "").length > 0,
    "the trigger-level slider has an accessible label");

  // --- trigger-state chip (mirrors the LCD state machine) --------------------
  const chip = await po.text("trigChip");
  t.ok(["AUTO", "NORM", "SNGL", "WAIT", "T'D", "STOP"].includes(chip),
    `header shows a trigger-state chip mirroring the LCD (${chip})`);
  t.ok((await po.text("run")).includes("▶") || (await po.text("run")).includes("■"),
    "RUN/STOP button carries a redundant glyph (not colour-only)");

  // --- keyboard shortcuts ----------------------------------------------------
  const curBefore = await po.isCardVisible("curCard");
  await po.page.keyboard.press("c");                 // toggle cursors via keyboard
  t.ok(await po.isCardVisible("curCard") !== curBefore, "keyboard 'c' toggles cursors");
  await po.page.keyboard.press("c");                 // back
  await po.page.keyboard.press("Shift+Slash");       // '?' opens help
  t.ok(await po.isCardVisible("help"), "'?' opens the keyboard-help overlay");
  await po.page.keyboard.press("Escape");
  t.ok(!(await po.isCardVisible("help")), "Escape closes the help overlay");
  // suppressed while a form control is focused (use the always-visible level slider)
  await po.page.focus("#lvl");
  const modeBefore = await po.hasClass("mFFT", "on");
  await po.page.keyboard.press("f");                 // must NOT switch to FFT while a control is focused
  t.ok(await po.hasClass("mFFT", "on") === modeBefore, "shortcuts are suppressed while a form control is focused");
  await po.page.evaluate(() => document.activeElement.blur());

  // --- collapsible panels + math + mouse gestures ----------------------------
  await po.eval(() => document.querySelector("#measCard h3").click());
  t.ok(await po.eval(() => document.getElementById("measCard").classList.contains("collapsed")),
    "clicking a card title minimises it");
  await po.eval(() => document.querySelector("#measCard h3").click());
  t.ok(!(await po.eval(() => document.getElementById("measCard").classList.contains("collapsed"))),
    "clicking the title again expands it");
  await po.click("panelToggle");
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("dock")).display) === "none",
    "the panel toggle collapses the whole dock (scope fills the width)");
  await po.click("panelToggle");
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("dock")).display) !== "none",
    "toggling again restores the dock");
  await po.setSelect("mathFn", "c1-c2");
  t.ok(await po.eval(() => mathFn === "c1-c2"), "math function selectable (C1 − C2)");
  await po.setSelect("mathFn", "off");
  // FFT carrier removal: select a C1 tone, subtract it, the residual shrinks
  await po.setMode("FFT"); await po.wait(400);
  await po.eval(() => { const pk = fftCh[1].peaks.slice().sort((a, b) => b.db - a.db)[0]; if (pk) togglePeakCh(1, pk.freq); });
  await po.setMode("YT"); await po.wait(200);
  const cFull = await po.eval(() => { let mn = 999, mx = -999; for (const v of frame.c1) if (v >= 0) { mn = Math.min(mn, v); mx = Math.max(mx, v); } return mx - mn; });
  await po.setSelect("mathFn", "res1"); await po.wait(200);
  const cRes = await po.eval(() => { const m = computeMath(); if (!m) return null; let mn = 999, mx = -999; for (const v of m) if (v >= 0) { mn = Math.min(mn, v); mx = Math.max(mx, v); } return mx - mn; });
  t.ok(cRes != null && cRes < cFull * 0.9, `removing a selected FFT peak reduces the residual (carrier removal): ${cFull} → ${cRes}`);
  // math must still render under persistence (the persist path now draws it too)
  await po.eval(() => { view.persist = true; });
  await po.setSelect("mathFn", "c1-c2"); await po.wait(120);
  await po.eval(() => { view.persist = false; }); await po.setSelect("mathFn", "off");
  // double-click resets zoom cleanly (no cursor yank / no level nudge)
  await po.eval(() => { view.win.a = 0.3; view.win.b = 0.55; userZoomed = true; redraw(); });
  const lvl0 = await po.eval(() => st.trig_volts);
  const gc = await po.eval(() => { const r = scope.getBoundingClientRect(); return { x: r.left + r.width * 0.5, y: r.top + r.height * 0.5 }; });
  await po.page.mouse.dblclick(gc.x, gc.y); await po.wait(150);
  t.ok(await po.eval(() => userZoomed === false), "double-click in the trace area resets the zoom");
  t.ok(Math.abs((await po.eval(() => st.trig_volts)) - lvl0) < 0.2, "double-click doesn't nudge the trigger level (no marker side-effect)");
  // time/div LABEL stays the hardware value at ANY zoom; zoom instead spreads the
  // grid dividers (the visible-division count changes, not the label).
  await po.eval(() => goHome());
  await po.wait(60);
  const tdHome = await po.eval(() => document.getElementById("line").textContent.split(" · ")[0]);
  const divsHome = await po.eval(() => (view.win.b - view.win.a) * frame.col_span_s / frame.tdiv_s);
  await po.eval(() => { const c = (view.win.a + view.win.b) / 2, h = (view.win.b - view.win.a) / 4; view.win.a = c - h; view.win.b = c + h; userZoomed = true; redraw(); }); // 2× zoom
  const lineZoom = await po.text("line");
  const divsZoom = await po.eval(() => (view.win.b - view.win.a) * frame.col_span_s / frame.tdiv_s);
  t.ok(lineZoom.split(" · ")[0] === tdHome, `time/div label stays the hardware value at any zoom (${tdHome})`);
  t.ok(lineZoom.includes("zoom ×"), "a zoom factor is shown");
  t.ok(Math.abs(divsZoom - divsHome / 2) < 0.5, `zoom 2× halves the visible grid divisions (${divsHome.toFixed(1)} → ${divsZoom.toFixed(1)}) — dividers spread 2×`);
  await po.eval(() => goHome());
  t.ok(!(await po.text("line")).includes("zoom ×"), "returning home clears the zoom factor");
  await po.page.mouse.move(3, 3); await po.wait(400); // break the double-click sequence before the drag test below
  // Ctrl+wheel steps the timebase
  const td0 = await po.eval(() => document.getElementById("tdiv").selectedIndex);
  await po.eval(() => { const e = new WheelEvent("wheel", { deltaY: 120, ctrlKey: true, bubbles: true, cancelable: true }); document.getElementById("scope").dispatchEvent(e); });
  await po.wait(120);
  t.ok(await po.eval(() => document.getElementById("tdiv").selectedIndex) !== td0,
    "Ctrl+wheel changes the timebase (time/div)");

  // --- direct manipulation: drag the trigger-level handle on the display ------
  // The whole point of the UX fix — a vertical quantity is moved VERTICALLY, in
  // the direction you drag, not by a sideways slider.
  await po.setMode("YT");
  const g0 = await po.eval(() => {
    const r = scope.getBoundingClientRect();
    const vpc = (st.trig_source === 1 ? frame.vpc2 : frame.vpc1) || (1 / 32);
    return { left: r.left, top: r.top, w: r.width, h: r.height, yN: yFor(128 + st.trig_volts / vpc, 1) / CH, before: st.trig_volts };
  });
  t.ok(g0.yN > 0.05 && g0.yN < 0.95, `trigger-level handle is on-screen (y=${g0.yN.toFixed(2)})`);
  const hx = g0.left + g0.w * 0.97, hy = g0.top + g0.h * g0.yN;
  await po.page.mouse.move(hx, hy);
  await po.page.mouse.down();
  await po.page.mouse.move(hx, hy - 120, { steps: 8 }); // drag UP
  await po.page.mouse.up();
  await po.wait(150);
  const after = await po.eval(() => st.trig_volts);
  t.ok(after > g0.before + 0.05,
    `dragging the level handle UP raises the level (${g0.before.toFixed(2)}V → ${after.toFixed(2)}V) — correct direction`);

  // --- responsive (narrow screen) --------------------------------------------
  await po.page.setViewportSize({ width: 700, height: 900 });
  await po.wait(200);
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("panelToggle")).display) !== "none",
    "panel-toggle button appears on a narrow screen");
  t.ok(await po.eval(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1),
    "body never scrolls horizontally at 700px");
  const dockLeftClosed = await po.eval(() => document.getElementById("dock").getBoundingClientRect().left);
  t.ok(dockLeftClosed >= 700 - 2, "dock is off-canvas (drawer closed) by default on narrow screens");
  await po.click("panelToggle");
  await po.wait(300);
  const dockLeftOpen = await po.eval(() => document.getElementById("dock").getBoundingClientRect().left);
  t.ok(dockLeftOpen < 700, `panel toggle opens the drawer (dock slid in to ${Math.round(dockLeftOpen)}px)`);
  t.ok(await po.eval(() => getComputedStyle(document.querySelector("footer")).overflowX) === "auto",
    "footer becomes a horizontally-scrolling toolbar (no multi-row wrap)");
  await po.page.setViewportSize({ width: 1400, height: 900 });

  // --- no uncaught page errors across the whole run --------------------------
  t.ok(pageErrors.length === 0, "no uncaught page errors: " + (pageErrors.join(" | ") || "none"));
});
