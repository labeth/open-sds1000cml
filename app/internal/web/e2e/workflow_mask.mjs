// workflow_mask.mjs — mask-violation source workflows.
import { near, assert } from "./workflow_assert.mjs";



// ---------------------------------------------------------------------------
// SOURCE maskviol — build-maskviol.sh 400 7 12 100
// Pulse train on C1 (400 µs period, 100 µs high pulse), every 7th period ends
// 12 µs late (a width violation); C2 = violation marker. Exercises pulse-width
// measurement + trigger, mask build/test/catch, and the zone trigger.
// ---------------------------------------------------------------------------
export const maskv = [
  { id: "M1", name: "Autoset the pulse train and read its repetition rate", run: async (op) => {
    await op.clickExpect("autoset", async () => (await op.readMeasValue(1, "Freq")) != null, { timeout: 12000, why: "autoset the pulse train" });
    const f = await op.waitMeas(1, "Freq");
    assert(near(f, 2500, 0.05), `pulse rate ${f} Hz, expected ~2.5 kHz (400 µs period)`);
  }},
  { id: "M2", name: "Measure the positive pulse width (~100 µs)", run: async (op) => {
    await op.autosetStable(1);
    await op.measMore();
    const w = await op.waitMeas(1, "+Width");
    assert(near(w, 100e-6, 0.25), `+Width ${w} s, expected ~100 µs`);
  }},
  { id: "M3", name: "Measure the pulse-train duty cycle (~25%)", run: async (op) => {
    await op.autosetStable(1);
    const d = await op.waitMeas(1, "Duty");
    assert(near(d, 25, 0, 8), `Duty ${d}%, expected ~25%`);
  }},
  { id: "M4", name: "Pulse-width trigger: select PULSE and reveal the qualifier panel", run: async (op) => {
    await op.selectExpect("ttype", "1", async () => await op.page.evaluate(() => {
      const q = document.getElementById("qualrow"); return q && getComputedStyle(q).display !== "none";
    }), { why: "PULSE trigger type must reveal the pulse-qualifier controls" });
    await op.selectExpect("ttype", "0", async () => await op.page.evaluate(() => {
      const q = document.getElementById("qualrow"); return !q || getComputedStyle(q).display === "none";
    }), { why: "EDGE hides the qualifier panel" });
  }},
  { id: "M5", name: "Mask test: build a golden mask from live frames, enable counting", run: async (op) => {
    await op.autosetStable(1);
    await op.fill("zmN", "24", { why: "build from 24 frames" });
    await op.clickExpect("zmBuild", async () => {
      const s = await op.readText("zmStats");
      return s && /mask (built|ready)/i.test(s);
    }, { timeout: 20000, why: "build mask must accumulate frames and install a golden envelope" });
    await op.selectExpect("zmMode", "1", null, { why: "enable mask counting" });
    const meter = await op.readUntil(async () => {
      const t = await op.readText("zmMeter");
      return t && /pass\s+\d/i.test(t) ? t : null;
    }, 8000, "mask meter never counted a tested frame");
    assert(/pass/.test(meter), `mask meter should show a pass count, got "${meter}"`);
  }},
  { id: "M6", name: "Mask stop-on-fail: arm the latch and confirm the tester is live", run: async (op) => {
    // (continues from M5's installed mask) A real operator arms stop-on-fail to
    // freeze on the next anomaly. Confirm the mode engages and the tester keeps
    // counting frames. (Counted-truth CATCH of a known violation — building the
    // golden from a clean source, then testing a violated DUT — is validated by
    // the FPGA counted-truth campaign; this single flash builds from an already
    // -violated source so its envelope learns the violation as normal.)
    await op.selectExpect("zmMode", "2", null, { why: "arm mask stop-on-fail" });
    const meter = await op.readUntil(async () => {
      const t = await op.readText("zmMeter");
      return t && /pass\s+\d/i.test(t) ? t : null;
    }, 8000, "mask stop-on-fail did not keep testing frames");
    assert(/pass/.test(meter), `mask tester should be live under stop-on-fail, meter="${meter}"`);
    await op.selectExpect("zmMode", "0", null, { why: "mask test off" });
    await op.click("zmClearStats", { why: "reset counters" });
  }},
  { id: "M7", name: "Zone trigger: draw a zone on the pulse and keep publishing", run: async (op) => {
    await op.clickExpect("autoset", async () => (await op.readMeasValue(1, "Freq")) != null, { timeout: 12000, why: "trigger the pulse" });
    await op.waitMeas(1, "Freq");
    await op.clickExpect("zmDraw", async () => await op.page.evaluate(() => typeof zm !== "undefined" && zm.drawArmed),
      { why: "draw-zone button must arm rectangle drawing" });
    const box = await (await op.page.$("#scope")).boundingBox();
    await op.page.mouse.move(box.x + box.width * 0.45, box.y + box.height * 0.2);
    await op.page.mouse.down();
    await op.page.mouse.move(box.x + box.width * 0.55, box.y + box.height * 0.4, { steps: 5 });
    await op.page.mouse.up();
    const nz = await op.page.evaluate(() => (typeof zm !== "undefined" && zm.zones) ? zm.zones.length : 0);
    assert(nz >= 1, "dragging on the scope did not create a zone");
    await op.clickExpect("zmTrig", async () => await op.page.evaluate(() => document.getElementById("zmTrig").classList.contains("on")),
      { why: "zone trigger toggle must engage" });
    await op.page.waitForTimeout(2000);
    assert((await op.status()).running, "scope stopped running under the zone trigger");
    await op.click("zmTrig", { why: "zone trigger off" });
    await op.click("zmClearZones", { why: "clear zones" });
  }},
  { id: "M8", name: "Measure the pulse-train period (~400 µs)", run: async (op) => {
    await op.autosetStable(1);
    await op.measMore();
    const per = await op.waitMeas(1, "Period");
    assert(near(per, 400e-6, 0.08), `Period ${per} s, expected ~400 µs`);
  }},
  { id: "M9", name: "Switch the trigger source to C2 and back, still acquiring", run: async (op) => {
    await op.autosetStable(1);
    await op.page.waitForTimeout(700); // let the autoset trigger-source settle before toggling
    await op.clickExpect("source", async () => await op.page.evaluate(() => document.getElementById("source").textContent.includes("C2")),
      { why: "trigger-source button must switch to C2" });
    await op.page.waitForTimeout(1200);
    assert((await op.status()).running, "scope stopped after switching trigger source");
    await op.page.waitForTimeout(700);
    await op.clickExpect("source", async () => await op.page.evaluate(() => document.getElementById("source").textContent.includes("C1")),
      { why: "trigger-source button must switch back to C1" });
  }},
  { id: "M10", name: "Set a trigger holdoff shorter than the period and stay locked", run: async (op) => {
    await op.autosetStable(1);
    await op.fill("holdoff", "0.0002", { why: "200 µs holdoff (< 400 µs period)" });
    await op.page.evaluate(() => { const e = document.getElementById("holdoff"); e.dispatchEvent(new Event("change")); });
    await op.page.waitForTimeout(2000);
    const f = await op.waitMeas(1, "Freq");
    // holdoff < period must not skip periods; allow the every-7th violation jitter
    assert(near(f, 2500, 0.15), `with a sub-period holdoff the rate reads ${f} Hz, expected ~2.5 kHz`);
    await op.fill("holdoff", "0", { why: "clear holdoff" });
    await op.page.evaluate(() => { const e = document.getElementById("holdoff"); e.dispatchEvent(new Event("change")); });
  }},
  { id: "M11", name: "Zoom into the pulse edge and keep a stable render", run: async (op) => {
    await op.clickExpect("autoset", async () => (await op.readMeasValue(1, "Freq")) != null, { timeout: 12000, why: "autoset" });
    await op.waitMeas(1, "Freq");
    const box = await (await op.page.$("#scope")).boundingBox();
    await op.page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await op.page.mouse.wheel(0, -400);
    await op.page.waitForTimeout(800);
    const sz = await op.lcdPng();
    assert(sz > 3000, "zoomed view did not render");
  }},
  { id: "M12", name: "Deeper memory depth still yields a measurable trace", run: async (op) => {
    await op.selectExpect("memdepth", await op.page.evaluate(() => {
      const e = document.getElementById("memdepth");
      return e.options[e.options.length - 1].value;
    }), null, { why: "select the deepest memory" });
    await op.autosetStable(1); // re-establish trigger at the new depth
    const f = await op.waitMeas(1, "Freq");
    assert(near(f, 2500, 0.12), `deep memory rate ${f} Hz`);
    await op.selectExpect("memdepth", await op.page.evaluate(() => document.getElementById("memdepth").options[0].value), null, { why: "back to shallow" });
  }},
];
