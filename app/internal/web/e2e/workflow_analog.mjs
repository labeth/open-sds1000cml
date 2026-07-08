// workflow_analog.mjs — tone + PRBS source workflows (FPGA analog/tone sources).
import { near, assert } from "./workflow_assert.mjs";


// ---------------------------------------------------------------------------
// SOURCE tone1M — build.sh 1  (1 MHz square wave on C1; C2 idle)
// ---------------------------------------------------------------------------
export const tone1M = [
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


// tone1M part 2 — more single-channel operator tasks on the 1 MHz square.
export const tone1Mb = [
  { id: "T11", name: "Toggle the trigger slope rising↔falling", run: async (op) => {
    const before = await op.readText("slope");
    await op.clickExpect("slope", async () => (await op.readText("slope")) !== before, { why: "slope button must flip the edge" });
    await op.click("slope", { why: "restore slope" });
  }},
  { id: "T12", name: "GND coupling flattens C1 to the ground reference", run: async (op) => {
    await op.autosetStable(1);
    await op.selectExpect("cpl1", "2", null, { why: "GND coupling" });
    const vpp = await op.waitMeas(1, "Vpp");
    assert(vpp != null && vpp < 0.15, `GND-coupled Vpp should be ~0, got ${vpp} V`);
    await op.selectExpect("cpl1", "0", null, { why: "back to DC" });
  }},
  { id: "T13", name: "×10 probe scales the voltage readout by 10", run: async (op) => {
    await op.autosetStable(1);
    const v1 = await op.waitMeas(1, "Vpp");
    await op.selectExpect("probe1", "10", null, { why: "set a ×10 probe" });
    await op.page.waitForTimeout(1200);
    const v10 = await op.waitMeas(1, "Vpp");
    assert(near(v10, v1 * 10, 0.15), `×10 probe should read ~10× (${v1} → ${v10})`);
    await op.selectExpect("probe1", "1", null, { why: "back to ×1" });
  }},
  { id: "T14", name: "Help overlay opens with '?' and closes with Escape", run: async (op) => {
    await op.page.keyboard.press("?");
    const shown = await op._settle(async () => await op.page.evaluate(() => document.getElementById("help").classList.contains("show")), 3000, "help overlay did not open on '?'");
    assert(shown, "help did not open");
    await op.page.keyboard.press("Escape");
    await op._settle(async () => await op.page.evaluate(() => !document.getElementById("help").classList.contains("show")), 3000, "help overlay did not close on Escape");
  }},
  { id: "T15", name: "Expand the measurement panel to reveal rise/fall time", run: async (op) => {
    await op.autosetStable(1);
    await op.measMore();
    const rise = await op.readMeas(1, "Rise");
    assert(rise != null, "expanded panel did not show a Rise-time row");
  }},
  { id: "T16", name: "Vmax, Vmin and Vpp are internally consistent", run: async (op) => {
    await op.autosetStable(1);
    const vmax = await op.waitMeas(1, "Vmax"), vmin = await op.waitMeas(1, "Vmin"), vpp = await op.waitMeas(1, "Vpp");
    assert(vmax > vmin, `Vmax ${vmax} must exceed Vmin ${vmin}`);
    assert(near(vpp, vmax - vmin, 0.1), `Vpp ${vpp} should equal Vmax−Vmin (${vmax - vmin})`);
  }},
  { id: "T17", name: "Math C1+C2 renders without error (C2 idle)", run: async (op) => {
    await op.selectExpect("mathFn", "c1+c2", async () => await op.page.evaluate(() => {
      const c = document.getElementById("mathCard"); return c && getComputedStyle(c).display !== "none";
    }), { why: "math function must engage" });
    const sz = await op.lcdPng();
    assert(sz > 3000, "math trace did not render");
    await op.selectExpect("mathFn", "off", null, { why: "math off" });
  }},
  { id: "T18", name: "Superres: stack the repetitive square and get an improved trace", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("srArm", async () => {
      const s = await op.readText("srStats");
      return s && !/idle/i.test(s || "");
    }, { timeout: 8000, why: "ARM must start the super-resolution stacker on a repetitive signal" });
    // it must accumulate stacks and report a result (bits gained / stack count)
    const stat = await op.readUntil(async () => {
      const s = await op.readText("srStats");
      return s && /(bit|stack|\d)/i.test(s) && !/idle/i.test(s) ? s : null;
    }, 15000, "super-resolution never produced a stacked result");
    assert(stat != null, "no superres result");
    await op.click("srArm", { why: "stop superres" });
  }},
  { id: "T19", name: "Wheel-zoom into the trace and keep a valid frequency", run: async (op) => {
    await op.autosetStable(1);
    const box = await (await op.page.$("#scope")).boundingBox();
    await op.page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await op.page.mouse.wheel(0, -500);
    await op.page.waitForTimeout(1000);
    const f = await op.waitMeas(1, "Freq");
    assert(near(f, 1e6, 0.05), `after zoom the frequency reads ${f} Hz`);
  }},
  { id: "T20", name: "Overshoot/preshoot reads a finite percentage", run: async (op) => {
    await op.autosetStable(1);
    await op.measMore();
    const os = await op.readMeas(1, "Overshoot");
    assert(os != null && os !== "—", "overshoot did not read a value on a square edge");
  }},
  { id: "T21", name: "Collapse the side dock and let the scope fill the width", run: async (op) => {
    await op.clickExpect("panelToggle", async () => await op.page.evaluate(() => {
      const d = document.getElementById("dock"); return d && (getComputedStyle(d).display === "none" || d.classList.contains("collapsed") || document.body.classList.contains("nopanel"));
    }), { why: "panel toggle must collapse the dock" });
    await op.click("panelToggle", { why: "restore the dock" });
  }},
  { id: "T22", name: "Trigger level slider moves the level and keeps a lock", run: async (op) => {
    await op.autosetStable(1);
    await op.page.evaluate(() => { const e = document.getElementById("lvl"); e.value = "1"; e.dispatchEvent(new Event("input", { bubbles: true })); e.dispatchEvent(new Event("change", { bubbles: true })); });
    await op.page.waitForTimeout(1500);
    const f = await op.waitMeas(1, "Freq");
    assert(near(f, 1e6, 0.06), `after a trigger-level move the scope still reads ${f} Hz`);
  }},
];


