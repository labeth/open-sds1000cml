// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e for All settings (argv[2]=URL): every control of the page is
// in its catalogue or named as navigation (ALL_EXCLUDE); every row's arrow
// shows its control where it lives — workspace, view, pane or menu — or says
// when it shows; a row's edit is the control's edit and follows edits made
// elsewhere; the filter finds settings by words; Default setup answers; the
// Setups popover saves and recalls slot 3 (the Go wrapper checks the store).
// SKIP/exit 0 without a browser.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2], { viewport: { width: 1600, height: 1000 } });
  t.browser = browser;
  const posts = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && /\/api\/(set|setup)\b/.test(r.url())) { try { posts.push(JSON.parse(r.postData() || "{}")); } catch { /* ignore */ } }
  });

  // ---- the catalogue names every control ---------------------------------------
  const cat = await page.evaluate(() => {
    const listed = new Set();
    for (const [, list] of ALL_GROUPS) for (const e of list) { listed.add(e[0]); for (const a of e[3] || []) listed.add(a); }
    const unlisted = [], gone = [];
    for (const el of document.querySelectorAll("[id]")) {
      if (!el.matches("button, select, input, textarea, .seg")) continue;
      if (el.closest("#allcol")) continue; // All settings' own rows
      if (!listed.has(el.id) && !ALL_EXCLUDE.has(el.id)) unlisted.push(el.id);
    }
    for (const id of listed) if (!document.getElementById(id)) gone.push(id);
    return { unlisted, gone, n: listed.size };
  });
  t.ok(cat.unlisted.length === 0, "every control is in All settings or named as navigation" + (cat.unlisted.length ? " — not listed: " + cat.unlisted.join(", ") : ""));
  t.ok(cat.gone.length === 0, `every one of the ${cat.n} catalogue ids exists` + (cat.gone.length ? " — missing: " + cat.gone.join(", ") : ""));

  // ---- the column ------------------------------------------------------------------
  await page.click("#allBtn");
  t.ok(await po.isVisible("allcol") && await po.hasClass("allBtn", "on"), "the All settings button opens the column beside the workspace");
  const entries = await page.evaluate(() => allEntries().map((e) => e.id));
  const rows = await page.evaluate(() => document.querySelectorAll("#allList .ar[data-entry]").length);
  t.ok(rows === entries.length, `one row per setting (${rows} of ${entries.length})`);
  t.ok(await page.evaluate(() => document.body.scrollWidth <= window.innerWidth + 1), "the page does not scroll sideways with the column open");

  // decoding on (UART) so entering BUS does not run its detection mid-test
  await po.eval(() => { const e = document.getElementById("decProto"); e.value = "uart"; e.dispatchEvent(new Event("change")); });

  // ---- every arrow shows its control -----------------------------------------------
  const bad = [];
  for (const id of entries) {
    await page.evaluate(() => { const tt = document.getElementById("toast"); tt.hidden = true; tt.textContent = ""; });
    const go = page.locator(`#allList .ar[data-entry="${id}"] .go`);
    await go.scrollIntoViewIfNeeded();
    await go.click();
    await page.waitForTimeout(40);
    const r = await page.evaluate((i) => {
      const el = document.getElementById(i), a = document.activeElement, tt = document.getElementById("toast");
      return {
        vis: el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden",
        focused: a === el || el.contains(a), disabled: !!el.disabled, said: tt.hidden ? "" : tt.textContent,
      };
    }, id);
    if (!((r.vis && (r.focused || r.disabled)) || / shows (when|with|in|once|while) /.test(r.said)))
      bad.push(`${id} (shown ${r.vis}, focused ${r.focused}${r.said ? ', said "' + r.said + '"' : ""})`);
  }
  t.ok(bad.length === 0, `every row's arrow shows its control or says when it shows (${entries.length} rows)` + (bad.length ? " — " + bad.join("; ") : ""));
  // a control shown only under a protocol says so
  await page.locator('#allList .ar[data-entry="decScl"] .go').click();
  t.ok(/I²C SCL channel shows with I²C decoding/.test(await po.text("toast")), "an I²C role's arrow says it shows with I²C decoding");
  await page.locator('#allList .ar[data-entry="p-min"] .go').click();
  t.ok(/trigger type is Pulse/.test(await po.text("toast")) && await page.evaluate(() => document.getElementById("ttype").contains(document.activeElement)),
    "a pulse qualifier's arrow lands on the trigger type and says Pulse");
  await page.locator('#allList .ar[data-entry="eCSV"] .go').click();
  t.ok(await po.isVisible("exportpop") && await page.evaluate(() => document.activeElement.id) === "eCSV", "an Export entry's arrow opens the Export menu on it");
  await page.keyboard.press("Escape");

  // ---- a row edits its control, and follows edits made elsewhere -------------------
  await po.eval(() => { const e = document.getElementById("allFilter"); e.value = ""; e.dispatchEvent(new Event("input")); });
  await page.locator('#allList .ar[data-entry="cpl1"] .ac button[data-value="1"]').click();
  await t.until(async () => (await po.value("cpl1")) === "1", "CH1 coupling AC in its row sets the Vertical pane's coupling");
  await t.until(() => posts.some((p) => p.control === "coupling1" && p.value === 1), "…and the scope is told (coupling1 = AC)", 3000);
  await page.locator('#allList .ar[data-entry="cpl1"] .ac button[data-value="0"]').click();
  await po.seg("mode", "1");
  await t.until(() => page.evaluate(() => document.querySelector('#allList .ar[data-entry="mode"] .ac .seg button.on')?.dataset.value === "1"),
    "the Trigger mode row follows a change made in the strip", 2500);
  await po.seg("mode", "0");
  await po.click("allBtn"); await po.click("allBtn"); // reopen: the column keeps working after a toggle
  const tdivRow = page.locator('#allList .ar[data-entry="tdiv"] .ac select');
  const tdivNow = await po.value("tdiv");
  const tdivOpts = await tdivRow.evaluate((s) => [...s.options].map((o) => o.value));
  const other = tdivOpts.find((v) => v !== tdivNow && +v >= 1e-6 && +v <= 1e-3);
  await tdivRow.selectOption(other);
  await t.until(async () => (await po.value("tdiv")) === other, "a Time/div row choice sets the strip's timebase", 3000);

  // ---- the filter --------------------------------------------------------------------
  await page.fill("#allFilter", "trig level");
  const shown = await page.evaluate(() => [...document.querySelectorAll("#allList .ar[data-entry]")].filter((r) => !r.hidden && !r.closest(".ag").hidden).map((r) => r.dataset.entry));
  t.ok(shown.includes("lvl") && shown.length < 6, `"trig level" finds the trigger level (${shown.join(", ")})`);
  await page.fill("#allFilter", "zzzz");
  t.ok(await po.isVisible("allNone") && /No setting matches/.test(await po.text("allNone")), "a word that matches nothing says so");
  await page.fill("#allFilter", "");
  await page.click("#allClose");
  t.ok(!(await po.isVisible("allcol")), "Close hides the column");

  // ---- top bar: Default setup and Setups ---------------------------------------------
  await page.click("#defaultBtn");
  await t.until(async () => /Default setup/.test(await po.text("toast")), "Default setup answers (this test server has no panel key for it)", 3000);
  await page.click("#setupsBtn");
  t.ok(await po.isVisible("setpop") && (await page.getAttribute("#setupsBtn", "aria-expanded")) === "true", "Setups opens its slots");
  t.ok(await page.evaluate(() => document.querySelectorAll("#setSlots [data-slot]").length) === 20, "20 slots");
  await page.click('#setSlots [data-slot="3"] button[data-op="save"]');
  await t.until(async () => /Setup saved in slot 3/.test(await po.text("toast")), "Save slot 3 stores the setup", 3000);
  await page.click('#setSlots [data-slot="3"] button[data-op="recall"]');
  await t.until(async () => /Setup 3 recalled/.test(await po.text("toast")), "Recall slot 3 brings it back", 3000);
  await page.keyboard.press("Escape");
  t.ok(!(await po.isVisible("setpop")), "Esc closes the Setups menu");

  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
