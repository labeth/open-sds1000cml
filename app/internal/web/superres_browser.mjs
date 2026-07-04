// Real-browser e2e for the superres stacker (argv[2]=URL of a server whose
// fakeScope generates a jittered noisy sine): arm → frames accumulate and
// stats populate → view shows the stacked waveform as a frozen synthetic
// frame (dense, Float32) → fit model lands in REF B → live resumes.
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

  // Arm with manual stop and a fine grid.
  await page.selectOption("#srDur", "0");
  await page.selectOption("#srK", "16");
  await page.click("#srArm");
  // Frames must accumulate (fakeScope publishes continuously).
  await page.waitForFunction(() => sr.st && sr.st.frames >= 25, null, { timeout: 20000 });
  const mid = await page.evaluate(() => ({ frames: sr.st.frames, stats: $("srStats").textContent, armed: sr.armed }));
  ok(mid.armed, "stacker armed and running");
  ok(mid.frames >= 25, `frames accumulate (${mid.frames})`);
  ok(/bits/.test(mid.stats) && /GSa\/s|MSa\/s/.test(mid.stats), `stats line live: ${mid.stats.slice(0, 90)}`);

  await page.click("#srArm"); // stop
  ok(await page.evaluate(() => !sr.armed), "stop disarms");

  // Review the stacked result.
  await page.click("#srShow");
  const rev = await page.evaluate(() => ({
    frozen, len: frame.c1.length, k: sr.st.K, n: sr.st.n,
    float: frame.c1 instanceof Float32Array,
    c2: frame.c2, span: frame.col_span_s,
  }));
  ok(rev.frozen, "review freezes the display");
  ok(rev.len === rev.n * rev.k, `stack is n*K fine bins (${rev.len})`);
  ok(rev.float, "stacked trace is Float32 (sub-code precision preserved)");

  // Noise on the stack must be visibly below a single frame's noise.
  const noise = await page.evaluate(() => {
    const res = srResult(sr.st);
    return { single: res.sigmaSingle, stack: res.sigmaStack, bits: res.bitsGained, fill: res.fill };
  });
  ok(noise.stack < noise.single / 1.5, `stack noise drops (σ ${noise.single.toFixed(2)}→${noise.stack.toFixed(3)}, +${noise.bits.toFixed(1)} bits)`);
  ok(noise.fill > 0.5, `fine grid fills (${(noise.fill * 100).toFixed(0)}%)`);

  // Model fit lands in REF B.
  await page.click("#srFit");
  const fit = await page.evaluate(() => ({ has: !!(refs.B && refs.B.c1 && refs.B.c1.length), msg: $("srStats").textContent }));
  ok(fit.has, `fit model → REF B (${fit.msg.slice(0, 60)})`);

  // Unfreeze → live frames take over again.
  await page.click("#freeze");
  await page.waitForFunction(() => !frozen && frame && !(frame.c1 instanceof Float32Array), null, { timeout: 10000 });
  ok(true, "live resumes after review");
} catch (e) {
  console.log("FAIL- driver error: " + (e && e.message));
  fails++;
} finally {
  await browser.close();
}
if (fails) { console.log(fails + " FAILURES"); process.exit(1); }
console.log("ALL PASS");
