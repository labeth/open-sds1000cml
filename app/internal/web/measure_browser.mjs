// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e for the MEASURE workspace (argv[2]=URL of a scope serving a
// 50 % square of 64 samples on C1 at 409.6 kSa/s, 6.4 kHz). The statistics
// table counts every capture (now, mean, min, max, sdev, N, a unit per row),
// Reset and a scale change restart it, the channel choice filters it; MEASURE
// again lists every parameter of both channels; the cursors read out; the
// mask pane builds a mask and counts; Super-res and Precision open from their
// collapsed headers. SKIP/exit 0 without a browser.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2], { viewport: { width: 1600, height: 1000 } });
  t.browser = browser;
  // TRLC-LINKS: REQ-SDS-217
  const stat = (key) => page.evaluate((k) => {
    const r = document.querySelector(`#statsBody tr[data-stat="${k}"]`);
    return r ? { unit: r.querySelector("th .unit").textContent, td: [...r.querySelectorAll("td")].map((c) => c.textContent) } : null;
  }, key);

  await page.click("#tab-measure");
  t.ok(await po.isVisible("statsTable") && !(await po.isVisible("valuesTable")), "MEASURE opens on the statistics");
  t.ok(await page.evaluate(() => [...document.querySelectorAll("#statsTable thead th")].map((h) => h.textContent).join("|")) === "Measurement|Now|Mean|Min|Max|Sdev|N",
    "columns: now, mean, min, max, sdev, N");
  t.ok(await page.evaluate(() => document.querySelectorAll("#statsBody tr[data-stat]").length) === 36, "18 parameters × 2 channels (preshoot too)");
  await t.until(async () => { const s = await stat("1:Freq"); return s && +s.td[5] >= 5; }, "N counts the captures", 8000);
  const f = await stat("1:Freq");
  t.ok(f.unit === "kHz", "the row carries its unit: " + f.unit);
  t.near(parseFloat(f.td[1]), 6.4, 0.05, "mean frequency (kHz)");
  t.ok(f.td[2] === f.td[3] && f.td[3] === f.td[1], "identical captures: min = max = mean");
  t.ok(parseFloat(f.td[4]) === 0, "…and sdev 0: " + f.td[4]);
  const d = await stat("1:Duty");
  t.near(parseFloat(d.td[1]), 50, 1, "mean duty (%)");
  const n1 = +f.td[5];
  await po.wait(800);
  t.ok(+(await stat("1:Freq")).td[5] > n1, "N keeps counting");
  await page.click("#statsReset");
  await t.until(async () => +(await stat("1:Freq")).td[5] < n1, "Reset restarts the statistics", 2000);
  await po.wait(600);
  const n2 = +(await stat("1:Freq")).td[5];
  t.ok(n2 >= 1, "…which count again from there: " + n2);
  // a scale change restarts them too: those captures do not belong together
  await po.wait(1000);
  const n3 = +(await stat("1:Freq")).td[5];
  await page.click("#vdiv1Dn");
  await t.until(async () => +(await stat("1:Freq")).td[5] < n3, "a V/div change restarts the statistics", 3000);
  await page.click("#vdiv1Up");
  // as on the scope, they count only while MEASURE is shown (not every
  // workspace's captures: the FFT's are filtered frames)
  const r0 = +(await stat("1:Freq")).td[5];
  await po.wait(1500);
  const n4 = +(await stat("1:Freq")).td[5]; // counted in 1.5 s here: n4 - r0
  await page.click("#tab-scope");
  await po.wait(1500);
  await page.click("#tab-measure");
  const n5 = +(await stat("1:Freq")).td[5];
  t.ok(n5 - n4 < (n4 - r0) / 2, `no counting while SCOPE was shown (${n4 - r0} in 1.5 s in MEASURE; ${n4} -> ${n5} across 1.5 s in SCOPE)`);
  await t.until(async () => +(await stat("1:Freq")).td[5] > n5, "…and counting again in MEASURE", 3000);
  // the channel choice
  await po.seg("measCh", "2");
  await t.until(() => page.evaluate(() => document.querySelectorAll('#statsBody tr[data-stat^="1:"]').length === 0 && document.querySelectorAll('#statsBody tr[data-stat^="2:"]').length === 18),
    "C2 shows only CH2's rows", 2000);
  await po.seg("measCh", "0");

  // MEASURE again: every value of both channels
  await page.evaluate(() => document.activeElement.blur());
  await page.keyboard.press("Alt+4");
  t.ok(await po.isVisible("valuesTable") && !(await po.isVisible("statsTable")), "MEASURE again: all values");
  await t.until(() => page.evaluate(() => [...document.querySelectorAll("#measBody [data-meas]")].filter((c) => c.textContent.trim()).length === 36),
    "all 18 parameters of both channels read", 3000);
  // nine and nine side by side, as on the scope: Duty ends the first column, Preshoot the second
  t.ok(await page.evaluate(() => { const r = [...document.querySelectorAll("#measBody tr")].filter((x) => !x.classList.contains("hd"));
    return r.length === 9 && r[7].querySelector("th").textContent === "Duty" && r[8].querySelectorAll("th")[1].textContent === "Preshoot"; }), "all values in two columns of nine");
  t.ok(await page.evaluate(() => /kHz/.test(document.querySelector('#measBody [data-meas="1:Freq"]').textContent)), "with units");
  await page.keyboard.press("Alt+4");
  t.ok(await po.isVisible("statsTable"), "MEASURE again: back to the statistics");

  // cursors
  t.ok(/Cursors are off/.test(await po.text("curBody")), "the Cursors pane says how to turn them on");
  await po.click("tCursors");
  await page.evaluate(() => { cur.t1 = 0.25; cur.t2 = 0.75; updateCursors(); });
  const cu = await page.evaluate(() => Object.fromEntries([...document.querySelectorAll("#curBody tr")].map((r) => [r.querySelector("th").textContent, r.querySelector("td").textContent])));
  t.ok(["A / B", "Δt", "1/Δt", "ΔV C1", "ΔV C2"].every((k) => cu[k]), "the cursor readout: A / B, Δt, 1/Δt, ΔV per channel");
  // half the screen of a 10-division 500 µs/div sweep: 2.5 ms
  t.ok(/^2\.5\d* ms$/.test(cu["Δt"]), "Δt reads half the sweep: " + cu["Δt"]);
  await po.click("tCursors");

  // the mask pane: build from the live square, test counts
  await po.eval(() => { const e = document.getElementById("zmN"); e.value = "4"; e.dispatchEvent(new Event("change")); });
  await po.click("zmBuild");
  await po.waitFor(() => zm.mask && zm.mask.win > 0, null, 20000);
  await po.seg("zmMode", "1");
  await t.until(async () => /pass \d/.test(await po.text("zmMeter")), "the mask test counts against the built mask", 6000);
  await t.until(() => page.evaluate(() => !document.querySelector("#tab-measure .tdot").hidden), "the MEASURE tab carries a dot while the mask test runs", 2500);
  await po.seg("zmMode", "0");

  // the collapsed panes open
  for (const id of ["p-sr", "p-prec"]) {
    t.ok(await page.evaluate((p) => document.getElementById(p).parentElement.classList.contains("is-collapsed"), id), `${id} starts collapsed (header only)`);
    await page.click(`#${id} > .ph > .pc`);
    t.ok(await page.evaluate((p) => !document.getElementById(p).parentElement.classList.contains("is-collapsed") && document.getElementById(p).querySelector(".pb").getClientRects().length > 0, id), `${id} opens from its caret`);
  }
  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
