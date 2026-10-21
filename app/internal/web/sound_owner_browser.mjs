// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e for who holds the acquisition while SOUND streams,
// launched by sound_owner_test.go against the real panel behind the real
// handlers (/test/leases reads the stream's lease). A key on the scope
// (/api/panel) takes the acquisition back: the page pauses and offers Take
// the scope; the page's other tabs and the phone's other tabs never lease.
// Output protocol: "SKIP: ..."+exit0 when the browser is absent, else "ALL PASS".
import { openScope, run } from "./scope_po.mjs";

const URL = process.argv[2];

// leases reads the stream's lease: running now, and how many were taken.
// TRLC-LINKS: REQ-SDS-277
async function leases() { return (await fetch(URL + "/test/leases")).json(); }

// lcdKey presses a key on the scope, as its own panel does.
// TRLC-LINKS: REQ-SDS-277
async function lcdKey(name) {
  await fetch(URL + "/api/panel", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ button: name }) });
}

// sleep waits ms.
// TRLC-LINKS: REQ-SDS-277
function sleep(ms) { return new Promise((r) => setTimeout(r, ms)); }

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(URL);
  t.browser = browser;

  // the page's other tabs never lease the stream
  const l0 = await leases();
  for (const id of ["scope", "bus", "spectrum", "measure", "compare"]) {
    await po.ws(id);
    await sleep(700);
  }
  const l1 = await leases();
  t.ok(l1.n === l0.n && !l1.on, `the page's other tabs leased nothing (${l0.n} -> ${l1.n})`);

  await page.keyboard.press("Alt+Digit6");
  await po.waitFor(() => ws.cur === "sound" && snd.shown, null, 4000);
  await t.until(() => po.eval(() => snd.next > 20000), "SOUND streams", 6000);
  t.ok((await leases()).on, "the SOUND tab leases the stream");

  await lcdKey("set50");
  await t.until(() => po.eval(() => snd.paused && !$("sndPaused").hidden), "a key on the scope pauses the page", 4000);
  t.ok(/paused: the scope is in use/.test(await po.text("sndStatus")), "the status line says paused: " + (await po.text("sndStatus")));
  t.ok((await po.eval(() => $("sndTakeScope").textContent)) === "Take the scope", "the page offers Take the scope");
  await sleep(1500);
  const lp = await leases();
  await sleep(1500);
  const lq = await leases();
  t.ok(!lq.on && lq.n === lp.n, `paused, the page does not lease (${lp.n} -> ${lq.n})`);

  await page.click("#sndTakeScope");
  await t.until(() => po.eval(() => !snd.paused && $("sndPaused").hidden), "Take the scope streams again", 4000);
  const n0 = await po.eval(() => snd.next);
  await t.until(async () => (await po.eval(() => snd.next)) > n0 + 20000, "samples arrive again", 6000);
  t.ok((await leases()).on, "the page holds the stream again");

  await page.keyboard.press("Alt+Digit1");
  await po.waitFor(() => ws.cur === "scope", null, 3000);
  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join(" | "));

  // the phone: its other tabs lease nothing; its Sound tab pauses and takes back
  const phone = await browser.newPage({ viewport: { width: 412, height: 915 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true });
  const phoneErrors = [];
  phone.on("pageerror", (e) => phoneErrors.push(e.message));
  await sleep(3500); // the web page's last lease runs out
  await phone.goto(URL + "/remote", { waitUntil: "domcontentloaded", timeout: 15000 });
  await phone.waitForSelector("#tab-sound", { timeout: 8000 });
  const m0 = await leases();
  for (const id of ["menus", "hist", "keys", "home"]) {
    await phone.tap("#tab-" + id);
    await sleep(600);
  }
  const m1 = await leases();
  t.ok(m1.n === m0.n && !m1.on, `the phone's other tabs leased nothing (${m0.n} -> ${m1.n})`);
  await phone.tap("#tab-sound");
  await t.until(async () => (await leases()).on, "the phone's Sound tab leases the stream", 4000);
  await lcdKey("set50");
  await t.until(() => phone.evaluate(() => window.RSOUND.client.paused), "a key on the scope pauses the phone", 4000);
  await t.until(() => phone.evaluate(() => !document.querySelector("#view-sound .snd-takescope").hidden), "the phone offers Take the scope", 2000);
  t.ok(/paused: the scope is in use/.test(await phone.textContent('#view-sound section[aria-label="Listen"] .snd-status')), "the phone's status says paused");
  await phone.tap("#view-sound .snd-takescope");
  await t.until(() => phone.evaluate(() => !window.RSOUND.client.paused), "the phone takes the scope back", 4000);
  await t.until(async () => (await leases()).on, "the phone holds the stream again", 4000);
  await phone.tap("#tab-home");
  t.ok(phoneErrors.length === 0, "no phone page errors: " + phoneErrors.join(" | "));
});