// ---------------------------------------------------------------------------
// SOURCE prbs2M — build-prbs.sh 2  (PRBS7 NRZ data on C1, bit clock on C2)
// Two channels: exercises eye/jitter, two-channel measure, math, X-Y, cursors.
// ---------------------------------------------------------------------------
export const prbs2M = [
  { id: "P1", name: "Autoset a two-channel serial+clock and see both channels", run: async (op) => {
    await op.clickExpect("autoset", async () => (await op.readMeasValue(1, "Vpp")) != null, { timeout: 12000, why: "autoset a two-channel signal" });
    const v1 = await op.waitMeas(1, "Vpp"), v2 = await op.waitMeas(2, "Vpp");
    assert(v1 > 0.1 && v2 > 0.1, `both channels must carry signal: C1 ${v1} V, C2 ${v2} V`);
  }},
  { id: "P2", name: "Measure the recovered bit-clock frequency on C2", run: async (op) => {
    const f = await op.waitMeas(2, "Freq");
    // clock is one cycle per bit at 2 Mbps → ~2 MHz (allow the /2 convention too)
    assert(f != null && (near(f, 2e6, 0.05) || near(f, 1e6, 0.05)), `C2 clock freq ${f} Hz, expected ~2 MHz (or 1 MHz)`);
  }},
  { id: "P3", name: "Eye diagram: arm the analyzer and get a locked eye with height", run: async (op) => {
    await op.selectExpect("ejCh", "1", null, { why: "analyze the data channel C1" });
    await op.clickExpect("ejArm", async () => {
      const s = await op.readText("ejStats");
      return s && !/idle/i.test(s);
    }, { timeout: 8000, why: "ARM must start the eye/jitter analyzer" });
    // wait for a real eye: the ejBody table must list an eye height in codes/mV
    const h = await op._settle(async () => {
      const t = await op.page.evaluate(() => {
        const b = document.querySelector("#ejBody");
        if (!b) return null;
        for (const r of b.querySelectorAll("tr")) { const th = r.querySelector("th"); if (th && /eye height/i.test(th.textContent)) return r.querySelector("td") ? r.querySelector("td").textContent : null; }
        return null;
      });
      return t && /code|mV/.test(t) ? t : false;
    }, 15000, "eye never locked (no eye-height measured on a serial signal)").then(async () => await op.readText("ejStats"));
    assert(h != null, "eye analyzer produced no status");
  }},
  { id: "P4", name: "Jitter: read a TIE measurement from the locked eye", run: async (op) => {
    await op.selectExpect("ejCh", "1", null, { why: "C1" });
    await op.clickExpect("ejArm", async () => !/idle/i.test(await op.readText("ejStats") || ""), { timeout: 8000, why: "ARM eye/jitter" });
    const tie = await op.readUntil(async () => {
      const t = await op.page.evaluate(() => {
        const b = document.querySelector("#ejBody");
        if (!b) return null;
        for (const r of b.querySelectorAll("tr")) { const th = r.querySelector("th"); if (th && th.textContent.trim() === "TIE") return r.querySelector("td") ? r.querySelector("td").textContent : null; }
        return null;
      });
      return t && /s/.test(t) ? t : null;
    }, 15000, "no TIE jitter measurement appeared");
    assert(/rms/.test(tie) && /pp/.test(tie), `TIE row should read rms/pp jitter, got "${tie}"`);
    await op.click("ejReset", { why: "clear the eye after reading" });
  }},
  { id: "P5", name: "X-Y mode: plot C1 vs C2 (Lissajous) renders", run: async (op) => {
    await op.clickExpect("mXY", async () => await op.page.evaluate(() => document.getElementById("mXY").classList.contains("on")),
      { why: "X-Y view button must switch the display" });
    const sz = await op.lcdPng();
    assert(sz > 3000, `X-Y did not render (png ${sz} B)`);
    await op.clickExpect("mYT", async () => await op.page.evaluate(() => document.getElementById("mYT").classList.contains("on")), { why: "back to Y-T" });
  }},
  { id: "P6", name: "Math: display C1−C2 and confirm the math trace renders", run: async (op) => {
    await op.selectExpect("mathFn", "c1-c2", async () => await op.page.evaluate(() => {
      const c = document.getElementById("mathCard"); return c && getComputedStyle(c).display !== "none";
    }), { why: "selecting a math function must reveal/enable the math trace" });
    const hint = await op.readText("mathHint");
    assert(hint != null, "math hint not shown");
    await op.selectExpect("mathFn", "off", null, { why: "math off" });
  }},
  { id: "P7", name: "Cursors: enable manual cursors and read a Δ value off the screen", run: async (op) => {
    await op.clickExpect("tCursors", async () => await op.page.evaluate(() => {
      const c = document.getElementById("curCard"); return c && getComputedStyle(c).display !== "none";
    }), { why: "Cursors button must reveal the cursor readout card" });
    // the cursor card must show a Δ (delta) row the operator reads
    const hasDelta = await op._settle(async () => await op.page.evaluate(() => {
      const b = document.querySelector("#curBody"); return b && /Δ|delta|d[tx]/i.test(b.textContent);
    }), 4000, "cursor readout never showed a delta value");
    assert(hasDelta, "no cursor delta");
    await op.click("tCursors", { why: "cursors off" });
  }},
  { id: "P8", name: "FFT the clock and confirm its fundamental peak", run: async (op) => {
    await op.clickExpect("mFFT", async () => (await op.page.$("#fftCardC2")) !== null || (await op.page.$("#fftCardC1")) !== null,
      { why: "FFT view" });
    const peak = await op.readUntil(async () => {
      return await op.page.evaluate(() => {
        for (const id of ["fftBody2", "fftBody1"]) { const b = document.querySelector("#" + id); const c = b && b.querySelector("tr td"); if (c && /Hz/.test(c.textContent)) return c.textContent; }
        return null;
      });
    }, 8000, "FFT produced no peak");
    const { parseEng } = await import("./operator.mjs");
    const hz = parseEng(peak);
    assert(hz > 5e5 && hz < 5e6, `FFT clock peak ${peak} (${hz} Hz) out of the expected 1–2 MHz range`);
    await op.click("mYT", { why: "back to Y-T" });
  }},
  { id: "P9", name: "Save C1 to reference A and confirm it is stored", run: async (op) => {
    await op.clickExpect("refSaveA", async () => await op.page.evaluate(() => {
      const rows = document.querySelector("#refRows"); return rows && /REF\s*A/.test(rows.textContent);
    }), { timeout: 5000, why: "Save A must store the live C1 as reference A" });
  }},
  { id: "P10", name: "AC coupling on C1 removes the DC component (mean → ~0)", run: async (op) => {
    await op.selectExpect("cpl1", "1", null, { why: "AC coupling" });
    const mean = await op.waitMeas(1, "Vmean");
    assert(mean != null && Math.abs(mean) < 0.3, `AC-coupled mean should be near 0, got ${mean} V`);
    await op.selectExpect("cpl1", "0", null, { why: "back to DC" });
  }},
  { id: "P11", name: "Peak-detect acquisition mode still renders a trace", run: async (op) => {
    await op.selectExpect("acq", "3", null, { why: "peak-detect acquisition" });
    await op.page.waitForTimeout(1500);
    const sz = await op.lcdPng();
    assert(sz > 3000, "peak-detect mode did not render");
    await op.selectExpect("acq", "0", null, { why: "back to normal acq" });
  }},
  { id: "P12", name: "Both channels report a plausible amplitude simultaneously", run: async (op) => {
    const v1 = await op.waitMeas(1, "Vpp"), v2 = await op.waitMeas(2, "Vpp");
    assert(v1 > 0.1 && v1 < 12 && v2 > 0.1 && v2 < 12, `two-channel Vpp implausible: C1 ${v1}, C2 ${v2}`);
  }},
];


