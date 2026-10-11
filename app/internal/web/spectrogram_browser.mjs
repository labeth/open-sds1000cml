// Real-browser e2e for the SPECTRUM workspace's spectrogram (FFT-over-time)
// view, driven by headless Chromium via Playwright. Launched by
// spectrogram_browser_test.go against an httptest server (URL in argv[2]) that
// serves a live triggered frame. Asserts, in a real DOM: showing the view runs
// the waterfall (rows accumulate from incoming frames), the scope canvas is
// actually painted, leaving the view stops it again, and Clear resets it —
// with NO page errors.
// Output protocol: "SKIP: ..."+exit0 when the browser is absent, else "ALL PASS".
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { po, browser, pageErrors } = await openScope(process.argv[2]);
  t.browser = browser;

  await po.setMode("SPG");
  t.ok(await po.hasClass("spgArm", "on"), "the spectrogram view runs the waterfall (spgArm on)");
  t.ok((await po.text("spgArm")) === "STOP", "spgArm label becomes STOP");

  // the 80 ms tick pushes each fresh frame's spectrum as a new waterfall row
  await po.waitFor(() => typeof spg !== "undefined" && spg.sg && spg.sg.rows > 2, null, 8000);
  const rows = await po.eval(() => spg.sg.rows);
  t.ok(rows > 2, "waterfall accumulated rows over successive frames: " + rows);

  // the canvas is actually painted: force a synchronous GL frame, then readPixels
  // and count pixels that differ from black — the waterfall + axis.
  const painted = await po.eval(() => {
    redraw();
    const gl = GLR.gl, cv = document.getElementById("scope");
    const px = new Uint8Array(cv.width * cv.height * 4);
    gl.readPixels(0, 0, cv.width, cv.height, gl.RGBA, gl.UNSIGNED_BYTE, px);
    let n = 0;
    for (let i = 0; i < px.length; i += 4) if (px[i] + px[i + 1] + px[i + 2] > 40) n++;
    return n;
  });
  t.ok(painted > 50, "spectrogram drawn on the scope canvas: " + painted + " painted pixels");
  t.ok(/Waterfall/.test(await po.text("roBody")), "the readout reports the waterfall");

  // leaving the view stops the waterfall it started; the rows are kept
  await po.setMode("FFT");
  await po.waitFor(() => !spg.armed, null, 4000);
  t.ok(!(await po.hasClass("spgArm", "on")), "leaving the spectrogram view stops it");
  t.ok(await po.eval(() => spg.sg.rows > 2), "the rows are kept");
  // back in the view it runs again; STOP by hand, then Clear empties it
  await po.setMode("SPG");
  await po.waitFor(() => spg.armed, null, 4000);
  await po.click("spgArm");
  t.ok(!(await po.hasClass("spgArm", "on")), "spgArm stops the waterfall by hand");
  await po.click("spgClear");
  t.ok((await po.eval(() => spg.sg.rows)) === 0, "spgClear resets rows to 0");
  await po.setMode("FFT");
  t.ok(await po.eval(() => !spg.armed), "a waterfall stopped by hand stays stopped");

  t.ok(pageErrors.length === 0, "no uncaught page errors: " + pageErrors.join("; "));
});
