// Real-browser e2e for the web SPECTROGRAM (FFT-over-time) card, driven by
// headless Chromium via Playwright. Launched by spectrogram_browser_test.go
// against an httptest server (URL in argv[2]) that serves a live triggered
// frame. Asserts, in a real DOM, that the app_views.js wiring works end to end:
// ARM builds the waterfall, rows accumulate from incoming frames, the canvas is
// actually painted, and the full-screen enlarge opens — with NO page errors.
// Output protocol: "SKIP: ..."+exit0 when the browser is absent, else "ALL PASS".
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { po, browser, pageErrors } = await openScope(process.argv[2]);
  t.browser = browser;

  await po.click("spgArm");
  t.ok(await po.hasClass("spgArm", "on"), "spgArm goes active on click");
  t.ok((await po.text("spgArm")) === "STOP", "spgArm label becomes STOP");

  // the 80 ms tick pushes each fresh frame's spectrum as a new waterfall row
  await po.waitFor(() => typeof spg !== "undefined" && spg.sg && spg.sg.rows > 2, null, 8000);
  const rows = await po.eval(() => spg.sg.rows);
  t.ok(rows > 2, "waterfall accumulated rows over successive frames: " + rows);

  // the canvas is actually painted: force a synchronous GL frame, then readPixels
  // and count pixels that differ from the cleared background (#141c26 = 20,28,38)
  // — i.e. the drawn waterfall + axis, not just the clear.
  const painted = await po.eval(() => {
    const cv = document.getElementById("spgCv");
    if (typeof spgRender === "function") spgRender(cv);
    const gl = cv._glbox && cv._glbox.R.gl;
    if (!gl) return -1;
    const px = new Uint8Array(cv.width * cv.height * 4);
    gl.readPixels(0, 0, cv.width, cv.height, gl.RGBA, gl.UNSIGNED_BYTE, px);
    let n = 0;
    for (let i = 0; i < px.length; i += 4) {
      if (Math.abs(px[i] - 20) + Math.abs(px[i + 1] - 28) + Math.abs(px[i + 2] - 38) > 40) n++;
    }
    return n;
  });
  t.ok(painted > 50, "spectrogram canvas has painted pixels: " + painted);

  // full-screen enlarge (reuses the eye big-view shell) opens and targets spg
  await po.click("spgCv");
  const big = await po.eval(() => ({
    open: !document.getElementById("ejBigWrap").classList.contains("hidden"),
    kind: typeof ejBigKind !== "undefined" ? ejBigKind : null,
  }));
  t.ok(big.open && big.kind === "spg", "spg enlarge opens (kind=" + big.kind + ")");

  // close the full-screen overlay before touching the card again (it covers it)
  await po.click("ejBigWrap");
  await po.waitFor(() => document.getElementById("ejBigWrap").classList.contains("hidden"), null, 4000);

  // STOP (so the tick stops pushing new rows) then CLEAR resets the waterfall
  await po.click("spgArm");
  t.ok(!(await po.hasClass("spgArm", "on")), "spgArm disarms on second click");
  await po.click("spgClear");
  t.ok((await po.eval(() => spg.sg.rows)) === 0, "spgClear resets rows to 0");

  t.ok(pageErrors.length === 0, "no uncaught page errors: " + pageErrors.join("; "));
});
