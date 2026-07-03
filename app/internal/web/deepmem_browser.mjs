// Real-browser e2e for the deep-memory navigator (argv[2]=URL of a server
// serving a deep decimated frame). Verifies the default window is the
// trigger-centered screen slice (unchanged view), the navigator shows the whole
// record, and wheel/pan zoom across it. SKIP/exit 0 when the browser is absent.
import { existsSync, readdirSync } from "node:fs";
import { execSync } from "node:child_process";
import path from "node:path";

const URL = process.argv[2];
if (!URL) { console.log("SKIP: no URL argument"); process.exit(0); }
function findPlaywright() {
  const cands = [];
  if (process.env.PLAYWRIGHT_DIR) cands.push(path.join(process.env.PLAYWRIGHT_DIR, "playwright/index.js"));
  const home = process.env.HOME || "";
  for (const base of [path.join(home, "ws"), path.join(home, "src"), path.join(home, "projects")]) {
    try { for (const d of readdirSync(base)) cands.push(path.join(base, d, "node_modules/playwright/index.js")); } catch {}
  }
  cands.push(path.join(home, "node_modules/playwright/index.js"));
  try { cands.push(path.join(execSync("npm root -g", { encoding: "utf8" }).trim(), "playwright/index.js")); } catch {}
  return cands.find(existsSync) || null;
}
const pwPath = findPlaywright();
if (!pwPath) { console.log("SKIP: playwright not installed"); process.exit(0); }
if (!process.env.PLAYWRIGHT_BROWSERS_PATH && process.env.HOME) process.env.PLAYWRIGHT_BROWSERS_PATH = path.join(process.env.HOME, ".cache/ms-playwright");
let chromium;
try { ({ chromium } = (await import(pwPath)).default); }
catch (e) { console.log("SKIP: cannot load playwright:", e.message); process.exit(0); }
let browser;
try { browser = await chromium.launch({ headless: true, args: ["--no-sandbox"] }); }
catch (e) { console.log("SKIP: cannot launch chromium:", e.message); process.exit(0); }

let fails = 0;
const ok = (c, m) => { console.log((c ? "ok  - " : "FAIL- ") + m); if (!c) fails++; };

try {
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  page.on("pageerror", e => { console.log("PAGEERROR:", e.message); fails++; });
  await page.goto(URL, { waitUntil: "domcontentloaded", timeout: 15000 });
  await page.waitForFunction(() => typeof frame !== "undefined" && frame && frame.depth > 0, null, { timeout: 15000 });

  const info = await page.evaluate(() => ({
    depth: frame.depth, len: frame.c1.length, winFrac: frame.win_frac, edgeFrac: frame.edge_frac,
    a: view.win.a, b: view.win.b,
  }));
  ok(info.depth === 6144 && info.len === 6144, `server serves the full ${info.len}-sample record`);
  ok(Math.abs(info.winFrac - 2048 / 6144) < 0.01, `win_frac = one screen / record (${info.winFrac.toFixed(3)})`);

  // Default window = the trigger-centered screen slice: ~1/3 of the record, NOT the whole thing.
  const span = info.b - info.a;
  ok(Math.abs(span - info.winFrac) < 0.02, `default window is one screen (~1/3): span ${span.toFixed(3)}`);
  // The trigger edge maps to the screen centre (pos_frac=0.5), like today.
  const edgeScreen = (info.edgeFrac - info.a) / span; // 0..1 across the main view
  ok(Math.abs(edgeScreen - 0.5) < 0.03, `trigger edge sits at screen centre (${edgeScreen.toFixed(3)})`);

  // Navigator shows MORE than the window: the viewport rect is a fraction of the strip.
  ok(span < 0.5, `navigator shows the whole record; window is a ${(span * 100).toFixed(0)}% slice you can pan`);

  // Wheel-zoom-OUT reaches (nearly) the full record now that we're not at {0,1}.
  await page.evaluate(() => {
    const s = document.getElementById("scope"), r = s.getBoundingClientRect();
    for (let i = 0; i < 12; i++) s.dispatchEvent(new WheelEvent("wheel", { deltaY: 300, clientX: r.left + r.width / 2, clientY: r.top + r.height / 2, bubbles: true, cancelable: true }));
  });
  await page.waitForTimeout(120);
  const zoomedOut = await page.evaluate(() => view.win.b - view.win.a);
  ok(zoomedOut > 0.9, `wheel-zoom-out reaches the full record (span ${zoomedOut.toFixed(2)})`);

  // Double-click nav returns to the trigger-centered home slice.
  await page.dispatchEvent("#nav", "dblclick");
  await page.waitForTimeout(120);
  const home = await page.evaluate(() => view.win.b - view.win.a);
  ok(Math.abs(home - info.winFrac) < 0.02, `double-click nav returns home (one screen, span ${home.toFixed(3)})`);

  // Pan via nav drag moves the window without changing its span.
  const before = await page.evaluate(() => view.win.a);
  await page.evaluate(() => {
    const r = nav.getBoundingClientRect();
    nav.dispatchEvent(new PointerEvent("pointerdown", { clientX: r.left + r.width * 0.5, clientY: r.top + r.height / 2, bubbles: true, pointerId: 1 }));
    nav.dispatchEvent(new PointerEvent("pointermove", { clientX: r.left + r.width * 0.8, clientY: r.top + r.height / 2, bubbles: true, pointerId: 1 }));
    nav.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, pointerId: 1 }));
  });
  await page.waitForTimeout(80);
  const after = await page.evaluate(() => ({ a: view.win.a, span: view.win.b - view.win.a }));
  ok(after.a > before + 0.1 && Math.abs(after.span - info.winFrac) < 0.03, `nav drag pans the window (a ${before.toFixed(2)} -> ${after.a.toFixed(2)})`);

  if (process.env.SHOT_DIR) await page.screenshot({ path: process.env.SHOT_DIR + "/deepmem.png" });
} finally {
  await browser.close();
}
console.log(fails ? `\n${fails} FAILED` : "\nALL PASS");
process.exit(fails ? 1 : 0);
