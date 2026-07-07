// Real-browser e2e for the zone/mask card (argv[2]=URL). Verifies:
// (1) armed zone drawing: click "draw zone", drag on the scope canvas -> a
//     zone lands in zm.zones (edge-anchored coords) and the list renders it;
// (2) "zone trig" sends zonemode=1;
// (3) "build mask" pulls raw frames, dilates, uploads (zm.mask installed);
// (4) the coupling guard refuses to build on an AC-coupled channel;
// (5) the failure gallery renders ring entries and clicking one freezes the
//     failing frame with the violation marked.
// The Go wrapper asserts the server-side effects (zones/mask/zonemode POSTs).
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

try {
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  page.on("pageerror", e => { console.log("PAGEERROR:", e.message); fails++; });
  await page.goto(URL, { waitUntil: "domcontentloaded", timeout: 15000 });
  await page.waitForFunction(() => typeof frame !== "undefined" && frame && frame.c1 && frame.c1.length > 0, null, { timeout: 15000 });
  await page.waitForFunction(() => typeof st !== "undefined" && st && st.win_cols > 0, null, { timeout: 15000 });

  // (1) draw a zone: arm, drag a rect over the scope canvas.
  await page.click("#zmDraw");
  ok(await page.evaluate(() => zm.drawArmed), "draw armed after click");
  const box = await (await page.$("#scope")).boundingBox();
  await page.mouse.move(box.x + box.width * 0.55, box.y + box.height * 0.30);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width * 0.70, box.y + box.height * 0.45, { steps: 5 });
  await page.mouse.up();
  const z = await page.evaluate(() => zm.zones);
  ok(z.length === 1, "drag created one zone");
  ok(z.length && z[0].dt_hi_s > z[0].dt_lo_s && z[0].code_hi > z[0].code_lo, "zone has a positive dt x code extent");
  ok(z.length && z[0].dt_lo_s > 0, "zone right of the centred edge has dt > 0 (edge-anchored transform)");
  ok((await page.evaluate(() => document.querySelectorAll("#zmZoneList .zmz").length
       + document.querySelectorAll("#zmZoneList button").length)) > 0, "zone list rendered controls");

  // (2) zone trigger toggle reaches the server.
  await page.click("#zmTrig");
  await page.waitForTimeout(300);

  // (3) coupling guard FIRST (client-side st mutation, read the refusal immediately).
  await page.evaluate(() => { st.cpl1 = 1; });
  await page.click("#zmBuild");
  await page.waitForTimeout(200);
  ok((await page.evaluate(() => $("zmStats").textContent)).includes("coupling"),
    "AC-coupled channel refuses the mask build");
  ok(await page.evaluate(() => !zm.mask), "no mask installed by the refused build");
  await page.evaluate(() => { st.cpl1 = 0; });

  // (4) build a mask from 8 raw frames and upload it.
  await page.fill("#zmN", "8");
  await page.click("#zmBuild");
  await page.waitForFunction(() => zm.mask && zm.mask.win > 0, null, { timeout: 20000 });
  const m = await page.evaluate(() => ({ win: zm.mask.win, n: zm.mask.lo.length, lo0: zm.mask.lo[0], hi0: zm.mask.hi[0] }));
  ok(m.win === m.n, "mask envelope length matches the window");
  ok(m.hi0 > m.lo0, "mask bounds ordered");

  // (5) vertical re-anchor: masks/zones are physically VOLTS — halving the
  // volts-per-code must re-map their codes (2x further from centre) and
  // re-install. Everything inside ONE evaluate: the live transport would
  // overwrite frame.vpc1 between round-trips.
  const rr = await page.evaluate(() => {
    const before = { lo0: zm.mask.lo[0], zlo: zm.zones[0].code_lo, zhi: zm.zones[0].code_hi };
    frame.vpc1 = frame.vpc1 / 2;
    window.zmRescale();
    return { before, after: { lo0: zm.mask.lo[0], zlo: zm.zones[0].code_lo, zhi: zm.zones[0].code_hi } };
  });
  const stretch = (c) => Math.round(128 + 2 * (c - 128));
  ok(Math.abs(rr.after.zlo - stretch(rr.before.zlo)) <= 2 && Math.abs(rr.after.zhi - stretch(rr.before.zhi)) <= 2,
    "zone re-anchored to the new V/div (codes stretched 2x about centre)");
  ok(Math.abs(rr.after.lo0 - stretch(rr.before.lo0)) <= 2, "mask envelope re-anchored to the new V/div");

  // (6) failure gallery: the Go fixture preloads one ring entry; the 1 Hz
  // meter renders it -> click freezes the failing frame with the mark.
  await page.waitForFunction(() => document.querySelectorAll("#zmGallery .zmf").length > 0, null, { timeout: 5000 });
  await page.click("#zmGallery .zmf");
  await page.waitForFunction(() => zm.failMark !== null && typeof frozen !== "undefined" && frozen, null, { timeout: 5000 });
  ok(true, "gallery click froze the failing frame with the violation marked");
  ok((await page.evaluate(() => $("zmStats").textContent)).includes("capture"), "status names the failing capture ordinal");
} catch (e) {
  console.log("FAIL- driver error:", e.message);
  fails++;
} finally {
  await browser.close();
}
console.log(fails ? `${fails} FAILURES` : "ALL OK");
process.exit(fails ? 1 : 0);
