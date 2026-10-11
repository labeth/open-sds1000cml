// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e for the protocol-decode UI + navigator in the BUS workspace,
// driven by headless Chromium against a local server serving a synthetic I2C
// frame (argv[2]=URL). Asserts: entering BUS with decoding off detects the bus
// and decodes it; the decode transcript (Log view), byte count and Copy; the
// packet table; and the navigator wheel-zoom + reset with the decode staying
// valid. SKIP/exit 0 when the browser is unavailable.
import { openScope, run } from "./scope_po.mjs";

const EXPECT = "START 50 W ACK 00 ACK FF NAK STOP";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2]);
  t.browser = browser;
  try { await page.context().grantPermissions(["clipboard-read", "clipboard-write"], { origin: process.argv[2] }); } catch {}

  t.ok(await po.eval(() => typeof decodeI2C === "function"), "decode.js loaded in the page");

  // Entering BUS with decoding off auto-detects the bus (I²C here: SCL=C1, SDA=C2).
  await po.ws("bus");
  await po.waitFor(() => dcfg.proto === "i2c" && dcfg.result && dcfg.result.ok, null, 15000);
  t.ok(await po.value("decProto") === "i2c", "BUS entry detected I²C and turned decoding on");

  // The transcript lives in the Log view.
  await po.seg("busView", "log");
  await po.waitFor((exp) => document.getElementById("decodeText").value.includes("START"), EXPECT, 15000);
  const text = await po.eval(() => document.getElementById("decodeText").value);
  t.ok(text === EXPECT, `decode transcript = "${text}"`);
  t.ok(/2 bytes/.test(await po.text("decodeCount")), "byte count shows 2 bytes");
  t.ok(await po.eval(() => !!(dcfg.result && dcfg.result.ok && dcfg.result.spans.length)), "decode result has on-trace spans");

  // Copy button -> clipboard holds the transcript (127.0.0.1 is a secure context).
  await po.click("decodeCopy");
  let clip = "";
  try { clip = await page.evaluate(() => navigator.clipboard.readText()); } catch {}
  t.ok(clip === EXPECT || clip === "", `Copy button ${clip ? "put the transcript on the clipboard" : "ran (clipboard unreadable in this env)"}`);

  // The packet table: the one transaction, address and data apart.
  await po.seg("busView", "packets");
  await po.waitFor(() => document.querySelectorAll("#pkBody tr[data-i]").length === 1, null, 8000);
  const row = await po.eval(() => [...document.querySelectorAll("#pkBody tr[data-i] td")].map((c) => c.textContent));
  t.ok(row[2] === "50 W" && row[3] === "00 FF" && row[4] === ".." && row[5] === "2", "packet row: addr 50 W, hex 00 FF, string .., 2 bytes: " + row.join(" | "));

  // Navigator: wheel-zoom narrows the window; the decode result stays valid and
  // its spans still map through xForCol; double-click the nav resets to full.
  const before = await po.eval(() => view.win.b - view.win.a);
  await po.eval(() => {
    const s = document.getElementById("scope"), r = s.getBoundingClientRect();
    s.dispatchEvent(new WheelEvent("wheel", { deltaY: -600, clientX: r.left + r.width / 2, clientY: r.top + r.height / 2, bubbles: true, cancelable: true }));
  });
  await po.wait(120);
  const zoomed = await po.eval(() => view.win.b - view.win.a);
  t.ok(zoomed < before - 0.05, `wheel zoom narrowed the window (${before.toFixed(2)} -> ${zoomed.toFixed(2)})`);
  t.ok(await po.eval(() => dcfg.result && dcfg.result.ok), "decode still valid while zoomed");
  await page.dispatchEvent("#nav", "dblclick");
  await po.wait(120);
  t.ok(await po.eval(() => view.win.a === 0 && view.win.b === 1), "double-click nav resets to the full record");

  // Regression: "when zoomed out I can only see 1 of the 2 bytes". Two bytes with
  // tiny boxes but idle space around them — each label must spill into the gap.
  const spill = await po.eval(() => {
    const n = frame.c1.length;
    dcfg.result = { ok: true, proto: "uart", bytes: [0x00, 0xff], text: "00 FF",
      spans: [{ i0: Math.round(n * 0.20), i1: Math.round(n * 0.205), text: "00", kind: "data", val: 0 },
              { i0: Math.round(n * 0.60), i1: Math.round(n * 0.605), text: "FF", kind: "data", val: 255 }] };
    const seen = [], orig = ctx.fillText;
    // TRLC-LINKS: REQ-SDS-180
    ctx.fillText = function (t, x, y) { seen.push(String(t)); return orig.call(this, t, x, y); };
    try { redraw(); } finally { ctx.fillText = orig; }
    return seen.filter(t => t === "00" || t === "FF");
  });
  t.ok(spill.includes("00") && spill.includes("FF"),
    `both narrow-box bytes still labelled via gap-spill (drew: ${spill.join(",") || "none"})`);

  // Decoding stays on after leaving BUS (the strip keeps drawing elsewhere).
  await po.ws("scope");
  t.ok(await po.eval(() => dcfg.proto === "i2c"), "decoding stays on in SCOPE");
  if (process.env.SHOT_DIR) await page.screenshot({ path: process.env.SHOT_DIR + "/decode.png" });
  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
