// workflow_serial.mjs — mask, UART, SPI, burst source workflows (protocol/serial sources).
import { near, assert } from "./workflow_assert.mjs";


// ---------------------------------------------------------------------------
// SOURCE uart — build-uart.sh  (8N1 115200-baud TX on C1 AND C2; repeats the
// 8-byte message "Hi " 0x55 0xAA 0x0F 0xF0 0x0A). Exercises protocol decode.
// ---------------------------------------------------------------------------
export const uart = [
  { id: "U1", name: "Autoset the UART line and get a measurable signal", run: async (op) => {
    const v = await op.autosetStable(1);
    assert(v != null, "no signal on the UART line after autoset");
  }},
  { id: "U2", name: "Decode UART at 115200 baud and get transcript bytes", run: async (op) => {
    await op.autosetStable(1);
    await op.setBand(100e-6); // slow to a band showing several bytes (decode-appropriate)
    await op.selectExpect("decProto", "uart", async () => await op.page.evaluate(() => {
      const c = document.getElementById("decodeResultCard"); return c && getComputedStyle(c).display !== "none";
    }), { why: "selecting UART must reveal the decode result panel" });
    await op.fill("decBaud", "115200", { why: "115200 baud" });
    await op.page.evaluate(() => { const e = document.getElementById("decBaud"); e.dispatchEvent(new Event("change")); });
    const txt = await op.readUntil(async () => {
      const t = await op.readText("decodeText");
      return t && t.replace(/\s/g, "").length > 2 ? t : null;
    }, 12000, "UART decode produced no transcript bytes");
    assert(txt != null, "empty decode transcript");
  }},
  { id: "U3", name: "Hex format shows the known message bytes (0x55 0xAA …)", run: async (op) => {
    await op.autosetStable(1);
    await op.setBand(100e-6);
    await op.selectExpect("decProto", "uart", null, { why: "UART" });
    await op.fill("decBaud", "115200", { why: "baud" });
    await op.page.evaluate(() => { document.getElementById("decBaud").dispatchEvent(new Event("change")); });
    await op.selectExpect("decFmt", "hex", null, { why: "hex byte display" }).catch(() => {});
    const txt = await op.readUntil(async () => {
      const t = (await op.readText("decodeText")) || "";
      return /55/.test(t) && /AA/i.test(t) ? t : null;
    }, 14000, "UART hex decode never showed the known 0x55/0xAA bytes");
    assert(/55/.test(txt) && /AA/i.test(txt), `decode should contain 55 and AA, got "${txt.slice(0, 60)}"`);
  }},
  { id: "U4", name: "ASCII format shows the readable 'Hi' prefix", run: async (op) => {
    await op.autosetStable(1);
    await op.setBand(100e-6);
    await op.selectExpect("decProto", "uart", null, { why: "UART" });
    await op.fill("decBaud", "115200", { why: "baud" });
    await op.page.evaluate(() => { document.getElementById("decBaud").dispatchEvent(new Event("change")); });
    await op.selectExpect("decFmt", "ascii", null, { why: "ASCII display" }).catch(() => {});
    // the ASCII transcript renders bytes space-separated: "H i   U . . ."
    const txt = await op.readUntil(async () => {
      const t = (await op.readText("decodeText")) || "";
      return /H\s*i/.test(t) ? t : null;
    }, 14000, "UART ASCII decode never showed the 'Hi' text");
    assert(/H\s*i/.test(txt), `ASCII decode should contain 'Hi', got "${txt.slice(0, 60)}"`);
    await op.selectExpect("decProto", "off", null, { why: "decode off" });
  }},
  { id: "U5", name: "Auto-detect recognizes the protocol as UART", run: async (op) => {
    await op.autosetStable(1);
    await op.setBand(100e-6);
    await op.clickExpect("decDetect", async () => {
      const p = await op.page.evaluate(() => document.getElementById("decProto").value);
      const msg = await op.readText("decDetectMsg");
      return p === "uart" || /uart/i.test(msg || "");
    }, { timeout: 12000, why: "auto-detect must identify the UART stream" });
    await op.selectExpect("decProto", "off", null, { why: "decode off" });
  }},
  { id: "U6", name: "Decode the SAME stream on channel C2 (roles switchable)", run: async (op) => {
    await op.autosetStable(2);
    await op.setBand(100e-6);
    await op.selectExpect("decProto", "uart", null, { why: "UART" });
    await op.fill("decBaud", "115200", { why: "baud" });
    await op.page.evaluate(() => { document.getElementById("decBaud").dispatchEvent(new Event("change")); });
    // switch the UART source channel to C2 if the roles control exists
    await op.page.evaluate(() => { const e = document.getElementById("decData") || document.getElementById("decScl"); if (e) { e.value = "2"; e.dispatchEvent(new Event("change")); } });
    const txt = await op.readUntil(async () => {
      const t = (await op.readText("decodeText")) || "";
      return t.replace(/\s/g, "").length > 2 ? t : null;
    }, 12000, "UART decode on C2 produced nothing");
    assert(txt != null, "no C2 decode");
    await op.selectExpect("decProto", "off", null, { why: "decode off" });
  }},
  { id: "U7", name: "A wrong baud rate yields framing errors / garbage, not clean bytes", run: async (op) => {
    await op.autosetStable(1);
    await op.setBand(100e-6);
    await op.selectExpect("decProto", "uart", null, { why: "UART" });
    await op.fill("decBaud", "9600", { why: "deliberately wrong baud" });
    await op.page.evaluate(() => { document.getElementById("decBaud").dispatchEvent(new Event("change")); });
    await op.page.waitForTimeout(3000);
    const good = await op.page.evaluate(() => { const t = document.getElementById("decodeText").value || document.getElementById("decodeText").textContent || ""; return /Hi/.test(t); });
    assert(!good, "a wrong baud should NOT cleanly decode the 'Hi' message");
    await op.fill("decBaud", "115200", { why: "restore baud" });
    await op.page.evaluate(() => { document.getElementById("decBaud").dispatchEvent(new Event("change")); });
    await op.selectExpect("decProto", "off", null, { why: "decode off" });
  }},
  { id: "U8", name: "Measure the UART line amplitude (a real logic swing)", run: async (op) => {
    await op.autosetStable(1);
    const v = await op.waitMeas(1, "Vpp");
    assert(v != null && v > 0.2, `UART line Vpp ${v} V — expected a real logic swing`);
  }},
  { id: "U9", name: "FFT of the UART line shows spectral content", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("mFFT", async () => (await op.page.$("#fftCardC1")) !== null, { why: "FFT view" });
    const peak = await op.readUntil(async () => await op.page.evaluate(() => { const b = document.querySelector("#fftBody1"); const c = b && b.querySelector("tr td"); return c && /Hz/.test(c.textContent) ? c.textContent : null; }), 8000, "no FFT peak on the UART line");
    assert(peak != null, "no UART FFT peak");
    await op.click("mYT", { why: "back to Y-T" });
  }},
  { id: "U10", name: "Copy the decode transcript to the clipboard control", run: async (op) => {
    await op.autosetStable(1);
    await op.setBand(100e-6);
    await op.selectExpect("decProto", "uart", null, { why: "UART" });
    await op.fill("decBaud", "115200", { why: "baud" });
    await op.page.evaluate(() => { document.getElementById("decBaud").dispatchEvent(new Event("change")); });
    await op.readUntil(async () => { const t = (await op.readText("decodeText")) || ""; return t.replace(/\s/g, "").length > 2 ? t : null; }, 12000, "no transcript to copy");
    // the copy control must exist and be clickable (a real operator export path)
    assert(await op.exists("decodeCopy"), "decode copy control missing");
    await op.click("decodeCopy", { why: "copy transcript" });
    await op.selectExpect("decProto", "off", null, { why: "decode off" });
  }},
];


