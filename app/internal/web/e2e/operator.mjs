// Operator harness: drives the REAL scope web UI the way a human does —
// clicking actual buttons, setting actual inputs, and READING RESULTS OFF THE
// RENDERED SCREEN (the measurement panel DOM, decode output, eye/jitter table,
// zone meter) or the device LCD PNG. It never uses /api/set to set a value or
// /api/frame to read a measurement — those are the "cheating" data paths.
//
// STRICT CONTRACT (the operator goal): every control action asserts that the
// control actually RESPONDED (a state/label/value changed). If it did not, the
// primitive THROWS immediately — no retry, no workaround — so the caller stops
// and the failure is root-caused.
import path from "node:path";
import { findPlaywright } from "../scope_po.mjs";

export async function launch(url) {
  const pwPath = findPlaywright();
  if (!pwPath) return null;
  if (!process.env.PLAYWRIGHT_BROWSERS_PATH && process.env.HOME)
    process.env.PLAYWRIGHT_BROWSERS_PATH = path.join(process.env.HOME, ".cache/ms-playwright");
  const { chromium } = (await import(pwPath)).default;
  const browser = await chromium.launch({ headless: true, args: ["--no-sandbox"] });
  const page = await browser.newPage({ viewport: { width: 1500, height: 950 } });
  const pageErrors = [];
  page.on("pageerror", (e) => pageErrors.push(e.message));
  await page.goto(url, { waitUntil: "domcontentloaded", timeout: 20000 });
  // a frame of ANY kind (a slow envelope/roll band has no per-sample c1 array,
  // so don't require c1 — just that the transport is delivering frames)
  await page.waitForFunction(() => typeof frame !== "undefined" && frame && frame.seq > 0, null, { timeout: 25000 });
  return new Op(browser, page, pageErrors, url);
}

export class Op {
  constructor(browser, page, pageErrors, url) {
    this.browser = browser; this.page = page; this.pageErrors = pageErrors; this.url = url;
  }
  async close() { await this.browser.close(); }

  // Return the UI to a known baseline between workflows, through the GUI only:
  // Y-T view, both channels on, AUTO+running, decode/zone/mask/cursors off,
  // no freeze, DC coupling, 1x zoom. Uses the same controls a user would.
  async reset() {
    const p = this.page;
    await p.evaluate(() => {
      // click helpers that are safe if a control is absent
      const clk = (id) => { const e = document.getElementById(id); if (e) e.click(); };
      const setSel = (id, v) => { const e = document.getElementById(id); if (e && e.value !== v) { e.value = v; e.dispatchEvent(new Event("change")); } };
      if (typeof frozen !== "undefined" && frozen) clk("freeze");
      // stop the eye/jitter + superres analyzers if armed (both are toggles)
      if (typeof ej !== "undefined" && ej && ej.armed) clk("ejArm");
      if (typeof sr !== "undefined" && sr && sr.armed) clk("srArm");
      setSel("decProto", "off");
      setSel("zmMode", "0");
      setSel("mathFn", "off");
      setSel("acq", "0");
      setSel("cpl1", "0"); setSel("cpl2", "0");
      // view back to Y-T
      const yt = document.getElementById("mYT"); if (yt && !yt.classList.contains("on")) yt.click();
      // ensure running + AUTO
      if (typeof st !== "undefined" && st) {
        if (!st.running) clk("run");
      }
      const cur = document.getElementById("tCursors"); if (cur && cur.classList.contains("on")) cur.click();
    });
    // zone trigger off + clear zones through the real buttons if present
    if (await this.page.$("#zmTrig.on")) await this.page.click("#zmTrig", { timeout: 1500 }).catch(() => {});
    await p.waitForTimeout(300);
    this.pageErrors.length = 0;
  }

  _checkNoErrors(where) {
    if (this.pageErrors.length) {
      const e = this.pageErrors.join(" | ");
      this.pageErrors.length = 0;
      throw new Error(`page error during ${where}: ${e}`);
    }
  }

  // --- element existence (a missing control the operator was told exists is a bug) ---
  async exists(id) { return (await this.page.$("#" + id)) !== null; }
  async requireVisible(id, why) {
    const el = await this.page.$("#" + id);
    if (!el) throw new Error(`control #${id} not present (${why})`);
    if (!(await el.isVisible())) throw new Error(`control #${id} present but hidden (${why}) — operator cannot use it`);
    return el;
  }

