// Perf regression guard for the superres-view hot path (argv[2]=server URL).
// Synthesizes a large multi-tone frame (a stand-in for a viewed stack), selects
// many FFT peaks and turns on the residual, then times a COLD redraw (which must
// fit every selected tone) against WARM redraws (which must hit the component +
// math memoization). Zoomed in, so per-pixel draw cost is negligible and the
// memoization speedup dominates the ratio — if the memoization regresses,
// cold≈warm and the ratio collapses toward 1. Machine-independent by design.
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

  // Install a large synthetic "stack" frame: a 12-harmonic comb so detectPeaks
  // finds plenty of real lines, at a fine-grid length like a real K× stack.
  const setup = await page.evaluate(() => {
    const M = 655360, TONES = 12;
    const c1 = new Array(M);
    for (let i = 0; i < M; i++) {
      let v = 128;
      for (let k = 1; k <= TONES; k++) v += (44 / k) * Math.sin(2 * Math.PI * (60 * k) * i / M);
      c1[i] = v < 0 ? 0 : v > 255 ? 255 : v;
    }
    frame = {
      c1, c2: c1, cols: M, col_span_s: 40.96e-6, is_env: false, seq: 1,
      vpc1: 1 / 32, vpc2: 1 / 32, off1_v: 0, off2_v: 0, edge_frac: 0.5,
      tdiv_s: 5e-9, displayed_sdiv_s: 5e-9, trigd: true, coherent: true,
    };
    setMode("YT"); maxPeaks = 16; computePeaksCh(1);
    const S = fftCh[1], n = Math.min(12, S.peaks.length);
    S.sel = S.peaks.slice(0, n).map(p => p.freq);
    S.selIdx = new Set(S.peaks.slice(0, n).map((_, i) => i));
    mathFn = "res1";
    view.win.a = 0.45; view.win.b = 0.55; // zoom in: draw cost negligible vs the fits
    return { M, peaks: S.peaks.length, selected: S.sel.length };
  });
  ok(setup.selected >= 8, `synthetic stack ready: ${setup.selected} tones selected of ${setup.peaks} peaks`);

  const r = await page.evaluate(() => {
    const once = fn => { const a = performance.now(); fn(); return performance.now() - a; };
    const cold = once(() => redraw());              // must fit every selected tone
    let sum = 0, best = Infinity;
    for (let i = 0; i < 6; i++) { const d = once(() => redraw()); sum += d; if (d < best) best = d; }
    return { cold: +cold.toFixed(1), warm: +(sum / 6).toFixed(1), warmBest: +best.toFixed(1) };
  });
  console.log(`  timings: cold=${r.cold}ms warm=${r.warm}ms (best ${r.warmBest}ms)`);

  // Memoization must make warm redraws dramatically cheaper than the cold fit.
  ok(r.cold / Math.max(r.warm, 0.1) > 3, `component/math memo active: cold ${r.cold}ms vs warm ${r.warm}ms (>3x faster warm)`);
  // Absolute backstop: a warm redraw of a viewed stack must stay interactive.
  ok(r.warm < 200, `warm stack redraw stays interactive: ${r.warm}ms < 200ms`);

  console.log(fails ? `\n${fails} FAILED` : "\nALL PASS");
} finally {
  await browser.close();
}
process.exit(fails ? 1 : 0);