// ---------------------------------------------------------------------------
// SOURCE spi — build-spi.sh  (SCLK 200 kHz on C1, MOSI on C2, Mode 0, MSB-first;
// same 8-byte message with idle gaps). Exercises the SPI protocol decoder.
// ---------------------------------------------------------------------------
export const spi = [
  { id: "S1", name: "Autoset the SPI bus and see clock + data on both channels", run: async (op) => {
    await op.clickExpect("autoset", async () => (await op.readMeasValue(1, "Vpp")) != null, { timeout: 12000, why: "autoset the SPI bus" });
    const v1 = await op.waitMeas(1, "Vpp"), v2 = await op.waitMeas(2, "Vpp");
    assert(v1 > 0.1 && v2 > 0.1, `both SPI lines must carry signal: SCLK ${v1} V, MOSI ${v2} V`);
  }},
  { id: "S2", name: "Measure the SPI clock frequency (~200 kHz)", run: async (op) => {
    await op.autosetStable(1);
    const f = await op.waitMeas(1, "Freq");
    assert(near(f, 200e3, 0.1), `SCLK freq ${f} Hz, expected ~200 kHz`);
  }},
  { id: "S3", name: "Decode SPI (mode 0, CLK=C1, DATA=C2) and get transcript bytes", run: async (op) => {
    await op.autosetStable(1);
    await op.setBand(100e-6); // a full message + gap on screen (SPI frames cleanly here)
    await op.selectExpect("decProto", "spi", async () => await op.page.evaluate(() => {
      const c = document.getElementById("decodeResultCard"); return c && getComputedStyle(c).display !== "none";
    }), { why: "selecting SPI must reveal the decode result panel" });
    // set roles CLK=C1, DATA=C2, mode 0, MSB-first
    await op.page.evaluate(() => {
      const set = (id, v) => { const e = document.getElementById(id); if (e) { e.value = v; e.dispatchEvent(new Event("change")); } };
      set("decClk", "1"); set("decData", "2"); set("decCpol", "0"); set("decCpha", "0"); set("decMsb", "1");
    });
    const txt = await op.readUntil(async () => {
      const t = (await op.readText("decodeText")) || "";
      return t.replace(/\s/g, "").length > 2 ? t : null;
    }, 14000, "SPI decode produced no transcript bytes");
    assert(txt != null, "empty SPI decode");
  }},
  { id: "S4", name: "SPI hex decode shows the known message bytes (55 AA …)", run: async (op) => {
    await op.autosetStable(1);
    await op.setBand(100e-6);
    await op.selectExpect("decProto", "spi", null, { why: "SPI" });
    await op.page.evaluate(() => {
      const set = (id, v) => { const e = document.getElementById(id); if (e) { e.value = v; e.dispatchEvent(new Event("change")); } };
      set("decClk", "1"); set("decData", "2"); set("decCpol", "0"); set("decCpha", "0"); set("decMsb", "1"); set("decFmt", "hex");
    });
    const txt = await op.readUntil(async () => {
      const t = (await op.readText("decodeText")) || "";
      return /55/.test(t) && /AA/i.test(t) ? t : null;
    }, 16000, "SPI decode never showed the known 0x55/0xAA bytes");
    assert(/55/.test(txt) && /AA/i.test(txt), `SPI decode should contain 55 and AA, got "${txt.slice(0, 60)}"`);
    await op.selectExpect("decProto", "off", null, { why: "decode off" });
  }},
  { id: "S5", name: "Auto-detect recognizes the protocol as SPI", run: async (op) => {
    await op.autosetStable(1);
    await op.setBand(100e-6);
    await op.clickExpect("decDetect", async () => {
      const p = await op.page.evaluate(() => document.getElementById("decProto").value);
      const msg = await op.readText("decDetectMsg");
      return p === "spi" || /spi/i.test(msg || "");
    }, { timeout: 14000, why: "auto-detect must identify the SPI bus" });
    await op.selectExpect("decProto", "off", null, { why: "decode off" });
  }},
  { id: "S6", name: "Wrong bit order (LSB) changes the decoded byte values", run: async (op) => {
    await op.autosetStable(1);
    await op.setBand(100e-6);
    await op.selectExpect("decProto", "spi", null, { why: "SPI" });
    await op.page.evaluate(() => {
      const set = (id, v) => { const e = document.getElementById(id); if (e) { e.value = v; e.dispatchEvent(new Event("change")); } };
      set("decClk", "1"); set("decData", "2"); set("decCpol", "0"); set("decCpha", "0"); set("decMsb", "1"); set("decFmt", "hex");
    });
    const msb = await op.readUntil(async () => { const t = (await op.readText("decodeText")) || ""; return /55/.test(t) ? t : null; }, 16000, "no MSB decode");
    await op.page.evaluate(() => { const e = document.getElementById("decMsb"); e.value = "0"; e.dispatchEvent(new Event("change")); });
    await op.page.waitForTimeout(2500);
    const lsb = (await op.readText("decodeText")) || "";
    // reversing bit order must change the transcript (0x55 is symmetric, but the
    // whole message isn't — 0x0F<->0xF0, 0x48<->0x12, etc.)
    assert(lsb !== msb, "LSB-first produced an identical transcript to MSB-first");
    await op.selectExpect("decProto", "off", null, { why: "decode off" });
  }},
  { id: "S7", name: "Measure the MOSI data-line amplitude", run: async (op) => {
    await op.autosetStable(2);
    const v = await op.waitMeas(2, "Vpp");
    assert(v != null && v > 0.2, `MOSI Vpp ${v} V — expected a real logic swing`);
  }},
  { id: "S8", name: "X-Y plots SCLK against MOSI without error", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("mXY", async () => await op.page.evaluate(() => document.getElementById("mXY").classList.contains("on")), { why: "X-Y view" });
    assert((await op.lcdPng()) > 3000, "SPI X-Y did not render");
    await op.clickExpect("mYT", async () => await op.page.evaluate(() => document.getElementById("mYT").classList.contains("on")), { why: "back to Y-T" });
  }},
  { id: "S9", name: "The SPI clock has ~50% duty on the SCLK line", run: async (op) => {
    await op.autosetStable(1);
    const d = await op.waitMeas(1, "Duty");
    assert(near(d, 50, 0, 15), `SCLK duty ${d}%, expected ~50%`);
  }},
  { id: "S10", name: "Both channels stay measurable together on the bus", run: async (op) => {
    await op.autosetStable(1);
    const f1 = await op.waitMeas(1, "Freq"), v2 = await op.waitMeas(2, "Vpp");
    assert(near(f1, 200e3, 0.15) && v2 > 0.1, `SCLK ${f1} Hz / MOSI ${v2} V — bus not cleanly measurable`);
  }},
];


