// Real-browser e2e for the SERIAL-TRIGGER sub-panel (argv[2]=URL). It lives
// INSIDE the decode card and reuses the decode config, so this verifies: it is
// hidden until decode is on; arming pushes a SerialParams assembled from the
// decode config + the match, THEN arms (config lands before serialmode); and
// turning decode off auto-disarms. No page errors. SKIP/exit 0 without browser.
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

  // decode OFF → the trigger sub-panel is hidden (it's inside #decRoles)
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("decRoles")).display === "none"),
    "decode off → trigger sub-panel hidden");

  // turn decode on: I2C. The sub-panel + the I2C addr row appear.
  await po.setSelect("decProto", "i2c");
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("stArm")).display !== "none"),
    "decode on → arm button visible");
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("stAddr").closest("span,div")).display !== "none"),
    "I2C → address match field visible");

  // set the match: address 0x24 write
  await po.eval(() => { const el = document.getElementById("stAddr"); el.value = "24"; el.dispatchEvent(new Event("change", { bubbles: true })); });
  await po.setSelect("stRW", "0");

  // arm — config POST must precede the arm POST (await ordering)
  await po.click("stArm");
  await po.waitFor(() => document.getElementById("stArm").classList.contains("on"), null, 4000);
  t.ok(await po.hasClass("stArm", "on"), "arm toggles on");

  const serial = posts.filter(p => /\/api\/serial/.test(p.url)).pop();
  const arm = posts.filter(p => /\/api\/set/.test(p.url) && p.body.control === "serialmode" && p.body.value === 1).pop();
  t.ok(!!serial && serial.body.proto === 2 && serial.body.addr === 0x24 && serial.body.rw === 0,
    "config from decode POSTed to /api/serial (proto=i2c addr=0x24 rw=W): " + JSON.stringify(serial && serial.body));
  t.ok(!!serial && serial.body.chA === 0 && serial.body.chB === 1,
    "channel roles inherited from decode (SCL=C1→chA0, SDA=C2→chB1): chA=" + (serial && serial.body.chA) + " chB=" + (serial && serial.body.chB));
  t.ok(!!serial && !!arm && serial.t <= arm.t, "config lands BEFORE arm (no unfiltered-window race)");

  // strict hex: an invalid pattern must NOT be silently dropped
  await po.eval(() => { const el = document.getElementById("stBytes"); el.value = "GG"; el.dispatchEvent(new Event("change", { bubbles: true })); });
  t.ok(await po.eval(() => document.getElementById("stStats").textContent.includes("invalid")),
    "invalid hex is flagged, not silently dropped");

  // turn decode OFF → auto-disarm
  await po.eval(() => { const el = document.getElementById("stBytes"); el.value = ""; el.dispatchEvent(new Event("change", { bubbles: true })); });
  await po.setSelect("decProto", "off");
  await po.waitFor(() => !document.getElementById("stArm").classList.contains("on"), null, 4000);
  t.ok(!(await po.hasClass("stArm", "on")), "turning decode off auto-disarms the trigger");
  const off = posts.filter(p => /\/api\/set/.test(p.url) && p.body.control === "serialmode").pop();
  t.ok(!!off && off.body.value === 0, "disarm POSTed serialmode=0");

  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
