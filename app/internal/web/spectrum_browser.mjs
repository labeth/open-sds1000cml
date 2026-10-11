// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e for the SPECTRUM workspace (argv[2]=URL of a scope serving a
// 50 % square of 64 samples on C1 and a small sine on C2). The FFT is large; the
// peak table lists the square's peaks in frequency order with level (dBV),
// level relative to the largest and harmonic number (H1, H3 … H9; H11 and up
// are past H10); the readout gives the THD of H2..H10 (≈ 42.9 %); the small
// time trace draws; a row click picks a peak; the peaks channel and count are
// settings; SPECTRUM again cycles FFT → spectrogram → Bode → FFT.
// SKIP/exit 0 without a browser.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2], { viewport: { width: 1600, height: 1000 } });
  t.browser = browser;
  // TRLC-LINKS: REQ-SDS-216
  const table = () => page.evaluate(() => [...document.querySelectorAll("#fftBody tr.pk")].map((r) => ({
    f: +r.dataset.freq, cells: [...r.querySelectorAll("td")].map((c) => c.textContent),
  })));
  // TRLC-LINKS: REQ-SDS-216
  const readout = () => page.evaluate(() => Object.fromEntries([...document.querySelectorAll("#roBody tr")].map((r) => [r.querySelector("th").textContent, r.querySelector("td").textContent])));

  await page.click("#tab-spectrum");
  t.ok(await page.evaluate(() => view.mode) === "FFT", "SPECTRUM opens on the FFT");
  const sc = await page.evaluate(() => { const r = document.getElementById("scope").getBoundingClientRect(); return { w: r.width, h: r.height }; });
  t.ok(sc.w > 900 && sc.h > 450, `the FFT is large (${Math.round(sc.w)}×${Math.round(sc.h)})`);
  await t.until(async () => (await table()).length === 8, "the peak table lists the 8 strongest peaks", 6000);
  const T = await table();
  t.ok(T.every((r, i) => i === 0 || r.f > T[i - 1].f), "rows are in frequency order");
  const harm = T.map((r) => r.cells[4]).join(",");
  t.ok(harm === "H1,H3,H5,H7,H9,,,", "Harm names the odd harmonics up to H9 (H11+ are past H10): " + harm);
  const f0 = T[0].f;
  t.ok(T.slice(0, 5).every((r, i) => Math.abs(r.f - (2 * i + 1) * f0) < f0 * 0.01), "the harmonics sit on 1, 3, 5, 7, 9 × the fundamental");
  t.ok(T[0].cells[3] === "0.0 dB", "the fundamental is the largest (Rel 0.0 dB)");
  t.near(parseFloat(T[1].cells[3]), 20 * Math.log10(1 / 3), 0.3, "H3 is a third of H1 (Rel −9.5 dB)");
  // level: (4/π)·60 codes × 1/25 V per code, rms = 6.7 dBV
  t.near(parseFloat(T[0].cells[2]), 20 * Math.log10(4 / Math.PI * 60 / 25 / Math.SQRT2), 0.3, "the fundamental's level in dBV");
  const ro = await readout();
  t.ok(/^CH1/.test(ro.Source || ""), "the readout names the source channel");
  t.near(parseFloat(ro.THD), 42.88, 1.0, "THD of H2..H10 of the square (≈ 42.9 %)");
  t.ok(/4 harmonics/.test(ro.THD), "…counting the four listed harmonics: " + ro.THD);
  t.ok(/Hz/.test(ro["Largest peak"] || "") && /dBV/.test(ro["Largest peak"] || ""), "the readout gives the largest peak: " + ro["Largest peak"]);

  // the small time trace draws both channels
  const lit = await page.evaluate(() => {
    drawMiniTrace();
    const cv = document.getElementById("tracecv"), gl = cv._glbox && cv._glbox.R.gl;
    if (!gl) return -1;
    const px = new Uint8Array(cv.width * cv.height * 4);
    gl.readPixels(0, 0, cv.width, cv.height, gl.RGBA, gl.UNSIGNED_BYTE, px);
    let k = 0;
    for (let i = 0; i < px.length; i += 4) if (px[i] + px[i + 1] + px[i + 2] > 150) k++;
    return k;
  });
  t.ok(lit > 300, "the small time trace is drawn: " + lit + " lit pixels");

  // pick a peak; the picked line reads it; Clear
  await page.click("#fftBody tr.pk:nth-child(2)");
  await t.until(() => page.evaluate(() => document.querySelector("#fftBody tr.pk:nth-child(2)").classList.contains("is-sel")), "a row click picks that peak", 2000);
  t.ok(/dBc/.test(await po.text("fftSel")), "the picked peak's line reads its level: " + await po.text("fftSel"));
  await po.click("fftClear");
  t.ok(!(await po.text("fftSel")), "Clear drops the picks");

  // settings: how many peaks, which channel
  await po.eval(() => { const e = document.getElementById("fftN"); e.value = "3"; e.dispatchEvent(new Event("change")); });
  await t.until(async () => (await table()).length === 3, "top: 3 lists three peaks", 3000);
  await t.until(async () => /2 harmonics of the 3 /.test((await readout()).THD || ""), "…and the THD counts what is listed", 3000);
  await po.eval(() => { const e = document.getElementById("fftN"); e.value = "8"; e.dispatchEvent(new Event("change")); });
  await po.seg("pkCh", "2");
  // C2: a 30-code sine of 6.006 cycles in the record, 409.6 kSa/s: 1.20 kHz
  await t.until(async () => { const tb = await table(); const top = tb.find((r) => r.cells[3] === "0.0 dB"); return top && Math.abs(top.f - 1201) < 200; },
    "the channel choice lists C2's peaks: its sine at 1.2 kHz is the largest", 3000);
  await t.until(async () => /^CH2/.test((await readout()).Source || ""), "…and the readout follows it", 3000);
  await po.seg("pkCh", "1");

  // SPECTRUM again: spectrogram → Bode → FFT
  await page.evaluate(() => document.activeElement.blur());
  await page.keyboard.press("Alt+3");
  t.ok(await page.evaluate(() => view.mode) === "SPG" && await po.hasClass("spgArm", "on"), "SPECTRUM again: the spectrogram, running");
  await t.until(async () => /Waterfall/.test(Object.keys(await readout()).join()), "…its rows counted in the readout", 4000);
  await page.keyboard.press("Alt+3");
  t.ok(await page.evaluate(() => view.mode) === "BODE" && !(await po.hasClass("spgArm", "on")), "SPECTRUM again: Bode (the spectrogram stops)");
  t.ok(await po.isVisible("bodeTable") && !(await po.isVisible("peakTable")), "…the peak pane lists Bode points instead of peaks");
  await page.keyboard.press("Alt+3");
  t.ok(await page.evaluate(() => view.mode) === "FFT" && await po.isVisible("peakTable"), "SPECTRUM again: back to the FFT and its peaks");
  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
