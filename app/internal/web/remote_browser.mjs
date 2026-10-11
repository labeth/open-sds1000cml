// ENGMODEL-OWNER-UNIT: FU-WEB-REMOTE
// Real-browser e2e for the phone remote (argv[2] = the scope's URL, argv[3] =
// the control server of TestRemoteBrowser): Pixel 7 and iPhone 14 in phone
// emulation with touch drive every tab, a tile, typed numbers with units and
// a refused one, match bytes in hex and text, a workspace switch and "again"
// while the LCD has a sheet open (the control server shows that sheet
// untouched), undo in History and a scope knob listed as the scope's,
// Default Setup refusing a tap and running on a 1 s hold, and the reconnect
// after the scope's server drops. At 390x844, 360x780 and landscape every
// shown control is at least 44 px, every text at least 13 px, nothing
// scrolls sideways. No console errors. SKIP/exit 0 without a browser.
// REMOTE_SHOTS=<dir> saves screenshots of every tab and sheet.
// TRLC-LINKS: REQ-SDS-220
import { findPlaywright, run } from "./scope_po.mjs";
import { mkdirSync } from "node:fs";
import path from "node:path";

const APP = process.argv[2], CTRL = process.argv[3];
const SHOTS = process.env.REMOTE_SHOTS || "";

