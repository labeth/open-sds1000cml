// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// webdec_browser.mjs — the browser's stream decode (webdec_worker.js) in
// headless Chromium against a Go test server that plays recorded digital
// streams as /api/stream/words.bin (internal/decode/webdec_browser_test.go).
// It opens the server's bench page, which runs the worker, waits until the
// server has played every stream, and prints the worker's last status per
// stream ("STAT {json}"). Output protocol: "SKIP: ..." when the browser is
// absent, else "ALL PASS".
import { findPlaywright, run } from "./scope_po.mjs";

run(async (t) => {
  const base = process.argv[2];
  const { chromium } = (await import(findPlaywright())).default;
  const browser = await chromium.launch({ headless: true, args: ["--no-sandbox"] });
  t.browser = browser;
  const page = await browser.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
  await page.goto(base + "/bench.html", { waitUntil: "domcontentloaded", timeout: 15000 });
  const limit = Date.now() + (+process.env.WEBDEC_BENCH_MS || 600000);
  let state = "";
  while (Date.now() < limit) {
    state = await page.evaluate(() => fetch("/bench/state").then((r) => r.text()));
    if (state === "done") break;
    await new Promise((r) => setTimeout(r, 250));
  }
  t.ok(state === "done", "the server played every stream to the worker");
  // the last status the worker posted for each session
  await new Promise((r) => setTimeout(r, 700));
  const stats = await page.evaluate(() => window.benchStats);
  for (const s of Object.values(stats)) console.log("STAT " + JSON.stringify(s));
  const werr = await page.evaluate(() => window.benchErrors);
  t.ok(!werr.length && !errors.length, "no worker or page errors: " + JSON.stringify(werr.concat(errors)).slice(0, 300));
});
