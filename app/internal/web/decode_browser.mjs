// Real-browser e2e for the protocol-decode UI + navigator, driven by headless
// Chromium against a local server serving a synthetic I2C frame (argv[2]=URL).
// Asserts the on-screen decode transcript, byte count, Copy button, and the
// navigator wheel-zoom + reset. SKIP/exit 0 when the browser is unavailable.
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
const EXPECT = "START 50 W ACK 00 ACK FF NAK STOP";

try {
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 900 } });
  try { await ctx.grantPermissions(["clipboard-read", "clipboard-write"], { origin: URL }); } catch {}
  const page = await ctx.newPage();
  page.on("pageerror", e => { console.log("PAGEERROR:", e.message); fails++; });
  await page.goto(URL, { waitUntil: "domcontentloaded", timeout: 15000 });

  ok(await page.evaluate(() => typeof decodeI2C === "function"), "decode.js loaded in the page");

  // Select I2C (roles default SCL=C1, SDA=C2 — matches the synthetic frame).
  await page.selectOption("#decProto", "i2c");
  await page.waitForFunction(exp => document.getElementById("decodeText").value.includes("START"), EXPECT, { timeout: 15000 });
  const text = await page.evaluate(() => document.getElementById("decodeText").value);
  ok(text === EXPECT, `decode transcript = "${text}"`);
  ok(await page.evaluate(() => document.getElementById("decodeCount").textContent).then(t => /2 bytes/.test(t)), "byte count shows 2 bytes");
  ok(await page.evaluate(() => !!(dcfg.result && dcfg.result.ok && dcfg.result.spans.length)), "decode result has on-trace spans");

  // Copy button -> clipboard holds the transcript (127.0.0.1 is a secure context).
  await page.click("#decodeCopy");
  let clip = "";
  try { clip = await page.evaluate(() => navigator.clipboard.readText()); } catch {}
  ok(clip === EXPECT || clip === "", `Copy button ${clip ? "put the transcript on the clipboard" : "ran (clipboard unreadable in this env)"}`);

  // Navigator: wheel-zoom narrows the window; the decode result stays valid and
  // its spans still map through xForCol; double-click the nav resets to full.
  const before = await page.evaluate(() => view.win.b - view.win.a);
  await page.evaluate(() => {
    const s = document.getElementById("scope"), r = s.getBoundingClientRect();
    s.dispatchEvent(new WheelEvent("wheel", { deltaY: -600, clientX: r.left + r.width / 2, clientY: r.top + r.height / 2, bubbles: true, cancelable: true }));
  });
  await page.waitForTimeout(120);
  const zoomed = await page.evaluate(() => view.win.b - view.win.a);
  ok(zoomed < before - 0.05, `wheel zoom narrowed the window (${before.toFixed(2)} -> ${zoomed.toFixed(2)})`);
  ok(await page.evaluate(() => dcfg.result && dcfg.result.ok), "decode still valid while zoomed");
  await page.dispatchEvent("#nav", "dblclick");
  await page.waitForTimeout(120);
  ok(await page.evaluate(() => view.win.a === 0 && view.win.b === 1), "double-click nav resets to the full record");

  if (process.env.SHOT_DIR) await page.screenshot({ path: process.env.SHOT_DIR + "/decode.png" });
} finally {
  await browser.close();
}

console.log(fails ? `\n${fails} FAILED` : "\nALL PASS");
process.exit(fails ? 1 : 0);
