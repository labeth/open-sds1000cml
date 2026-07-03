// Real-browser e2e for the FFT peak UI, driven by headless Chromium via
// Playwright. It is launched by fft_browser_test.go against a local httptest
// server (URL in argv[2]) that serves a synthetic multi-tone frame whose peak
// magnitudes swap dominance every poll — the exact condition that made the old
// index-based selection jump. This asserts, in a real browser DOM:
//   * peaks are detected and listed,
//   * clicking a peak selects it and clicking another RE-selects (the "can't
//     pick a new one" bug),
//   * the selection stays on its FREQUENCY while the magnitude ranking
//     reshuffles across frames (the "jumps around" bug),
//   * the peak list is hidden when leaving FFT and restored on return (the
//     mode-switch re-render bug).
// Output protocol for the Go harness: prints "SKIP: ..." + exit 0 when the
// browser is unavailable, "ALL PASS" + exit 0 on success, exit 1 on failure.
import { existsSync, readdirSync } from "node:fs";
import { execSync } from "node:child_process";
import path from "node:path";

const URL = process.argv[2];
if (!URL) { console.log("SKIP: no URL argument"); process.exit(0); }

// --- locate a Playwright install (dev machines keep it in various projects) --
function findPlaywright() {
  const cands = [];
  if (process.env.PLAYWRIGHT_DIR) cands.push(path.join(process.env.PLAYWRIGHT_DIR, "playwright/index.js"));
  const home = process.env.HOME || "";
  for (const base of [path.join(home, "ws"), path.join(home, "src"), path.join(home, "projects")]) {
    try {
      for (const d of readdirSync(base)) cands.push(path.join(base, d, "node_modules/playwright/index.js"));
    } catch {}
  }
  cands.push(path.join(home, "node_modules/playwright/index.js"));
  try {
    const groot = execSync("npm root -g", { encoding: "utf8" }).trim();
    cands.push(path.join(groot, "playwright/index.js"));
  } catch {}
  return cands.find(existsSync) || null;
}

