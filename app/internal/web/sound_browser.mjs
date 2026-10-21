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
  // SOUND_SHOTS=<dir> keeps a picture of each view (for a look, not compared)
  const shots = process.env.SOUND_SHOTS;
  if (shots) await page.screenshot({ path: shots + "/web_sound_spec.png" });
  await page.click('#sndView button[data-value="ab"]');
  await t.until(() => po.eval(() => snd.ab && snd.ab.ok), "A/B reads", 3000);
  if (shots) {
    await page.screenshot({ path: shots + "/web_sound_ab.png" });
    for (const v of ["cycle", "mod"]) { await page.click(`#sndView button[data-value="${v}"]`); await po.wait(1500); await page.screenshot({ path: shots + "/web_sound_" + v + ".png" }); }
  }
  const ab = await po.eval(() => snd.ab);
  t.near(ab.gainDB, -6.02, 0.2, "A/B gain");
  t.near(ab.phaseDeg, -15.87, 1, "A/B phase (0.1 ms at 440.8 Hz)");

  // settings go to the scope, actions too
  await page.fill("#sndA4", "442");
  await page.dispatchEvent("#sndA4", "change");
  await page.click('#sndRead button[data-value="voct"]');
  await page.click("#sndVTake");
  await po.wait(400);

  // takes: STOP shows the take (its trigger at the marker), the cursors read
  // it, the tuner reads it, Listen plays it at half speed; RUN is live again
  await page.click("#sndRun");
  await t.until(() => po.eval(() => snd.takeOn && snd.view === "take"), "STOP shows the take in the Take view", 5000);
  const tk = await po.eval(() => ({ n: snd.take.n, trig: snd.take.trig - snd.take.first, has: snd.take.hasTrig }));
  t.ok(tk.has && Math.abs(tk.n - 3 * 61035) < 2 && Math.abs(tk.trig - 61035) < 2, "the take: 3 s, the trigger a second in: " + JSON.stringify(tk));
  const cv = await page.locator("#sndViewCv").boundingBox();
  await page.mouse.click(cv.x + cv.width * 0.5, cv.y + cv.height * 0.2);
  await page.keyboard.down("Shift");
  await page.mouse.click(cv.x + cv.width * 0.6, cv.y + cv.height * 0.2);
  await page.keyboard.up("Shift");
  await po.wait(600);
  const rows = await po.eval(() => $("sndCtxBody").innerText);
  t.ok(/Δt/.test(rows) && /440\.\d+ Hz A4/.test(rows), "the cursors read time, volts, the pitch and Δt: " + rows.replace(/\s+/g, " ").slice(0, 160));
  await t.until(() => po.eval(() => Math.abs(snd.hz - 440.76) < 0.3), "the tuner reads the take", 3000);
  await page.click('#sndSpeed button[data-value="0.5"]');
  await t.until(() => po.eval(() => snd.play.on && snd.play.pos > 0), "Listen plays the take", 3000);
  const p0 = await po.eval(() => snd.play.pos);
  await po.wait(1000);
  const p1 = await po.eval(() => snd.play.pos);
  t.ok(p1 !== p0, "the take's playback moves: " + p0 + " → " + p1);
  await po.eval(() => sndTakeZoom(snd, 0.1, snd.take.trig - snd.take.first));
  t.ok(await po.eval(() => Math.abs(snd.tview.span - snd.take.n * 0.1) < 2), "the take zooms about the trigger");
  if (shots) { await po.wait(800); await page.screenshot({ path: shots + "/web_sound_take.png" }); }
  await page.click("#sndRun");
  await t.until(() => po.eval(() => !snd.takeOn && snd.view !== "take"), "RUN: live again, the view before the take", 5000);

  // leaving SOUND stops Listen and the stream
  await page.keyboard.press("Alt+Digit1");
  await po.waitFor(() => ws.cur === "scope", null, 3000);
  t.ok(await po.eval(() => !snd.shown && !snd.audio.on), "leaving SOUND stops the stream and Listen");
  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join(" | "));

  // the phone: the Sound tab's tuner, meters and Listen
  const phone = await browser.newPage({ viewport: { width: 412, height: 915 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true });
  const phoneErrors = [];
  phone.on("pageerror", (e) => phoneErrors.push(e.message));
  await phone.goto(process.argv[2] + "/remote", { waitUntil: "domcontentloaded", timeout: 15000 });
  await phone.waitForSelector("#tab-sound", { timeout: 8000 });
  await phone.tap("#tab-sound");
  await t.until(() => phone.evaluate(() => window.RSOUND.state.hz > 0), "the phone's tuner reads a pitch", 6000);
  t.ok((await phone.textContent("#view-sound .snd-note")) === "A4", "the phone shows A4");
  t.ok(/dBu/.test(await phone.textContent("#view-sound .snd-meter")), "the phone's meter reads dBu");
  await phone.tap("#view-sound .snd-listen");
  await t.until(() => phone.evaluate(() => window.RSOUND.client.audio.on && window.RSOUND.client.audio.stat.primed), "the phone plays the stream", 5000);
  await phone.waitForTimeout(2000);
  if (shots) await phone.screenshot({ path: shots + "/phone_sound.png" });
  t.ok((await phone.evaluate(() => window.RSOUND.client.audio.stat.under || 0)) <= 1, "the phone's player runs without underruns");
  await phone.tap("#view-sound .snd-run");
  await t.until(() => phone.evaluate(() => window.RSOUND.client.takeOn), "the phone's STOP shows the take", 5000);
  await t.until(() => phone.evaluate(() => window.RSOUND.client.play.on), "the phone's Listen plays the take", 3000);
  if (shots) await phone.screenshot({ path: shots + "/phone_sound_take.png" });
  await phone.tap("#view-sound .snd-run");
  await t.until(() => phone.evaluate(() => !window.RSOUND.client.takeOn), "the phone's RUN is live again", 5000);
  await phone.tap("#tab-home");
  t.ok(await phone.evaluate(() => !window.RSOUND.client.shown && !window.RSOUND.client.audio.on), "leaving the Sound tab stops the stream");
  t.ok(phoneErrors.length === 0, "no phone page errors: " + phoneErrors.join(" | "));
});
