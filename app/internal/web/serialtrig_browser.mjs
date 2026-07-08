// Real-browser e2e for the SERIAL-TRIGGER panel (argv[2]=URL). Verifies the
// app_serialtrig.js wiring: selecting a protocol reveals its config rows, and
// ARM pushes the config to /api/serial + arms via /api/set serialmode — with no
// page errors. SKIP/exit 0 when the browser is absent.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2]);
  t.browser = browser;

  const posts = [];
  page.on("request", r => {
    if (r.method() === "POST" && /\/api\/(serial|set)/.test(r.url())) {
      let body = {}; try { body = JSON.parse(r.postData() || "{}"); } catch {}
      posts.push({ url: r.url(), body });
    }
  });

  // configure: I2C write to address 0x50
  await po.setSelect("stProto", "2");
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("stI2cRow")).display !== "none"),
    "I2C address row appears for proto=I2C");
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("stSpiRow")).display === "none"),
    "SPI row hidden for proto=I2C");
  await po.eval(() => { const el = document.getElementById("stAddr"); el.value = "50"; el.dispatchEvent(new Event("change", { bubbles: true })); });
  await po.setSelect("stRW", "0"); // write

  // arm
  await po.click("stArm");
  await po.wait(250);
  t.ok(await po.hasClass("stArm", "on"), "arm button toggles on");

  const serial = posts.filter(p => /\/api\/serial/.test(p.url)).pop();
  t.ok(!!serial && serial.body.proto === 2 && serial.body.addr === 0x50 && serial.body.rw === 0,
    "config POSTed to /api/serial (proto=i2c addr=0x50 rw=W): " + JSON.stringify(serial && serial.body));
  const arm = posts.filter(p => /\/api\/set/.test(p.url) && p.body.control === "serialmode").pop();
  t.ok(!!arm && arm.body.value === 1, "armed via /api/set serialmode=1");

  // switch to UART: byte pattern row shows, address row hides
  await po.setSelect("stProto", "1");
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("stUartRow")).display !== "none"),
    "UART baud row appears for proto=UART");
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("stI2cRow")).display === "none"),
    "I2C row hidden for proto=UART");

  // disarm
  await po.click("stArm");
  t.ok(!(await po.hasClass("stArm", "on")), "arm toggles off");
  const disarm = posts.filter(p => /\/api\/set/.test(p.url) && p.body.control === "serialmode").pop();
  t.ok(!!disarm && disarm.body.value === 0, "disarmed via /api/set serialmode=0");

  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
