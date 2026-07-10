// Regression (from the 200-iteration operator fuzz): frames that lack a
// per-sample channel array — channel toggled OFF, or an envelope/roll band —
// must never throw in the FFT/spectrogram/decode draw paths. Root cause was
// peakNyq() reading frame.c1.length unguarded (app_fft.js), reached from
// drawFFT→displayNyq and the spectrogram status ticker. Exits 1 on any error.
import { launch } from "./operator.mjs";

const URL = process.env.SCOPE_URL || "http://192.168.1.209:8080";
const op = await launch(URL);
if (!op) { console.log("SKIP"); process.exit(0); }
const stacks = [];
op.page.on("pageerror", (e) => stacks.push(e.stack || e.message));

const clk = async (id) => { const el = await op.page.$("#" + id); if (el && await el.isVisible()) await el.click(); };
const sel = async (id, v) => op.page.evaluate((a) => { const e = document.getElementById(a.id); if (e) { e.value = a.v; e.dispatchEvent(new Event("change")); } }, { id, v });
const report = async (tag) => {
  await op.page.waitForTimeout(1800);
  if (stacks.length) {
    console.log(`\n=== ${tag}: ${stacks.length} error(s) ===`);
    console.log(stacks[0].split("\n").slice(0, 6).join("\n"));
  } else console.log(`\n=== ${tag}: clean ===`);
  if (stacks.length) globalThis.__fails = true;
  stacks.length = 0;
};

// scenario 1: SPI decode with C2 disabled
await sel("decProto", "spi");
await op.page.waitForTimeout(500);
await clk("tC2"); // C2 off
await report("S1 spi-decode + C2 off");
await clk("tC2"); await sel("decProto", "off"); await op.page.waitForTimeout(400);

// scenario 2: C1 off alone (Y-T)
await clk("tC1");
await report("S2 C1 off alone");
await clk("tC1"); await op.page.waitForTimeout(300);

// scenario 3: FFT view with C1 off
await clk("tC1"); await clk("mFFT");
await report("S3 FFT view + C1 off");
await clk("mYT"); await clk("tC1"); await op.page.waitForTimeout(300);

// scenario 4: spectrogram armed, then hop to a slow (env/roll) band
await clk("spgArm");
await sel("tdiv", "0.5");
await report("S4 spectrogram armed + roll band");
await sel("tdiv", "0.0000005"); await op.page.waitForTimeout(1500); await clk("spgArm");

// scenario 5: freeze then ctrl-wheel zoom
await clk("freeze");
const box = await op.page.evaluate(() => { const c = document.getElementById("scope") || document.querySelector("canvas"); const r = c.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; });
await op.page.mouse.move(box.x, box.y);
await op.page.keyboard.down("Control"); await op.page.mouse.wheel(0, -120); await op.page.keyboard.up("Control");
await report("S5 freeze + ctrl-wheel");
await clk("freeze");

// scenario 6: XY view with C2 off
await clk("tC2"); await clk("mXY");
await report("S6 XY + C2 off");
await clk("mYT"); await clk("tC2");

await op.close();
if (globalThis.__fails) process.exit(1);
