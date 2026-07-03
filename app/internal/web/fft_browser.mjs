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

  // LIST CLICKS must be reliable: click several rows in turn; each must select
  // that row's own frequency (rows carry data-freq, so this is robust to the
  // list re-sorting between render and click — the old index-based bug).
  const rowFreqs = await page.evaluate(() =>
    [...document.querySelectorAll("#fftBody .pk")].map(r => +r.dataset.freq));
  let f1 = 0;
  for (const di of [0, Math.floor(nrows / 2), nrows - 1, 1]) {
    await page.click(`#fftBody .pk[data-i="${di}"]`);
    const sel = await page.evaluate(() => selFreq);
    ok(Math.abs(sel - rowFreqs[di]) < 600, `list-click row ${di} selects its frequency (${(rowFreqs[di]/1000).toFixed(1)}k -> sel ${(sel/1000).toFixed(1)}k)`);
    f1 = sel;
  }
  const f0 = rowFreqs[0];
  ok(Math.abs(f1 - f0) > 2000, `re-selecting a different row changes the selection (${(f0/1000).toFixed(1)}k vs ${(f1/1000).toFixed(1)}k)`);

  // Stability while magnitudes reshuffle: the selection must keep pointing at a
  // peak of frequency f1, even as the STRONGEST peak (what an index-based
  // selection would follow) swaps between the two tones.
  const strongestIdx = new Set();
  let stable = true, worstDrift = 0;
  for (let i = 0; i < 16; i++) {
    await page.waitForTimeout(90);
    const s = await page.evaluate(() => {
      const strong = fftPeaks.map((p, i) => [p.db, i]).sort((a, b) => b[0] - a[0])[0][1];
      return { selPeak, strong, selCur: fftPeaks[selPeak]?.freq };
    });
    strongestIdx.add(s.strong);
    const drift = s.selCur == null ? 1e9 : Math.abs(s.selCur - f1);
    if (s.selPeak < 0 || drift > 2000) stable = false;
    worstDrift = Math.max(worstDrift, drift);
  }
  ok(stable, `selection stayed on ${(f1/1000).toFixed(1)}k across 16 reshuffling frames (max drift ${Math.round(worstDrift)} Hz)`);
  ok(strongestIdx.size > 1, `magnitude ranking actually reshuffled (strongest peak took ${strongestIdx.size} positions)`);

  // mode-switch behaviour: list lives in FFT and Y-T, hidden in X-Y.
  ok(await page.evaluate(() => getComputedStyle(fftCard).display) !== "none", "list visible in FFT");
  await page.click("#mYT"); await page.waitForTimeout(200);
  await page.waitForFunction(() => typeof fftPeaks !== "undefined" && fftPeaks.length >= 2, null, { timeout: 8000 });
  ok(await page.evaluate(() => getComputedStyle(fftCard).display) !== "none", "list ALSO visible in Y-T (peaks in the time view)");

  // In Y-T, selecting a list frequency reconstructs that tone as an overlay.
  const ok2 = await page.evaluate(() => {
    const r = [...document.querySelectorAll("#fftBody .pk")]
      .sort((a, b) => +b.dataset.freq - +a.dataset.freq)[0]; // highest-freq row
    r.click();
    const comp = component(peakSrc(), selFreq * frame.col_span_s);
    return selPeak >= 0 && Array.isArray(comp) && comp.length === peakSrc().length;
  });
  ok(ok2, "Y-T list-click selects a frequency and it reconstructs as an overlay curve");

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