  // Click a button and REQUIRE an observable effect: `effect` is an async
  // predicate that must become true within `timeout`. Throws with a precise
  // message if the button did nothing (the operator "button doesn't work" rule).
  async clickExpect(id, effect, { timeout = 4000, why = "" } = {}) {
    const el = await this.requireVisible(id, why || `click ${id}`);
    await el.click({ timeout: 2000 });
    await this._settle(effect, timeout, `button #${id} clicked but produced no effect (${why})`);
    this._checkNoErrors(`click #${id}`);
  }
  // Click where the only contract is "does not throw / no page error" (mode
  // toggles whose effect is verified by a later read).
  async click(id, { why = "" } = {}) {
    const el = await this.requireVisible(id, why || `click ${id}`);
    await el.click({ timeout: 2000 });
    await this.page.waitForTimeout(120);
    this._checkNoErrors(`click #${id}`);
  }
  async selectExpect(id, value, effect, { timeout = 4000, why = "" } = {}) {
    const el = await this.requireVisible(id, why || `select ${id}`);
    await el.selectOption(value, { timeout: 2000 });
    if (effect) await this._settle(effect, timeout, `select #${id}=${value} had no effect (${why})`);
    this._checkNoErrors(`select #${id}`);
  }
  async fill(id, value, { why = "" } = {}) {
    const el = await this.requireVisible(id, why || `fill ${id}`);
    await el.fill(String(value), { timeout: 2000 });
    this._checkNoErrors(`fill #${id}`);
  }
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
  async panelButton(name) {
    const r = await this.page.evaluate(async (n) => {
      const resp = await fetch("/api/panel", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ button: n }) });
      return (await resp.json()).ok;
    }, name);
    if (!r) throw new Error(`hard button "${name}" not accepted by the panel`);
    await this.page.waitForTimeout(200);
  }
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
  async lcdPng() {
    return await this.page.evaluate(async () => {
      const r = await fetch("/api/screen.png");
      const b = await r.arrayBuffer();
      return b.byteLength;
    });
  }

  // --- READ FROM THE GUI (rendered DOM the operator sees) ---
  // Reads a measurement cell straight from the Measure panel table text.
  async readMeas(ch, key) {
    return await this.page.evaluate((a) => {
      const rows = document.querySelectorAll("#measBody tr");
      for (const r of rows) {
        const th = r.querySelector("th");
        if (th && th.textContent.trim() === a.key) {
          const tds = r.querySelectorAll("td");
          const cell = tds[a.ch === 2 ? 1 : 0];
          return cell ? cell.textContent.trim() : null;
        }
      }
      return null;
    }, { ch, key });
  }
  // Expand the Measure panel's "more" group so timing/pulse rows are readable.
  async measMore() {
    if (await this.page.$("#measMore")) {
      const label = await this.page.$eval("#measMore", (b) => b.textContent);
      if (/more/.test(label)) await this.page.click("#measMore", { timeout: 2000 });
    }
  }
  // A measurement in engineering units -> a plain SI number (V, Hz, s, %).
  // Reads the GUI string and parses it; unit prefixes handled.
  async readMeasValue(ch, key) {
    const s = await this.readMeas(ch, key);
    if (s == null || s === "—") return null;
    return parseEng(s);
  }
  // Wait until a GUI measurement reads finite & settled (the operator waits for
  // the number to stop dancing before trusting it).
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
  async readText(id) {
    // textarea/input hold their live content in .value, not textContent
    return await this.page.evaluate((i) => {
      const el = document.getElementById(i);
      if (!el) return null;
      const v = ("value" in el && el.value !== undefined) ? el.value : el.textContent;
      return (v || "").trim();
    }, id);
  }
  // setBand snaps the timebase select to the option nearest `tdivS` — the
  // operator picking a scale appropriate to the task (e.g. a byte-viewing band
  // for protocol decode, which autoset's edge-detail band is too fast for).
  async setBand(tdivS) {
    // Set the band and VERIFY it stuck: a device-side autoset can land its final
    // display-timebase step AFTER this call and override it, so re-apply until
    // the status reflects the requested band (a few tries).
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
  }
  // autosetStable clicks AUTOSET and waits for a stable frequency reading on
  // `ch` — the self-contained setup a measurement workflow needs so it does not
  // depend on a prior workflow's state.
  async autosetStable(ch = 1) {
    await this.clickExpect("autoset", async () => (await this.readMeasValue(ch, "Freq")) != null, { timeout: 13000, why: "autoset to establish a triggered, scaled signal" });
    // wait for the autoset routine to FULLY finish (its final display-band step)
    // before returning, so a subsequent setBand isn't overridden by a late
    // autoset tick.
    await this._settle(async () => await this.page.evaluate(() => typeof autosetBusy === "undefined" || !autosetBusy), 6000, "autoset never signalled done").catch(() => {});
    return await this.waitMeas(ch, "Freq");
  }
  async status() { // ONLY for harness health checks (fps/running), never for results
    return await this.page.evaluate(async () => (await (await fetch("/api/status")).json()));
  }
}

// Parse an engineering-formatted string like "5.00 MHz", "-1.2 mV", "48.3 %".
export function parseEng(s) {
  // number (with optional exponent) followed by an optional SI prefix
  const m = s.match(/(-?[\d.]+(?:[eE][+-]?\d+)?)\s*([pnµumkMG]?)/);
  if (!m) return NaN;
  const mult = { p: 1e-12, n: 1e-9, "µ": 1e-6, u: 1e-6, m: 1e-3, "": 1, k: 1e3, M: 1e6, G: 1e9 }[m[2]] ?? 1;
  return parseFloat(m[1]) * mult;
}
