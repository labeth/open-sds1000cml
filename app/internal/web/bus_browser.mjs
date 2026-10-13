// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e for the BUS workspace (argv[2]=URL of a scope serving a
// 115200-baud UART on C1: "Hi" twice, then a third "Hi" cut by the record end).
// Entering BUS with decoding off finds the UART and turns decoding on with a
// notice; the packet table lists only the two complete messages in fixed
// columns (hex, then a left-aligned string) and holds still while they repeat;
// a click (or Enter) freezes the display and zooms onto the packet; the strip's
// format is a setting (hex by default); BUS again shows the eye of the decoded
// line with its figures, and again goes back. SKIP/exit 0 without a browser.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2], { viewport: { width: 1600, height: 1000 } });
  t.browser = browser;
  // TRLC-LINKS: REQ-SDS-214
  const rows = () => page.evaluate(() => [...document.querySelectorAll("#pkBody tr[data-i]")].map((r) => [...r.querySelectorAll("td")].map((c) => c.textContent)));

  // ---- entry: detection turns decoding on ---------------------------------------
  t.ok(await po.value("decProto") === "off", "decoding starts off");
  await page.click("#tab-bus");
  await t.until(async () => (await po.value("decProto")) === "uart", "entering BUS finds the UART and turns decoding on", 10000);
  await t.until(async () => /BUS: detected .*UART.*decoding is on/i.test(await po.text("toast")), "…and says so in a notice", 3000);
  const note = await po.text("toast"), baud = +(await po.value("decBaud"));
  t.ok(/115/.test(note) && (baud === 0 || Math.abs(baud - 115200) < 3000), `…naming the stream's rate, the decoder following it (bit rate ${baud === 0 ? "auto" : baud}): "${note}"`);
  t.ok(await page.evaluate(() => !document.querySelector("#tab-bus .tdot").hidden), "the BUS tab carries a dot while decoding");

  // ---- the packet table ------------------------------------------------------------
  await t.until(async () => (await rows()).length === 2, "the table lists the two complete messages, not the one the record end cuts", 6000);
  const r0 = await rows();
  t.ok(r0.every((c) => c[3] === "48 69" && c[4] === "Hi" && c[5] === "2"), "each row reads hex 48 69, string Hi, 2 bytes: " + JSON.stringify(r0));
  t.ok(r0.every((c) => /s$/.test(c[1])), "each row has its time from the trigger");
  const lay = await page.evaluate(() => {
    const tb = document.getElementById("pktable"), r = document.querySelector("#pkBody tr[data-i]");
    return {
      layout: getComputedStyle(tb).tableLayout,
      heads: [...tb.querySelectorAll("thead th")].map((th) => th.textContent).join("|"),
      hex: getComputedStyle(r.querySelector("td.hex")).textAlign, str: getComputedStyle(r.querySelector("td.str")).textAlign,
      widths: [...tb.querySelectorAll("thead th")].map((th) => Math.round(th.getBoundingClientRect().width)),
    };
  });
  t.ok(lay.layout === "fixed", "the table's columns are fixed (table-layout: fixed)");
  t.ok(lay.heads === "#|Time|Addr|Hex|String|Bytes", "columns # Time Addr Hex String Bytes: " + lay.heads);
  t.ok(/left|start/.test(lay.hex) && /left|start/.test(lay.str), `hex and string are left-aligned (${lay.hex}, ${lay.str})`);
  // the rows hold still while the same message repeats
  await page.evaluate(() => { for (const r of document.querySelectorAll("#pkBody tr[data-i]")) r.dataset.keep = "1"; });
  const seq0 = await page.evaluate(() => frame.seq);
  await po.wait(1500);
  const held = await page.evaluate(() => ({ frames: frame.seq, kept: document.querySelectorAll('#pkBody tr[data-keep="1"]').length, rows: document.querySelectorAll("#pkBody tr[data-i]").length }));
  t.ok(held.frames > seq0 + 5 && held.kept === 2 && held.rows === 2, `the rows hold still while the message repeats (${held.frames - seq0} frames; ${held.kept} rows kept)`);
  // a trigger pattern marks the matching rows; the columns keep their widths
  await po.eval(() => { const e = document.getElementById("stBytes"); e.value = "48 69"; e.dispatchEvent(new Event("change", { bubbles: true })); });
  await t.until(() => page.evaluate(() => document.querySelectorAll("#pkBody tr.hit").length === 2), "the rows holding the trigger-on-bytes pattern are marked ◆", 3000);
  const w2 = await page.evaluate(() => [...document.querySelectorAll("#pktable thead th")].map((th) => Math.round(th.getBoundingClientRect().width)));
  t.ok(w2.join() === lay.widths.join(), `the columns keep their widths whatever the content (${w2.join()} = ${lay.widths.join()})`);
  await po.eval(() => { const e = document.getElementById("stBytes"); e.value = ""; e.dispatchEvent(new Event("change", { bubbles: true })); });

  // ---- the strip's format is a setting ------------------------------------------------
  t.ok(await po.value("decFmt") === "hex", "the decode strip shows hex by default");
  await po.seg("decFmt", "ascii");
  t.ok(await page.evaluate(() => dcfg.fmt) === "ascii", "Strip: String switches the strip to text");
  await po.wait(400);
  t.ok((await rows()).every((c) => c[3] === "48 69" && c[4] === "Hi"), "the table keeps both hex and string whatever the strip shows");
  await po.seg("decFmt", "hex");

  // ---- a packet click zooms onto it -------------------------------------------------
  const span0 = await page.evaluate(() => view.win.b - view.win.a);
  await page.click('#pkBody tr[data-i="1"]');
  await t.until(() => page.evaluate(() => frozen), "clicking a packet freezes the display so it holds still", 2000);
  const z = await page.evaluate(() => ({ a: view.win.a, b: view.win.b, sel: busSel, n: frame.c1.length }));
  t.ok(z.b - z.a < span0 * 0.5, `…and zooms in (window ${(z.b - z.a).toFixed(3)} of ${span0.toFixed(3)})`);
  t.ok(z.sel && z.sel.i0 >= 900 && z.sel.i1 <= 1500 && z.a * (z.n - 1) <= z.sel.i0 && z.b * (z.n - 1) >= z.sel.i1,
    "…framing the second message: " + JSON.stringify(z));
  t.ok(await page.evaluate(() => document.querySelector('#pkBody tr[data-i="1"]').classList.contains("is-sel")), "the clicked row is marked");
  await page.focus('#pkBody tr[data-i="0"]');
  await page.keyboard.press("Enter");
  await t.until(() => page.evaluate(() => busSel && busSel.i0 < 900), "Enter on a focused row zooms onto that packet", 2000);
  await po.click("freeze");
  await t.until(() => page.evaluate(() => !frozen), "Freeze resumes the live view", 2000);

  // ---- BUS again: the eye of the decoded line -----------------------------------------
  await page.evaluate(() => document.activeElement.blur());
  await page.keyboard.press("Alt+2");
  t.ok(await po.isVisible("ejEye") && await po.hasClass("ejArm", "on"), "BUS again shows the eye diagram, analyzer armed");
  t.ok(await po.value("ejCh") === "1", "…on the decoded line, C1");
  // TRLC-LINKS: REQ-SDS-215
  const figs = () => page.evaluate(() => Object.fromEntries([...document.querySelectorAll("#ejBody tr")].filter((r) => r.querySelector("th") && r.querySelector("td"))
    .map((r) => [r.querySelector("th").textContent, r.querySelector("td").textContent])));
  await t.until(async () => { const f = await figs(); return /bit\/s/.test(f["bit rate"] || "") && /s$/.test(f["TIE rms"] || ""); }, "the eye locks and measures", 15000);
  // the eye's height and width need enough folded edges (a few captures)
  await t.until(async () => { const f = await figs(); return /code|V/.test(f["eye height"] || "") && /UI/.test(f["eye width"] || ""); }, "the eye opens: height and width", 20000);
  const f = await figs();
  const br = parseFloat(f["bit rate"]) * ({ k: 1e3, M: 1e6 }[(/([kM])bit\/s/.exec(f["bit rate"]) || [])[1]] || 1);
  t.near(br, 115200, 2500, "bit rate");
  t.ok(/µs/.test(f.UI) && Math.abs(parseFloat(f.UI) - 8.68) < 0.2, "UI ≈ 8.68 µs: " + f.UI);
  // the LCD's BUS eye figures, in its order
  t.ok(JSON.stringify(Object.keys(f).slice(0, 12)) === JSON.stringify(["bit rate", "UI", "crossing", "folded", "eye height", "eye width",
    "rise", "fall", "TIE rms", "TIE pk-pk", "RJ / DJ", "period / c2c"]), "the LCD eye's figures, in its order: " + Object.keys(f).join(", "));
  for (const k of ["eye height", "eye width", "crossing", "rise", "fall", "TIE rms", "TIE pk-pk"])
    t.ok(f[k] && !/^—/.test(f[k]), `the eye reads its ${k}: ${f[k]}`);
  t.ok(/%/.test(f.crossing), "the crossing is a percentage");
  t.ok(/^[1-9]\d* UI$/.test(f.folded), "the UIs folded: " + f.folded);
  t.ok(!/[≤≥]/.test(f["eye width"] + f.rise + f.fall), "at 20 samples per UI the figures are measurements, not bounds");
  await page.keyboard.press("Alt+2");
  t.ok(!(await po.hasClass("ejArm", "on")) && await po.isVisible("pktable"), "BUS again: back to the packets, the eye stops");

  // ---- decoding stays on elsewhere -------------------------------------------------------
  await page.click("#tab-scope");
  t.ok(await po.value("decProto") === "uart", "decoding stays on in the other workspaces");
  await page.click("#tab-bus");
  await po.wait(300);
  t.ok(await po.value("decProto") === "uart" && !(await page.evaluate(() => busDetecting)), "coming back keeps the user's protocol (no new detection)");

  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
