// Real-browser e2e for the Bode / FRA view of the SPECTRUM workspace, driven
// by headless Chromium via Playwright. Launched by bode_browser_test.go against
// an httptest server (URL in argv[2]) whose /api/bode returns synthetic points.
// Asserts: SPECTRUM again reaches the Bode view (FFT → spectrogram → Bode), ARM
// toggles state + posts bodemode with the ref/dut channels, the view fetches
// /api/bode and draws the curve on the scope canvas, the peak pane lists the
// points, and Clear runs — all with NO page errors.
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

  // Alt+3 enters SPECTRUM (FFT); Alt+3 again cycles to the spectrogram, then Bode.
  await page.keyboard.press("Alt+Digit3");
  await po.waitFor(() => ws.cur === "spectrum" && view.mode === "FFT", null, 4000);
  await page.keyboard.press("Alt+Digit3");
  await po.waitFor(() => view.mode === "SPG", null, 4000);
  await page.keyboard.press("Alt+Digit3");
  await po.waitFor(() => view.mode === "BODE", null, 4000);
  t.ok(true, "SPECTRUM again cycles FFT → spectrogram → Bode");
  t.ok(await po.isVisible("bodeArm") && !(await po.isVisible("spgArm")), "the Bode tools show in the pane header");

  await po.click("bodeArm");
  t.ok(await po.hasClass("bodeArm", "on"), "bodeArm goes active on click");
  t.ok((await po.text("bodeArm")) === "STOP", "bodeArm label becomes STOP");
  await po.wait(200);
  t.ok(bodeModePost && bodeModePost.value === 1, "ARM posts bodemode value=1");
  t.ok(bodeModePost && bodeModePost.lo === 0 && bodeModePost.hi === 1, "bodemode carries ref/dut in lo/hi");

  // the view fetches /api/bode and draws — returns the point count
  const n = await po.eval(() => bodeRenderNow());
  t.ok(n === 2, "bodeRenderNow drew " + n + " points from /api/bode (want 2)");
  await po.waitFor(() => document.querySelectorAll("#bodeBody tr").length === 2 && !document.getElementById("bodeTable").hidden, null, 4000);
  t.ok(/Hz/.test(await po.text("bodeBody")) && /dB/.test(await po.text("bodeBody")), "the peak pane lists the Bode points (frequency, gain, phase)");
  await t.until(async () => /Points\s*2/.test(await po.text("roBody")), "the readout counts the points", 2000);

  // the scope canvas shows the plot: pixels other than the background
  const painted = await po.eval(() => {
    redraw();
    const gl = GLR.gl, cv = document.getElementById("scope");
    const px = new Uint8Array(cv.width * cv.height * 4);
    gl.readPixels(0, 0, cv.width, cv.height, gl.RGBA, gl.UNSIGNED_BYTE, px);
    let k = 0;
    for (let i = 0; i < px.length; i += 4) if (px[i] + px[i + 1] + px[i + 2] > 60) k++;
    return k;
  });
  t.ok(painted > 200, "the Bode plot is drawn on the scope canvas: " + painted + " lit pixels");

  // the page follows the scope's own Bode state: its Bode view or VIEW tab
  // (here a direct /api/set) disarms it and swaps the channels
  await po.eval(() => fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ control: "bodemode", value: 0, lo: 1, hi: 0 }) }));
  await t.until(async () => (await po.text("bodeArm")) === "ARM" && !(await po.hasClass("bodeArm", "on")),
    "the ARM button follows the scope disarming", 4000);
  await t.until(async () => (await po.value("bodeRef")) === "1" && (await po.value("bodeDut")) === "0",
    "the REF / DUT selectors follow the scope's channels", 4000);
  // picking the other's channel swaps them, and the scope gets both
  bodeModePost = null;
  await page.selectOption("#bodeDut", "1");
  await po.wait(300);
  t.ok((await po.value("bodeRef")) === "0" && bodeModePost && bodeModePost.lo === 0 && bodeModePost.hi === 1,
    "DUT C2 over REF C2 swaps the REF to C1, sent to the scope disarmed: " + JSON.stringify(bodeModePost));

  // CLEAR runs without error
  await po.click("bodeClear");
  t.ok((await po.text("bodeStats")).length > 0, "bodeClear updates the status line");

  t.ok(pageErrors.length === 0, "no uncaught page errors: " + pageErrors.join("; "));
});