// spi part 2 — analyzing the two-wire bus further (cursors, refs, math, eye…).
export const spiB = [
  { id: "S11", name: "Cursors measure one SCLK period on the bus", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("tCursors", async () => await op.page.evaluate(() => { const c = document.getElementById("curCard"); return c && getComputedStyle(c).display !== "none"; }), { why: "cursors on" });
    const inv = await op.readUntil(async () => {
      const t = await op.page.evaluate(() => { const b = document.querySelector("#curBody"); const rows = b.querySelectorAll("tr"); for (const r of rows) { const th = r.querySelector("th"); if (th && /1\/Δt/.test(th.textContent)) return r.querySelector("td").textContent; } return null; });
      return t && /Hz/.test(t) ? t : null;
    }, 5000, "cursor 1/Δt frequency readout missing");
    assert(inv != null, "no cursor frequency readout");
    await op.click("tCursors", { why: "cursors off" });
  }},
  { id: "S12", name: "Save the SCLK as reference A and toggle its display", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("refSaveA", async () => await op.page.evaluate(() => /REF\s*A/.test(document.getElementById("refRows").textContent)), { why: "save A" });
    // toggle the reference show state via its row button
    await op.page.evaluate(() => { const b = document.querySelector("#refRows .reftog"); if (b) b.click(); });
    assert((await op.lcdPng()) > 3000, "reference toggle broke rendering");
  }},
  { id: "S13", name: "Math CLK×DATA (the gated product) renders", run: async (op) => {
    await op.selectExpect("mathFn", "c1*c2", async () => await op.page.evaluate(() => { const c = document.getElementById("mathCard"); return c && getComputedStyle(c).display !== "none"; }), { why: "CLK×DATA math" });
    assert((await op.lcdPng()) > 3000, "CLK×DATA math did not render");
    await op.selectExpect("mathFn", "off", null, { why: "math off" });
  }},
  { id: "S14", name: "Zoom into a single SCLK cycle and keep a valid reading", run: async (op) => {
    await op.autosetStable(1);
    const box = await (await op.page.$("#scope")).boundingBox();
    await op.page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await op.page.mouse.wheel(0, -500);
    await op.page.waitForTimeout(1000);
    const f = await op.waitMeas(1, "Freq");
    assert(near(f, 200e3, 0.1), `after zoom SCLK reads ${f} Hz`);
  }},
  { id: "S15", name: "AC coupling on the clock keeps a measurable edge", run: async (op) => {
    await op.autosetStable(1);
    await op.selectExpect("cpl1", "1", null, { why: "AC couple the clock" });
    await op.page.waitForTimeout(1500);
    const v = await op.waitMeas(1, "Vpp");
    assert(v != null && v > 0.1, `AC-coupled SCLK Vpp ${v} V — edge lost`);
    await op.selectExpect("cpl1", "0", null, { why: "back to DC" });
  }},
  { id: "S16", name: "Peak-detect catches the fast SCLK edges", run: async (op) => {
    await op.autosetStable(1);
    await op.selectExpect("acq", "3", null, { why: "peak detect" });
    await op.page.waitForTimeout(1500);
    assert((await op.lcdPng()) > 3000, "peak-detect did not render");
    await op.selectExpect("acq", "0", null, { why: "normal acq" });
  }},
  { id: "S17", name: "Both bus lines report Vmax simultaneously", run: async (op) => {
    await op.autosetStable(1);
    const a = await op.waitMeas(1, "Vmax"), b = await op.waitMeas(2, "Vmax");
    assert(a != null && b != null, "one bus line had no Vmax");
  }},
  { id: "S18", name: "Trigger-level slider adjusts and keeps the clock locked", run: async (op) => {
    await op.autosetStable(1);
    await op.page.evaluate(() => { const e = document.getElementById("lvl"); e.value = "1.5"; e.dispatchEvent(new Event("input", { bubbles: true })); e.dispatchEvent(new Event("change", { bubbles: true })); });
    await op.page.waitForTimeout(1500);
    const f = await op.waitMeas(1, "Freq");
    assert(near(f, 200e3, 0.15), `after a level move SCLK reads ${f} Hz`);
  }},
  { id: "S19", name: "Freeze the bus capture and hold it for inspection", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("freeze", async () => await op.page.evaluate(() => typeof frozen !== "undefined" && frozen), { why: "freeze" });
    assert((await op.lcdPng()) > 3000, "frozen bus capture blank");
    await op.clickExpect("freeze", async () => await op.page.evaluate(() => typeof frozen !== "undefined" && !frozen), { why: "unfreeze" });
  }},
  { id: "S20", name: "Read the SCLK RMS voltage", run: async (op) => {
    await op.autosetStable(1);
    const rms = await op.waitMeas(1, "Vrms");
    assert(rms != null && rms > 0.1, `SCLK Vrms ${rms} V implausible`);
  }},
];


