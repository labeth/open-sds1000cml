// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// workflow_analog.mjs — tone + PRBS source workflows (FPGA analog/tone sources).
import { near, assert } from "./workflow_assert.mjs";
import { parseEng } from "./operator.mjs";


// ---------------------------------------------------------------------------
// SOURCE tone1M — build.sh 1  (1 MHz square wave on C1; C2 idle)
// ---------------------------------------------------------------------------
export const tone1M = [
  // TRLC-LINKS: REQ-SDS-208
  { id: "T1", name: "Autoset a 1 MHz square and read its frequency", run: async (op) => {
    await op.clickExpect("autoset", async () => {
      const f = await op.readMeasValue(1, "Freq");
      return f != null && isFinite(f);
    }, { timeout: 12000, why: "AUTOSET must lock the signal and produce a frequency reading" });
    const f = await op.waitMeas(1, "Freq");
    assert(near(f, 1e6, 0.03), `Freq read ${f} Hz, expected ~1 MHz`);
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T2", name: "Read peak-to-peak voltage of the square", run: async (op) => {
    const vpp = await op.waitMeas(1, "Vpp");
    assert(vpp != null && vpp > 0.2 && vpp < 12, `Vpp read ${vpp} V, expected a real square amplitude`);
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T3", name: "Read the period and confirm it is 1/frequency", run: async (op) => {
    await op.measMore();
    const per = await op.waitMeas(1, "Period");
    assert(near(per, 1e-6, 0.03), `Period read ${per} s, expected ~1 µs`);
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T4", name: "Read the duty cycle of the square (≈50%)", run: async (op) => {
    const duty = await op.waitMeas(1, "Duty");
    assert(near(duty, 50, 0, 12), `Duty read ${duty}%, expected ~50%`);
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T5", name: "Confirm the fundamental in SPECTRUM's peak table", run: async (op) => {
    // SPECTRUM's peak table lists C1's peaks; the largest (Rel 0 dB) is the fundamental.
    const peak = await op.topPeak(1);
    const hz = parseEng(peak);
    assert(near(hz, 1e6, 0.05), `FFT fundamental read ${peak} (${hz} Hz), expected ~1 MHz`);
    await op.ws("scope");
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T6", name: "Stop acquisition and confirm the STOPPED indicator", run: async (op) => {
    await op.clickExpect("run", async () => {
      const st = await op.status();
      return st.running === false;
    }, { why: "RUN/STOP button must halt acquisition" });
    // the STOPPED banner in the GUI must be visible
    const vis = await op.page.evaluate(() => {
      const e = document.getElementById("stopped");
      return e && getComputedStyle(e).display !== "none";
    });
    assert(vis, "STOPPED banner not shown after stopping");
    await op.clickExpect("run", async () => (await op.status()).running === true, { why: "RUN must resume" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T7", name: "Single-shot: autoset, arm SINGLE, capture and hold a triggered frame", run: async (op) => {
    // real operator flow: get a stable trigger first, then arm SINGLE and let it
    // capture one triggered frame and STOP on it.
    await op.clickExpect("autoset", async () => (await op.readMeasValue(1, "Freq")) != null, { timeout: 12000, why: "autoset before single" });
    await op.waitMeas(1, "Freq");
    await op.clickExpect("single", async () => {
      const st = await op.status();
      return st.single === true || st.running === false;
    }, { timeout: 8000, why: "SINGLE must arm a one-shot capture" });
    // it must capture a triggered frame and stop by itself
    await op._settle(async () => (await op.status()).running === false, 8000,
      "SINGLE armed but never captured a triggered frame (trigger not on the signal?)");
    await op.clickExpect("run", async () => (await op.status()).running === true, { why: "RUN resumes continuous acquisition after a single capture" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T8", name: "Freeze the display and confirm it holds, then unfreeze", run: async (op) => {
    await op.clickExpect("freeze", async () => await op.page.evaluate(() => typeof frozen !== "undefined" && frozen),
      { why: "Freeze must latch the displayed frame" });
    await op.clickExpect("freeze", async () => await op.page.evaluate(() => typeof frozen !== "undefined" && !frozen),
      { why: "Freeze toggles back to live" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T9", name: "Turn on persistence and confirm the display still renders", run: async (op) => {
    await op.clickExpect("tPersist", async () => await op.page.evaluate(() => {
      const e = document.getElementById("tPersist"); return e && e.classList.contains("on");
    }), { why: "Persistence toggle must engage" });
    const sz = await op.lcdPng();
    assert(sz > 2000, `LCD did not render with persistence on (png ${sz} B)`);
    await op.click("tPersist", { why: "persistence off" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T10", name: "Read the RMS voltage of the square", run: async (op) => {
    const rms = await op.waitMeas(1, "Vrms");
    const vpp = await op.readMeasValue(1, "Vpp");
    // a square's RMS ≈ half its Vpp (amplitude); loose bound
    assert(rms != null && rms > 0.1 && rms < vpp, `Vrms read ${rms} V vs Vpp ${vpp} V — implausible`);
  }},
];


// tone1M part 2 — more single-channel operator tasks on the 1 MHz square.
export const tone1Mb = [
  // TRLC-LINKS: REQ-SDS-208
  { id: "T11", name: "Flip the trigger slope rising↔falling in the strip", run: async (op) => {
    const before = await op.readText("slope");
    const cur = await op.page.evaluate(() => document.getElementById("slope").value);
    const next = cur === "1" ? "0" : "1";
    await op.selectExpect("slope", next, async () => (await op.readText("slope")) !== before, { why: "the slope choice must flip the edge" });
    // past the status echo guard: the strip now shows what the scope reports
    await op.page.waitForTimeout(1800);
    assert((await op.readText("slope")) !== before, `the scope did not keep the ${next === "1" ? "rising" : "falling"} slope`);
    await op.selectExpect("slope", cur, null, { why: "restore slope" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T12", name: "GND coupling flattens C1 to the ground reference", run: async (op) => {
    await op.autosetStable(1);
    await op.selectExpect("cpl1", "2", null, { why: "GND coupling" });
    const vpp = await op.waitMeas(1, "Vpp");
    assert(vpp != null && vpp < 0.15, `GND-coupled Vpp should be ~0, got ${vpp} V`);
    await op.selectExpect("cpl1", "0", null, { why: "back to DC" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T13", name: "×10 probe scales the voltage readout by 10", run: async (op) => {
    await op.autosetStable(1);
    const v1 = await op.waitMeas(1, "Vpp");
    await op.selectExpect("probe1", "10", null, { why: "set a ×10 probe" });
    await op.page.waitForTimeout(1200);
    const v10 = await op.waitMeas(1, "Vpp");
    assert(near(v10, v1 * 10, 0.15), `×10 probe should read ~10× (${v1} → ${v10})`);
    await op.selectExpect("probe1", "1", null, { why: "back to ×1" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T14", name: "Help overlay opens with '?' and closes with Escape", run: async (op) => {
    await op.page.keyboard.press("?");
    const shown = await op._settle(async () => await op.page.evaluate(() => document.getElementById("help").classList.contains("show")), 3000, "help overlay did not open on '?'");
    assert(shown, "help did not open");
    await op.page.keyboard.press("Escape");
    await op._settle(async () => await op.page.evaluate(() => !document.getElementById("help").classList.contains("show")), 3000, "help overlay did not close on Escape");
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T15", name: "Expand the measurement panel to reveal rise/fall time", run: async (op) => {
    await op.autosetStable(1);
    await op.measMore();
    const rise = await op.readMeas(1, "Rise");
    assert(rise != null, "expanded panel did not show a Rise-time row");
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T16", name: "Vmax, Vmin and Vpp are internally consistent", run: async (op) => {
    await op.autosetStable(1);
    const vmax = await op.waitMeas(1, "Vmax"), vmin = await op.waitMeas(1, "Vmin"), vpp = await op.waitMeas(1, "Vpp");
    assert(vmax > vmin, `Vmax ${vmax} must exceed Vmin ${vmin}`);
    assert(near(vpp, vmax - vmin, 0.1), `Vpp ${vpp} should equal Vmax−Vmin (${vmax - vmin})`);
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T17", name: "Math C1+C2 renders without error (C2 idle)", run: async (op) => {
    await op.selectExpect("mathFn", "c1+c2", async () => await op.mathShown(), { why: "math function must engage (COMPARE's Math pane)" });
    const sz = await op.lcdPng();
    assert(sz > 3000, "math trace did not render");
    await op.selectExpect("mathFn", "off", null, { why: "math off" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T18", name: "Superres: stack the repetitive square and get an improved trace", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("srArm", async () => {
      const s = await op.readText("srStats");
      return s && !/idle/i.test(s || "");
    }, { timeout: 8000, why: "ARM must start the super-resolution stacker on a repetitive signal" });
    // it must accumulate stacks and report a result (bits gained / stack count)
    const stat = await op.readUntil(async () => {
      const s = await op.readText("srStats");
      return s && /(?:[1-9]\d* stacked|[1-9]\d* hits)/i.test(s) ? s : null;
    }, 15000, "super-resolution never produced a stacked result");
    assert(stat != null, "no superres result");
    await op.click("srArm", { why: "stop superres" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T19", name: "Wheel-zoom into the trace and keep a valid frequency", run: async (op) => {
    await op.autosetStable(1);
    const box = await (await op.page.$("#scope")).boundingBox();
    await op.page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await op.page.mouse.wheel(0, -500);
    await op.page.waitForTimeout(1000);
    const f = await op.waitMeas(1, "Freq");
    assert(near(f, 1e6, 0.05), `after zoom the frequency reads ${f} Hz`);
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T20", name: "Overshoot/preshoot reads a finite percentage", run: async (op) => {
    await op.autosetStable(1);
    await op.measMore();
    const os = await op.readMeas(1, "Overshoot");
    assert(os != null && os !== "—", "overshoot did not read a value on a square edge");
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T21", name: "Maximize the scope pane and restore it with Esc", run: async (op) => {
    await op.ws("scope");
    const w0 = await op.page.evaluate(() => document.getElementById("scope").width);
    // double-click the pane's title bar: the pane fills the workspace and the
    // trace canvas follows its size
    await op.page.dblclick("#p-scope > .ph .pt");
    await op._settle(async () => await op.page.evaluate((w) => document.getElementById("scope").width > w * 1.1, w0), 3000,
      "maximizing the scope pane did not widen the trace canvas");
    await op.page.keyboard.press("Escape");
    await op._settle(async () => await op.page.evaluate((w) => Math.abs(document.getElementById("scope").width - w) <= 2, w0), 3000,
      "Esc did not restore the scope pane");
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "T22", name: "Trigger level slider moves the level and keeps a lock", run: async (op) => {
    await op.autosetStable(1);
    await op.page.evaluate(() => { const e = document.getElementById("lvl"); e.value = "1"; e.dispatchEvent(new Event("input", { bubbles: true })); e.dispatchEvent(new Event("change", { bubbles: true })); });
    await op.page.waitForTimeout(1500);
    const f = await op.waitMeas(1, "Freq");
    assert(near(f, 1e6, 0.06), `after a trigger-level move the scope still reads ${f} Hz`);
  }},
  // TRLC-LINKS: REQ-SDS-208, REQ-SDS-216
  { id: "T23", name: "SPECTRUM names the square's odd harmonics and reads its THD", run: async (op) => {
    await op.autosetStable(1);
    await op.topPeak(1);
    // the peak table's Harm column names the multiples of the largest peak
    const harms = await op.readUntil(async () => await op.page.evaluate(() => {
      const h = [...document.querySelectorAll("#fftBody tr.pk .ph")].map((c) => c.textContent).filter(Boolean);
      return h.includes("H3") ? h : null;
    }), 8000, "the peak table never marked the 3rd harmonic");
    assert(harms.includes("H1") && !harms.includes("H2"), `a square has odd harmonics only, the table reads ${harms.join(" ")}`);
    const thd = await op.readUntil(async () => await op.page.evaluate(() => {
      for (const r of document.querySelectorAll("#roBody tr")) { const th = r.querySelector("th"); if (th && th.textContent === "THD") return r.querySelector("td").textContent; }
      return null;
    }), 6000, "SPECTRUM's readout shows no THD");
    const v = parseFloat(thd);
    // H2..H10 of an ideal square (H3, H5, H7, H9) make 42.9 %; the edge rate takes a little off
    assert(v > 30 && v < 50, `THD of a square reads "${thd}", expected about 40–43 %`);
    await op.ws("scope");
  }},
  // TRLC-LINKS: REQ-SDS-208, REQ-SDS-217
  { id: "T24", name: "MEASURE statistics count captures, keep a tight frequency spread, and reset", run: async (op) => {
    await op.autosetStable(1);
    await op.ws("measure");
    await op.selectExpect("measView", "stats", null, { why: "MEASURE's statistics view" });
    // TRLC-LINKS: REQ-SDS-208
    const row = async () => await op.page.evaluate(() => {
      const r = document.querySelector('#statsBody tr[data-stat="1:Freq"]');
      return r ? { unit: r.querySelector("th .unit").textContent, td: [...r.querySelectorAll("td")].map((c) => c.textContent.trim()) } : null;
    });
    // columns: Now, Mean, Min, Max, Sdev, N
    const a = await op.readUntil(async () => { const r = await row(); return r && +r.td[5] >= 5 ? r : null; }, 12000, "the Freq statistics never counted 5 captures");
    const [mean, min, max] = [1, 2, 3].map((i) => parseEng(a.td[i] + " " + a.unit));
    assert(near(mean, 1e6, 0.03) && min <= mean && mean <= max, `Freq mean ${mean} Hz (min ${min}, max ${max}) — expected ~1 MHz, ordered`);
    assert((max - min) / mean < 0.02, `the frequency spread ${min}…${max} Hz is wide for a crystal source`);
    const n0 = +a.td[5];
    await op.clickExpect("statsReset", async () => { const r = await row(); return r && +r.td[5] < n0; }, { why: "Reset must restart the statistics" });
  }},
  // TRLC-LINKS: REQ-SDS-208, REQ-SDS-218
  { id: "T25", name: "COMPARE: stack a master, build the mask from it and test live captures", run: async (op) => {
    await op.autosetStable(1);
    await op.ws("compare");
    await op.selectExpect("masterN", "64", null, { why: "average 64 captures" });
    await op.clickExpect("masterBtn", async () => /master ×64/.test(await op.readText("refInfoA") || ""),
      { timeout: 90000, why: "Stack master must average 64 captures into REF A" });
    // the acquisition is back to what it was, the mask test counts raw captures
    assert((await op.readText("acq")) === "Normal", `the acquisition should be restored to Normal, reads ${await op.readText("acq")}`);
    assert((await op.readText("zmMode")) === "Count", `the mask test should be counting, reads ${await op.readText("zmMode")}`);
    const meter = await op.readUntil(async () => { const t = await op.readText("zmMeter"); return t && /pass\s+\d/i.test(t) ? t : null; },
      10000, "the mask test never counted a live capture against the master");
    assert(/pass/.test(meter), `mask meter should count passes, got "${meter}"`);
    // the live square sits on the master: nearly every sample within half a division
    const within = await op.readUntil(async () => await op.page.evaluate(() => {
      const r = document.querySelector('#diffBody tr[data-ref="A"]');
      const td = r && r.querySelectorAll("td");
      return td && td.length === 3 ? parseFloat(td[2].textContent) : null;
    }), 6000, "COMPARE's difference pane did not compare the live trace with the master");
    assert(within > 90, `only ${within} % of the live samples are within 0.5 div of the master`);
    await op.selectExpect("zmMode", "0", null, { why: "mask test off" });
  }},
];


// ---------------------------------------------------------------------------
// SOURCE prbs2M — build-prbs.sh 2  (PRBS7 NRZ data on C1, bit clock on C2)
// Two channels: exercises eye/jitter, two-channel measure, math, X-Y, cursors.
// ---------------------------------------------------------------------------
export const prbs2M = [
  // TRLC-LINKS: REQ-SDS-208
  { id: "P1", name: "Autoset a two-channel serial+clock and see both channels", run: async (op) => {
    await op.clickExpect("autoset", async () => (await op.readMeasValue(1, "Vpp")) != null, { timeout: 12000, why: "autoset a two-channel signal" });
    const v1 = await op.waitMeas(1, "Vpp"), v2 = await op.waitMeas(2, "Vpp");
    assert(v1 > 0.1 && v2 > 0.1, `both channels must carry signal: C1 ${v1} V, C2 ${v2} V`);
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P2", name: "Measure the recovered bit-clock frequency on C2", run: async (op) => {
    const f = await op.waitMeas(2, "Freq");
    // clock is one cycle per bit at 2 Mbps → ~2 MHz (allow the /2 convention too)
    assert(f != null && (near(f, 2e6, 0.05) || near(f, 1e6, 0.05)), `C2 clock freq ${f} Hz, expected ~2 MHz (or 1 MHz)`);
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P3", name: "Eye diagram: BUS's eye view locks an eye with height on the data", run: async (op) => {
    await op.eyeOn(1, "analyze the data channel C1");
    // wait for a real eye: the eye/jitter table must list an eye height in codes/mV
    const h = await op.readUntil(async () => { const t = await op.eyeRow("eye height"); return t && /code|mV/.test(t) ? t : null; },
      15000, "eye never locked (no eye-height measured on a serial signal)");
    assert(h != null, "eye analyzer produced no eye height");
    // the crossing reads too
    const x = await op.eyeRow("crossing");
    assert(x && /%/.test(x), `eye crossing should read a percentage, got "${x}"`);
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P4", name: "Jitter: read the TIE rms and pk-pk off the locked eye", run: async (op) => {
    await op.eyeOn(1, "ARM eye/jitter on C1");
    const rms = await op.readUntil(async () => { const t = await op.eyeRow("TIE rms"); return t && /s$/.test(t) ? t : null; },
      15000, "no TIE jitter measurement appeared");
    const pp = await op.eyeRow("TIE pk-pk");
    assert(/s$/.test(rms) && pp && /s$/.test(pp), `TIE rows should read rms and pk-pk times, got "${rms}" / "${pp}"`);
    // TRLC-LINKS: REQ-SDS-208
    const num = (t) => parseEng(t.replace(/^[≤≥]\s*/, ""));
    assert(num(pp) >= num(rms), `TIE pk-pk ${pp} must be at least the rms ${rms}`);
    await op.click("ejReset", { why: "clear the eye after reading" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P5", name: "X-Y mode: plot C1 vs C2 (Lissajous) renders", run: async (op) => {
    await op.clickExpect("mXY", async () => await op.page.evaluate(() => document.getElementById("mXY").classList.contains("on")),
      { why: "X-Y view button must switch the display" });
    const sz = await op.lcdPng();
    assert(sz > 3000, `X-Y did not render (png ${sz} B)`);
    await op.clickExpect("mYT", async () => await op.page.evaluate(() => document.getElementById("mYT").classList.contains("on")), { why: "back to Y-T" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P6", name: "Math: display C1−C2 and confirm the math trace renders", run: async (op) => {
    await op.selectExpect("mathFn", "c1-c2", async () => await op.mathShown(), { why: "selecting a math function must draw the math trace" });
    const scale = await op.readText("mathScale");
    assert(/V\/div/.test(scale), `math scale should read V/div, got "${scale}"`);
    await op.selectExpect("mathFn", "off", null, { why: "math off" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P7", name: "Cursors: enable manual cursors and read a Δ value off the screen", run: async (op) => {
    await op.ws("measure"); // MEASURE's Cursors pane holds the readout
    await op.clickExpect("tCursors", async () => await op.isOn("tCursors"), { why: "Cursors button must engage the cursors" });
    const hasDelta = await op._settle(async () => await op.page.evaluate(() => {
      const b = document.querySelector("#curBody"); return b && /Δt/.test(b.textContent);
    }), 4000, "cursor readout never showed a delta value");
    assert(hasDelta, "no cursor delta");
    await op.click("tCursors", { why: "cursors off" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P8", name: "FFT the clock and confirm its fundamental peak", run: async (op) => {
    const peak = await op.topPeak(2);
    const hz = parseEng(peak);
    assert(hz > 5e5 && hz < 5e6, `FFT clock peak ${peak} (${hz} Hz) out of the expected 1–2 MHz range`);
    await op.ws("scope");
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P9", name: "Save C1 to reference A and confirm it is stored", run: async (op) => {
    await op.clickExpect("refSaveA", async () => /C1/.test(await op.readText("refInfoA") || ""),
      { timeout: 5000, why: "Save A must store the live C1 as reference A (COMPARE's References)" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P10", name: "AC coupling on C1 removes the DC component (mean → ~0)", run: async (op) => {
    await op.selectExpect("cpl1", "1", null, { why: "AC coupling" });
    const mean = await op.waitMeas(1, "Vmean");
    assert(mean != null && Math.abs(mean) < 0.3, `AC-coupled mean should be near 0, got ${mean} V`);
    await op.selectExpect("cpl1", "0", null, { why: "back to DC" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P11", name: "Peak-detect acquisition mode still renders a trace", run: async (op) => {
    await op.selectExpect("acq", "3", null, { why: "peak-detect acquisition" });
    await op.page.waitForTimeout(1500);
    const sz = await op.lcdPng();
    assert(sz > 3000, "peak-detect mode did not render");
    await op.selectExpect("acq", "0", null, { why: "back to normal acq" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P12", name: "Both channels report a plausible amplitude simultaneously", run: async (op) => {
    const v1 = await op.waitMeas(1, "Vpp"), v2 = await op.waitMeas(2, "Vpp");
    assert(v1 > 0.1 && v1 < 12 && v2 > 0.1 && v2 < 12, `two-channel Vpp implausible: C1 ${v1}, C2 ${v2}`);
  }},
];


// prbs2M part 2 — big-view popups, math variants, dual-channel measure, refs.
export const prbs2Mb = [
  // TRLC-LINKS: REQ-SDS-208
  { id: "P13", name: "Enlarge the eye diagram to a full-screen view and close it", run: async (op) => {
    await op.eyeOn(1, "arm eye on C1");
    await op.readUntil(async () => { const s = await op.readText("ejStats"); return s && !/lock|idle/i.test(s) ? s : (/lock/i.test(s || "") ? null : (s || null)); }, 10000, "eye did not progress").catch(() => {});
    await op.clickExpect("ejEye", async () => await op.page.evaluate(() => !document.getElementById("ejBigWrap").classList.contains("hidden")),
      { why: "clicking the eye must open the enlarged view" });
    await op.page.click("#ejBigWrap");
    await op._settle(async () => await op.page.evaluate(() => document.getElementById("ejBigWrap").classList.contains("hidden")), 3000, "enlarged eye did not close");
    await op.click("ejReset", { why: "reset eye" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P14", name: "Math C2−C1 renders the difference trace", run: async (op) => {
    await op.selectExpect("mathFn", "c2-c1", async () => await op.mathShown(), { why: "C2−C1 math" });
    assert((await op.lcdPng()) > 3000, "C2−C1 math did not render");
    await op.selectExpect("mathFn", "off", null, { why: "math off" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P15", name: "Math C1×C2 (mixer product) renders", run: async (op) => {
    await op.selectExpect("mathFn", "c1*c2", async () => await op.mathShown(), { why: "C1×C2 math" });
    assert((await op.lcdPng()) > 3000, "C1×C2 math did not render");
    await op.selectExpect("mathFn", "off", null, { why: "math off" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P16", name: "Save both references A and B and confirm both are stored", run: async (op) => {
    await op.clickExpect("refSaveA", async () => !/empty/.test(await op.readText("refInfoA") || "empty"), { why: "save A" });
    await op.clickExpect("refSaveB", async () => !/empty/.test(await op.readText("refInfoB") || "empty"), { why: "save B" });
    // COMPARE's difference pane reads the live traces against them
    await op.ws("compare");
    const d = await op.readUntil(async () => await op.page.evaluate(() => {
      const r = document.querySelector('#diffBody tr[data-ref="A"]');
      return r && /%/.test(r.textContent) ? r.textContent : null;
    }), 4000, "COMPARE's difference pane did not read the live trace against REF A");
    assert(/REF A vs C1/.test(d), `the difference row should name REF A vs C1, got "${d}"`);
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P17", name: "Both channels report a Vmean, and they differ", run: async (op) => {
    await op.autosetStable(1);
    const m1 = await op.waitMeas(1, "Vmean"), m2 = await op.waitMeas(2, "Vmean");
    assert(m1 != null && m2 != null, "one channel had no Vmean");
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P18", name: "Enlarge the TIE histogram to full screen and close it", run: async (op) => {
    await op.eyeOn(1, "arm eye on C1");
    await op.page.waitForTimeout(3000);
    await op.clickExpect("ejHist", async () => await op.page.evaluate(() => !document.getElementById("ejBigWrap").classList.contains("hidden")),
      { why: "clicking the histogram must open the enlarged view" });
    await op.page.click("#ejBigWrap");
    await op._settle(async () => await op.page.evaluate(() => document.getElementById("ejBigWrap").classList.contains("hidden")), 3000, "enlarged histogram did not close");
    await op.click("ejReset", { why: "reset" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P19", name: "Superres on the clean clock channel yields a stacked trace", run: async (op) => {
    await op.autosetStable(2);
    await op.selectExpect("srCh", "2", null, { why: "stack the C2 clock" }).catch(() => {});
    await op.clickExpect("srArm", async () => { const s = await op.readText("srStats"); return s && !/idle/i.test(s || ""); }, { timeout: 8000, why: "arm superres" });
    const stat = await op.readUntil(async () => { const s = await op.readText("srStats"); return s && /(?:[1-9]\d* stacked|[1-9]\d* hits)/i.test(s) ? s : null; }, 15000, "superres produced no result on the clock");
    assert(stat != null, "no superres result");
    await op.click("srArm", { why: "stop superres" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P20", name: "Cursors read a ΔV (voltage delta) between the two channels", run: async (op) => {
    await op.ws("measure");
    await op.clickExpect("tCursors", async () => await op.isOn("tCursors"), { why: "cursors on" });
    const hasDV = await op._settle(async () => await op.page.evaluate(() => /ΔV/.test(document.querySelector("#curBody").textContent)), 4000, "cursor readout never showed a ΔV");
    assert(hasDV, "no ΔV");
    await op.click("tCursors", { why: "cursors off" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P21", name: "FFT of the PRBS data shows spectral content", run: async (op) => {
    await op.autosetStable(1);
    const peak = await op.topPeak(1);
    assert(peak != null, "no FFT peak on the data channel");
    await op.ws("scope");
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P22", name: "AVERAGE acquisition mode smooths and still measures", run: async (op) => {
    await op.autosetStable(2);
    await op.selectExpect("acq", "1", null, { why: "AVERAGE acquisition" });
    await op.page.waitForTimeout(1800);
    const f = await op.waitMeas(2, "Freq");
    assert(f != null && f > 5e5, `AVERAGE mode clock freq ${f} Hz implausible`);
    await op.selectExpect("acq", "0", null, { why: "back to normal" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P23", name: "ERES acquisition mode renders and measures the clock", run: async (op) => {
    await op.autosetStable(2);
    await op.selectExpect("acq", "2", null, { why: "ERES acquisition" });
    await op.page.waitForTimeout(1800);
    assert((await op.lcdPng()) > 3000, "ERES mode did not render");
    await op.selectExpect("acq", "0", null, { why: "back to normal" });
  }},
  // TRLC-LINKS: REQ-SDS-208
  { id: "P24", name: "Reset the eye analyzer on the clock and keep it running", run: async (op) => {
    await op.eyeOn(2, "eye on the C2 clock");
    await op.page.waitForTimeout(1500);
    await op.click("ejReset", { why: "reset clears the accumulated data" });
    const s = await op.readText("ejStats");
    assert(s != null && s.length > 0, "eye status vanished after reset");
    await op.click("ejArm", { why: "stop" }).catch(() => {});
  }},
];

