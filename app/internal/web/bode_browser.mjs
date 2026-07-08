// Real-browser e2e for the web BODE / FRA card, driven by headless Chromium via
// Playwright. Launched by bode_browser_test.go against an httptest server (URL
// in argv[2]) whose /api/bode returns synthetic points. Asserts the app_views.js
// bode wiring works end to end: ARM toggles state + posts bodemode, the render
// path fetches /api/bode and draws the curve, the full-screen enlarge opens, and
// CLEAR runs — all with NO page errors.
// Output protocol: "SKIP: ..."+exit0 when the browser is absent, else "ALL PASS".
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2]);
  t.browser = browser;

  // record the /api/set bodemode POST the ARM button should fire
  let bodeModePost = null;
  page.on("request", (r) => {
    if (r.url().endsWith("/api/set") && r.method() === "POST") {
      try {
        const b = JSON.parse(r.postData() || "{}");
        if (b.control === "bodemode") bodeModePost = b;
      } catch {}
    }
  });

  await po.click("bodeArm");
  t.ok(await po.hasClass("bodeArm", "on"), "bodeArm goes active on click");
  t.ok((await po.text("bodeArm")) === "STOP", "bodeArm label becomes STOP");
  await po.wait(200);
  t.ok(bodeModePost && bodeModePost.value === 1, "ARM posts bodemode value=1");
  t.ok(bodeModePost && bodeModePost.lo === 0 && bodeModePost.hi === 1, "bodemode carries ref/dut in lo/hi");

  // the render path fetches /api/bode and draws — returns the point count
  const n = await po.eval(() => bodeRenderNow());
  t.ok(n === 2, "bodeRenderNow drew " + n + " points from /api/bode (want 2)");

  // full-screen enlarge (reuses the eye big-view shell) opens and targets bode
  await po.click("bodeCv");
  const big = await po.eval(() => ({
    open: !document.getElementById("ejBigWrap").classList.contains("hidden"),
    kind: typeof ejBigKind !== "undefined" ? ejBigKind : null,
  }));
  t.ok(big.open && big.kind === "bode", "bode enlarge opens (kind=" + big.kind + ")");
  await po.click("ejBigWrap");
  await po.waitFor(() => document.getElementById("ejBigWrap").classList.contains("hidden"), null, 4000);

  // CLEAR runs without error
  await po.click("bodeClear");
  t.ok((await po.text("bodeStats")).length > 0, "bodeClear updates the status line");

  t.ok(pageErrors.length === 0, "no uncaught page errors: " + pageErrors.join("; "));
});