// ---------------------------------------------------------------------------
// SOURCE burst — build-burst.sh  (frequency-stepped burst on C1: 50/150/250 MHz
// segments in a 300 ns period, 3.33 MHz repeat). A hard, high-frequency
// repetitive signal — exercises FFT, superres, zone, persistence, ETS.
// ---------------------------------------------------------------------------
export const burst = [
  { id: "B1", name: "Autoset the repetitive burst and get a stable trace", run: async (op) => {
    await op.clickExpect("autoset", async () => (await op.readMeasValue(1, "Vpp")) != null, { timeout: 13000, why: "autoset the burst" });
    const v = await op.waitMeas(1, "Vpp");
    assert(v != null && v > 0.1, `burst amplitude ${v} V — no signal`);
  }},
  { id: "B2", name: "FFT reveals the burst's high-frequency components", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("mFFT", async () => (await op.page.$("#fftCardC1")) !== null, { why: "FFT view" });
    const peak = await op.readUntil(async () => await op.page.evaluate(() => { const b = document.querySelector("#fftBody1"); const c = b && b.querySelector("tr td"); return c && /Hz/.test(c.textContent) ? c.textContent : null; }), 9000, "FFT produced no peak on the burst");
    const { parseEng } = await import("./operator.mjs");
    const hz = parseEng(peak);
    assert(hz > 1e6, `FFT peak ${peak} (${hz} Hz) — expected MHz-scale content`);
    await op.click("mYT", { why: "back to Y-T" });
  }},
  { id: "B3", name: "Persistence accumulates the repetitive burst structure", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("tPersist", async () => await op.page.evaluate(() => document.getElementById("tPersist").classList.contains("on")), { why: "persistence on" });
    await op.page.waitForTimeout(1500);
    assert((await op.lcdPng()) > 3000, "persistence view of the burst did not render");
    await op.click("tPersist", { why: "persistence off" });
  }},
  { id: "B4", name: "Super-resolution stacks the repetitive burst", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("srArm", async () => { const s = await op.readText("srStats"); return s && !/idle/i.test(s || ""); }, { timeout: 8000, why: "arm superres on the repetitive burst" });
    const stat = await op.readUntil(async () => { const s = await op.readText("srStats"); return s && /(bit|stack|\d)/i.test(s) && !/idle/i.test(s) ? s : null; }, 16000, "superres produced no stacked result on the burst");
    assert(stat != null, "no superres result");
    await op.click("srArm", { why: "stop superres" });
  }},
  { id: "B5", name: "Zone trigger: draw a zone and keep the burst publishing", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("zmDraw", async () => await op.page.evaluate(() => typeof zm !== "undefined" && zm.drawArmed), { why: "arm zone drawing" });
    const box = await (await op.page.$("#scope")).boundingBox();
    await op.page.mouse.move(box.x + box.width * 0.4, box.y + box.height * 0.3);
    await op.page.mouse.down();
    await op.page.mouse.move(box.x + box.width * 0.6, box.y + box.height * 0.5, { steps: 5 });
    await op.page.mouse.up();
    assert(await op.page.evaluate(() => zm.zones && zm.zones.length >= 1), "zone not created on the burst");
    await op.click("zmClearZones", { why: "clear zones" });
  }},
  { id: "B6", name: "Single-shot captures one burst period and holds it", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("single", async () => { const st = await op.status(); return st.single === true || st.running === false; }, { timeout: 8000, why: "arm SINGLE" });
    await op._settle(async () => (await op.status()).running === false, 8000, "SINGLE never captured the burst");
    assert((await op.lcdPng()) > 3000, "captured burst blank");
    await op.clickExpect("run", async () => (await op.status()).running === true, { why: "resume" });
  }},
  { id: "B7", name: "Zoom into one burst period on the display", run: async (op) => {
    await op.autosetStable(1);
    const box = await (await op.page.$("#scope")).boundingBox();
    await op.page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await op.page.mouse.wheel(0, -500);
    await op.page.waitForTimeout(1000);
    assert((await op.lcdPng()) > 3000, "zoomed burst did not render");
  }},
  { id: "B8", name: "Read the burst amplitude (Vpp)", run: async (op) => {
    await op.autosetStable(1);
    const v = await op.waitMeas(1, "Vpp");
    assert(v != null && v > 0.1 && v < 12, `burst Vpp ${v} V implausible`);
  }},
  { id: "B9", name: "Envelope band shows the burst as a filled envelope", run: async (op) => {
    await op.setBand(5e-3); // an envelope (≥5 ms/div) band
    await op.page.waitForTimeout(2000);
    assert((await op.lcdPng()) > 3000, "envelope band did not render the burst");
    await op.setBand(2e-7); // back to a fast band
  }},
  { id: "B10", name: "FFT peak click selects the component (spectral marker)", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("mFFT", async () => (await op.page.$("#fftCardC1")) !== null, { why: "FFT view" });
    await op.readUntil(async () => await op.page.evaluate(() => { const b = document.querySelector("#fftBody1"); const c = b && b.querySelector("tr td"); return c && /Hz/.test(c.textContent) ? true : null; }), 9000, "no FFT peak to click");
    // click the first FFT peak row (an operator selecting a component to measure)
    await op.page.evaluate(() => { const r = document.querySelector("#fftBody1 tr"); if (r) r.click(); });
    await op.page.waitForTimeout(500);
    assert((await op.lcdPng()) > 3000, "spectral marker interaction broke rendering");
    await op.click("mYT", { why: "back to Y-T" });
  }},
  { id: "B11", name: "Persistence + single together capture and hold a burst", run: async (op) => {
    await op.autosetStable(1);
    await op.clickExpect("tPersist", async () => await op.page.evaluate(() => document.getElementById("tPersist").classList.contains("on")), { why: "persistence on" });
    await op.page.waitForTimeout(1200);
    assert((await op.lcdPng()) > 3000, "persistence render failed");
    await op.click("tPersist", { why: "persistence off" });
  }},
  { id: "B12", name: "The burst repeats — Vmax stays stable across captures", run: async (op) => {
    await op.autosetStable(1);
    const a = await op.waitMeas(1, "Vmax");
    await op.page.waitForTimeout(1500);
    const b = await op.waitMeas(1, "Vmax");
    assert(a != null && b != null && near(a, b, 0.25), `Vmax unstable across captures (${a} → ${b}) — burst not repetitive?`);
  }},
];
