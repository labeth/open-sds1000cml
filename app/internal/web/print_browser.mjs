// Real-browser e2e: the web's Device screenshot (PRINT) presses the scope's
// PRINT and then says what the scope saved, as the LCD does (it only ever
// said "saving"; bug hunt #3). argv[2]=URL. SKIP/exit 0 without browser.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, browser, pageErrors } = await openScope(process.argv[2] + "/", { waitFrame: false, viewport: { width: 1440, height: 900 } });
  t.browser = browser;
  await page.waitForFunction(() => document.getElementById("ePrint") && typeof panelPress === "function", null, { timeout: 15000 });
  await page.evaluate(() => document.getElementById("ePrint").click());
  await t.until(async () => /Saved on the scope's USB stick as SDS0042\.png/.test(await page.evaluate(() => document.getElementById("toast").textContent || "")),
    "the page names the file the scope saved (SDS0042.png)", 8000);
  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
