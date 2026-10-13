// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e: the desktop page follows the scope's own sheet (argv[2] = the app server of a real panel
// controller). Settings changed on the scope (its keys, the phone: here /api/sheets/set) show on the page
// without a reload - the bandwidth limit, the invert and the master's depth - and the page's controls set
// the scope's rows; COMPARE shows the scope's word on whether the trigger source repeats. SKIP/exit 0
// without a browser.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const app = process.argv[2];
  const { page, po, browser, pageErrors } = await openScope(app, { waitFrame: false }); // the rig sends no frames
  t.browser = browser;
  await page.waitForFunction(() => typeof st !== "undefined" && st && st.panel && st.panel.SheetsSeq !== undefined, null, { timeout: 15000 });
  // TRLC-LINKS: REQ-SDS-211
  const set = (row, value) => page.evaluate(([u, r, v]) => fetch(u + "/api/sheets/set", { method: "POST",
    headers: { "Content-Type": "application/json" }, body: JSON.stringify({ row: r, value: v }) }).then((x) => x.json()), [app, row, value]);
  // TRLC-LINKS: REQ-SDS-211
  const sheet = (row) => page.evaluate(([u, r]) => fetch(u + "/api/sheets").then((x) => x.json()).then((m) => {
    for (const tb of m.Tabs) for (const x of tb.Rows) if (x.ID === r) return x.Value;
    return null;
  }), [app, row]);

  t.ok(await po.isVisible("bwl1") && await po.isVisible("inv2"), "the Vertical pane has the BW limit and Invert rows");
  // the scope changes them: the page follows without a reload
  for (const [row, v] of [["ch1.bwl", 1], ["ch2.invert", 1], ["cmp.master_depth", 16]]) {
    const r = await set(row, v);
    t.ok(r && r.OK !== false, `the scope's ${row} set to ${v}: ${JSON.stringify(r).slice(0, 80)}`);
  }
  await t.until(async () => (await po.value("bwl1")) === "1" && (await po.value("inv2")) === "1" && (await po.value("masterN")) === "16",
    "the page shows the scope's BW limit, invert and master depth", 6000);
  // the page sets them on the scope
  await po.seg("bwl2", "1");
  await t.until(async () => (await sheet("ch2.bwl")) === 1, "the page's CH2 BW limit reaches the scope's CH2 tab", 4000);
  await po.seg("inv2", "0");
  await t.until(async () => (await sheet("ch2.invert")) === 0, "…and its invert", 4000);
  // COMPARE shows the scope's word on the trigger source, as the LCD's caption
  await page.evaluate(() => setWs("compare"));
  await t.until(async () => /^Trigger source C1/.test(await po.text("cmpRepeat")), "COMPARE shows whether the trigger source repeats", 4000);
  // the scope's own super-res shows on the page's Super-res card and MEASURE's tab while the page's stacker
  // is idle; the rig has no frames to stack, so the status carries a stack here
  const idle = await po.text("srStats");
  await page.route("**/api/status", async (route) => {
    const r = await route.fetch(), j = await r.json();
    await route.fulfill({ response: r, json: { ...j, sr_active: true, sr_status: "31 stk / 4f / 0 rej  +2.8b" } });
  });
  await t.until(async () => /^On the scope: 31 stk \/ 4f \/ 0 rej {2}\+2\.8b/.test(await po.text("srStats")), "the Super-res card shows the scope's stack", 4000);
  await t.until(() => page.evaluate(() => !document.querySelector("#tab-measure .tdot").hidden), "…and MEASURE's tab its dot", 2500);
  await page.unroute("**/api/status");
  await t.until(async () => (await po.text("srStats")) === idle, "the card is idle again when the scope's stack ends: " + idle, 4000);
  // the scope's hardware stream shows in BUS's Packets view: its status and newest transcript lines as its
  // LCD list writes them; entering BUS does not auto-detect the bus meanwhile (the streaming scope is
  // stopped)
  await page.evaluate(() => setWs("scope"));
  await page.route("**/api/status", async (route) => {
    const r = await route.fetch(), j = await r.json();
    await route.fulfill({ response: r, json: { ...j, stream: { state: "STREAM", label: "UART 115200 b/s C1", units: 50174, errors: 0, lost: 0, lines: 3212,
      units_per_s: 8778, record: false, trigger_line: -1 } } });
  });
  const LINE = "     +5641.2630        48 69 20 55 AA 0F F0 0A 48 69 20 55 AA 0F F0 0A  Hi U....Hi U....";
  await page.route("**/api/stream/window*", (route) => route.fulfill({ json: { Header: "         t ms        hex", Lines: [LINE], Top: 3211, Total: 3212, TriggerLine: -1 } }));
  await page.waitForFunction(() => st && st.stream, null, { timeout: 4000 });
  await page.evaluate(() => setWs("bus"));
  await t.until(async () => /^On the scope: STREAM · UART 115200 b\/s C1 · 50174 units · 8\.78 k\/s · err 0 · lost 0$/.test(await po.text("scopeStreamHead")),
    "BUS shows the scope's stream", 4000);
  await t.until(async () => (await po.text("scopeStreamText")).split("\n")[1] === LINE, "…and its newest transcript lines as the LCD writes them", 4000);
  t.ok((await po.text("decDetectMsg")) === "", "entering BUS did not auto-detect beside the stream: " + await po.text("decDetectMsg"));
  await page.unroute("**/api/status");
  await t.until(() => page.evaluate(() => document.getElementById("scopeStream").hidden), "the stream's box goes when the scope's stream ends", 4000);
  // a serial trigger armed on the scope (its keys, the phone) while this page decodes nothing: the page says
  // it is armed, on the scope's protocol and match
  await page.evaluate(() => { const e = document.getElementById("decProto"); e.value = "off"; e.dispatchEvent(new Event("change")); });
  await page.route("**/api/status", async (route) => {
    const r = await route.fetch(), j = await r.json();
    await route.fulfill({ response: r, json: { ...j, serial_mode: 1, serial_backend: "hardware-uart" } });
  });
  await page.route("**/api/serial", (route) => route.request().method() === "GET"
    ? route.fulfill({ json: { ok: true, params: { proto: 1, chA: 0, chB: 1, bytes: [0x48], pattern: [], addr: -1, rw: 2 } } }) : route.continue());
  await t.until(async () => /^Armed on the scope: triggering on 48 in its UART decode/.test(await po.text("stSum")),
    "the page shows the scope's armed serial trigger", 5000);
  t.ok(await page.evaluate(() => document.getElementById("stArm").classList.contains("on")), "…its Trigger on match lit");
  await page.unroute("**/api/status");
  await t.until(() => page.evaluate(() => !document.getElementById("stArm").classList.contains("on")), "…and out when the scope disarms", 5000);
  await page.unroute("**/api/serial");
  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
