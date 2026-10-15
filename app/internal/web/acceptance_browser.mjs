// Acceptance suite for the scope web-UI paths NOT already covered by the
// decode/fft/deepmem drivers: boot/liveness, acquire, trigger, vertical/
// horizontal, cursors, X-Y, export, references, keys, panes and the narrow
// layout. Driven through the shared page-object (scope_po.mjs) against
// httptest+fakeScope over the task-workspace UI (SCOPE, BUS, SPECTRUM,
// MEASURE, COMPARE over the instrument strip).
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
  t.ok((await po.curWs()) === "scope" && await po.isVisible("scope"), "the app opens in SCOPE with the scope shown");

  // --- acquire (RUN / SINGLE) — the strip --------------------------------------
  // the button shows the ACTION you can take, not the current state
  t.ok((await po.text("run")).includes("STOP") && await po.hasClass("run", "is-stop"),
    "while running, the button offers STOP");
  await po.click("run"); // stop
  await t.until(async () => (await po.text("run")).includes("RUN") && await po.hasClass("run", "is-run"),
    "after stopping, the button offers RUN (the correct way round)");
  await po.click("run"); // run again
  await t.until(async () => (await po.text("run")).includes("STOP") && await po.hasClass("run", "is-stop"),
    "running again offers STOP");
  await po.click("single");
  await t.until(() => po.hasClass("single", "on"), "SINGLE arms (on state)");
  // AUTOSET delegates to the DEVICE autoset routine (one robust implementation,
  // shared with the front-panel AUTO button). The fake server has no engine to
  // run the sweep, so verify the delegation contract: a POST /api/panel {button:"auto"}.
  const asReq = po.page.waitForRequest(
    (r) => r.url().includes("/api/panel") && r.method() === "POST" && (r.postData() || "").includes('"auto"'),
    { timeout: 5000 });
  await po.click("autoset");
  let delegated = true;
  try { await asReq; } catch (e) { delegated = false; }
  t.ok(delegated, "autoset delegates to the device routine (POST /api/panel button=auto)");
  await po.waitFor(() => !document.getElementById("autoset").classList.contains("on"), null, 8000);

  // --- trigger: strip choices + SCOPE's Trigger pane ----------------------------
  const mode0 = await po.value("mode");
  await po.seg("mode", mode0 === "1" ? "0" : "1");
  await t.until(async () => (await po.value("mode")) !== mode0, `trig mode switches (from ${mode0 === "1" ? "Normal" : "Auto"})`);
  const src0 = await po.value("source");
  await po.seg("source", src0 === "1" ? "0" : "1");
  await t.until(async () => (await po.value("source")) !== src0, "trig source switches");
  await t.until(async () => (await po.eval(() => document.querySelector('#p-trig [data-mirror-of="source"]').value)) === await po.value("source"),
    "the Trigger pane's source mirrors the strip's");
  await po.seg("ttype", "1"); // PULSE
  await t.until(async () => (await po.isCardVisible("qualrow")) && (await po.isCardVisible("qp-pulse")),
    "PULSE type reveals the pulse qualifiers");
  await po.seg("ttype", "2"); // SLOPE
  await t.until(async () => (await po.isCardVisible("qp-slope")) && !(await po.isCardVisible("qp-pulse")),
    "switching to SLOPE reveals slope, hides pulse");
  await po.seg("ttype", "0"); // EDGE
  await t.until(async () => !(await po.isCardVisible("qualrow")), "EDGE hides the qualifier panel");
  // the caption follows the server's status (the next poll), the panel the click
  await t.until(async () => (await po.text("trigCap")).includes("Edge"), "the strip's trigger caption names the type");
  await po.setRange("lvl", 1.5);
  await t.until(async () => ((await po.text("lvlv")) || "").includes("1.5"), "trigger level readout tracks the slider");
  // with the channel offset off 0 V the slider still stops on 0 V (Default setup's -4.56 mV offset must not put
  // the slider's grid 4.39 mV off 0 V)
  const zeroLvl = await po.eval(() => {
    const keep = [st.off1_v, st.off2_v];
    st.off1_v = st.off2_v = -0.00456; applyStatus();
    const el = document.getElementById("lvl"); el.value = "0"; const v = Number(el.value);
    [st.off1_v, st.off2_v] = keep; applyStatus();
    return v;
  });
  t.ok(Math.abs(zeroLvl) < 1e-9, `the trigger level slider stops on 0 V with an offset channel (${zeroLvl})`);

  // --- vertical / horizontal -------------------------------------------------
  await po.click("tC1");
  t.ok(!(await po.hasClass("tC1", "on")) && (await po.text("tC1")) === "Off", "C1 display toggles off (the button says so)");
  await po.click("tC1");
  t.ok(await po.hasClass("tC1", "on"), "C1 display toggles back on");
  const tdivOpts = await po.eval(() => document.getElementById("tdiv").options.length);
  t.ok(tdivOpts > 3, `time/div select is populated (${tdivOpts} detents)`);
  t.ok(await po.eval(() => ["probe1", "probe2", "cpl1", "cpl2"].every(id => document.getElementById(id).querySelectorAll("button").length === 3)),
    "per-channel probe + coupling choices present (3 options each)");
  const sets = [];
  page.on("request", (r) => { if (r.url().endsWith("/api/set")) try { sets.push(JSON.parse(r.postData())); } catch {} });
  await po.seg("cpl2", "1");
  await t.until(() => sets.some((s) => s.control === "coupling2" && s.value === 1), "C2 coupling AC is sent");
  const td0 = await po.eval(() => document.getElementById("tdiv").selectedIndex);
  await po.click("tdivUp");
  await t.until(() => sets.some((s) => s.control === "tdiv"), "the strip's + steps the timebase");
  t.ok(await po.eval((i) => document.getElementById("tdiv").selectedIndex === i + 1, td0), "+ is the next slower detent");

  // --- cursors (the readout lives in MEASURE) ---------------------------------
  await po.click("tCursors");
  t.ok(await po.hasClass("tCursors", "on"), "cursors on");
  await po.ws("measure");
  await t.until(async () => /Δt/.test(await po.text("curBody")), "MEASURE's Cursors pane shows Δt");
  await po.click("tCursors");
  await t.until(async () => /off/.test(await po.text("curBody")), "cursors off: the pane says so");
  await po.ws("scope");

  // --- views: X-Y in SCOPE, FFT in SPECTRUM, remembered per workspace -------------
  await po.setMode("XY");
  t.ok(await po.eval(() => view.mode === "XY"), "SCOPE's view switches to X-Y");
  await po.ws("spectrum");
  t.ok(await po.eval(() => view.mode === "FFT"), "SPECTRUM shows the FFT");
  await po.waitFor(() => fftCh[1].peaks.length > 0, null, 8000);
  t.ok(await po.isVisible("peakTable") && (await po.count("#fftBody .pk")) > 0, "SPECTRUM lists the FFT peaks");
  await po.ws("scope");
  t.ok(await po.eval(() => view.mode === "XY"), "SCOPE remembers X-Y");
  await po.setMode("YT");
  t.ok(await po.eval(() => view.mode === "YT"), "Y-T again");

  // --- reference waveforms (COMPARE) ----------------------------------------------
  await po.click("refSaveA"); // the scope stores it; the page shows the scope's REF A
  await t.until(() => po.eval(() => !document.getElementById("refShowA").disabled && document.getElementById("refShowA").classList.contains("on")),
    "Save A enables and shows REF A", 6000);
  t.ok(await po.eval(() => typeof refs !== "undefined" && !!refs.A && refs.A.show), "REF A captured and visible");
  await t.until(async () => (await po.count("#diffBody tr[data-ref='A']")) > 0, "the difference readout compares the live trace with REF A");
  await po.click("refClearA");
  await t.until(() => po.eval(() => !refs.A && document.getElementById("refShowA").disabled), "clearing REF A empties it", 6000);

  // --- export (PNG data URL + CSV + sigrok, from the Export menu) ----------------
  t.ok((await po.eval(() => document.getElementById("scope").toDataURL("image/png"))).startsWith("data:image/png"),
    "canvas exports a PNG data URL");
  await po.exportClick("eCSV"); // must not throw
  t.ok(true, "CSV export click completes without error");
  t.ok(await po.eval(() => {
    const s = sigrokSeries(frame);
    const wav = s && sigrokWAV(s);
    return !!s && sigrokSR(s).length > 100 && sigrokVCD(s).includes("$enddefinitions") &&
      (wav === null || wav.length > 46); // null only for >uint32-Hz rates
  }), "sigrok encoders produce output from the live frame");
  await po.exportClick("eSR"); // the three sigrok entries must not throw
  await po.exportClick("eVCD");
  await po.exportClick("eWAV");
  t.ok(await po.eval(() => document.getElementById("exportpop").hidden), "the Export menu closes after a choice");

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
  t.ok(await po.eval(() => document.querySelector('#mode > button.on').getAttribute("aria-checked")) === "true",
    "a segmented choice marks its option aria-checked");
  t.ok((await po.eval(() => document.getElementById("lvl").getAttribute("aria-label")) || "").length > 0,
    "the trigger-level slider has an accessible label");
  t.ok(await po.eval(() => document.getElementById("wstabs").getAttribute("role") === "tablist" &&
    document.querySelectorAll('#wstabs [role="tab"][aria-selected="true"]').length === 1), "the workspace tabs are a tablist with one selected tab");

  // --- trigger-state chip (mirrors the LCD state machine) --------------------
  const chip = await po.text("trigChip");
  t.ok(["AUTO", "NORM", "SNGL", "WAIT", "T'D", "STOP"].includes(chip),
    `top bar shows a trigger-state chip mirroring the LCD (${chip})`);
  t.ok((await po.text("run")).includes("▶") || (await po.text("run")).includes("■"),
    "RUN/STOP button carries a redundant glyph (not colour-only)");

  // --- keyboard shortcuts ----------------------------------------------------
  await po.page.evaluate(() => document.activeElement && document.activeElement.blur());
  const curBefore = await po.hasClass("tCursors", "on");
  await po.page.keyboard.press("c");                 // toggle cursors via keyboard
  t.ok(await po.hasClass("tCursors", "on") !== curBefore, "keyboard 'c' toggles cursors");
  await po.page.keyboard.press("c");                 // back
  await po.page.keyboard.press("Shift+Slash");       // '?' opens help
  t.ok(await po.isCardVisible("help"), "'?' opens the help sheet");
  t.ok(/Alt\+1/.test(await po.text("helpBody")) && /SPECTRUM/.test(await po.text("helpBody")), "help lists the workspaces and their keys");
  await po.page.keyboard.press("Escape");
  t.ok(!(await po.isCardVisible("help")), "Escape closes the help sheet");
  // suppressed while a text field is focused (the holdoff field)
  await po.reveal("holdoff");
  await po.page.focus("#holdoff");
  const wsBefore = await po.curWs();
  await po.page.keyboard.press("f");                 // must NOT switch to SPECTRUM while typing
  t.ok((await po.curWs()) === wsBefore, "shortcuts are suppressed while a text field is focused");
  await po.page.evaluate(() => document.activeElement.blur());

  // --- panes: collapse + maximize ----------------------------------------------
  await po.ws("scope");
  await po.eval(() => document.querySelector("#p-quick .pc").click());
  t.ok(await po.eval(() => document.getElementById("p-quick").parentElement.classList.contains("is-collapsed")),
    "a pane's caret collapses it");
  await po.eval(() => document.querySelector("#p-quick .pc").click());
  t.ok(!(await po.eval(() => document.getElementById("p-quick").parentElement.classList.contains("is-collapsed"))),
    "the caret expands it again");
  const wBefore = await po.eval(() => document.getElementById("scope").width);
  await po.page.dblclick("#p-scope .pt");
  await t.until(async () => (await po.eval(() => document.getElementById("scope").width)) > wBefore,
    "double-clicking the title maximizes the scope pane (the GL canvas grows with it)");
  await po.page.keyboard.press("Escape");
  await t.until(async () => (await po.eval(() => document.getElementById("scope").width)) === wBefore,
    "Esc restores the pane (the canvas shrinks back)");

  // --- math + mouse gestures ---------------------------------------------------
  await po.setSelect("mathFn", "c1-c2");
  t.ok(await po.eval(() => mathFn === "c1-c2"), "math function selectable (C1 − C2)");
  await po.setSelect("mathFn", "off");
  // FFT carrier removal: pick a C1 tone in SPECTRUM, subtract it, the residual shrinks
  await po.setMode("FFT"); await po.wait(400);
  await po.eval(() => { const pk = fftCh[1].peaks.slice().sort((a, b) => b.db - a.db)[0]; if (pk) togglePeakCh(1, pk.freq); });
  await po.ws("scope"); await po.wait(200);
  const cFull = await po.eval(() => { let mn = 999, mx = -999; for (const v of frame.c1) if (v >= 0) { mn = Math.min(mn, v); mx = Math.max(mx, v); } return mx - mn; });
  await po.setSelect("mathFn", "res1"); await po.wait(200);
  const cRes = await po.eval(() => { const m = computeMath(); if (!m) return null; let mn = 999, mx = -999; for (const v of m) if (v >= 0) { mn = Math.min(mn, v); mx = Math.max(mx, v); } return mx - mn; });
  t.ok(cRes != null && cRes < cFull * 0.9, `removing a picked FFT peak reduces the residual (carrier removal): ${cFull} → ${cRes}`);
  await po.eval(() => { view.persist = true; });
  await po.setSelect("mathFn", "c1-c2"); await po.wait(120);
  await po.eval(() => { view.persist = false; }); await po.setSelect("mathFn", "off");
  await po.ws("scope");
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
  await po.seg("zoomSeg", "10");
  t.ok(await po.eval(() => Math.abs((view.win.b - view.win.a) - (homeWindow(frame).b - homeWindow(frame).a) / 10) < 1e-6), "the Horizontal pane's Zoom 10× narrows the window ten times");
  await po.eval(() => goHome());
  t.ok(!(await po.text("line")).includes("zoom ×"), "returning home clears the zoom factor");
  await po.page.mouse.move(3, 3); await po.wait(400); // break the double-click sequence before the drag test below
  // Ctrl+wheel steps the timebase
  const td1 = await po.eval(() => document.getElementById("tdiv").selectedIndex);
  await po.eval(() => { const e = new WheelEvent("wheel", { deltaY: 120, ctrlKey: true, bubbles: true, cancelable: true }); document.getElementById("scope").dispatchEvent(e); });
  await po.wait(120);
  await t.until(async () => (await po.eval(() => document.getElementById("tdiv").selectedIndex)) !== td1,
    "Ctrl+wheel changes the timebase (time/div)");
  // the wheel on the time/div list steps like the knob: up = faster
  const td2 = await po.eval(() => document.getElementById("tdiv").selectedIndex);
  await po.eval(() => document.getElementById("tdiv").dispatchEvent(new WheelEvent("wheel", { deltaY: -100, bubbles: true, cancelable: true })));
  t.ok(await po.eval((i) => document.getElementById("tdiv").selectedIndex === i - 1, td2), "wheel up on time/div = a faster timebase (the knob rule)");

  // --- direct manipulation: drag the trigger-level handle on the display ------
  await po.setMode("YT");
  const g0 = await po.eval(() => {
    const r = scope.getBoundingClientRect();
    const vpc = (st.trig_source === 1 ? frame.vpc2 : frame.vpc1) || (1 / 32);
    return { left: r.left, top: r.top, w: r.width, h: r.height, yN: yFor(128 + st.trig_volts / vpc, 1) / CH, before: st.trig_volts };
  });
  t.ok(g0.yN > 0.05 && g0.yN < 0.95, `trigger-level handle is on-screen (y=${g0.yN.toFixed(2)})`);
  // The grab needs the pointer within ±6% of canvas height of the handle's
  // LIVE y (markerHit, app_geom.js) — retry the whole gesture on a loaded runner.
  let rose = false;
  for (let attempt = 0; attempt < 3 && !rose; attempt++) {
    const g = await po.eval(() => {
      const r = scope.getBoundingClientRect();
      const vpc = (st.trig_source === 1 ? frame.vpc2 : frame.vpc1) || (1 / 32);
      return { left: r.left, top: r.top, w: r.width, h: r.height, yN: yFor(128 + st.trig_volts / vpc, 1) / CH };
    });
    const hx = g.left + g.w * 0.97, hy = g.top + g.h * g.yN;
    await po.page.mouse.move(hx, hy);
    await po.page.mouse.down();
    await po.page.mouse.move(hx, hy - 120, { steps: 8 }); // drag UP
    await po.page.mouse.up();
    for (const end = Date.now() + 1500; !rose && Date.now() < end; ) {
      rose = (await po.eval(() => st.trig_volts)) > g0.before + 0.05;
      if (!rose) await po.wait(50);
    }
    if (!rose) { // a missed grab box-zoomed — reset to home before retrying
      await po.page.mouse.dblclick(g.left + g.w * 0.5, g.top + g.h * 0.5);
      await po.wait(200);
    }
  }
  t.ok(rose, `dragging the level handle UP raises the level (from ${g0.before.toFixed(2)}V) — correct direction`);

  // --- responsive (narrow window) ----------------------------------------------
  await po.page.setViewportSize({ width: 700, height: 900 });
  await po.wait(300);
  t.ok(await po.eval(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1),
    "body never scrolls horizontally at 700px");
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("strip")).overflowX) === "auto",
    "the strip scrolls sideways instead of wrapping into rows");
  t.ok(await po.eval(() => document.getElementById("scope").getBoundingClientRect().width > 600),
    "the panes stack: the scope keeps the full width");
  await po.page.setViewportSize({ width: 1400, height: 900 });

  // --- no uncaught page errors across the whole run --------------------------
  t.ok(pageErrors.length === 0, "no uncaught page errors: " + (pageErrors.join(" | ") || "none"));
});
