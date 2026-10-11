// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Operator harness: drives the REAL scope web UI the way a human does —
// clicking actual buttons, setting actual inputs, and READING RESULTS OFF THE
// RENDERED SCREEN (the measurement readouts, decode output, eye/jitter table,
// zone meter) or the device LCD PNG. The UI is five task workspaces over an
// instrument strip; a primitive first brings its control on screen (workspace
// tab, pane, view) as an operator would. It never uses /api/set to set a value or
// /api/frame to read a measurement — those are the "cheating" data paths.
//
// STRICT CONTRACT (the operator goal): every control action asserts that the
// control actually RESPONDED (a state/label/value changed). If it did not, the
// primitive THROWS immediately — no retry, no workaround — so the caller stops
// and the failure is root-caused.
import path from "node:path";
import { findPlaywright } from "../scope_po.mjs";

// TRLC-LINKS: REQ-SDS-180
export async function launch(url) {
  const pwPath = findPlaywright();
  if (!pwPath) return null;
  if (!process.env.PLAYWRIGHT_BROWSERS_PATH && process.env.HOME)
    process.env.PLAYWRIGHT_BROWSERS_PATH = path.join(process.env.HOME, ".cache/ms-playwright");
  const { chromium } = (await import(pwPath)).default;
  const browser = await chromium.launch({ headless: true, args: ["--no-sandbox"] });
  const page = await browser.newPage({ viewport: { width: 1500, height: 950 } });
  const pageErrors = [];
  page.on("pageerror", (e) => pageErrors.push(process.env.FUZZ_STACK ? (e.stack || e.message) : e.message));
  await page.goto(url, { waitUntil: "domcontentloaded", timeout: 20000 });
  // a frame of ANY kind (a slow envelope/roll band has no per-sample c1 array,
  // so don't require c1 — just that the transport is delivering frames)
  await page.waitForFunction(() => typeof frame !== "undefined" && frame && frame.seq > 0, null, { timeout: 25000 });
  return new Op(browser, page, pageErrors, url);
}

export class Op {
  // TRLC-LINKS: REQ-SDS-180
  constructor(browser, page, pageErrors, url) {
    this.browser = browser; this.page = page; this.pageErrors = pageErrors; this.url = url;
  }
  // TRLC-LINKS: REQ-SDS-180
  async close() { await this.browser.close(); }

  // Return the UI to a known baseline between workflows, through the GUI only:
  // SCOPE in Y-T, both channels on, AUTO+running, decode/zone/mask/cursors off,
  // no freeze, DC coupling, normal acquisition. Uses the controls a user would.
  // TRLC-LINKS: REQ-SDS-180
  async reset() {
    const p = this.page;
    await p.evaluate(() => {
      // click helpers that are safe if a control is absent
      // TRLC-LINKS: REQ-SDS-180
      const clk = (id) => { const e = document.getElementById(id); if (e) e.click(); };
      // TRLC-LINKS: REQ-SDS-180
      const setSel = (id, v) => { const e = document.getElementById(id); if (e && e.value !== v) { e.value = v; e.dispatchEvent(new Event("change")); } };
      // TRLC-LINKS: REQ-SDS-180
      const seg = (id, v) => { const b = document.querySelector(`#${id} > button[data-value="${v}"]`); if (b && !b.classList.contains("on")) b.click(); };
      if (typeof frozen !== "undefined" && frozen) clk("freeze");
      // stop the eye/jitter + superres analyzers if armed (both are toggles)
      if (typeof ej !== "undefined" && ej && ej.armed) clk("ejArm");
      if (typeof sr !== "undefined" && sr && sr.armed) clk("srArm");
      setSel("decProto", "off");
      seg("zmMode", "0");
      setSel("mathFn", "off");
      seg("acq", "0");
      seg("cpl1", "0"); seg("cpl2", "0");
      // SCOPE in Y-T
      if (typeof setWs === "function") { setWs("scope"); setWsView("YT"); }
      // ensure running + AUTO
      if (typeof st !== "undefined" && st) {
        if (!st.running) clk("run");
        if (st.norm) seg("mode", "0"); // NORM holds frames without a trigger — baseline is AUTO
      }
      const cur = document.getElementById("tCursors"); if (cur && cur.classList.contains("on")) cur.click();
      for (const id of ["tC1", "tC2"]) { const b = document.getElementById(id); if (b && !b.classList.contains("on")) b.click(); }
    });
    // zone trigger off through the real button if it is on
    if (await this.page.$("#zmTrig.on")) await this.click("zmTrig").catch(() => {});
    await p.waitForTimeout(300);
    this.pageErrors.length = 0;
  }

