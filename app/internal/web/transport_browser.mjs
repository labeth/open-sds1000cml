// Real-browser e2e for the frame transport (argv[2]=URL). Verifies:
// (1) the binary /api/frame.bin long-poll is the ACTIVE transport (a silent
//     fallback to the JSON poll would fake-pass every other suite at old
//     performance), and frames advance over it;
// (2) killing /api/frame.bin drops the client to the JSON poll and frames
//     STILL advance (the fallback actually works);
// (3) ?transport=json forces the legacy path.
// SKIP/exit 0 when the browser is absent.
import path from "node:path";
import { findPlaywright } from "./scope_po.mjs";

const URL = process.argv[2];
if (!URL) { console.log("SKIP: no URL argument"); process.exit(0); }
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
const seqAdvances = async (page, ms) => {
  const s0 = await page.evaluate(() => (typeof frame !== "undefined" && frame) ? frame.seq : 0);
  await page.waitForTimeout(ms);
  const s1 = await page.evaluate(() => (typeof frame !== "undefined" && frame) ? frame.seq : 0);
  return s1 > s0;
};

try {
  // (1) binary transport active + delivering.
  const page = await browser.newPage({ viewport: { width: 1200, height: 800 } });
  page.on("pageerror", e => { console.log("PAGEERROR:", e.message); fails++; });
  await page.goto(URL, { waitUntil: "domcontentloaded", timeout: 15000 });
  await page.waitForFunction(() => typeof frame !== "undefined" && frame && frame.c1 && frame.c1.length > 0, null, { timeout: 15000 });
  ok(await page.evaluate(() => transport) === "bin", "binary transport is the active path");
  ok(await page.evaluate(() => frame.c1 instanceof Int16Array), "frame arrays arrive as Int16Array");
  ok(await seqAdvances(page, 800), "frames advance over the binary transport");
  ok(await page.evaluate(() => frame.m1 && typeof frame.m1.vpp === "number"), "measurements ride in the binary header");
  await page.close();

  // (2) dead binary endpoint → automatic fallback to the JSON poll.
  const page2 = await browser.newPage({ viewport: { width: 1200, height: 800 } });
  page2.on("pageerror", e => { console.log("PAGEERROR:", e.message); fails++; });
  await page2.route("**/api/frame.bin*", r => r.fulfill({ status: 404, body: "gone" }));
  await page2.goto(URL, { waitUntil: "domcontentloaded", timeout: 15000 });
  await page2.waitForFunction(() => typeof frame !== "undefined" && frame && frame.c1 && frame.c1.length > 0, null, { timeout: 15000 });
  ok(await page2.evaluate(() => transport) === "json", "404 on .bin falls back to the JSON poll");
  ok(await seqAdvances(page2, 800), "frames still advance on the fallback");
  await page2.close();

  // (3) forced legacy path for A/B measurement.
  const page3 = await browser.newPage({ viewport: { width: 1200, height: 800 } });
  page3.on("pageerror", e => { console.log("PAGEERROR:", e.message); fails++; });
  await page3.goto(URL + "/?transport=json", { waitUntil: "domcontentloaded", timeout: 15000 });
  await page3.waitForFunction(() => typeof frame !== "undefined" && frame && frame.c1 && frame.c1.length > 0, null, { timeout: 15000 });
  ok(await page3.evaluate(() => transport) === "json", "?transport=json forces the legacy poll");
  ok(await seqAdvances(page3, 800), "frames advance on the forced legacy poll");
  await page3.close();
} catch (e) {
  console.log("FAIL- driver error: " + (e && e.message));
  fails++;
} finally {
  await browser.close();
}
if (fails) { console.log(fails + " FAILURES"); process.exit(1); }
console.log("ALL PASS");
