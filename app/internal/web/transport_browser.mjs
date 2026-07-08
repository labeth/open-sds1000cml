// Real-browser e2e for the frame transport (argv[2]=URL). There is ONE
// transport — the binary /api/frame.bin long-poll — with no JSON fallback.
// Verifies:
// (1) frames arrive over /api/frame.bin as Int16Array, measurements ride in the
//     binary header, and the sequence advances;
// (2) killing /api/frame.bin STOPS frame advance — there is no silent fallback
//     that would fake-pass at old performance — and raises NO uncaught errors
//     (the poll retries with backoff); restoring the endpoint RESUMES advance
//     (the single path self-heals).
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
try { browser = await chromium.launch({ headless: true, args: ["--no-sandbox", "--use-gl=egl", "--enable-gpu", "--ignore-gpu-blocklist"] }); }
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
  // (1) the binary transport is the one path and is delivering.
  const page = await browser.newPage({ viewport: { width: 1200, height: 800 } });
  page.on("pageerror", e => { console.log("PAGEERROR:", e.message); fails++; });
  await page.goto(URL, { waitUntil: "domcontentloaded", timeout: 15000 });
  await page.waitForFunction(() => typeof frame !== "undefined" && frame && frame.c1 && frame.c1.length > 0, null, { timeout: 15000 });
  ok(await page.evaluate(() => frame.c1 instanceof Int16Array), "frame arrays arrive as Int16Array (binary transport)");
  ok(await seqAdvances(page, 800), "frames advance over the binary transport");
  ok(await page.evaluate(() => frame.m1 && typeof frame.m1.vpp === "number"), "measurements ride in the binary header");
  await page.close();

  // (2) a dead /api/frame.bin must STOP advance (no hidden fallback) without any
  //     uncaught error, then RESUME once the endpoint returns (retry self-heals).
  const page2 = await browser.newPage({ viewport: { width: 1200, height: 800 } });
  page2.on("pageerror", e => { console.log("PAGEERROR:", e.message); fails++; });
  await page2.goto(URL, { waitUntil: "domcontentloaded", timeout: 15000 });
  await page2.waitForFunction(() => typeof frame !== "undefined" && frame && frame.c1 && frame.c1.length > 0, null, { timeout: 15000 });
  // now kill the endpoint and confirm frames stall (no silent second transport)
  await page2.route("**/api/frame.bin*", r => r.fulfill({ status: 404, body: "gone" }));
  await page2.waitForTimeout(1600); // let the in-flight long-poll + a couple retries land on 404
  ok(!(await seqAdvances(page2, 900)), "a dead /api/frame.bin STOPS frames (no silent fallback)");
  // restore the endpoint; the retrying poll must pick back up
  await page2.unroute("**/api/frame.bin*");
  ok(await seqAdvances(page2, 2500), "frames RESUME once /api/frame.bin returns (retry self-heals)");
  await page2.close();
} catch (e) {
  console.log("FAIL- driver error: " + (e && e.message));
  fails++;
} finally {
  await browser.close();
}
if (fails) { console.log(fails + " FAILURES"); process.exit(1); }
console.log("ALL PASS");
