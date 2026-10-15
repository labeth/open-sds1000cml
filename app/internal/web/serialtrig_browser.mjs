// Real-browser e2e for the SERIAL TRIGGER — BUS's "Trigger on bytes" pane
// (argv[2]=URL). It reuses the Decoder pane's configuration, so this verifies:
// arming with decoding off first tries to detect the bus and refuses when it
// finds none; arming pushes a SerialParams assembled from the decode config +
// the match, THEN arms (config lands before serialmode); invalid hex is flagged,
// not dropped; turning decode off auto-disarms; every protocol arms on its
// line; a rejected config never arms. No page errors. SKIP/exit 0 without browser.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2]);
  t.browser = browser;

  const posts = [];
  page.on("request", r => {
    if (r.method() === "POST" && /\/api\/(serial|set)/.test(r.url())) {
      let body = {}; try { body = JSON.parse(r.postData() || "{}"); } catch {}
      posts.push({ url: r.url(), body, t: Date.now() });
    }
  });

  await po.ws("bus");
  // the fake serves sines: entry detection finds no bus and decoding stays off
  await po.waitFor(() => !busDetecting, null, 10000);
  await po.setSelect("decProto", "off");
  t.ok(await po.isVisible("stArm") && await po.isVisible("stBytes"), "BUS shows the Trigger on bytes pane");
  await po.click("stArm");
  await t.until(async () => /turn on decode/.test(await po.text("stStats")), "with decoding off and no bus found, arming refuses and says why", 8000);
  t.ok(!(await po.hasClass("stArm", "on")), "…and stays disarmed");
  // a second click while the first still searches for the bus cancels the arm
  // (a second click must not start an arm of its own)
  const sent = posts.length;
  await po.eval(() => { const b = document.getElementById("stArm"); b.click(); b.click(); });
  await t.until(async () => /arming cancelled/.test(await po.text("stStats")), "a second click while arming cancels it", 4000);
  await new Promise((r) => setTimeout(r, 3000)); // the first click's search ends
  t.ok(!(await po.hasClass("stArm", "on")) && !posts.slice(sent).some((p) => /\/api\/serial/.test(p.url)),
    "…and the search that ends after it sends nothing to the scope");

  // a second click that lands while the arm itself is on its way takes the arm back
  {
    await po.setSelect("decProto", "uart");
    let release; const held = new Promise((r) => { release = r; });
    await page.route("**/api/set", async (route) => {
      const b = JSON.parse(route.request().postData() || "{}");
      if (b.control === "serialmode" && b.value === 1) await held;
      await route.continue();
    });
    const sent = posts.length;
    await po.click("stArm");
    await po.waitFor(() => document.getElementById("stArm").classList.contains("on"), null, 8000); // the arm is sent
    await po.click("stArm");   // ...and the cancel lands before its answer
    release();
    await t.until(async () => posts.slice(sent).some((p) => p.body.control === "serialmode" && p.body.value === 0),
      "a cancel that came after the arm was sent takes it back", 6000);
    t.ok(!(await po.hasClass("stArm", "on")), "…and the trigger shows off");
    await page.unroute("**/api/set");
    await po.setSelect("decProto", "off");
  }

  // decode on: I2C. The I2C address row appears.
  await po.setSelect("decProto", "i2c");
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("stAddr").closest(".r")).display !== "none"),
    "I2C → address match field visible");

  // set the match: address 0x24 write
  await po.eval(() => { const el = document.getElementById("stAddr"); el.value = "24"; el.dispatchEvent(new Event("change", { bubbles: true })); });
  await po.seg("stRW", "0");

  // arm — config POST must precede the arm POST (await ordering)
  await po.click("stArm");
  await po.waitFor(() => document.getElementById("stArm").classList.contains("on"), null, 8000);
  t.ok(await po.hasClass("stArm", "on"), "arm toggles on");
  await t.until(async () => /Armed/.test(await po.text("stSum")), "the summary says it is armed");

  const serial = posts.filter(p => /\/api\/serial/.test(p.url)).pop();
  const arm = posts.filter(p => /\/api\/set/.test(p.url) && p.body.control === "serialmode" && p.body.value === 1).pop();
  t.ok(!!serial && serial.body.proto === 2 && serial.body.addr === 0x24 && serial.body.rw === 0,
    "config from decode POSTed to /api/serial (proto=i2c addr=0x24 rw=W): " + JSON.stringify(serial && serial.body));
  t.ok(!!serial && serial.body.chA === 0 && serial.body.chB === 1,
    "channel roles inherited from decode (SCL=C1→chA0, SDA=C2→chB1): chA=" + (serial && serial.body.chA) + " chB=" + (serial && serial.body.chB));
  t.ok(!!serial && !!arm && serial.t <= arm.t, "config lands BEFORE arm (no unfiltered-window race)");

  // the insert buttons build the pattern
  await po.click("stInsAny"); await po.click("stInsErr");
  t.ok((await po.value("stBytes")) === "?? ERR", "?? and ERR insert pattern tokens");
  await po.click("stClear");
  t.ok((await po.value("stBytes")) === "", "Clear empties the match");

  // strict hex: an invalid pattern must NOT be silently dropped
  await po.eval(() => { const el = document.getElementById("stBytes"); el.value = "GG"; el.dispatchEvent(new Event("change", { bubbles: true })); });
  t.ok(await po.eval(() => document.getElementById("stStats").textContent.includes("invalid")),
    "invalid hex is flagged, not silently dropped");

  // turn decode OFF → auto-disarm
  await po.eval(() => { const el = document.getElementById("stBytes"); el.value = ""; el.dispatchEvent(new Event("change", { bubbles: true })); });
  await po.setSelect("decProto", "off");
  await po.waitFor(() => !document.getElementById("stArm").classList.contains("on"), null, 8000);
  t.ok(!(await po.hasClass("stArm", "on")), "turning decode off auto-disarms the trigger");
  await t.until(() => { const off = posts.filter(p => /\/api\/set/.test(p.url) && p.body.control === "serialmode").pop(); return !!off && off.body.value === 0; },
    "disarm POSTed serialmode=0", 3000);

  for (const [proto, id] of [["manchester", 4], ["sent", 5], ["can", 6], ["mil1553", 7], ["arinc429", 8], ["usb", 9], ["flexray", 10]]) {
    await po.setSelect("decProto", proto);
    await po.setSelect("decLine", "2");
    const word = proto === "mil1553" ? "ABCD" : proto === "arinc429" ? "7ABCD" : "";
    await po.eval(value => { document.getElementById("stBytes").value = value; }, word);
    t.ok(await page.locator("#decLine").isVisible(), proto + " line selection visible");
    await po.click("stArm");
    await po.waitFor(() => document.getElementById("stArm").classList.contains("on"), null, 8000);
    const p = posts.filter(p => /\/api\/serial/.test(p.url)).pop();
    t.ok(p?.body.proto === id && p.body.chA === 1, proto + " arms with C2 and correct protocol ID");
    if (word) t.ok(p.body.bytes[0] === parseInt(word, 16), proto + " preserves wide match values");
    if (proto === "manchester") t.ok(p.body.ieee === true, "Manchester trigger uses the decode convention");
    await po.setSelect("decProto", "off");
    await po.waitFor(() => !document.getElementById("stArm").classList.contains("on"), null, 8000);
  }
  await po.setSelect("decProto", "uart");
  await po.eval(() => { document.getElementById("stBytes").value = "55"; });
  await page.route("**/api/serial", route => route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ ok: false }) }));
  await po.click("stArm");
  await po.waitFor(() => document.getElementById("stStats").textContent.includes("not applied"), null, 8000);
  t.ok(!(await po.hasClass("stArm", "on")), "rejected settings never arm the trigger");
  await page.unroute("**/api/serial");
  // Enter in the match field arms too
  await po.page.focus("#stBytes");
  await po.page.keyboard.press("Enter");
  await po.waitFor(() => document.getElementById("stArm").classList.contains("on"), null, 8000);
  await t.until(async () => !(await page.locator("#stStats").textContent()).includes("not applied"), "Enter in the match field arms; a successful retry clears the error", 3000);
  // the caption names what the scope's status confirms, so it follows the
  // arm by a status round trip; on a loaded runner that has taken over 5 s
  await t.until(async () => /Serial 55/.test(await po.text("trigCap")), "the strip's trigger caption shows the armed serial trigger", 15000);
  if (!/Serial 55/.test(await po.text("trigCap")))
    console.log("caption: " + JSON.stringify(await po.eval(() => ({ cap: $("trigCap").textContent, bytes: $("stBytes").value, mode: st.serial_mode, armed: stArmed(), own: stOwn }))));
  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