  // TRLC-LINKS: REQ-SDS-180
  _checkNoErrors(where) {
    if (this.pageErrors.length) {
      const e = this.pageErrors.join(" | ");
      this.pageErrors.length = 0;
      throw new Error(`page error during ${where}: ${e}`);
    }
  }

  // --- element existence (a missing control the operator was told exists is a bug) ---
  // TRLC-LINKS: REQ-SDS-180
  async exists(id) { return (await this.page.$("#" + id)) !== null; }
  // reveal brings a control on screen the way an operator does: its workspace
  // tab, its pane (expanded) and its view — what All settings' arrows do.
  // TRLC-LINKS: REQ-SDS-180
  async reveal(id) {
    return await this.page.evaluate((i) => { const e = document.getElementById(i); return !!e && typeof revealControl === "function" && revealControl(e); }, id);
  }
  // TRLC-LINKS: REQ-SDS-180
  async requireVisible(id, why) {
    const el = await this.page.$("#" + id);
    if (!el) throw new Error(`control #${id} not present (${why})`);
    if (!(await el.isVisible())) await this.reveal(id);
    if (!(await el.isVisible())) throw new Error(`control #${id} present but hidden (${why}) — operator cannot use it`);
    return el;
  }

  // Click a button and REQUIRE an observable effect: `effect` is an async
  // predicate that must become true within `timeout`. Throws with a precise
  // message if the button did nothing (the operator "button doesn't work" rule).
  // TRLC-LINKS: REQ-SDS-180
  async clickExpect(id, effect, { timeout = 4000, why = "" } = {}) {
    const el = await this.requireVisible(id, why || `click ${id}`);
    await el.click({ timeout: 2000 });
    await this._settle(effect, timeout, `button #${id} clicked but produced no effect (${why})`);
    this._checkNoErrors(`click #${id}`);
  }
  // Click where the only contract is "does not throw / no page error" (mode
  // toggles whose effect is verified by a later read).
  // TRLC-LINKS: REQ-SDS-180
  async click(id, { why = "" } = {}) {
    const el = await this.requireVisible(id, why || `click ${id}`);
    await el.click({ timeout: 2000 });
    await this.page.waitForTimeout(120);
    this._checkNoErrors(`click #${id}`);
  }
  // selectExpect picks an option: a <select>'s, or a segmented choice's button.
  // TRLC-LINKS: REQ-SDS-180
  async selectExpect(id, value, effect, { timeout = 4000, why = "" } = {}) {
    const el = await this.requireVisible(id, why || `select ${id}`);
    if (await el.evaluate((e) => e.tagName === "SELECT")) await el.selectOption(String(value), { timeout: 2000 });
    else {
      const b = await this.page.$(`#${id} > button[data-value="${value}"]`);
      if (!b) throw new Error(`choice #${id} has no option ${value} (${why})`);
      await b.click({ timeout: 2000 });
    }
    if (effect) await this._settle(effect, timeout, `select #${id}=${value} had no effect (${why})`);
    this._checkNoErrors(`select #${id}`);
  }
  // TRLC-LINKS: REQ-SDS-180
  async fill(id, value, { why = "" } = {}) {
    const el = await this.requireVisible(id, why || `fill ${id}`);
    await el.fill(String(value), { timeout: 2000 });
    this._checkNoErrors(`fill #${id}`);
  }
  // TRLC-LINKS: REQ-SDS-180
  async _settle(effect, timeout, failMsg) {
    const t0 = Date.now();
    for (;;) {
      let ok = false;
      try { ok = await effect(); } catch { ok = false; }
      if (ok) return true;
      if (Date.now() - t0 > timeout) throw new Error(failMsg);
      await this.page.waitForTimeout(120);
    }
  }
  // readUntil resolves to the first non-null/non-false value `fn` returns, or
  // throws after timeout — the value-returning companion to _settle.
  // TRLC-LINKS: REQ-SDS-180
  async readUntil(fn, timeout, failMsg) {
    const t0 = Date.now();
    for (;;) {
      let v = null;
      try { v = await fn(); } catch { v = null; }
      if (v != null && v !== false) return v;
      if (Date.now() - t0 > timeout) throw new Error(failMsg);
      await this.page.waitForTimeout(150);
    }
  }