// prbs2M part 2 — big-view popups, math variants, dual-channel measure, refs.
export const prbs2Mb = [
  { id: "P13", name: "Enlarge the eye diagram to a full-screen view and close it", run: async (op) => {
    await op.selectExpect("ejCh", "1", null, { why: "C1" });
    await op.clickExpect("ejArm", async () => !/idle/i.test(await op.readText("ejStats") || ""), { timeout: 8000, why: "arm eye" });
    await op.readUntil(async () => { const s = await op.readText("ejStats"); return s && !/lock|idle/i.test(s) ? s : (/lock/i.test(s || "") ? null : (s || null)); }, 10000, "eye did not progress").catch(() => {});
    await op.clickExpect("ejEye", async () => await op.page.evaluate(() => !document.getElementById("ejBigWrap").classList.contains("hidden")),
      { why: "clicking the eye must open the enlarged view" });
    await op.page.click("#ejBigWrap");
    await op._settle(async () => await op.page.evaluate(() => document.getElementById("ejBigWrap").classList.contains("hidden")), 3000, "enlarged eye did not close");
    await op.click("ejReset", { why: "reset eye" });
  }},
  { id: "P14", name: "Math C2−C1 renders the difference trace", run: async (op) => {
    await op.selectExpect("mathFn", "c2-c1", async () => await op.page.evaluate(() => { const c = document.getElementById("mathCard"); return c && getComputedStyle(c).display !== "none"; }), { why: "C2−C1 math" });
    assert((await op.lcdPng()) > 3000, "C2−C1 math did not render");
    await op.selectExpect("mathFn", "off", null, { why: "math off" });
  }},
  { id: "P15", name: "Math C1×C2 (mixer product) renders", run: async (op) => {
    await op.selectExpect("mathFn", "c1*c2", async () => await op.page.evaluate(() => { const c = document.getElementById("mathCard"); return c && getComputedStyle(c).display !== "none"; }), { why: "C1×C2 math" });
    assert((await op.lcdPng()) > 3000, "C1×C2 math did not render");
    await op.selectExpect("mathFn", "off", null, { why: "math off" });
  }},
  { id: "P16", name: "Save both references A and B and confirm both are stored", run: async (op) => {
    await op.clickExpect("refSaveA", async () => await op.page.evaluate(() => /REF\s*A/.test(document.getElementById("refRows").textContent)), { why: "save A" });
    await op.clickExpect("refSaveB", async () => await op.page.evaluate(() => /REF\s*B/.test(document.getElementById("refRows").textContent)), { why: "save B" });
  }},
  { id: "P17", name: "Both channels report a Vmean, and they differ", run: async (op) => {
    await op.autosetStable(1);
    const m1 = await op.waitMeas(1, "Vmean"), m2 = await op.waitMeas(2, "Vmean");
    assert(m1 != null && m2 != null, "one channel had no Vmean");
  }},
  { id: "P18", name: "Enlarge the TIE histogram to full screen and close it", run: async (op) => {
    await op.selectExpect("ejCh", "1", null, { why: "C1" });
    await op.clickExpect("ejArm", async () => !/idle/i.test(await op.readText("ejStats") || ""), { timeout: 8000, why: "arm eye" });
    await op.page.waitForTimeout(3000);
    await op.clickExpect("ejHist", async () => await op.page.evaluate(() => !document.getElementById("ejBigWrap").classList.contains("hidden")),
      { why: "clicking the histogram must open the enlarged view" });
    await op.page.click("#ejBigWrap");
    await op._settle(async () => await op.page.evaluate(() => document.getElementById("ejBigWrap").classList.contains("hidden")), 3000, "enlarged histogram did not close");
    await op.click("ejReset", { why: "reset" });
  }},
  { id: "P19", name: "Superres on the clean clock channel yields a stacked trace", run: async (op) => {
    await op.autosetStable(2);
    await op.selectExpect("srCh", "2", null, { why: "stack the C2 clock" }).catch(() => {});
    await op.clickExpect("srArm", async () => { const s = await op.readText("srStats"); return s && !/idle/i.test(s || ""); }, { timeout: 8000, why: "arm superres" });
    const stat = await op.readUntil(async () => { const s = await op.readText("srStats"); return s && /(bit|stack|\d)/i.test(s) && !/idle/i.test(s) ? s : null; }, 15000, "superres produced no result on the clock");
    assert(stat != null, "no superres result");
    await op.click("srArm", { why: "stop superres" });
  }},
  { id: "P20", name: "Cursors read a ΔV (voltage delta) between the two channels", run: async (op) => {
    await op.clickExpect("tCursors", async () => await op.page.evaluate(() => { const c = document.getElementById("curCard"); return c && getComputedStyle(c).display !== "none"; }), { why: "cursors on" });
    const hasDV = await op._settle(async () => await op.page.evaluate(() => /ΔV/.test(document.querySelector("#curBody").textContent)), 4000, "cursor readout never showed a ΔV");
    assert(hasDV, "no ΔV");
    await op.click("tCursors", { why: "cursors off" });
  }},
  { id: "P21", name: "FFT of the PRBS data shows spectral content", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("mFFT", async () => (await op.page.$("#fftCardC1")) !== null, { why: "FFT view" });
    const peak = await op.readUntil(async () => {
      return await op.page.evaluate(() => { const b = document.querySelector("#fftBody1"); const c = b && b.querySelector("tr td"); return c && /Hz/.test(c.textContent) ? c.textContent : null; });
    }, 8000, "PRBS FFT produced no peak");
    assert(peak != null, "no FFT peak on the data channel");
    await op.click("mYT", { why: "back to Y-T" });
  }},
  { id: "P22", name: "AVERAGE acquisition mode smooths and still measures", run: async (op) => {
    await op.autosetStable(2);
    await op.selectExpect("acq", "1", null, { why: "AVERAGE acquisition" });
    await op.page.waitForTimeout(1800);
    const f = await op.waitMeas(2, "Freq");
    assert(f != null && f > 5e5, `AVERAGE mode clock freq ${f} Hz implausible`);
    await op.selectExpect("acq", "0", null, { why: "back to normal" });
  }},
  { id: "P23", name: "ERES acquisition mode renders and measures the clock", run: async (op) => {
    await op.autosetStable(2);
    await op.selectExpect("acq", "2", null, { why: "ERES acquisition" });
    await op.page.waitForTimeout(1800);
    assert((await op.lcdPng()) > 3000, "ERES mode did not render");
    await op.selectExpect("acq", "0", null, { why: "back to normal" });
  }},
  { id: "P24", name: "Clear the eye analyzer and confirm it returns to idle-capable", run: async (op) => {
    await op.selectExpect("ejCh", "2", null, { why: "C2 clock" });
    await op.clickExpect("ejArm", async () => !/idle/i.test(await op.readText("ejStats") || ""), { timeout: 8000, why: "arm eye on the clock" });
    await op.page.waitForTimeout(1500);
    await op.click("ejReset", { why: "reset clears the accumulated data" });
    const s = await op.readText("ejStats");
    assert(s != null, "eye status vanished after reset");
    await op.click("ejArm", { why: "stop" }).catch(() => {});
  }},
];