const pwPath = findPlaywright();
if (!pwPath) { console.log("SKIP: playwright not installed"); process.exit(0); }
if (!process.env.PLAYWRIGHT_BROWSERS_PATH && process.env.HOME) {
  process.env.PLAYWRIGHT_BROWSERS_PATH = path.join(process.env.HOME, ".cache/ms-playwright");
}

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

  ok(await page.evaluate(() => typeof detectPeaks === "function" && typeof nearestPeak === "function"),
    "peaks.js loaded in the page");

  await page.click("#mFFT");
  await page.waitForFunction(() => typeof fftPeaks !== "undefined" && fftPeaks.length >= 2, null, { timeout: 15000 });
  const peaks = await page.evaluate(() => fftPeaks.map(p => Math.round(p.freq)));
  ok(peaks.length >= 2, `detected ${peaks.length} peaks (freq-ordered): ${peaks.map(f => (f/1000).toFixed(1)+"k").join(",")}`);
  ok(peaks.every((f, i) => i === 0 || f >= peaks[i-1]), "peak list is frequency-ordered");
  const nrows = await page.locator("#fftBody .pk").count();
  ok(nrows === peaks.length, `table row per peak (${nrows})`);

  await page.evaluate(() => clearPeaks());
  const near = (a, b) => Math.abs(a - b) < 800;
  const selFn = () => page.evaluate(() => [...selIdx].map(i => Math.round(fftPeaks[i].freq)).sort((a, b) => a - b));
  // Toggle the peak nearest a frequency by finding+clicking its row in ONE
  // in-page step (no index race with the background poll re-rendering the list).
  const toggle = f => page.evaluate(freq => {
    let best = null, bd = Infinity;
    for (const r of document.querySelectorAll("#fftBody .pk")) {
      const d = Math.abs(+r.dataset.freq - freq); if (d < bd) { bd = d; best = r; }
    }
    if (best) best.click();
  }, f);
  // The two always-present main tones (the 2 strongest peaks); the weaker
  // sidelobes come and go, so target the stable ones by frequency.
  const [mLo, mHi] = await page.evaluate(() => fftPeaks.map(p => ({ f: p.freq, db: p.db }))
    .sort((a, b) => b.db - a.db).slice(0, 2).sort((a, b) => a.f - b.f).map(x => x.f));

  // MULTI-SELECT: selections accumulate.
  await toggle(mLo);
  let sel = await selFn();
  ok(sel.length === 1 && sel.some(f => near(f, mLo)), `clicking a peak selects it (${(mLo/1000).toFixed(1)}k, ${sel.length} selected)`);
  await toggle(mHi);
  sel = await selFn();
  ok(sel.length === 2 && sel.some(f => near(f, mHi)), `a second click ADDS a peak (multi-select, ${sel.length} selected)`);

  // DESELECT: clicking a selected peak again removes just that one.
  await toggle(mLo);
  sel = await selFn();
  ok(sel.length === 1 && !sel.some(f => near(f, mLo)) && sel.some(f => near(f, mHi)),
    `clicking a selected peak deselects it (${(mLo/1000).toFixed(1)}k gone, ${sel.length} left)`);

  // CLEAR ALL.
  await page.click("#fftClear");
  ok((await selFn()).length === 0, "clear removes all selections");

  // Re-select both mains for the stability check.
  await toggle(mLo); await toggle(mHi);
  const want2 = await selFn();
  ok(want2.length === 2, `two peaks selected for stability: ${want2.map(f => (f/1000).toFixed(1)+"k").join(",")}`);

  // Stability while magnitudes reshuffle: BOTH selections survive, tracked by
  // frequency, even as the strongest peak swaps between the two tones.
  const strongestIdx = new Set();
  let stable = true;
  for (let i = 0; i < 16; i++) {
    await page.waitForTimeout(90);
    const s = await page.evaluate(() => ({
      sel: [...selIdx].map(i => Math.round(fftPeaks[i].freq)).sort((a, b) => a - b),
      strong: fftPeaks.map((p, i) => [p.db, i]).sort((a, b) => b[0] - a[0])[0][1],
    }));
    strongestIdx.add(s.strong);
    if (s.sel.length !== 2 || !s.sel.every((f, k) => Math.abs(f - want2[k]) < 2000)) stable = false;
  }
  ok(stable, `both selections stayed put across 16 reshuffling frames`);
  ok(strongestIdx.size > 1, `magnitude ranking actually reshuffled (strongest peak took ${strongestIdx.size} positions)`);

  // mode-switch behaviour: list lives in FFT and Y-T, hidden in X-Y.
  ok(await page.evaluate(() => getComputedStyle(fftCard).display) !== "none", "list visible in FFT");
  await page.click("#mYT"); await page.waitForTimeout(200);
  await page.waitForFunction(() => typeof fftPeaks !== "undefined" && fftPeaks.length >= 2, null, { timeout: 8000 });
  ok(await page.evaluate(() => getComputedStyle(fftCard).display) !== "none", "list ALSO visible in Y-T (peaks in the time view)");

  // In Y-T, each selected frequency reconstructs as an overlay curve.
  const ok2 = await page.evaluate(() => {
    if (!selIdx.size) return false;
    const src = peakSrc();
    return [...selIdx].every(i => {
      const comp = component(src, fftPeaks[i].freq * frame.col_span_s);
      return Array.isArray(comp) && comp.length === src.length;
    });
  });
  ok(ok2, "each selected frequency reconstructs as an overlay curve in Y-T");

  await page.click("#mXY"); await page.waitForTimeout(150);
  ok(await page.evaluate(() => getComputedStyle(fftCard).display) === "none", "list hidden in X-Y");
  await page.click("#mFFT");
  await page.waitForFunction(() => typeof fftPeaks !== "undefined" && fftPeaks.length >= 1, null, { timeout: 8000 });
  ok(await page.evaluate(() => getComputedStyle(fftCard).display) !== "none", "list restored on return to FFT");
  await page.click("#mYT"); await page.waitForTimeout(200); // land on Y-T for a shot
  if (process.env.SHOT_DIR) { await page.screenshot({ path: process.env.SHOT_DIR + "/yt_component.png" }); }
} finally {
  await browser.close();
}

console.log(fails ? `\n${fails} FAILED` : "\nALL PASS");
process.exit(fails ? 1 : 0);