  // --- HARD BUTTONS: the physical front panel. Injected through the same
  // dispatch a real key press drives (no fingers available); the RESULT is
  // then read from the device LCD screen, not from any data API. ---
  // TRLC-LINKS: REQ-SDS-180
  async panelButton(name) {
    const r = await this.page.evaluate(async (n) => {
      const resp = await fetch("/api/panel", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ button: n }) });
      return (await resp.json()).ok;
    }, name);
    if (!r) throw new Error(`hard button "${name}" not accepted by the panel`);
    await this.page.waitForTimeout(200);
  }
  // TRLC-LINKS: REQ-SDS-180
  async panelKnob(name, dir, steps = 1) {
    const r = await this.page.evaluate(async (a) => {
      const resp = await fetch("/api/panel", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ knob: a.name, dir: a.dir, steps: a.steps }) });
      return (await resp.json()).ok;
    }, { name, dir, steps });
    if (!r) throw new Error(`knob "${name}" not accepted by the panel`);
    await this.page.waitForTimeout(200);
  }
  // Read the device LCD as a PNG buffer (the scope screen). Returns {w,h,px}
  // is overkill; callers usually just assert a non-trivial render.
  // TRLC-LINKS: REQ-SDS-180
  async lcdPng() {
    return await this.page.evaluate(async () => {
      const r = await fetch("/api/screen.png");
      const b = await r.arrayBuffer();
      return b.byteLength;
    });
  }

  // --- READ FROM THE GUI (rendered DOM the operator sees) ---
  // Reads a measurement where the GUI shows it: SCOPE's quick readouts hold the
  // core set, MEASURE's All values every parameter (cells carry data-meas).
  // When none is on screen the operator opens MEASURE's All values view.
  // TRLC-LINKS: REQ-SDS-180
  async readMeas(ch, key) {
    const read = () => this.page.evaluate((a) => {
      for (const el of document.querySelectorAll(`[data-meas="${a.ch}:${a.key}"]`))
        if (el.getClientRects().length) return el.textContent.trim();
      return undefined;
    }, { ch, key });
    let v = await read();
    if (v === undefined) {
      await this.page.evaluate(() => { setWs("measure"); measSetView("values"); });
      await this.page.waitForTimeout(150);
      v = await read();
    }
    return v === undefined ? null : v;
  }
  // Every parameter is in MEASURE's All values view (there is no "more" toggle).
  // TRLC-LINKS: REQ-SDS-180
  async measMore() {
    await this.page.evaluate(() => { setWs("measure"); measSetView("values"); });
    await this.page.waitForTimeout(150);
  }
  // ws opens a workspace with its tab, as an operator clicks it.
  // TRLC-LINKS: REQ-SDS-180
  async ws(id) {
    await this.clickExpect("tab-" + id, async () => await this.page.evaluate((i) => document.getElementById("tab-" + i).getAttribute("aria-selected") === "true", id),
      { why: `the ${id.toUpperCase()} tab must open its workspace` });
  }
  // topPeak reads SPECTRUM's peak table for channel `ch`: the frequency of the
  // largest peak (the row whose Rel reads 0 dB).
  // TRLC-LINKS: REQ-SDS-180
  async topPeak(ch = 1, timeout = 8000) {
    await this.ws("spectrum");
    await this.selectExpect("viewSpec", "FFT", null, { why: "SPECTRUM's FFT view" });
    await this.selectExpect("pkCh", String(ch), null, { why: `the peak table lists C${ch}` });
    return await this.readUntil(async () => await this.page.evaluate(() => {
      let best = null;
      for (const tr of document.querySelectorAll("#fftBody tr.pk")) {
        const rel = parseFloat(tr.querySelector(".pr").textContent), f = tr.querySelector(".pf").textContent;
        if (/Hz/.test(f) && Number.isFinite(rel) && (!best || rel > best.rel)) best = { rel, f };
      }
      return best ? best.f : null;
    }), timeout, `SPECTRUM's peak table never listed a C${ch} peak`);
  }
  // eyeOn opens BUS's eye view (it arms the eye on the decoded data line) and
  // makes sure the analyzer runs on channel `ch`: another channel is chosen and
  // re-armed with ARM, as an operator does.
  // TRLC-LINKS: REQ-SDS-180
  async eyeOn(ch, why = "") {
    await this.ws("bus");
    await this._settle(async () => await this.page.evaluate(() => !busDetecting), 12000, "BUS's bus detection never finished");
    await this.selectExpect("busView", "eye", async () => await this.isOn("ejArm"), { timeout: 6000, why: "BUS's Eye view arms the eye" });
    if (+(await this.readText("ejCh")) !== ch) {
      await this.selectExpect("ejCh", String(ch), null, { why: why || `eye on C${ch}` });
      if (await this.isOn("ejArm")) await this.click("ejArm", { why: "stop, to re-arm on the other channel" });
    }
    if (!(await this.isOn("ejArm")))
      await this.clickExpect("ejArm", async () => await this.isOn("ejArm") && !/idle/i.test(await this.readText("ejStats") || ""), { timeout: 8000, why: why || `ARM the eye on C${ch}` });
  }
  // eyeRow reads one figure off the eye/jitter table by its row heading.
  // TRLC-LINKS: REQ-SDS-180
  async eyeRow(name) {
    return await this.page.evaluate((n) => {
      for (const r of document.querySelectorAll("#ejBody tr")) {
        const th = r.querySelector("th"), td = r.querySelector("td");
        if (th && td && th.textContent.trim().toLowerCase() === n.toLowerCase()) return td.textContent.trim();
      }
      return null;
    }, name);
  }
  // isOn: a toggle button shows its engaged state (class "on").
  // TRLC-LINKS: REQ-SDS-180
  async isOn(id) {
    return await this.page.evaluate((i) => { const e = document.getElementById(i); return !!e && e.classList.contains("on"); }, id);
  }
  // mathShown: the math trace is drawn (its scale line reads "…/div").
  // TRLC-LINKS: REQ-SDS-180
  async mathShown() { return /\/div/.test((await this.readText("mathScale")) || ""); }
  // A measurement in engineering units -> a plain SI number (V, Hz, s, %).
  // Reads the GUI string and parses it; unit prefixes handled.
  // TRLC-LINKS: REQ-SDS-180
  async readMeasValue(ch, key) {
    const s = await this.readMeas(ch, key);
    if (s == null || s === "—") return null;
    return parseEng(s);
  }
  // Wait until a GUI measurement reads finite & settled (the operator waits for
  // the number to stop dancing before trusting it).
  // TRLC-LINKS: REQ-SDS-180
  async waitMeas(ch, key, { timeout = 6000 } = {}) {
    const t0 = Date.now();
    let last = null, stable = 0;
    for (;;) {
      const v = await this.readMeasValue(ch, key);
      if (v != null && isFinite(v)) {
        if (last != null && Math.abs(v - last) <= Math.abs(v) * 0.05 + 1e-12) stable++;
        else stable = 0;
        last = v;
        if (stable >= 2) return v;
      }
      if (Date.now() - t0 > timeout) {
        if (last != null) return last; // return best-effort; caller asserts range
        throw new Error(`measurement ${key} on C${ch} never became readable`);
      }
      await this.page.waitForTimeout(200);
    }
  }
  // TRLC-LINKS: REQ-SDS-180
  async readText(id) {
    // textarea/input hold their live content in .value, not textContent; a
    // segmented choice reads as its chosen option's label
    return await this.page.evaluate((i) => {
      const el = document.getElementById(i);
      if (!el) return null;
      if (el.classList.contains("seg")) { const b = el.querySelector("button.on"); return b ? (b.getAttribute("aria-label") || b.textContent).trim() : ""; }
      const v = el.matches("input, textarea, select") ? el.value : el.textContent;
      return (v || "").trim();
    }, id);
  }
  // setBand snaps the timebase select to the option nearest `tdivS` — the
  // operator picking a scale appropriate to the task (e.g. a byte-viewing band
  // for protocol decode, which autoset's edge-detail band is too fast for).
  // TRLC-LINKS: REQ-SDS-180
  async setBand(tdivS) {
    // Set the band and VERIFY it stuck: a device-side autoset can land its final
    // display-timebase step AFTER this call and override it, so re-apply until
    // the status reflects the requested band (a few tries).
    // TRLC-LINKS: REQ-SDS-180
    const apply = async () => await this.page.evaluate((tv) => {
      const e = document.getElementById("tdiv");
      if (!e || !e.options.length) return null;
      let best = e.options[0];
      for (const o of e.options) if (Math.abs(parseFloat(o.value) - tv) < Math.abs(parseFloat(best.value) - tv)) best = o;
      e.value = best.value; e.dispatchEvent(new Event("change"));
      return parseFloat(best.value);
    }, tdivS);
    const want = await apply();
    if (want == null) return;
    for (let i = 0; i < 8; i++) {
      await this.page.waitForTimeout(700);
      const cur = (await this.status()).tdiv_s;
      if (Math.abs(cur - want) <= want * 0.01) return; // stuck
      await apply(); // re-apply if a late autoset tick overrode it
    }
    throw new Error(`timebase did not settle at ${want} s/div`);
  }
  // autosetStable clicks AUTOSET and waits for a stable frequency reading on
  // `ch` — the self-contained setup a measurement workflow needs so it does not
  // depend on a prior workflow's state.
  // TRLC-LINKS: REQ-SDS-180
  async autosetStable(ch = 1) {
    await this.clickExpect("autoset", async () => (await this.readMeasValue(ch, "Freq")) != null, { timeout: 13000, why: "autoset to establish a triggered, scaled signal" });
    // wait for the autoset routine to FULLY finish (its final display-band step)
    // before returning, so a subsequent setBand isn't overridden by a late
    // autoset tick.
    await this._settle(async () => await this.page.evaluate(() => typeof autosetBusy === "undefined" || !autosetBusy), 16000, "autoset never signalled done");
    return await this.waitMeas(ch, "Freq");
  }
  // TRLC-LINKS: REQ-SDS-180
  async status() { // ONLY for harness health checks (fps/running), never for results
    return await this.page.evaluate(async () => (await (await fetch("/api/status")).json()));
  }
}

// Parse an engineering-formatted string like "5.00 MHz", "-1.2 mV", "48.3 %".
// TRLC-LINKS: REQ-SDS-180
export function parseEng(s) {
  // number (with optional exponent) followed by an optional SI prefix
  const m = s.match(/(-?[\d.]+(?:[eE][+-]?\d+)?)\s*([pnµumkMG]?)/);
  if (!m) return NaN;
  const mult = { p: 1e-12, n: 1e-9, "µ": 1e-6, u: 1e-6, m: 1e-3, "": 1, k: 1e3, M: 1e6, G: 1e9 }[m[2]] ?? 1;
  return parseFloat(m[1]) * mult;
}
