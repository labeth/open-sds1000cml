// Shared Playwright page-object + harness for the scope web-UI acceptance suite.
// Every `<path>_browser.mjs` driver imports this so a DOM-id or launch change is
// fixed in ONE place (the audit's #1 e2e finding: the drivers duplicated setup).
//
// It self-locates a Playwright install across dev-machine layouts and SKIPs
// (prints "SKIP: ...", the caller exits 0) when the browser is unavailable — so
// `go test ./...` stays green on hosts/CI without node+Playwright.
//
// Usage in a driver:
//   import { openScope, run } from "./scope_po.mjs";
//   run(async (t) => {                      // t: {ok, near, browser?}
//     const { page, po, browser } = await openScope(process.argv[2]);
//     t.browser = browser;
//     await po.setMode("FFT");
//     t.ok(await po.isCardVisible("fftCardC1"), "C1 FFT box visible in FFT mode");
//   });
import { existsSync, readdirSync } from "node:fs";
import { execSync } from "node:child_process";
import path from "node:path";

// ---- Playwright discovery (identical policy to the original drivers) ---------
export function findPlaywright() {
  const cands = [];
  if (process.env.PLAYWRIGHT_DIR) cands.push(path.join(process.env.PLAYWRIGHT_DIR, "playwright/index.js"));
  const home = process.env.HOME || "";
  for (const base of [path.join(home, "ws"), path.join(home, "src"), path.join(home, "projects")]) {
    try { for (const d of readdirSync(base)) cands.push(path.join(base, d, "node_modules/playwright/index.js")); } catch {}
  }
  cands.push(path.join(home, "node_modules/playwright/index.js"));
  try { cands.push(path.join(execSync("npm root -g", { encoding: "utf8" }).trim(), "playwright/index.js")); } catch {}
  return cands.find(existsSync) || null;
}

// ---- run(): the driver skeleton — SKIP/exit-0 policy + ALL PASS protocol -----
// Wraps a driver body so every driver reports identically to its Go wrapper:
// "SKIP: ..."+exit 0 when the browser is absent, "ALL PASS"+exit 0 on success,
// exit 1 (with FAIL lines) otherwise. `t.ok(cond,msg)` / `t.near(a,b,tol,msg)`.
export async function run(body) {
  const URL = process.argv[2];
  if (!URL) { console.log("SKIP: no URL argument"); process.exit(0); }
  const pwPath = findPlaywright();
  if (!pwPath) { console.log("SKIP: playwright not installed"); process.exit(0); }
  if (!process.env.PLAYWRIGHT_BROWSERS_PATH && process.env.HOME)
    process.env.PLAYWRIGHT_BROWSERS_PATH = path.join(process.env.HOME, ".cache/ms-playwright");

  let fails = 0;
  const t = {
    ok: (c, m) => { console.log((c ? "ok  - " : "FAIL- ") + m); if (!c) fails++; },
    near: (a, b, tol, m) => { const c = Math.abs(a - b) <= tol; console.log((c ? "ok  - " : "FAIL- ") + `${m} (got ${a}, want ${b}±${tol})`); if (!c) fails++; },
    // Poll-convergent assertion. The UI mirrors /api/status once a second, so
    // state a control applied optimistically can be reverted for one poll
    // period by a stale in-flight status reply (the same tiny race exists
    // against the real device). Assert by retrying until the state converges
    // instead of racing the poll; the deadline keeps real failures loud.
    until: async (fn, m, ms = 3000) => {
      const end = Date.now() + ms;
      let c = !!(await fn());
      while (!c && Date.now() < end) { await new Promise((r) => setTimeout(r, 50)); c = !!(await fn()); }
      t.ok(c, m);
    },
    browser: null,
  };
  try {
    await body(t);
  } catch (e) {
    console.log("FAIL- driver threw:", e && e.message ? e.message : e);
    fails++;
  } finally {
    if (t.browser) { try { await t.browser.close(); } catch {} }
  }
  console.log(fails ? `\n${fails} FAILED` : "\nALL PASS");
  process.exit(fails ? 1 : 0);
}

// ---- openScope(): launch + page-object over the REAL ui.html -----------------
export async function openScope(url, opts = {}) {
  const pwPath = findPlaywright();
  const { chromium } = (await import(pwPath)).default;
  // The whole UI (scope, nav, analysis cards) is WebGL. Headless Chromium's
  // default GL backend (SwiftShader-GL) drops the context under sustained
  // rendering; --use-gl=egl selects the stable ANGLE-Vulkan backend so the
  // contexts survive and pixel reads work. On a real GPU host it uses that GPU.
  const browser = await chromium.launch({
    headless: true,
    args: ["--no-sandbox", "--use-gl=egl", "--enable-gpu", "--ignore-gpu-blocklist"],
  });
  const page = await browser.newPage({ viewport: opts.viewport || { width: 1400, height: 900 } });
  const pageErrors = [];
  page.on("pageerror", (e) => pageErrors.push(e.message));
  await page.goto(url, { waitUntil: "domcontentloaded", timeout: 15000 });
  // Wait for the first real frame unless the caller wants the bare page.
  if (opts.waitFrame !== false)
    await page.waitForFunction(() => typeof frame !== "undefined" && frame && frame.c1, null, { timeout: 15000 });
  return { browser, page, pageErrors, po: pageObject(page), close: () => browser.close() };
}

// ---- the page-object: intent methods over the UI, no per-driver DOM knowledge -
export function pageObject(page) {
  const $ = (id) => "#" + id;
  const po = {
    page,
    // view modes + toggles
    setMode: (m) => page.click($({ YT: "mYT", XY: "mXY", FFT: "mFFT" }[m] || m)),
    toggle: (id) => page.click($(id)),
    click: (id) => page.click($(id)),
    // form controls
    setSelect: (id, value) => page.selectOption($(id), String(value)),
    fill: (id, value) => page.fill($(id), String(value)),
    // a range/slider: set value + fire input+change so listeners run
    setRange: (id, value) => page.evaluate(([i, v]) => {
      const el = document.getElementById(i); el.value = String(v);
      el.dispatchEvent(new Event("input", { bubbles: true }));
      el.dispatchEvent(new Event("change", { bubbles: true }));
    }, [id, value]),
    // queries
    isCardVisible: (id) => page.evaluate((i) => {
      const el = document.getElementById(i);
      return !!el && getComputedStyle(el).display !== "none";
    }, id),
    text: (id) => page.evaluate((i) => { const el = document.getElementById(i); return el ? el.textContent.trim() : null; }, id),
    value: (id) => page.evaluate((i) => { const el = document.getElementById(i); return el ? el.value : null; }, id),
    statusLine: () => po.text("line"),
    count: (sel) => page.locator(sel).count(),
    eval: (fn, arg) => page.evaluate(fn, arg),
    hasClass: (id, cls) => page.evaluate(([i, c]) => document.getElementById(i)?.classList.contains(c), [id, cls]),
    wait: (ms) => page.waitForTimeout(ms),
    waitFor: (fn, arg, timeout = 8000) => page.waitForFunction(fn, arg, { timeout }),
    screenshot: (p) => page.screenshot({ path: p }),
  };
  return po;
}
