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

  // Review the stacked result — a first-class view.
  await page.click("#srShow");
  const rev = await page.evaluate(() => ({
    frozen, len: frame.c1.length, k: sr.st.K, n: sr.st.n,
    float: frame.c1 instanceof Float32Array,
    hasC2: frame.c2 instanceof Float32Array && frame.c2.length === frame.c1.length,
    span: frame.col_span_s, showing: sr.showing,
    m1: frame.m1, m2: frame.m2,
  }));
  ok(rev.frozen && rev.showing, "review freezes the display (toggle on)");
  ok(rev.len === rev.n * rev.k, `stack is n*K fine bins (${rev.len})`);
  ok(rev.float, "stacked trace is Float32 (sub-code precision preserved)");
  ok(rev.hasC2, "BOTH channels stacked (c2 present)");
  ok(rev.m1 && rev.m1.vpp > 0 && rev.m1.has_timing, `measurements on the stack (vpp ${rev.m1 && rev.m1.vpp.toFixed(2)} V, f ${rev.m1 && (rev.m1.freq/1000).toFixed(1)} kHz)`);
  ok(rev.m2 && rev.m2.vpp > 0, "C2 measurements too");

  // Toggle back to live and back to the stack — the zoom is remembered.
  await page.evaluate(() => { view.win.a = 0.4; view.win.b = 0.6; redraw(); }); // zoom into the stack
  await page.click("#srShow"); // -> live
  await page.waitForFunction(() => !sr.showing && !frozen, null, { timeout: 5000 });
  await page.waitForFunction(() => frame && !(frame.c1 instanceof Float32Array), null, { timeout: 10000 });
  ok(true, "toggle returns to live frames");
  await page.click("#srShow"); // -> stack again
  const back = await page.evaluate(() => ({ showing: sr.showing, a: view.win.a, b: view.win.b, float: frame.c1 instanceof Float32Array }));
  ok(back.showing && back.float, "toggle re-enters the stack view");
  ok(Math.abs(back.a - 0.4) < 0.01 && Math.abs(back.b - 0.6) < 0.01, `stack zoom remembered (${back.a.toFixed(2)}..${back.b.toFixed(2)})`);

  // FFT mode on the stack: peaks appear for both channels.
  await page.click("#mFFT");
  await page.waitForTimeout(600);
  const fft = await page.evaluate(() => ({ p1: fftCh[1].peaks.length, p2: fftCh[2].peaks.length }));
  ok(fft.p1 > 0, `FFT peaks on stacked C1 (${fft.p1})`);
  ok(fft.p2 > 0, `FFT peaks on stacked C2 (${fft.p2})`);

  // X-Y mode on the stack renders without page errors.
  await page.click("#mXY");
  await page.waitForTimeout(400);
  ok(await page.evaluate(() => view.mode === "XY" && frame.c1.length > 0 && frame.c2.length > 0), "X-Y mode on the stack");
  await page.click("#mYT");

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
