// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e for the COMPARE workspace (argv[2]):
//   * the difference readout: REF A against the live channels (max |Δ|, RMS,
//     % within 0.5 div), following Show/Clear; math draws;
//   * Stack master has the scope stack its master (the scope's Stack master
//     row, mocked here: the panel's master has its own tests): the page sets
//     the depth, starts the stack, follows the row and takes the scope's REF A
//     ("master ×64"); a refusal is said in the scope's words; Esc cancels.
//     The page no longer averages itself (it timed out on the SRAM image).
// The Go wrapper checks the page drove no acquisition of its own.
// SKIP/exit 0 without a browser.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2], { viewport: { width: 1600, height: 1000 } });
  t.browser = browser;
  // TRLC-LINKS: REQ-SDS-218
  const diff = (p) => p.evaluate(() => [...document.querySelectorAll("#diffBody tr")].map((r) => ({
    ref: r.dataset.ref || "", head: r.querySelector("th") ? r.querySelector("th").textContent : "", cells: [...r.querySelectorAll("td")].map((c) => c.textContent),
  })));
  // ---- the difference readout ------------------------------------------------------
  await page.click("#tab-compare");
  t.ok(/Save and show a reference/.test(await po.text("diffBody")), "with no reference the readout says what to do");
  await po.click("refSaveA");
  t.ok(/C1\+C2/.test(await po.text("refInfoA")), "Save A stores both shown channels: " + await po.text("refInfoA"));
  await t.until(async () => (await diff(page)).filter((r) => r.ref === "A" && r.cells.length === 3).length === 2, "REF A is compared with C1 and C2", 3000);
  const d0 = await diff(page);
  const a1 = d0.find((r) => r.head === "REF A vs C1");
  t.ok(a1 && /^0 V \(0\.00 div\)$/.test(a1.cells[0]) && a1.cells[2] === "100.0 %", "identical captures: max |Δ| 0, 100 % within 0.5 div: " + JSON.stringify(a1));
  await po.click("refShowA");
  await t.until(async () => (await diff(page)).every((r) => r.ref !== "A"), "a hidden reference leaves the readout", 2000);
  t.ok(await page.evaluate(() => document.querySelector("#tab-compare .tdot").hidden), "…and COMPARE's dot goes");
  await po.click("refShowA");
  await t.until(async () => (await diff(page)).some((r) => r.ref === "A"), "shown again, it is compared again", 2000);
  await po.click("refClearA");
  t.ok(await po.text("refInfoA") === "empty" && await page.evaluate(() => document.getElementById("refShowA").disabled), "Clear empties REF A (Show disabled)");
  await po.setSelect("mathFn", "c1-c2");
  await t.until(async () => /V\/div · zero at centre/.test(await po.text("mathScale")), "math C1 − C2 draws, with its scale", 2000);
  await po.setSelect("mathFn", "off");

  // ---- Stack master: the scope's master, shown here ---------------------------------
  const posts = [];
  page.on("request", (r) => { if (r.method() === "POST" && /\/api\/sheets\/(action|set)/.test(r.url())) posts.push(r.postData() || ""); });
  let rowText = "none", actionReply = { ok: true }, stackAsked = 0;
  await page.route("**/api/sheets/action", (route) => {
    const b = JSON.parse(route.request().postData() || "{}");
    if (b.row === "cmp.stack_master" && b.action === "stack") { stackAsked++; if (actionReply.ok) rowText = "stacking"; }
    if (b.row === "cmp.stack_master" && b.action === "cancel") rowText = "none";
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(actionReply) });
  });
  await page.route("**/api/sheets", (route) => route.fulfill({ status: 200, contentType: "application/json",
    body: JSON.stringify({ Tabs: [{ ID: "cmp", Rows: [{ ID: "cmp.stack_master", Kind: "action", Text: rowText }] }] }) }));
  const sq = Array.from({ length: 800 }, (_, i) => ((Math.floor(i / 50) % 2) ? 180 : 76));
  await page.route("**/api/refs", (route) => route.fulfill({ status: 200, contentType: "application/json",
    body: JSON.stringify({ ok: true, refs: [{ has: true, show: true, cols: 800, c1: sq, vdiv: [1, 1], off: [0, 0], tdiv_s: 0.0005, pos_frac: 0.5 }, { has: false }] }) }));
  await page.evaluate(() => document.activeElement.blur());
  await page.keyboard.press("Alt+5"); // COMPARE again: Stack master
  await t.until(async () => stackAsked === 1, "COMPARE again starts the scope's stack", 4000);
  t.ok(posts.some((p) => /"row":"cmp.master_depth","value":64/.test(p)), "…at the page's depth (64): " + JSON.stringify(posts));
  await t.until(async () => /The scope is averaging 64 captures/.test(await po.text("masterStat")), "the pane follows the scope's stack", 4000);
  rowText = "REF A: 64 x C1";
  await t.until(async () => /master ×64/.test(await po.text("refInfoA")), "the scope's REF A is this page's REF A, \"master ×64\"", 6000);
  t.ok(/Master ×64 of C1 stored as REF A/.test(await po.text("masterStat")), "the pane says what was done: " + await po.text("masterStat"));
  await t.until(async () => (await diff(page)).some((r) => r.ref === "A"), "the master is compared with the live trace", 3000);
  t.ok(await po.text("masterBtn") === "Stack master" && !(await po.hasClass("masterBtn", "on")), "the button is ready again");
  // Esc cancels: the scope's stack is cancelled
  rowText = "none"; stackAsked = 0;
  await po.click("masterBtn");
  await t.until(async () => stackAsked === 1, "a second master starts", 4000);
  await page.keyboard.press("Escape");
  await t.until(async () => posts.some((p) => /"action":"cancel"/.test(p)), "Esc cancels the scope's stack", 6000);
  await t.until(async () => /cancelled/i.test(await po.text("masterStat")) && await po.text("masterBtn") === "Stack master", "…and says so", 6000);
  // the scope refuses: its words
  actionReply = { ok: false, error: "No master: C1 differs between captures (1/3 repeat) - set a stable trigger first" };
  await po.click("masterBtn");
  await t.until(async () => /Master refused: No master: C1 differs between captures/.test(await po.text("masterStat")), "a refusal is said in the scope's words", 6000);
  t.ok(/Master refused/.test(await po.text("toast")), "…with a notice");
  await page.unrouteAll({ behavior: "ignoreErrors" });

  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
