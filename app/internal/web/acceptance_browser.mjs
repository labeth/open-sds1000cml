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
  await po.click("run"); // optimistic toggle running -> stopped
  t.ok((await po.text("run")).includes("STOP") && await po.hasClass("run", "stopped"),
    "RUN click optimistically shows STOP + stopped style");
  await po.click("run");
  t.ok((await po.text("run")).includes("RUN") && await po.hasClass("run", "running"),
    "second RUN click returns to RUN + running style");
  await po.click("single");
  t.ok(await po.hasClass("single", "on"), "SINGLE arms (on state)");

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
