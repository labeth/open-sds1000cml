// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e for the SOUND workspace, driven by headless Chromium via
// Playwright. Launched by sound_browser_test.go against an httptest server
// whose /api/sound.bin streams A4 + 3 cents on C1 and its half-amplitude,
// 0.1 ms late copy on C2, marked on C1's rising zero crossings.
// Output protocol: "SKIP: ..."+exit0 when the browser is absent, else "ALL PASS".
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2]);
  t.browser = browser;

  await page.keyboard.press("Alt+Digit6");
  await po.waitFor(() => ws.cur === "sound" && snd.shown, null, 4000);
  t.ok(true, "Alt+6 shows SOUND");
  await t.until(() => po.eval(() => snd.next > 20000), "the stream arrives in the page", 6000);
  await t.until(() => po.eval(() => snd.hz > 0), "the tuner reads a pitch", 4000);
  const tun = await po.eval(() => ({ note: $("sndNote").textContent, cents: sndNote(snd.hz, snd.settings.a4).cents }));
  t.ok(tun.note === "A4", "the note is A4: " + tun.note);
  t.near(tun.cents, 3, 0.3, "three cents sharp");
  const met = await po.eval(() => $("sndMeter1").textContent);
  t.ok(/dBu/.test(met) && /Vpp 4/.test(met), "the C1 meter reads dBu and 4 Vpp: " + met.replace(/\s+/g, " "));
  await t.until(() => po.eval(() => snd.cycles.length > 0 && snd.cycleWhy === ""), "the cycle trace locks on the FPGA's marks", 4000);
  const c0 = await po.eval(() => snd.cycles[snd.cycles.length - 1][0]);
  t.near(c0, 0, 0.1, "the cycle starts on the marked crossing (0 V)");

  // Listen: a click starts the AudioContext and the worklet
  await page.click("#sndListen");
  await t.until(() => po.eval(() => snd.audio.on && snd.audio.stat.primed), "Listen plays: the worklet's buffer primed", 5000);
  await po.wait(3000);
  const st = await po.eval(() => Object.assign({}, snd.audio.stat));
  t.ok((st.under || 0) <= 1, "no underruns while playing: " + st.under);
  t.ok(st.fillMs > 40 && st.fillMs < 400, "the jitter buffer holds near its target: " + Math.round(st.fillMs) + " ms");
  t.ok(/latency/.test(await po.text("sndStatus")), "the status line reports the latency");

  // each view runs and draws
  for (const v of ["env", "mod", "spec", "ab", "cycle"]) {
    await page.click(`#sndView button[data-value="${v}"]`);
    await po.wait(1300);
    t.ok(await po.eval((x) => snd.view === x, v), "view " + v + " shows");
  }
  await page.click('#sndView button[data-value="spec"]');
  await t.until(() => po.eval(() => snd.harm && snd.harm.ok && Math.abs(snd.harm.f0 - 440.76) < 0.5), "the spectrum's H1 is the tone", 3000);
  await page.click('#sndView button[data-value="ab"]');
  await t.until(() => po.eval(() => snd.ab && snd.ab.ok), "A/B reads", 3000);
  const ab = await po.eval(() => snd.ab);
  t.near(ab.gainDB, -6.02, 0.2, "A/B gain");
  t.near(ab.phaseDeg, -15.87, 1, "A/B phase (0.1 ms at 440.8 Hz)");

  // settings go to the scope, actions too
  await page.fill("#sndA4", "442");
  await page.dispatchEvent("#sndA4", "change");
  await page.click('#sndRead button[data-value="voct"]');
  await page.click("#sndVTake");
  await po.wait(400);

  // leaving SOUND stops Listen and the stream
  await page.keyboard.press("Alt+Digit1");
  await po.waitFor(() => ws.cur === "scope", null, 3000);
  t.ok(await po.eval(() => !snd.shown && !snd.audio.on), "leaving SOUND stops the stream and Listen");
  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join(" | "));
});