run(async (t) => {
  const pw = (await import(findPlaywright())).default;
  const browser = await pw.chromium.launch({ headless: true, args: ["--no-sandbox", "--autoplay-policy=user-gesture-required"] });
  t.browser = browser;
  // TRLC-LINKS: REQ-SDS-220
  const ctrl = async (p) => (await fetch(CTRL + p)).json();
  // TRLC-LINKS: REQ-SDS-220
  const rows = async () => {
    const m = await (await fetch(APP + "/api/sheets")).json(), out = {};
    for (const tab of m.Tabs) for (const r of tab.Rows) out[r.ID] = r;
    return out;
  };
  // TRLC-LINKS: REQ-SDS-220
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  // TRLC-LINKS: REQ-SDS-220
  const lcdClosed = async () => (await ctrl("/status")).Mode === "workspace";
  let dropped = false;
  if (SHOTS) mkdirSync(SHOTS, { recursive: true });

  // TRLC-LINKS: REQ-SDS-220
  async function phone(devName, opts) {
    const d = Object.assign({}, pw.devices[devName]);
    delete d.defaultBrowserType;
    if (opts.viewport) d.viewport = opts.viewport;
    const ctx = await browser.newContext(d);
    // the scope's plain HTTP: browsers have no Screen Wake Lock there (127.0.0.1 counts as secure)
    if (opts.noWakeLock) await ctx.addInitScript(() => { delete Navigator.prototype.wakeLock; });
    // haptics: Android vibrates (recorded here); Safari has no Vibration API, so an iPhone has the visual tick only
    await ctx.addInitScript((iphone) => {
      window.__vib = [];
      if (iphone) delete Navigator.prototype.vibrate;
      else Navigator.prototype.vibrate = function (p) { window.__vib.push(p); return true; };
    }, /iPhone/.test(devName));
    const page = await ctx.newPage();
    const errors = [];
    // while the test drops the scope's server the browser logs the failed requests: expected there only
    page.on("console", (m) => {
      if (m.type() === "error" && !(dropped && /Failed to load resource|ERR_EMPTY_RESPONSE|ERR_CONNECTION/.test(m.text()))) errors.push(m.text());
    });
    page.on("pageerror", (e) => errors.push("uncaught: " + e.message));
    const cdp = await ctx.newCDPSession(page);
    await page.goto(APP + "/remote", { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".tile .tv", { timeout: 15000 });
    await page.waitForFunction(() => document.querySelector('[data-tile="time.tdiv"] .tv') && document.querySelector('[data-tile="time.tdiv"] .tv').textContent.trim() !== "");
    // TRLC-LINKS: REQ-SDS-220
    const centre = async (sel) => {
      const el = page.locator(sel).first();
      await el.scrollIntoViewIfNeeded();
      const b = await el.boundingBox();
      if (!b) throw new Error("not on screen: " + sel);
      return { x: b.x + b.width / 2, y: b.y + b.height / 2, b };
    };
    const P = {
      page, ctx, errors, name: opts.label || devName,
      tap: async (sel) => { await page.locator(sel).first().tap(); await page.waitForTimeout(60); },
      // a finger held down for ms (Playwright's tap is short)
      hold: async (sel, ms) => {
        const c = await centre(sel);
        await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: c.x, y: c.y }] });
        await page.waitForTimeout(ms);
        await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
        await page.waitForTimeout(80);
      },
      // a finger dragged across fx0..fx1 of the element's width
      drag: async (sel, fx0, fx1, ms) => {
        const c = await centre(sel), y = c.y, n = Math.max(4, Math.round(ms / 16));
        await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: c.b.x + c.b.width * fx0, y }] });
        for (let i = 1; i <= n; i++) {
          await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: c.b.x + c.b.width * (fx0 + (fx1 - fx0) * i / n), y }] });
          await page.waitForTimeout(16);
        }
        await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
        await page.waitForTimeout(80);
      },
      text: (sel) => page.evaluate((s) => { const e = document.querySelector(s); return e ? e.textContent.replace(/\s+/g, " ").trim() : null; }, sel),
      visible: (sel) => page.evaluate((s) => { const e = document.querySelector(s); return !!e && e.getClientRects().length > 0 && !e.closest("[hidden]"); }, sel),
      toast: () => page.evaluate(() => { const e = document.querySelector(".toast"); return e && !e.hidden ? e.textContent : ""; }),
      shot: async (n) => { if (SHOTS) { await page.waitForTimeout(260); await page.screenshot({ path: path.join(SHOTS, (opts.label || devName).replace(/\s+/g, "") + "-" + n + ".png") }); } },
      until: async (fn, msg, ms) => t.until(fn, (opts.label || devName) + ": " + msg, ms || 4000),
      ok: (c, msg) => t.ok(c, (opts.label || devName) + ": " + msg),
    };
    return P;
  }

  // the layout rules on what is shown now: targets of 44 px, text of 13 px, no sideways scroll
  // TRLC-LINKS: REQ-SDS-220
  async function layout(P, where) {
    const r = await P.page.evaluate(() => {
      const small = [], tiny = [];
      const shown = (e) => { const s = getComputedStyle(e); if (s.visibility === "hidden" || s.display === "none") return false; const b = e.getBoundingClientRect(); return b.width > 0 && b.height > 0 && b.bottom > 0 && b.top < innerHeight && !e.closest("[hidden]"); };
      for (const e of document.querySelectorAll("button, input, [role=slider]")) {
        if (!shown(e) || e.closest(".scrim")) continue;
        const b = e.getBoundingClientRect();
        if (b.width < 43.5 || b.height < 43.5) small.push((e.dataset.key || e.dataset.q || e.id || e.className || e.tagName) + " " + Math.round(b.width) + "x" + Math.round(b.height));
      }
      const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
      for (let n = walker.nextNode(); n; n = walker.nextNode()) {
        if (!n.textContent.trim() || !n.parentElement || !shown(n.parentElement)) continue;
        const fs = parseFloat(getComputedStyle(n.parentElement).fontSize);
        if (fs < 12.95) tiny.push(n.textContent.trim().slice(0, 20) + " " + fs + "px");
      }
      const se = document.scrollingElement;
      return { small, tiny, side: se.scrollWidth > innerWidth + 1 || document.body.scrollWidth > innerWidth + 1 };
    });
    P.ok(!r.small.length, where + ": every shown control is at least 44 px" + (r.small.length ? " - " + r.small.slice(0, 6).join(", ") : ""));
    P.ok(!r.tiny.length, where + ": no text under 13 px" + (r.tiny.length ? " - " + r.tiny.slice(0, 6).join(", ") : ""));
    P.ok(!r.side, where + ": nothing scrolls sideways");
  }

  // ---- one phone through every requirement ----------------------------------------------------
  // TRLC-LINKS: REQ-SDS-220
  async function flow(devName, label, opts) {
    // a clean scope: factory settings, SCOPE, no menu on the LCD
    await ctrl("/reset");
    const P = await phone(devName, Object.assign({ label }, opts || {}));
    const { page } = P;
    P.ok(await P.text('[data-tile="time.tdiv"] .tv') === "500 us", "Home opens with the scope's values (Time/div 500 us)");
    P.ok(await P.text("#again") === "again: X-Y", "the active chip names its again (SCOPE: X-Y)");
    await P.shot("home");

    // a tile: the quick button names the value it reaches; right is clockwise (toward ns)
    P.ok(await P.text('[data-tile="time.tdiv"] [data-q="cw"] .qv') === "200 us" && await P.text('[data-tile="time.tdiv"] [data-q="ccw"] .qv') === "1 ms",
      "the quick buttons say where they go: 1 ms counter-clockwise, 200 us clockwise");
    await P.tap('[data-tile="time.tdiv"] [data-q="cw"]');
    await P.until(async () => (await P.text('[data-tile="time.tdiv"] .tv')) === "200 us", "a tap on it shows 200 us");
    P.ok(await page.evaluate(() => /\bflash\b/.test((document.querySelector(".tick") || {}).className || "")), "the step ticks on screen");
    if (/iPhone/.test(devName)) P.ok(await page.evaluate(() => typeof navigator.vibrate !== "function"), "an iPhone has no vibration: the tick alone, and no error");
    else P.ok(await page.evaluate(() => window.__vib.length > 0 && window.__vib.every((p) => p)), "the step vibrates on Android");
    await P.until(async () => Math.abs((await ctrl("/engine")).tdiv - 200e-6) < 1e-9, "the scope's timebase is 200 us");
    await P.until(async () => /^REMOTE {2}TIME time\/div 200us/.test((await ctrl("/status")).Remote || ""), "the LCD confirms with a REMOTE line");
    P.ok(await lcdClosed(), "nothing opened on the LCD");
    // a held quick button repeats
    await P.hold('[data-tile="trig.level"] [data-q="cw"]', 1150);
    await P.until(async () => (await rows())["trig.level"].Value > 0.15, "holding the level's quick button steps it again and again");
    await P.until(() => page.evaluate(() => { const L = RN.S.history.list.filter((e) => e.rows.length && e.rows[0].id === "trig.level"); return L.length === 1 && L[0].n > 1; }),
      "the held steps are one History entry (first old to last new)");

    // typed numbers with units, and a refused one
    await P.tap('[data-tile="trig.level"] .tmain');
    await P.until(() => P.visible('.sheet [data-keypad="trig.level"]'), "the level's sheet opens with a keypad");
    await P.shot("sheet-number");
    for (const k of ["1", ".", "2", "5", "V"]) await P.tap(`.sheet [data-key="${k}"]`);
    P.ok(await P.text("#kp-msg") === "= 1.25 V", "the keypad reads 1.25V as = 1.25 V");
    await P.shot("sheet-number-typed");
    await P.tap('.sheet [data-key="enter"]');
    await P.until(async () => !(await P.visible(".sheet")), "Set closes the sheet");
    await P.until(async () => Math.abs((await rows())["trig.level"].Value - 1.25) < 0.002, "the scope's level is 1.25 V");
    await P.until(async () => (await P.text('[data-tile="trig.level"] .tv')) === "1.25 V", "the tile shows 1.25 V");
    await P.tap('[data-tile="trig.level"] .tmain');
    for (const k of ["9", "9", "V"]) await P.tap(`.sheet [data-key="${k}"]`);
    P.ok(/^level 99V is outside /.test(await P.text("#kp-msg")), "99 V: the keypad says it is outside the range before Set");
    await P.tap('.sheet [data-key="enter"]');
    await P.until(async () => /^level 99V is outside /.test(await P.text("#kp-msg")) && await P.visible(".sheet"), "Set: the scope's refusal is shown as it is, the sheet stays");
    await P.shot("sheet-number-refused");
    P.ok(Math.abs((await rows())["trig.level"].Value - 1.25) < 0.002, "a refused value changes nothing (no clamping)");
    await P.tap(".sheet-close");
    // a unit on the time keys, and a value between steps
    await P.tap('[data-tile="time.tdiv"] .tmain');
    for (const k of ["2", "us"]) await P.tap(`.sheet [data-key="${k}"]`);
    P.ok(await P.text("#kp-msg") === "= 2 us", "the keypad reads 2us");
    await P.tap('.sheet [data-key="enter"]');
    await P.until(async () => Math.abs((await ctrl("/engine")).tdiv - 2e-6) < 1e-12, "typed 2us sets the timebase");
    // the drag strip: a detent per 16 px, right = clockwise
    await P.tap('[data-tile="ch1.offset"] .tmain');
    await P.drag(".sheet [data-strip]", 0.3, 0.3 + 56 / (await page.locator(".sheet [data-strip]").boundingBox()).width, 400);
    await P.until(async () => Math.abs((await rows())["ch1.offset"].Value - 0.6) < 0.01, "a 56 px drag on the strip moves CH1 three steps (+0.6 V)");
    await P.tap('.sheet [data-preset="0 V"]');
    await P.until(async () => Math.abs((await rows())["ch1.offset"].Value) < 1e-6, "the 0 V preset zeroes CH1");

    // every tab: Menus (root, a page, search with everyday words)
    await P.tap('[data-tabbar="menus"]');
    await P.until(() => P.visible('[data-tab="trig"]'), "Menus lists the tabs");
    await P.shot("menus");
    await layout(P, "Menus");
    await P.tap('[data-tab="ch2"]');
    await P.tap('[data-row="ch2.coupling"] [data-value="1"]');
    await P.until(async () => (await rows())["ch2.coupling"].Text === "AC", "CH2 coupling AC from its Settings list");
    await P.tap('[data-row="ch2.probe"] [data-value="10"]');
    await P.until(async () => (await rows())["ch2.probe"].Text === "10x", "CH2 probe 10x");
    await P.shot("menus-ch2");
    await P.tap("#back");
    await page.locator("#search").fill("averaging");
    await P.until(() => P.visible('.mbody [data-row="acq.averages"]'), "search: averaging finds Averages");
    await P.shot("menus-search");
    await P.tap('.mbody [data-row="acq.averages"] [data-value="16"]');
    await P.until(async () => (await rows())["acq.mode"].Text === "Average", "a count from the results sets Mode = Average, as on the LCD");
    await page.locator("#search").fill("holdoff");
    await P.until(() => P.visible('.mbody [data-row="trig.holdoff"]'), "search: holdoff");
    await P.tap('.mbody [data-row="trig.holdoff"] [data-open]');
    await P.tap('.sheet [data-preset="1 ms"]');
    await P.until(async () => Math.abs((await ctrl("/engine")).holdoff - 1e-3) < 1e-9, "the holdoff's 1 ms preset");
    // a long press pins a row to this workspace's Home; edit mode moves and removes tiles
    await P.hold('.mbody [data-row="trig.holdoff"] [data-open]', 700);
    await P.until(async () => /^Pinned to SCOPE Home: Trigger holdoff/.test(await P.toast()), "a long press on a row pins it to Home");
    await P.until(async () => /on Home/.test(await P.text('.mbody [data-row="trig.holdoff"] .cl small') || ""), "the row says it is on Home");
    await P.tap('[data-tabbar="home"]');
    const order = () => page.evaluate(() => [...document.querySelectorAll(".tile")].map((x) => x.dataset.tile));
    P.ok((await order()).slice(-1)[0] === "trig.holdoff", "the pinned tile joins the end of the grid");
    await P.hold('[data-tile="trig.holdoff"] .tmain', 700);
    await P.until(() => P.visible("#add-tile"), "a long press on a tile edits the grid");
    await P.tap('[data-tile="trig.holdoff"] [aria-label="Move Trigger holdoff earlier"]');
    P.ok((await order()).slice(-2)[0] === "trig.holdoff", "a tile moves earlier");
    await P.tap('[data-tile="trig.holdoff"] [aria-label="Remove Trigger holdoff"]');
    P.ok((await order()).indexOf("trig.holdoff") < 0 && (await order()).length === 8, "and is removed");
    await P.tap("#edit-done");

    // match bytes: hex, then text (a bus first: the serial trigger needs one)
    await P.tap('[data-tabbar="menus"]');
    await P.tap('[data-tabbar="menus"]');
    await P.tap('[data-tab="bus"]');
    await P.tap('[data-row="bus.protocol"]');
    await P.tap('.sheet [data-value="2"]');
    await P.until(async () => (await rows())["bus.protocol"].Text === "UART", "Protocol UART from its options sheet");
    await P.tap('[data-row="bus.match"]');
    await P.until(() => P.visible('.sheet [data-keypad="bus.match"]'), "Match bytes opens the hex keypad");
    for (const k of ["5", "5", "A", "A"]) await P.tap(`.sheet [data-key="${k}"]`);
    P.ok(await P.text(".sheet .dv") === "55 AA" && /^= 55 AA \(2 bytes\)/.test(await P.text("#match-msg")), "the hex pad groups 55 AA");
    await P.shot("sheet-bytes");
    await P.tap('.sheet [data-act="arm"]');
    await P.until(async () => { const e = await ctrl("/engine"); return e.serialMode !== 0 && JSON.stringify(e.serialBytes) === "[85,170]"; }, "Trigger on it: the scope triggers on 55 AA");
    await P.tap('[data-row="bus.match"]');
    await P.tap('.sheet [data-mode="text"]');
    await page.locator("#match-text").fill("Hi");
    P.ok(/^= 48 69 \(2 bytes\)/.test(await P.text("#match-msg")), "text Hi reads as 48 69");
    await P.shot("sheet-bytes-text");
    await P.tap('.sheet [data-act="set"]');
    await P.until(async () => JSON.stringify((await ctrl("/engine")).serialBytes) === "[72,105]", "Set: the match is Hi (48 69)");
    await P.tap('[data-row="bus.match"]');
    await P.tap('.sheet [data-key="5"]');
    P.ok(/^bad byte "5"/.test(await P.text("#match-msg")), "a lone hex digit is refused before Set");
    await P.tap(".sheet-close");
    P.ok(await lcdClosed(), "nothing opened on the LCD");

    // a workspace switch and again while the LCD has a sheet open
    await ctrl("/button?name=trigmenu");
    await ctrl("/knob?name=adjust&dir=1");
    const before = await ctrl("/status");
    P.ok(before.Mode === "sheet" && before.Sheet.Tab === "trig", "the person at the scope opened the TRIG sheet");
    await P.tap('[data-tabbar="home"]');
    await P.tap('[data-ws="spectrum"]');
    await P.until(async () => (await ctrl("/status")).Workspace === "SPECTRUM", "SPEC: the scope is in SPECTRUM");
    let after = await ctrl("/status");
    P.ok(after.Mode === "sheet" && after.Sheet.Tab === "trig" && after.Sheet.Row.ID === before.Sheet.Row.ID && after.Sheet.Edit === before.Sheet.Edit,
      "the LCD's sheet stayed open on its tab and row (" + after.Sheet.Row.ID + ")");
    P.ok(/^REMOTE {2}workspace SPECTRUM/.test(after.Remote), "the LCD confirms the switch with a REMOTE line");
    await P.until(async () => (await P.text("#again")) === "again: Spectrogram", "the SPEC chip names its again while the LCD shows a sheet");
    await P.tap('[data-ws="spectrum"]');
    await P.until(async () => (await ctrl("/status")).View === "Spectrogram", "SPEC again: the spectrogram");
    after = await ctrl("/status");
    P.ok(after.Mode === "sheet" && after.Sheet.Tab === "trig" && after.Sheet.Row.ID === before.Sheet.Row.ID, "again left the LCD's sheet as it was");
    await P.tap('[data-ws="scope"]');
    await P.until(async () => (await ctrl("/status")).Workspace === "SCOPE", "back to SCOPE");
    await P.shot("home-scope-menu");
    // the tools wait while the LCD's menu has the knobs
    await P.tap('[data-tabbar="keys"]');
    P.ok(await page.locator('[data-knob="adjust"][data-q="cw"]').isDisabled(), "Adjust waits while the scope's menu has it");
    await ctrl("/button?name=trigmenu");
    P.ok(await lcdClosed(), "the person at the scope closed the sheet");

    // History: the phone's changes, a scope knob as the scope's, undo
    await ctrl("/knob?name=tdiv&dir=-1");
    await P.tap('[data-tabbar="hist"]');
    await P.until(async () => /\bscope\b/.test(await P.text('.hent:first-child .hl small') || ""), "a knob turned on the scope is listed as the scope's");
    P.ok(/^TIME · Time\/div$/.test(await P.text(".hent:first-child .hl b")), "titled by its row");
    await P.shot("history");
    await layout(P, "History");
    P.ok(await page.evaluate(() => [...document.querySelectorAll(".hent .hl b")].some((b) => b.textContent === "BUS · Trigger on it")),
      "Trigger on it is one entry, titled by its row, with what it brought along");
    const coup = await page.evaluate(() => { const e = [...document.querySelectorAll(".hent")].find((x) => /CH2 · Coupling/.test(x.textContent)); return e ? e.dataset.hist : ""; });
    P.ok(!!coup && /\bphone\b/.test(await P.text(`[data-hist="${coup}"] .hl small`)), "the phone's coupling change is listed as the phone's, DC to AC");
    await P.tap(`[data-hundo="${coup}"]`);
    await P.until(async () => (await rows())["ch2.coupling"].Text === "DC", "Undo puts CH2 coupling back to DC");
    await P.until(async () => /^Undo: CH2 · Coupling/.test(await P.text(".hent:first-child .hl b") || ""), "the undo is itself an entry");
    P.ok(await page.evaluate((id) => document.querySelector(`[data-hist="${id}"]`).classList.contains("undone"), coup), "the original is marked undone");
    await P.tap(".hent:first-child .hmain");
    await P.until(() => P.visible(".sheet [data-hundo-one]"), "an entry's details offer Undo this");
    await P.shot("history-entry");
    await P.tap(".sheet [data-hundo-one]");
    await P.until(async () => (await rows())["ch2.coupling"].Text === "AC", "undoing the undo is a redo");
    P.ok(Number(await P.text("#hist-badge")) >= 5, "the History tab counts the changes");
    // back to before the bus was set up: everything since, from the phone and the scope, on a 1 s hold
    const proto = await page.evaluate(() => { const e = [...document.querySelectorAll(".hent")].find((x) => /BUS · Protocol/.test(x.textContent)); return e ? e.dataset.hist : ""; });
    await P.tap(`[data-hopen="${proto}"]`);
    await P.until(() => P.visible(".sheet [data-hundo-all]"), "an older entry offers Back to before this");
    await P.shot("history-before");
    await P.tap(".sheet [data-hundo-all]");
    await P.until(async () => /^Hold to confirm/.test(await P.toast()), "a tap on Back to before this only says to hold it");
    P.ok((await rows())["bus.protocol"].Text === "UART", "a tap undid nothing");
    await P.hold(".sheet [data-hundo-all]", 1150);
    await P.until(async () => { const r = await rows(); return r["bus.protocol"].Text === "Off" && r["bus.match"].Text === "any byte" && r["time.tdiv"].Text === "2us"; },
      "the hold puts the bus, the match and the scope's timebase back to before", 8000);
    P.ok((await rows())["ch2.coupling"].Text === "AC", "changes older than the entry stay");

    // Keys: Default Setup refuses a tap and runs on a 1 s hold (the LCD's sheet stays)
    await P.tap('[data-tabbar="keys"]');
    await P.shot("keys");
    await layout(P, "Keys");
    await P.tap("#default-setup");
    await P.until(async () => /^Hold to confirm/.test(await P.toast()), "a tap on Default Setup only says to hold it");
    await sleep(300);
    P.ok(Math.abs((await ctrl("/engine")).holdoff - 1e-3) < 1e-9 && (await rows())["ch2.coupling"].Text === "AC", "a tap restored nothing");
    await ctrl("/button?name=utility");
    await P.hold("#default-setup", 1150);
    await P.until(async () => { const r = await rows(); return r["ch2.coupling"].Text === "DC" && r["time.tdiv"].Text === "500us" && r["acq.mode"].Text === "Normal"; },
      "a 1 s hold restores the factory settings");
    const lcd = await ctrl("/status");
    P.ok(lcd.Mode === "sheet" && lcd.Sheet.Tab === "bus", "Default Setup from the phone left the LCD's BUS sheet open");
    await ctrl("/button?name=utility");

    // the dock: Run/Stop, Single and Auto act as the scope's keys and show its state
    await P.until(async () => (await ctrl("/engine")).running && /^(running|waiting)$/.test(await P.text("#runstop .sub")), "the dock says the scope runs");
    await P.tap("#runstop");
    await P.until(async () => !(await ctrl("/engine")).running && (await P.text("#runstop .sub")) === "stopped", "Run/Stop stops the scope, and the dock says so");
    await P.tap("#single");
    await P.until(async () => /^(armed|done)$/.test(await P.text("#single .sub")), "Single arms one shot: " + await P.text("#single .sub"));
    await P.tap("#auto");
    await P.until(async () => { const e = await ctrl("/engine"); return e.running && !e.norm; }, "Auto runs autoset (on this rig without frames: running, Auto trigger)");
    await P.until(async () => /^Autoset/.test(await P.toast()), "and says so");

    // Keys: the pushes act as the scope's, the knob jobs turn, a menu key opens the phone's page
    const zero = (s) => /^-?0\s*m?V$/.test(s || "");
    await P.tap('[data-tabbar="home"]');
    await P.tap('[data-tile="trig.level"] [data-q="cw"]');
    await P.tap('[data-tile="ch1.offset"] [data-q="cw"]');
    await P.until(async () => { const r = await rows(); return !zero(r["trig.level"].Text) && !zero(r["ch1.offset"].Text); }, "the level and CH1's offset off 0 V");
    await P.tap('[data-tabbar="keys"]');
    await P.tap('[data-k="triglvlpush"]');
    await P.until(async () => zero((await rows())["trig.level"].Text), "Level push puts the level to 0 V");
    await P.tap('[data-k="ch1pospush"]');
    await P.until(async () => zero((await rows())["ch1.offset"].Text), "CH1 Position push puts the offset to 0 V");
    const zoom0 = (await rows())["time.zoom"].Value;
    await P.tap('[data-k="tdivpush"]');
    await P.until(async () => (await rows())["time.zoom"].Value !== zoom0, "S/DIV push turns Zoom on");
    await P.tap('[data-k="tdivpush"]');
    await P.until(async () => (await rows())["time.zoom"].Value === zoom0, "and off again");
    const pos0 = (await rows())["time.trig_pos"].Text;
    await P.tap('[data-knob="horizpos"][data-q="cw"]');
    await P.until(async () => (await rows())["time.trig_pos"].Text !== pos0, "the Position knob moves the trigger position (" + pos0 + ")");
    await P.tap('[data-knob="horizpos"][data-push]');
    await P.until(async () => (await rows())["time.trig_pos"].Text === pos0, "its push centres it again");
    // each key's own answer (the toast cleared first): the phone's confirmation, or the scope's notice
    for (const [k, said] of [["force", /^Forced a trigger$/], ["set50", /^Level set to 50 %$/], ["print", /^(Print: the scope saves a screenshot|PRINT: .+)$/],
      ["ch1vdivpush", /^CH1 Variable: no fine V\/div/]]) {
      await page.evaluate(() => { const e = document.querySelector(".toast"); if (e) { e.hidden = true; e.textContent = ""; } });
      await P.tap(`[data-k="${k}"]`);
      await P.until(async () => said.test(await P.toast()), "the " + k + " key answers");
    }
    await P.tap('[data-k="menu:trigmenu"]');
    await P.until(() => P.visible('.mbody [data-row="trig.holdoff"]'), "Trig Menu opens TRIG here");
    P.ok(await lcdClosed(), "the keys and knobs left no menu open on the LCD");
    const ks = await P.text("#keep-awake small"), fs = await P.text("#fullscreen small");
    P.ok(/^(awake: wake lock|awake: video|off: tap to keep awake)/.test(ks), "keep awake says its state: " + ks);
    if (opts && opts.noWakeLock) {
      P.ok(/^awake: video/.test(ks), "without a wake lock (plain HTTP) the silent video keeps the phone awake");
      const vid = () => page.evaluate(() => {
        const v = document.querySelector("video.awake-video");
        if (v && v.seeks === undefined) { v.seeks = 0; v.addEventListener("seeking", () => { v.seeks++; }); }
        return v ? { paused: v.paused, t: v.currentTime, muted: v.muted, loop: v.loop, seeks: v.seeks } : null;
      });
      const v1 = await vid();
      await sleep(1600);
      const v2 = await vid();
      P.ok(!!v1 && !!v2 && !v2.paused && !v1.muted && !v1.loop && v2.seeks >= 1,
        "the video plays on past its 1 s length, unmuted, seeking back instead of looping: " + JSON.stringify([v1, v2]));
    }
    P.ok(/^(on|off|refused|not )/.test(fs), "fullscreen says its state: " + fs);

    // the scope's server drops: the banner, then the resync
    dropped = true;
    await ctrl("/down");
    await P.until(() => P.visible("#banner"), "the scope gone: a connection-lost banner", 6000);
    P.ok(/Connection lost - retrying/.test(await P.text("#banner")), "it says the remote is retrying");
    await P.shot("offline");
    await ctrl("/knob?name=tdiv&dir=1");
    await sleep(1200);
    await ctrl("/up");
    await P.until(async () => !(await P.visible("#banner")), "the scope back: the banner goes", 12000);
    dropped = false;
    await P.tap('[data-tabbar="home"]');
    await P.until(async () => (await P.text('[data-tile="time.tdiv"] .tv')) === "200 us", "the change made while away shows after the resync");
    await P.tap('[data-tabbar="hist"]');
    P.ok(/\bscope\b/.test(await P.text(".hent:first-child .hl small") || ""), "and is listed as the scope's");
    P.ok(await lcdClosed(), "the remote never left a menu open on the LCD");
    t.ok(P.errors.length === 0, P.name + ": no console errors" + (P.errors.length ? " - " + P.errors.slice(0, 3).join(" | ") : ""));
    await P.ctx.close();
  }

  await flow("iPhone 14", "iPhone 14");
  await flow("Pixel 7", "Pixel 7", { noWakeLock: true });

  // ---- two phones at once: each follows the other; the other's change is "other remote" ------
  {
    await ctrl("/reset");
    const A = await phone("iPhone 14", { label: "phone A" }), B = await phone("Pixel 7", { label: "phone B" });
    await A.tap('[data-tile="ch1.vdiv"] [data-q="cw"]');
    await B.until(async () => (await B.text('[data-tile="ch1.vdiv"] .tv')) === "500 mV", "phone B follows phone A's change at once (one long-poll each)");
    await B.tap('[data-tabbar="hist"]');
    await B.until(async () => /other remote/.test(await B.text(".hent:first-child .hl small") || ""), "phone B lists it as another remote's");
    await sleep(2100);
    await B.tap('[data-tabbar="home"]');
    await B.tap('[data-tile="ch2.vdiv"] [data-q="ccw"]');
    await A.until(async () => (await A.text('[data-tile="ch2.vdiv"] .tv')) === "2 V", "phone A follows phone B");
    t.ok(A.errors.length + B.errors.length === 0, "two phones: no console errors");
    await A.ctx.close();
    await B.ctx.close();
  }

  // ---- three phone sizes: every tab and sheet kind keeps the layout rules ---------------------
  for (const [label, dev, vp] of [["390x844", "iPhone 14", { width: 390, height: 844 }], ["360x780", "Pixel 7", { width: 360, height: 780 }],
    ["844x390", "Pixel 7", { width: 844, height: 390 }]]) {
    await ctrl("/reset");
    const P = await phone(dev, { viewport: vp, label });
    await layout(P, "Home");
    await P.shot("home");
    await P.tap('[data-tile="time.tdiv"] .tmain');
    await layout(P, "number sheet");
    await P.shot("sheet-number");
    await P.tap(".sheet-close");
    await P.tap('[data-tile="trig.edge"] .tmain');
    await layout(P, "options sheet");
    await P.shot("sheet-options");
    await P.tap(".sheet-close");
    await P.tap('[data-ws="bus"]');
    await P.until(async () => (await P.text("#again")) === "again: Eye diagram", "BUS: again: Eye diagram");
    await P.shot("home-bus");
    await P.tap('[data-tile="bus.match"] .tmain');
    await layout(P, "bytes sheet");
    await P.shot("sheet-bytes");
    await P.tap(".sheet-close");
    await P.tap('[data-tile="tool"] .tmain');
    await layout(P, "tools sheet");
    await P.shot("sheet-tools");
    await P.tap(".sheet-close");
    await P.tap('[data-ws="measure"]');
    await P.shot("home-measure");
    await P.tap('[data-tabbar="menus"]');
    await layout(P, "Menus");
    await P.shot("menus");
    await P.page.locator("#search").fill("coupling");
    await P.until(() => P.visible('.mbody [data-row="ch2.coupling"]'), "search: coupling");
    await layout(P, "search results");
    await P.shot("menus-search");
    await P.page.locator("#search").fill("");
    await P.until(() => P.visible('[data-tab="file"]'), "an empty search shows the tabs again");
    await P.tap('[data-tab="file"]');
    await layout(P, "FILE page");
    await P.shot("menus-file");
    await P.tap('[data-row="file.setup3"] [data-act="save"]');
    await P.until(async () => (await rows())["file.setup3"].Text !== "empty", "Save to setup 3 from its row");
    await P.tap('[data-row="file.setup3"] [data-open]');
    await layout(P, "slot sheet");
    await P.shot("sheet-slot");
    await P.tap(".sheet-close");
    await P.tap("#back");
    await P.tap('[data-tab="sys"]');
    await P.shot("menus-sys");
    await P.tap('[data-tabbar="hist"]');
    await layout(P, "History");
    await P.shot("history");
    await P.tap(".hent:last-child .hmain");
    await P.until(() => P.visible('.sheet [data-sheet="hist"]'), "an entry opens its details");
    await layout(P, "history entry sheet");
    await P.shot("sheet-history");
    await P.tap(".sheet-close");
    await P.tap('[data-tabbar="keys"]');
    await layout(P, "Keys");
    await P.shot("keys");
    await P.tap('[data-k="help"]');
    await layout(P, "help sheet");
    await P.shot("sheet-help");
    await P.tap(".sheet-close");
    await P.tap('[data-tabbar="home"]');
    await P.hold('[data-tile="time.tdiv"] .tmain', 700);
    await P.until(() => P.visible("#add-tile"), "a long press edits the tiles");
    await layout(P, "edit tiles");
    await P.shot("home-edit");
    await P.tap("#add-tile");
    await P.until(() => P.visible('.sheet [data-sheet="add"] [data-add]'), "Add a tile lists the settings");
    await layout(P, "add sheet");
    await P.shot("sheet-add");
    await P.tap(".sheet-close");
    await P.tap("#edit-done");
    t.ok(P.errors.length === 0, label + ": no console errors" + (P.errors.length ? " - " + P.errors.slice(0, 3).join(" | ") : ""));
    await P.ctx.close();
  }
  await ctrl("/reset");
});
