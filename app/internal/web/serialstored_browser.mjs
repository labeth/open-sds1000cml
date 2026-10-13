// Real-browser e2e: the scope keeps a serial match while its trigger is off
// (set on the LCD or a phone). The page shows that match from the start, and
// its Trigger on match arms it: the page's empty field armed "any byte" over
// it (bug hunt #3). An armed match the FPGA cannot take yet is named as that,
// not as software matching; disarmed, the page follows a match set elsewhere.
// argv[2]=URL. SKIP/exit 0 without browser.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2]);
  t.browser = browser;
  const posts = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && /\/api\/serial/.test(r.url())) posts.push(r.postData() || "");
  });
  await po.ws("bus");
  await po.waitFor(() => !busDetecting, null, 10000);
  await po.setSelect("decProto", "uart");
  await t.until(async () => (await po.eval(() => document.getElementById("stBytes").value)) === "55 AA",
    "the page shows the scope's stored match (55 AA) with its trigger off", 6000);
  t.ok(!(await po.hasClass("stArm", "on")), "…and the trigger as off");
  await po.toggle("stArm");
  await t.until(async () => posts.some((p) => /"bytes":\[85,170\]/.test(p)), "Trigger on match arms the stored 55 AA", 6000);
  t.ok(!posts.some((p) => /"bytes":(null|\[\])/.test(p)), "and never posts an empty match: " + JSON.stringify(posts));
  // The FPGA decides the trigger: an armed match it cannot take yet is named
  // as such, never as software matching.
  await t.until(async () => /armed · no hardware trigger yet: learning the line levels on C1/.test(
    await po.eval(() => document.getElementById("stStats").textContent)),
    "the status names why the FPGA has no trigger yet", 6000);
  // Disarmed, the page follows the scope's stored match: set (or cleared) on
  // the LCD, a phone or by Default setup, the field showed the old one.
  await po.toggle("stArm");
  await t.until(async () => !(await po.hasClass("stArm", "on")), "Trigger on match again disarms", 4000);
  await fetch(process.argv[2] + "/api/serial", { method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ proto: 1, chA: 0, chB: 0, baud: 115200, bits: 8, parity: "none", addr: -1, rw: 2, bytes: [0x12], pattern: [] }) });
  await t.until(async () => (await po.eval(() => document.getElementById("stBytes").value)) === "12",
    "a match set elsewhere (12) shows on the page", 6000);
  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
