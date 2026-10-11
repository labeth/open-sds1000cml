// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e for the FFT peak UI (SPECTRUM's peak table), driven by
// headless Chromium via Playwright. It is launched by fft_browser_test.go
// against a local httptest server (URL in argv[2]) that serves a synthetic
// multi-tone frame whose peak magnitudes swap dominance every poll — the exact
// condition that made the old index-based selection jump. This asserts, in a
// real browser DOM:
//   * peaks are detected and listed (one table, C1/C2 chosen in its header),
//   * clicking a peak picks it and clicking another ADDS it; clicking a picked
//     one drops just that one (the "can't pick a new one" bug),
//   * the picks stay on their FREQUENCY while the ranking reshuffles across
//     frames (the "jumps around" bug),
//   * the picks survive leaving SPECTRUM and coming back, and each picked tone
//     reconstructs as an overlay curve in Y-T.
// Output protocol for the Go harness: "SKIP: ..." + exit 0 when the browser is
// unavailable, "ALL PASS" + exit 0 on success, exit 1 on failure.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2]);
  t.browser = browser;

  t.ok(await po.eval(() => typeof detectPeaks === "function" && typeof nearestPeak === "function"),
    "peaks.js loaded in the page");

  await po.setMode("FFT");
  await po.waitFor(() => typeof fftCh !== "undefined" && fftCh[1].peaks.length >= 2, null, 15000);
  const peaks = await po.eval(() => fftCh[1].peaks.map(p => Math.round(p.freq)));
  t.ok(peaks.length >= 2, `detected ${peaks.length} peaks (freq-ordered): ${peaks.map(f => (f/1000).toFixed(1)+"k").join(",")}`);
  t.ok(peaks.every((f, i) => i === 0 || f >= peaks[i-1]), "peak list is frequency-ordered");
  await po.waitFor((n) => document.querySelectorAll("#fftBody .pk").length === n, peaks.length, 4000).catch(() => {});
  const nrows = await po.count("#fftBody .pk");
  t.ok(nrows === peaks.length, `table row per peak (${nrows})`);
  t.ok(await po.eval(() => [...document.querySelectorAll("#peakTable thead th")].map((th) => th.textContent).join("|") === "#|Frequency|Level|Rel|Harm"),
    "columns: frequency, level, level re the largest, harmonic number");

  // Configurable list length: "top N" caps the number of peaks shown.
  const full = await po.count("#fftBody .pk");
  await page.fill("#fftN", "1");
  await po.waitFor(() => document.querySelectorAll("#fftBody .pk").length === 1, null, 4000);
  t.ok(await po.count("#fftBody .pk") === 1, "top-N=1 caps the list to 1 peak");
  await page.fill("#fftN", String(full));
  await po.waitFor((c) => document.querySelectorAll("#fftBody .pk").length === c, full, 4000);
  t.ok(await po.count("#fftBody .pk") === full, `raising top-N restores the full list (${full})`);

  await po.eval(() => clearPeaksCh(1));
  // TRLC-LINKS: REQ-SDS-180
  const near = (a, b) => Math.abs(a - b) < 800;
  // TRLC-LINKS: REQ-SDS-180
  const selFn = (ch = 1) => po.eval((c) => [...fftCh[c].selIdx].map(i => Math.round(fftCh[c].peaks[i].freq)).sort((a, b) => a - b), ch);
  // Toggle the peak nearest a frequency by finding+clicking its row in ONE
  // in-page step (no index race with the background poll re-rendering the list).
  // TRLC-LINKS: REQ-SDS-180
  const toggle = f => po.eval(freq => {
    let best = null, bd = Infinity;
    for (const r of document.querySelectorAll("#fftBody .pk")) {
      const d = Math.abs(+r.dataset.freq - freq); if (d < bd) { bd = d; best = r; }
    }
    if (best) best.click();
  }, f);
  // The two always-present main tones (the 2 strongest peaks).
  const [mLo, mHi] = await po.eval(() => fftCh[1].peaks.map(p => ({ f: p.freq, db: p.db }))
    .sort((a, b) => b.db - a.db).slice(0, 2).sort((a, b) => a.f - b.f).map(x => x.f));

  // MULTI-SELECT: picks accumulate.
  await toggle(mLo);
  let sel = await selFn();
  t.ok(sel.length === 1 && sel.some(f => near(f, mLo)), `clicking a peak picks it (${(mLo/1000).toFixed(1)}k, ${sel.length} picked)`);
  await toggle(mHi);
  sel = await selFn();
  t.ok(sel.length === 2 && sel.some(f => near(f, mHi)), `a second click ADDS a peak (multi-select, ${sel.length} picked)`);
  t.ok(await po.count("#fftBody .pk.is-sel") === 2, "picked rows are marked");
  t.ok(/dBc/.test(await po.text("fftSel")), "the picked-peak readout under the table");

  // DESELECT: clicking a picked peak again removes just that one.
  await toggle(mLo);
  sel = await selFn();
  t.ok(sel.length === 1 && !sel.some(f => near(f, mLo)) && sel.some(f => near(f, mHi)),
    `clicking a picked peak drops it (${(mLo/1000).toFixed(1)}k gone, ${sel.length} left)`);

  // CLEAR.
  await po.click("fftClear");
  t.ok((await selFn()).length === 0, "Clear removes all picks");

  // PER-CHANNEL: C2 carries different tones; the table shows C2's peaks when
  // chosen, with its own picks. Picking in C2 must not touch C1.
  const c2peaks = await po.eval(() => fftCh[2].peaks.map(p => Math.round(p.freq)));
  t.ok(c2peaks.length >= 2, `C2 has its own peaks: ${c2peaks.map(f => (f/1000).toFixed(1)+"k").join(",")}`);
  const c1peaks = await po.eval(() => fftCh[1].peaks.map(p => Math.round(p.freq)));
  t.ok(JSON.stringify(c1peaks) !== JSON.stringify(c2peaks), "C1 and C2 peak sets differ (independent FFTs)");
  await toggle(mLo);                                   // pick one in C1
  await po.seg("pkCh", "2");
  await po.waitFor(() => { const r = document.querySelector("#fftBody .pk"); return r && r.querySelector(".pf").classList.contains("cc2"); }, null, 4000);
  t.ok(await po.count("#fftBody .pk") === await po.eval(() => fftCh[2].peaks.length), "C2 chosen: a row per C2 peak");
  await po.eval(() => document.querySelector("#fftBody .pk").click()); // pick one in C2
  const s1 = await po.eval(() => fftCh[1].sel.length), s2 = await po.eval(() => fftCh[2].sel.length);
  t.ok(s1 === 1 && s2 === 1, `C1 and C2 picks are independent (C1=${s1}, C2=${s2})`);
  await po.click("fftClear");
  t.ok(await po.eval(() => fftCh[2].sel.length) === 0 && await po.eval(() => fftCh[1].sel.length) === 1,
    "clearing C2 leaves C1's pick intact");
  await po.seg("pkCh", "1");
  await po.click("fftClear");

  // Re-pick both mains for the stability check.
  await toggle(mLo); await toggle(mHi);
  const want2 = await selFn();
  t.ok(want2.length === 2, `two peaks picked for stability: ${want2.map(f => (f/1000).toFixed(1)+"k").join(",")}`);

  // Stability while magnitudes reshuffle: BOTH picks survive, tracked by
  // frequency, even as the strongest peak swaps between the two tones.
  const strongestIdx = new Set();
  let stable = true;
  for (let i = 0; i < 16; i++) {
    await po.wait(90);
    const s = await po.eval(() => ({
      sel: [...fftCh[1].selIdx].map(i => Math.round(fftCh[1].peaks[i].freq)).sort((a, b) => a - b),
      strong: fftCh[1].peaks.map((p, i) => [p.db, i]).sort((a, b) => b[0] - a[0])[0][1],
    }));
    strongestIdx.add(s.strong);
    if (s.sel.length !== 2 || !s.sel.every((f, k) => Math.abs(f - want2[k]) < 2000)) stable = false;
  }
  t.ok(stable, `both picks stayed put across 16 reshuffling frames`);
  t.ok(strongestIdx.size > 1, `magnitude ranking actually reshuffled (strongest peak took ${strongestIdx.size} positions)`);

  // Leaving SPECTRUM: the view returns to Y-T and the picks keep working there.
  await po.ws("scope");
  t.ok(await po.eval(() => view.mode === "YT"), "leaving SPECTRUM returns the screen to Y-T");
  await po.waitFor(() => typeof fftCh !== "undefined" && fftCh[1].peaks.length >= 2, null, 8000);
  t.ok((await selFn()).length === 2, "the picks survive leaving SPECTRUM");

  // In Y-T, each picked frequency reconstructs as an overlay curve.
  const ok2 = await po.eval(() => {
    if (!fftCh[1].selIdx.size) return false;
    const src = peakSrcCh(1);
    return [...fftCh[1].selIdx].every(i => {
      const comp = component(src, fftCh[1].peaks[i].freq * frame.col_span_s);
      return Array.isArray(comp) && comp.length === src.length;
    });
  });
  t.ok(ok2, "each picked frequency reconstructs as an overlay curve in Y-T");

  await po.ws("spectrum");
  await po.waitFor(() => view.mode === "FFT" && document.querySelectorAll("#fftBody .pk.is-sel").length === 2, null, 8000);
  t.ok(true, "back in SPECTRUM the FFT and its two picks return");
  if (process.env.SHOT_DIR) { await po.ws("scope"); await page.screenshot({ path: process.env.SHOT_DIR + "/yt_component.png" }); }
  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
