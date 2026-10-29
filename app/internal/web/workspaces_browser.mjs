// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e for the task workspaces (argv[2]=URL): the tabs and their
// panes, Alt+1..5 matched on the key code (macOS Option+digit types a symbol),
// roving focus on the tab list, "again" per workspace, the view each workspace
// remembers, the Y/X/F letters, the dots, pane collapse / maximize / gutter
// keys resizing the GL trace canvas, the help sheet, Ctrl+, and the
// single-letter shortcuts (ignored while typing; F1-F5 left to the browser).
// SKIP/exit 0 without a browser.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2], { viewport: { width: 1600, height: 1000 } });
  t.browser = browser;
  // TRLC-LINKS: REQ-SDS-219
  const cur = () => po.curWs();
  // TRLC-LINKS: REQ-SDS-219
  const mode = () => page.evaluate(() => view.mode);
  // TRLC-LINKS: REQ-SDS-219
  const canvas = () => page.evaluate(() => { const c = document.getElementById("scope"); const r = c.getBoundingClientRect(); return { w: c.width, h: c.height, cw: r.width, ch: r.height }; });
  // TRLC-LINKS: REQ-SDS-219
  const key = async (k) => { await page.evaluate(() => { const a = document.activeElement; if (a && a !== document.body) a.blur(); }); await page.keyboard.press(k); await po.wait(120); };
  // TRLC-LINKS: REQ-SDS-219
  const status = () => page.evaluate(async () => (await (await fetch("/api/status")).json()));

  // ---- the tab list ------------------------------------------------------------
  t.ok(await cur() === "scope", "the page opens in SCOPE");
  t.ok(await page.evaluate(() => [...document.querySelectorAll("#wstabs [role=tab]")].filter((b) => b.tabIndex === 0).map((b) => b.id).join() === "tab-scope"),
    "one tab (the selected one) is in the tab order");

  const PANES = {
    scope: ["p-scope", "p-horiz", "p-quick", "p-vert", "p-trig", "p-acq"],
    bus: ["p-bus-scope", "p-packets", "p-decoder", "p-match"],
    spectrum: ["p-analysis", "p-time", "p-peaks", "p-readout"],
    measure: ["p-meas-scope", "p-stats", "p-cursors", "p-mask", "p-acq", "p-sr", "p-prec"],
    compare: ["p-cmp-scope", "p-diff", "p-refs", "p-math", "p-mask"],
  };
  for (const [w, panes] of Object.entries(PANES)) {
    await page.click("#tab-" + w);
    await po.waitFor((x) => ws.cur === x, w, 4000);
    const vis = await page.evaluate((ps) => ps.filter((p) => {
      const el = document.getElementById(p), host = el && el.closest(".ws");
      return !(el && host && !host.hidden && el.getClientRects().length > 0);
    }), panes);
    t.ok(vis.length === 0, `${w.toUpperCase()} shows its panes` + (vis.length ? " — missing " + vis.join(", ") : ""));
    const c = await canvas();
    t.ok(c.cw > 300 && c.ch > 150 && c.w >= c.cw, `${w.toUpperCase()}: the scope display sits in the workspace, sized ${Math.round(c.cw)}×${Math.round(c.ch)}`);
    t.ok(await page.evaluate((x) => document.getElementById("tab-" + x).getAttribute("aria-selected") === "true" && document.getElementById("tab-" + x).tabIndex === 0, w),
      `${w} tab is selected and in the tab order`);
  }
  // every workspace's panes, once each: no pane is shown twice
  t.ok(await page.evaluate(() => new Set([...document.querySelectorAll(".pane")].map((p) => p.id)).size === document.querySelectorAll(".pane").length), "every pane exists once");

  // ---- Alt+1..5 on the key code ---------------------------------------------------
  await key("Alt+1");
  t.ok(await cur() === "scope", "Alt+1 opens SCOPE");
  await key("Alt+3");
  t.ok(await cur() === "spectrum", "Alt+3 opens SPECTRUM");
  // macOS Option+4 types "¢": the key differs, the code is Digit4
  const prevented = await page.evaluate(() => {
    const e = new KeyboardEvent("keydown", { key: "¢", code: "Digit4", altKey: true, bubbles: true, cancelable: true });
    window.dispatchEvent(e);
    return e.defaultPrevented;
  });
  t.ok(await cur() === "measure" && prevented, "Option+4 (key ¢, code Digit4) opens MEASURE — matched on the key code");
  await page.evaluate(() => window.dispatchEvent(new KeyboardEvent("keydown", { key: "5", code: "Numpad5", altKey: true, bubbles: true, cancelable: true })));
  t.ok(await cur() === "compare", "Alt+Numpad5 opens COMPARE");
  // F1-F5 stay the browser's (F5 reloads)
  const fkeys = await page.evaluate(() => ["F1", "F2", "F3", "F4", "F5"].map((k) => {
    const e = new KeyboardEvent("keydown", { key: k, code: k, bubbles: true, cancelable: true });
    window.dispatchEvent(e);
    return e.defaultPrevented;
  }));
  t.ok(fkeys.every((p) => !p) && await cur() === "compare", "F1–F5 are not taken (F5 keeps reloading the page)");

  // ---- roving focus on the tabs ---------------------------------------------------------
  await page.focus("#tab-sound");
  await page.keyboard.press("ArrowRight");
  t.ok(await cur() === "scope" && await page.evaluate(() => document.activeElement.id) === "tab-scope", "ArrowRight on the last tab wraps to SCOPE and focuses it");
  await page.keyboard.press("ArrowRight");
  t.ok(await cur() === "bus", "ArrowRight opens the next workspace");
  await page.keyboard.press("End");
  t.ok(await cur() === "sound", "End opens the last workspace");
  await page.keyboard.press("Home");
  t.ok(await cur() === "scope" && await page.evaluate(() => document.activeElement.id) === "tab-scope", "Home opens SCOPE, focus follows");

  // ---- again ------------------------------------------------------------------------------
  t.ok(await mode() === "YT", "SCOPE shows Y-T");
  await key("Alt+1");
  t.ok(await mode() === "XY" && await po.value("viewScope") === "XY", "SCOPE again: X-Y (the view control follows)");
  await key("Alt+1");
  t.ok(await mode() === "YT", "SCOPE again: back to Y-T");
  await key("Alt+3");
  t.ok(await mode() === "FFT", "SPECTRUM opens on the FFT");
  await key("Alt+3");
  t.ok(await mode() === "SPG" && await po.isVisible("spgArm"), "SPECTRUM again: the spectrogram, with its tools");
  await key("Alt+3");
  t.ok(await mode() === "BODE" && await po.isVisible("bodeArm") && !(await po.isVisible("spgArm")), "SPECTRUM again: Bode, with its tools");
  await key("Alt+3");
  t.ok(await mode() === "FFT", "SPECTRUM again: back to the FFT");
  await key("Alt+2");
  const busViews = [];
  for (let i = 0; i < 4; i++) { await key("Alt+2"); busViews.push(await po.value("busView")); }
  t.ok(busViews.join(" ") === "eye stream visual packets", "BUS again: eye diagram, stream, visual, back to the packets, as F2 on the scope: " + busViews.join(" "));
  await key("Alt+4");
  t.ok(await po.isVisible("statsTable") && !(await po.isVisible("valuesTable")), "MEASURE opens on the statistics");
  await key("Alt+4");
  t.ok(await po.isVisible("valuesTable") && !(await po.isVisible("statsTable")), "MEASURE again: all values");
  await key("Alt+4");
  t.ok(await po.isVisible("statsTable"), "MEASURE again: back to the statistics");
  await key("Alt+2");
  await po.waitFor(() => !busDetecting, null, 10000);
  t.ok(await po.isVisible("pktable") || await po.isVisible("pkEmpty"), "BUS opens on the packets");
  await key("Alt+2");
  t.ok(await po.isVisible("ejEye") && await po.hasClass("ejArm", "on"), "BUS again: the eye diagram, analyzer armed");
  await key("Alt+2");
  t.ok(!(await po.isVisible("ejEye")) && !(await po.hasClass("ejArm", "on")), "BUS again: back to the packets, the eye stops");

  // ---- the view each workspace remembers + Y/X/F ---------------------------------------
  await key("Alt+1");
  await po.click("mXY");
  await key("Alt+3");
  await po.click("mSPG");
  await page.click("#tab-scope");
  t.ok(await mode() === "XY", "SCOPE comes back in its X-Y");
  await page.click("#tab-spectrum");
  t.ok(await mode() === "SPG", "SPECTRUM comes back in its spectrogram");
  await page.click("#tab-measure");
  t.ok(await mode() === "YT", "MEASURE shows Y-T whatever the others show");
  await key("f");
  t.ok(await cur() === "spectrum" && await mode() === "FFT", "F opens SPECTRUM's FFT");
  await key("x");
  t.ok(await cur() === "scope" && await mode() === "XY", "X opens SCOPE's X-Y");
  await key("y");
  t.ok(await cur() === "scope" && await mode() === "YT", "Y: SCOPE's Y-T");

  // ---- dots ---------------------------------------------------------------------------------
  // TRLC-LINKS: REQ-SDS-219
  const dot = (w) => page.evaluate((x) => !document.querySelector("#tab-" + x + " .tdot").hidden, w);
  // (BUS's entry detection may have found something on the square: start from decode off)
  await po.eval(() => { const e = document.getElementById("decProto"); e.value = "off"; e.dispatchEvent(new Event("change")); });
  await po.wait(1200);
  t.ok(!(await dot("bus")) && !(await dot("compare")), "no dots while nothing runs");
  await po.eval(() => { const e = document.getElementById("decProto"); e.value = "uart"; e.dispatchEvent(new Event("change")); });
  await t.until(() => dot("bus"), "decoding puts a dot on BUS", 2500);
  t.ok(/decoding UART/.test(await page.getAttribute("#tab-bus", "title")), "…and the tab's tip says why");
  await po.eval(() => { const e = document.getElementById("decProto"); e.value = "off"; e.dispatchEvent(new Event("change")); });
  await t.until(async () => !(await dot("bus")), "decode off: the BUS dot goes", 2500);
  await po.click("refSaveA");
  await t.until(() => dot("compare"), "a shown reference puts a dot on COMPARE", 6000);
  await po.click("refClearA");
  await t.until(async () => !(await dot("compare")), "cleared: the COMPARE dot goes", 6000);

  // ---- panes resize the GL canvas -------------------------------------------------------
  await page.click("#tab-measure");
  await po.wait(200);
  const m0 = await canvas();
  await page.click("#p-stats > .ph > .pc");
  await t.until(async () => (await canvas()).h > m0.h + 40, "collapsing MEASURE's statistics pane grows the scope canvas", 2000);
  t.ok(await page.evaluate(() => document.getElementById("p-stats").parentElement.classList.contains("is-collapsed") &&
    document.querySelector("#p-stats > .ph > .pc").getAttribute("aria-expanded") === "false"), "…the pane shows collapsed (caret aria-expanded=false)");
  await page.click("#p-stats > .ph > .pc");
  await t.until(async () => Math.abs((await canvas()).h - m0.h) <= 2, "expanding it gives the canvas its height back", 2000);
  await page.dblclick("#p-meas-scope > .ph .pt");
  await t.until(async () => (await canvas()).w > m0.w * 1.2, "double-clicking the scope pane's title maximizes it (wider canvas)", 2000);
  // TRLC-LINKS: REQ-SDS-219
  const inert = () => page.evaluate(() => [!!document.querySelector("#p-stats").closest("[inert]"), !!document.querySelector("#p-meas-scope").closest("[inert]")]);
  t.ok(JSON.stringify(await inert()) === "[true,false]", "the panes under the maximized one are inert (no focus, no clicks), it is not: " + JSON.stringify(await inert()));
  await key("Escape");
  await t.until(async () => Math.abs((await canvas()).w - m0.w) <= 2, "Esc restores the maximized pane", 2000);
  t.ok(JSON.stringify(await inert()) === "[false,false]", "restored, no pane is inert");
  // the gutter between the scope column and the side column is a separator the keyboard moves
  const gut = await page.$("#ws-measure > .split > .gut");
  t.ok(!!gut && (await gut.getAttribute("role")) === "separator", "the column gutter is a separator");
  await gut.focus();
  await page.keyboard.press("Shift+ArrowLeft");
  await page.keyboard.press("Shift+ArrowLeft");
  await t.until(async () => (await canvas()).cw < m0.cw - 100, "Shift+ArrowLeft on the gutter widens the side column (canvas narrower)", 2000);
  await page.keyboard.press("Enter");
  await t.until(async () => Math.abs((await canvas()).cw - m0.cw) <= 2, "Enter on the gutter resets the split", 2000);

  // ---- help ------------------------------------------------------------------------------------
  await key("?");
  t.ok(await po.hasClass("help", "show"), "? opens the help sheet");
  const help = await po.text("helpBody") + " " + await po.text("helpKeys") + " " + await po.text("helpMouse");
  t.ok(["Alt+1", "Alt+2", "Alt+3", "Alt+4", "Alt+5", "SCOPE", "BUS", "SPECTRUM", "MEASURE", "COMPARE", "Ctrl+,"].every((s) => help.includes(s)), "help lists the five workspaces, their keys and Ctrl+,");
  t.ok(/Again: FFT → spectrogram → Bode/.test(help) && /Again: packets → eye diagram → stream → visual/.test(help), "help says what each workspace's key does again");
  await page.keyboard.press("Tab");
  t.ok(await page.evaluate(() => document.activeElement.id) === "helpClose", "Tab stays in the help sheet");
  await page.keyboard.press("Escape");
  t.ok(!(await po.hasClass("help", "show")), "Esc closes it");

  // ---- Ctrl+, ------------------------------------------------------------------------------------
  await key("Control+Comma");
  t.ok(await po.isVisible("allcol") && await page.evaluate(() => document.activeElement.id) === "allFilter", "Ctrl+, opens All settings with the filter focused");
  await key("Control+Comma");
  t.ok(!(await po.isVisible("allcol")), "Ctrl+, again closes it");

  // ---- single-letter shortcuts ---------------------------------------------------------------
  await page.click("#tab-scope");
  t.ok((await status()).running === true, "the scope runs");
  await key("Space");
  await t.until(async () => (await status()).running === false, "Space stops", 3000);
  await key("Space");
  await t.until(async () => (await status()).running === true, "Space runs again", 3000);
  const c0 = await po.hasClass("tCursors", "on");
  await key("c");
  t.ok((await po.hasClass("tCursors", "on")) === !c0, "C toggles the cursors");
  await key("c");
  // typing: the letters belong to the field
  await page.click("#holdoff");
  await page.keyboard.press("c");
  t.ok((await po.hasClass("tCursors", "on")) === c0, "C typed into a field does not toggle the cursors");
  await page.keyboard.press("Alt+2");
  t.ok(await cur() === "bus", "Alt+2 still switches while a field has focus");
  await page.evaluate(() => document.activeElement.blur());
  const src0 = await po.value("source");
  await key("t");
  t.ok(await po.value("source") !== src0, "T steps the trigger source");
  await key("t");
  const sl0 = await po.value("slope");
  await key("e");
  t.ok(await po.value("slope") !== sl0, "E flips the trigger slope");
  await key("e");

  // V/div reads at the probe tip, as on the LCD: 10x at a BNC range reads ten times it
  // TRLC-LINKS: REQ-SDS-125
  const vdivText = () => po.eval(() => { const el = document.getElementById("vdiv1"); return [+el.value, el.options[el.selectedIndex].textContent]; });
  const [bnc] = await vdivText();
  await po.seg("probe1", "10");
  await t.until(async () => (await vdivText())[1] === (await po.eval((v) => fmtVdiv(+(v * 10).toPrecision(3)), bnc)), "with a 10x probe the C1 V/div reads at the tip (ten times the BNC range)");
  await po.seg("probe1", "1");
  await t.until(async () => (await vdivText())[1] === (await po.eval((v) => fmtVdiv(v), bnc)), "…and back at 1x it reads the BNC range");

  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
