// Acceptance suite for the scope web-UI paths NOT already covered by the
// decode/fft/deepmem drivers: boot/liveness, acquire, trigger, vertical/
// horizontal, cursors, X-Y, export, and mode-driven panel visibility. Driven
// through the shared page-object (scope_po.mjs) against httptest+fakeScope.
// Captures TODAY's behavior so the design-system refactor can't regress it.
// SKIP/exit 0 when the browser is unavailable; ALL PASS/exit 1 otherwise.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2]);
  t.browser = browser;
  // selects (tdiv/vdiv) are filled by the status poll — wait for it once.
  await po.waitFor(() => document.getElementById("tdiv").options.length > 0);

  // --- boot / liveness -------------------------------------------------------
  t.ok((await po.statusLine() || "").length > 0, "status line shows a connection state");
  t.ok(await po.eval(() => typeof frame !== "undefined" && !!frame.c1), "a live frame is present");
  t.ok(await po.eval(() => getComputedStyle(document.getElementById("stopped")).display) === "none",
    "STOPPED overlay hidden while running");

  // --- acquire (RUN / SINGLE) ------------------------------------------------
  await po.click("run"); // optimistic toggle running -> stopped
  t.ok(await po.text("run") === "STOP" && await po.hasClass("run", "stopped"),
    "RUN click optimistically shows STOP + stopped style");
  await po.click("run");
  t.ok(await po.text("run") === "RUN" && await po.hasClass("run", "running"),
    "second RUN click returns to RUN + running style");
  await po.click("single");
  t.ok(await po.hasClass("single", "on"), "SINGLE arms (on state)");

  // --- trigger ---------------------------------------------------------------
  const mode0 = await po.text("mode");
  await po.click("mode");
  t.ok(await po.text("mode") !== mode0, `trig mode toggles (${mode0} -> ${await po.text("mode")})`);
  const src0 = await po.text("source");
  await po.click("source");
  t.ok(await po.text("source") !== src0, `trig source toggles (${src0} -> ${await po.text("source")})`);
  await po.setSelect("ttype", "1"); // PULSE
  t.ok(await po.isCardVisible("qualrow") && await po.isCardVisible("qp-pulse"),
    "PULSE type reveals the pulse qualifier panel");
  await po.setSelect("ttype", "2"); // SLOPE
  t.ok(await po.isCardVisible("qp-slope") && !(await po.isCardVisible("qp-pulse")),
    "switching to SLOPE reveals slope, hides pulse");
  await po.setSelect("ttype", "0"); // EDGE
  t.ok(!(await po.isCardVisible("qualrow")), "EDGE hides the qualifier panel");
  await po.setRange("lvl", 1.5);
  t.ok((await po.text("lvlv") || "").includes("1.5"), "trigger level readout tracks the slider");

  // --- vertical / horizontal -------------------------------------------------
  await po.click("tC1");
  t.ok(!(await po.hasClass("tC1", "on")), "C1 enable toggles off");
  await po.click("tC1");
  t.ok(await po.hasClass("tC1", "on"), "C1 enable toggles back on");
  const tdivOpts = await po.eval(() => document.getElementById("tdiv").options.length);
  t.ok(tdivOpts > 3, `time/div select is populated (${tdivOpts} detents)`);

  // --- cursors ---------------------------------------------------------------
  await po.click("tCursors");
  t.ok(await po.isCardVisible("curCard") && await po.hasClass("tCursors", "on"),
    "cursors on reveals the Cursors card");
  await po.click("tCursors");
  t.ok(!(await po.isCardVisible("curCard")), "cursors off hides the Cursors card");

  // --- view modes + panel visibility ----------------------------------------
  await po.setMode("FFT");
  t.ok(await po.isCardVisible("fftCardC1") && await po.isCardVisible("fftCardC2"),
    "FFT mode shows both per-channel FFT boxes");
  t.ok(await po.hasClass("mFFT", "on"), "FFT mode button active");
  await po.setMode("XY");
  t.ok(!(await po.isCardVisible("fftCardC1")) && await po.hasClass("mXY", "on"),
    "X-Y mode hides FFT boxes, activates X-Y");
  await po.setMode("YT");
  t.ok(await po.hasClass("mYT", "on") && await po.isCardVisible("fftCardC1"),
    "Y-T mode active; FFT boxes also shown in the time view (peaks-in-time feature)");

  // --- export (PNG data URL + CSV) ------------------------------------------
  t.ok((await po.eval(() => document.getElementById("scope").toDataURL("image/png"))).startsWith("data:image/png"),
    "canvas exports a PNG data URL");
  await po.click("eCSV"); // must not throw
  t.ok(true, "CSV export click completes without error");

  // --- no uncaught page errors across the whole run --------------------------
  t.ok(pageErrors.length === 0, "no uncaught page errors: " + (pageErrors.join(" | ") || "none"));
});
