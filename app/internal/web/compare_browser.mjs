// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e for the COMPARE workspace on three scopes (argv[2..4]): a
// repeating square whose average mode works like the deep-memory path (block
// average, SINGLE completes the block), the same square on the fast path
// (running average), and a free-running square that does not repeat.
//   * the difference readout: REF A against the live channels (max |Δ|, RMS,
//     % within 0.5 div), following Show/Clear; math draws;
//   * COMPARE again stacks a master: REF A "master ×64", the mask built from
//     it, the acquisition and trigger mode restored, the test counting;
//     Esc cancels one;
//   * the free-running signal is refused with trigger advice and nothing is
//     touched.
// The Go wrapper checks the acquisition calls each scope saw.
// SKIP/exit 0 without a browser.
import { openScope, pageObject, run } from "./scope_po.mjs";

run(async (t) => {
  const [blockURL, ringURL, freeURL] = process.argv.slice(2);
  const { page, po, browser, pageErrors } = await openScope(blockURL, { viewport: { width: 1600, height: 1000 } });
  t.browser = browser;
  // TRLC-LINKS: REQ-SDS-218
  const open = async (url) => {
    const p = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
    const errs = [];
    p.on("pageerror", (e) => errs.push(e.message));
    await p.goto(url, { waitUntil: "domcontentloaded", timeout: 15000 });
    await p.waitForFunction(() => typeof frame !== "undefined" && frame && frame.c1, null, { timeout: 15000 });
    return { p, errs, po: pageObject(p) };
  };
  // TRLC-LINKS: REQ-SDS-218
  const diff = (p) => p.evaluate(() => [...document.querySelectorAll("#diffBody tr")].map((r) => ({
    ref: r.dataset.ref || "", head: r.querySelector("th") ? r.querySelector("th").textContent : "", cells: [...r.querySelectorAll("td")].map((c) => c.textContent),
  })));
  // TRLC-LINKS: REQ-SDS-218
  const masterChecks = async (p, q, label) => {
    await t.until(async () => /master ×64/.test(await q.text("refInfoA")), `${label}: the master lands in REF A as "master ×64"`, 40000);
    await t.until(async () => /Master ×64 stored as REF A/.test(await q.text("masterStat")), `${label}: the pane says what it did`, 5000);
    t.ok(await q.value("acq") === "0", `${label}: the acquisition is back to Normal (live captures are tested raw)`);
    t.ok(await q.value("mode") === "0", `${label}: the trigger mode is back to Auto`);
    t.ok(await q.value("zmMode") === "1", `${label}: the mask test is counting`);
    t.ok(await p.evaluate(() => !!(zm.mask && zm.mask.win > 0 && zm.mask.lo.length === zm.mask.win)), `${label}: the mask is built from the master`);
    await t.until(async () => /pass \d/.test(await q.text("zmMeter")), `${label}: the meter counts`, 4000);
    await t.until(async () => { const d = await diff(p); const r = d.find((x) => x.head === "REF A vs C1"); return r && r.cells[2] === "100.0 %"; },
      `${label}: the live square sits on the master (100 % within 0.5 div)`, 4000);
    t.ok(await q.text("masterBtn") === "Stack master" && !(await q.hasClass("masterBtn", "on")), `${label}: the button is ready again`);
  };

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

  // ---- COMPARE again stacks a master (deep-memory block average) --------------------
  await page.evaluate(() => document.activeElement.blur());
  await page.keyboard.press("Alt+5");
  await masterChecks(page, po, "block average");
  // Esc cancels a running master; the acquisition stays as it was
  await po.click("masterBtn");
  await page.keyboard.press("Escape");
  await t.until(async () => /cancelled/i.test(await po.text("masterStat")) && await po.text("masterBtn") === "Stack master", "Esc cancels a master", 8000);
  t.ok(await po.value("acq") === "0", "…and the acquisition is Normal");

  // ---- the fast path: a running average --------------------------------------------
  const R = await open(ringURL);
  await R.p.click("#tab-compare");
  await R.p.click("#masterBtn");
  await masterChecks(R.p, R.po, "running average");
  t.ok(R.errs.length === 0, "running average: no page errors " + R.errs.join("; "));

  // ---- a signal that does not repeat is refused -------------------------------------
  const F = await open(freeURL);
  await F.p.click("#tab-compare");
  await F.p.click("#masterBtn");
  await t.until(async () => /Master refused: C1 does not repeat from capture to capture/.test(await F.po.text("masterStat")), "a free-running signal is refused", 10000);
  t.ok(/trigger on C1 in Normal mode with the level at 50 %/.test(await F.po.text("masterStat")), "…with advice on the trigger: " + await F.po.text("masterStat"));
  t.ok(/Master refused/.test(await F.po.text("toast")), "…and a notice");
  t.ok(await F.po.text("refInfoA") === "empty" && await F.po.value("acq") === "0" && await F.po.value("zmMode") === "0", "nothing was stored or changed");
  t.ok(F.errs.length === 0, "free-running: no page errors " + F.errs.join("; "));

  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
