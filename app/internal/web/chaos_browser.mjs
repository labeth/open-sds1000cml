// Browser chaos monkey (argv[2]=URL, argv[3]=seed, argv[4]=actions).
// Randomly pokes every interactive control, drags/wheels/keys the scope
// canvas, and flips modes as fast as the UI allows. ANY pageerror or
// unhandled rejection is a bug (each one is a broken UI state a user can
// reach); at the end the render loop must still be alive (seq advancing).
// SKIP/exit 0 when the browser is absent.
import path from "node:path";
import { findPlaywright } from "./scope_po.mjs";

const URL = process.argv[2];
const SEED = +(process.argv[3] || 1337);
const ACTIONS = +(process.argv[4] || 700);
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

// mulberry32: reproducible chaos — the failing seed is the repro recipe
let s = SEED >>> 0;
const rnd = () => (s = (s + 0x6d2b79f5) | 0, ((Math.imul(s ^ (s >>> 15), 1 | s) + 0x6d2b79f5) >>> 0) / 4294967296);
const pick = (a) => a[Math.floor(rnd() * a.length)];

let fails = 0;
const ok = (c, m) => { console.log((c ? "ok  - " : "FAIL- ") + m); if (!c) fails++; };
try {
  const page = await browser.newPage({ viewport: { width: 1500, height: 950 } });
  const errs = [];
  page.on("pageerror", e => errs.push(e.message));
  await page.goto(URL, { waitUntil: "domcontentloaded", timeout: 15000 });
  await page.waitForFunction(() => typeof frame !== "undefined" && frame && frame.c1 && frame.c1.length > 0, null, { timeout: 15000 });

  const box = await (await page.$("#scope")).boundingBox();
  const keys = ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "+", "-", "Escape", "Home", "f", "z", " "];

  for (let i = 0; i < ACTIONS; i++) {
    const kind = rnd();
    try {
      if (kind < 0.30) { // click a random button
        const btns = await page.$$("button:not([disabled])");
        if (btns.length) await pick(btns).click({ timeout: 700, force: true });
      } else if (kind < 0.45) { // random select option
        const sels = await page.$$("select");
        if (sels.length) {
          const sel = pick(sels);
          const opts = await sel.$$eval("option", os => os.map(o => o.value));
          if (opts.length) await sel.selectOption(pick(opts), { timeout: 700 });
        }
      } else if (kind < 0.58) { // random number input
        const ins = await page.$$('input[type="number"]');
        if (ins.length) {
          const v = pick(["0", "-1", "1e9", "3", "255", "0.0001", "-42", "99999"]);
          await pick(ins).fill(v, { timeout: 700 });
        }
      } else if (kind < 0.78) { // pointer gesture on the scope canvas
        const x0 = box.x + rnd() * box.width, y0 = box.y + rnd() * box.height;
        const x1 = box.x + rnd() * box.width, y1 = box.y + rnd() * box.height;
        await page.mouse.move(x0, y0);
        if (rnd() < 0.5) {
          await page.mouse.down();
          await page.mouse.move(x1, y1, { steps: 3 });
          await page.mouse.up();
        } else if (rnd() < 0.5) {
          await page.mouse.wheel(0, (rnd() - 0.5) * 800);
        } else {
          await page.mouse.dblclick(x0, y0);
        }
      } else if (kind < 0.9) { // keyboard
        await page.keyboard.press(pick(keys));
      } else { // shift/ctrl-modified canvas click (cursors etc.)
        const mod = rnd() < 0.5 ? "Shift" : "Control";
        await page.keyboard.down(mod);
        await page.mouse.click(box.x + rnd() * box.width, box.y + rnd() * box.height);
        await page.keyboard.up(mod);
      }
    } catch (e) { /* per-action timeouts are fine; pageerrors are not */ }
    if (errs.length) break; // stop at first JS error: the trace names the culprit
  }

  ok(errs.length === 0, `no page errors after chaos (seed ${SEED})` + (errs.length ? `: ${errs[0]}` : ""));
  // unfreeze if the monkey froze the display, then require liveness
  await page.evaluate(() => { if (typeof frozen !== "undefined" && frozen) { frozen = false; lastSig = ""; } });
  const s0 = await page.evaluate(() => frame ? frame.seq : 0);
  await page.waitForTimeout(1500);
  const s1 = await page.evaluate(() => frame ? frame.seq : 0);
  ok(s1 > s0, `render loop alive after chaos (seq ${s0} -> ${s1})`);
} catch (e) {
  console.log("FAIL- driver error:", e.message);
  fails++;
} finally {
  await browser.close();
}
console.log(fails ? `${fails} FAILURES` : "ALL OK");
process.exit(fails ? 1 : 0);
