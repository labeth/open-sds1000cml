// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e on a real panel controller (argv[2] the app server, argv[3] its control server): the page's
// REF A/B are the scope's. A reference stored, shown or cleared on the scope (here its COMPARE rows, as the
// phone sets them) shows on the page; one saved, hidden or cleared on the page is the scope's; the difference
// readout compares against them. AUTO on the page frames for the workspace the page shows: with the LCD on
// SCOPE, the page on BUS runs BUS's AUTO and the page on COMPARE COMPARE's. SKIP/exit 0 without a browser.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const app = process.argv[2], ctrl = process.argv[3];
  const { page, po, browser, pageErrors } = await openScope(app);
  t.browser = browser;
  // TRLC-LINKS: REQ-SDS-227
  const post = (path, body) => page.evaluate(([u, b]) => fetch(u, { method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify(b) }).then((x) => x.json()), [app + path, body]);
  // TRLC-LINKS: REQ-SDS-227
  const row = (id) => page.evaluate(([u, r]) => fetch(u + "/api/sheets").then((x) => x.json()).then((m) => {
    for (const tb of m.Tabs) for (const x of tb.Rows) if (x.ID === r) return x;
    return null;
  }), [app, id]);
  // TRLC-LINKS: REQ-SDS-226
  const lcd = () => fetch(ctrl + "/status").then((x) => x.json());
  // a status round trip, as on a loaded CI runner
  const RT = 8000;

  // ---- references ----------------------------------------------------------------------
  await page.click("#tab-compare");
  t.ok(await po.text("refInfoA") === "empty" && await po.text("refInfoB") === "empty", "both slots start empty");
  let r = await post("/api/sheets/action", { row: "cmp.store_a", action: "store" });
  t.ok(r && r.ok, "the scope stores REF A (its COMPARE row, as the phone runs it): " + JSON.stringify(r));
  await t.until(async () => /^C1\+C2 · 500 µs\/div/.test(await po.text("refInfoA")) && await po.hasClass("refShowA", "on"),
    "the page shows the scope's REF A, shown: " + await po.text("refInfoA"), RT);
  t.ok(await po.eval(() => refs.A && refs.A.c1.length === 800 && refs.A.gen > 0), "…its 800 screen columns, as the LCD holds them");
  await t.until(async () => (await page.evaluate(() => [...document.querySelectorAll("#diffBody tr[data-ref='A'] td")].map((c) => c.textContent))).length === 6,
    "the difference readout compares the live C1 and C2 with the scope's REF A", RT);
  const cells = await page.evaluate(() => [...document.querySelectorAll("#diffBody tr[data-ref='A']")].map((x) => [...x.querySelectorAll("td")].map((c) => c.textContent)));
  t.ok(cells.length === 2 && cells.every((c) => c[2] === "100.0 %" && /^0 V/.test(c[0])), "identical captures: 0 V apart, 100 % within 0.5 div: " + JSON.stringify(cells));

  await po.click("refSaveB");
  await t.until(async () => /^holds C1 and C2 at /.test(((await row("cmp.store_b")) || {}).Text || ""), "Save B on the page stores REF B on the scope (the phone's row says so)", RT);
  await t.until(async () => /^C1\+C2/.test(await po.text("refInfoB")), "…and the page shows it", RT);
  await po.click("refShowB");
  await t.until(async () => ((await row("cmp.show_b")) || {}).Value === 0, "Show on the page hides REF B on the scope", RT);
  r = await post("/api/sheets/set", { row: "cmp.show_b", value: 1 });
  await t.until(() => po.hasClass("refShowB", "on"), "the scope shows REF B again: the page follows", RT);
  await po.click("refClearA");
  await t.until(async () => ((await row("cmp.store_a")) || {}).Text === "empty", "Clear A on the page deletes the scope's REF A", RT);
  await t.until(async () => await po.text("refInfoA") === "empty", "…and the page's", RT);
  r = await post("/api/sheets/action", { row: "cmp.clear_refs", action: "clear" });
  await t.until(async () => await po.text("refInfoB") === "empty" && await po.eval(() => !refs.A && !refs.B), "Clear refs on the scope empties the page's REF B", RT);

  // ---- AUTO frames for the page's workspace ------------------------------------------------
  t.ok((await lcd()).WorkspaceID === "scope", "the LCD shows SCOPE");
  // TRLC-LINKS: REQ-SDS-226
  const idle = async () => { const t1 = Date.now(); while ((await lcd()).AutosetBusy && Date.now() - t1 < 60000) await new Promise((r) => setTimeout(r, 100)); };
  // AUTO pressed on the page in workspace wsId: what the scope's AUTO said while it ran (with stop, the scope's
  // AUTO key cancels it once one of its words shows) and its notice after
  // TRLC-LINKS: REQ-SDS-226
  const auto = async (wsId, stop) => {
    await page.evaluate((w) => setWs(w), wsId);
    const msgs = new Set(), done0 = (await lcd()).AutosetDone || 0;
    await po.click("autoset");
    const t1 = Date.now();
    let notice = "";
    for (;;) {
      const s = await lcd();
      if (s.AutosetBusy && s.AutosetMsg) msgs.add(s.AutosetMsg);
      if (s.AutosetBusy && stop && stop.test(s.AutosetMsg || "")) { await fetch(ctrl + "/button?name=auto"); break; }
      if (!s.AutosetBusy && (s.AutosetDone || 0) !== done0) { notice = s.Notice || ""; break; }
      if (Date.now() - t1 > 40000) break;
      await new Promise((r) => setTimeout(r, 30));
    }
    await idle();
    await t.until(async () => !(await po.hasClass("autoset", "on")), "the page's AUTO on " + wsId + " ends", 40000);
    const s = await lcd();
    return { msgs: [...msgs], notice: notice || s.Notice || "", lcd: s.WorkspaceID };
  };
  const bus = await auto("bus", /framing the bus|finding the bus/);
  t.ok(bus.msgs.some((m) => /framing the bus|finding the bus/.test(m)) && !bus.msgs.includes("AUTOSET..."),
    "page on BUS, LCD on SCOPE: the scope runs BUS's AUTO (cancelled here): " + JSON.stringify(bus));
  t.ok(bus.lcd === "scope", "…and the LCD stays on SCOPE");
  // COMPARE's AUTO puts the scope back on REF A's setup (SPECTRUM's framing has its panel test)
  r = await post("/api/sheets/action", { row: "cmp.store_a", action: "store" });
  const cmp = await auto("compare");
  t.ok(/^AUTO \(COMPARE\): REF A's setup/.test(cmp.notice) && cmp.lcd === "scope", "page on COMPARE: COMPARE's AUTO: " + JSON.stringify(cmp));

  // the eye's Optimize fits with SCOPE's AUTO (an edge trigger) even from BUS, not BUS's frame-start trigger
  let eyeBody = "";
  await page.route("**/api/panel", (route) => { eyeBody = route.request().postData() || ""; route.fulfill({ json: { ok: false } }); });
  await page.evaluate(() => { setWs("bus"); return autoset("scope"); });
  t.ok(/"for":"scope"/.test(eyeBody), "the eye's fit asks for SCOPE's AUTO from BUS: " + eyeBody);
  await page.unroute("**/api/panel");

  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
