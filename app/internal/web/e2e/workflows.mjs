// 100 end-to-end operator workflows. Each mimics a real task: an FPGA source
// drives the scope, the operator drives the GUI / hard buttons, and reads the
// RESULT off the screen. `source` names the FPGA config that must be flashed
// (see e2e/README for the flash command per source).
//
// Assertions are on GUI-READ values (measurement panel, decode text, eye/jitter
// table, zone meter) — the numbers a human would trust — never on API data.

function near(v, target, tolFrac, absTol = 0) {
  return v != null && isFinite(v) && Math.abs(v - target) <= Math.abs(target) * tolFrac + absTol;
}
function assert(cond, msg) { if (!cond) throw new Error(msg); }

// ---------------------------------------------------------------------------
// SOURCE tone1M — build.sh 1  (1 MHz square wave on C1; C2 idle)
// ---------------------------------------------------------------------------
const tone1M = [
  { id: "T1", name: "Autoset a 1 MHz square and read its frequency", run: async (op) => {
    await op.clickExpect("autoset", async () => {
      const f = await op.readMeasValue(1, "Freq");
      return f != null && isFinite(f);
    }, { timeout: 12000, why: "AUTOSET must lock the signal and produce a frequency reading" });
    const f = await op.waitMeas(1, "Freq");
    assert(near(f, 1e6, 0.03), `Freq read ${f} Hz, expected ~1 MHz`);
  }},
  { id: "T2", name: "Read peak-to-peak voltage of the square", run: async (op) => {
    const vpp = await op.waitMeas(1, "Vpp");
    assert(vpp != null && vpp > 0.2 && vpp < 12, `Vpp read ${vpp} V, expected a real square amplitude`);
  }},
  { id: "T3", name: "Read the period and confirm it is 1/frequency", run: async (op) => {
    await op.measMore();
    const per = await op.waitMeas(1, "Period");
    assert(near(per, 1e-6, 0.03), `Period read ${per} s, expected ~1 µs`);
  }},
  { id: "T4", name: "Read the duty cycle of the square (≈50%)", run: async (op) => {
    const duty = await op.waitMeas(1, "Duty");
    assert(near(duty, 50, 0, 12), `Duty read ${duty}%, expected ~50%`);
  }},
  { id: "T5", name: "Confirm the fundamental on the FFT view", run: async (op) => {
    await op.clickExpect("mFFT", async () => (await op.readText("fftBody1")) !== null || (await op.page.$("#fftCardC1")) !== null,
      { why: "FFT view button must switch the display to the spectrum" });
    // FFT peak table on C1 lists the fundamental; read its first peak freq.
    const peak = await op._settle(async () => {
      const t = await op.page.evaluate(() => {
        const b = document.querySelector("#fftBody1");
        if (!b) return null;
        const cell = b.querySelector("tr td");
        return cell ? cell.textContent : null;
      });
      return t && /Hz/.test(t) ? t : false;
    }, 8000, "FFT peak table never populated a peak").then(async () => {
      return await op.page.evaluate(() => {
        const b = document.querySelector("#fftBody1");
        const cell = b && b.querySelector("tr td");
        return cell ? cell.textContent : null;
      });
    });
    const { parseEng } = await import("./operator.mjs");
    const hz = parseEng(peak);
    assert(near(hz, 1e6, 0.05), `FFT fundamental read ${peak} (${hz} Hz), expected ~1 MHz`);
    await op.click("mYT", { why: "return to Y-T" });
  }},
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
  { id: "T8", name: "Freeze the display and confirm it holds, then unfreeze", run: async (op) => {
    await op.clickExpect("freeze", async () => await op.page.evaluate(() => typeof frozen !== "undefined" && frozen),
      { why: "Freeze must latch the displayed frame" });
    await op.clickExpect("freeze", async () => await op.page.evaluate(() => typeof frozen !== "undefined" && !frozen),
      { why: "Freeze toggles back to live" });
  }},
  { id: "T9", name: "Turn on persistence and confirm the display still renders", run: async (op) => {
    await op.clickExpect("tPersist", async () => await op.page.evaluate(() => {
      const e = document.getElementById("tPersist"); return e && e.classList.contains("on");
    }), { why: "Persistence toggle must engage" });
    const sz = await op.lcdPng();
    assert(sz > 2000, `LCD did not render with persistence on (png ${sz} B)`);
    await op.click("tPersist", { why: "persistence off" });
  }},
  { id: "T10", name: "Read the RMS voltage of the square", run: async (op) => {
    const rms = await op.waitMeas(1, "Vrms");
    const vpp = await op.readMeasValue(1, "Vpp");
    // a square's RMS ≈ half its Vpp (amplitude); loose bound
    assert(rms != null && rms > 0.1 && rms < vpp, `Vrms read ${rms} V vs Vpp ${vpp} V — implausible`);
  }},
];

export const WORKFLOWS = [
  ...tone1M.map((w) => ({ ...w, source: "tone1M" })),
];
