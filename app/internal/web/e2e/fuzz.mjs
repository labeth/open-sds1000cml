// Operator fuzzer: drives the LIVE scope web UI the way a wandering human
// operator does — random-but-realistic control actions through the real DOM
// (never /api/set) — and checks a battery of invariants after EVERY action:
//
//   I1  no uncaught page error (a JS exception in a handler is a bug)
//   I2  the transport keeps delivering frames (frame.seq advances) unless the
//       action legitimately holds them (STOP / freeze / an armed publish gate)
//   I3  no visible readout shows NaN / undefined / Infinity
//   I4  /api/status stays healthy (responds, engine not wedged)
//   I5  the page body never scrolls horizontally
//   I6  no 5xx from any API the page calls
//
// Seeded PRNG (SEED env) + a full action log make every finding reproducible.
// Findings are appended to OUT/findings.jsonl with a screenshot per failure;
// the run CONTINUES after a finding (RUN_ALL-style) so one pass surfaces the
// whole population. Usage:
//   node fuzz.mjs                       # 200 iterations, seed 1
//   SEED=7 ITERS=50 node fuzz.mjs      # reproduce a slice
import fs from "node:fs";
import path from "node:path";
import { launch } from "./operator.mjs";

const URL = process.env.SCOPE_URL || "http://192.168.1.209:8080";
const N = parseInt(process.env.ITERS || "200", 10);
const SEED = parseInt(process.env.SEED || "1", 10);
const OUT = process.env.OUT || "/tmp/fuzz-findings";
fs.mkdirSync(OUT, { recursive: true });
const findingsPath = path.join(OUT, "findings.jsonl");

function mulberry32(a) {
  return function () {
    a |= 0; a = (a + 0x6D2B79F5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rng = mulberry32(SEED);
const pick = (arr) => arr[Math.floor(rng() * arr.length)];
const rint = (lo, hi) => lo + Math.floor(rng() * (hi - lo + 1));
const rfloat = (lo, hi) => lo + rng() * (hi - lo);

// ---------- generic DOM drivers (operator hands) ----------
async function selRandom(op, id) {
  const opts = await op.page.evaluate((i) => {
    const e = document.getElementById(i);
    return e ? [...e.options].map((o) => o.value) : null;
  }, id);
  if (!opts || !opts.length) throw new Error(`select #${id} missing/empty`);
  const v = opts[Math.floor(rng() * opts.length)];
  await op.page.evaluate((a) => {
    const e = document.getElementById(a.id);
    e.value = a.v; e.dispatchEvent(new Event("change"));
  }, { id, v });
  return v;
}
async function slider(op, id, v) {
  await op.page.evaluate((a) => {
    const e = document.getElementById(a.id);
    if (!e) throw new Error("no #" + a.id);
    e.value = String(a.v);
    e.dispatchEvent(new Event("input"));
    e.dispatchEvent(new Event("change"));
  }, { id, v });
}
async function clickId(op, id) {
  const el = await op.page.$("#" + id);
  if (!el) throw new Error(`button #${id} missing`);
  if (!(await el.isVisible())) return `#${id} hidden — skipped`;
  await el.click({ timeout: 6000 });
  return null;
}

// ---------- the action palette ----------
// Each action: { name, w: weight, hold: frames may legitimately stop after it,
// run(op) -> optional detail string }.
const ACTIONS = [
  { name: "tdiv-random", w: 8, run: async (op) => "tdiv=" + (await selRandom(op, "tdiv")) },
  { name: "vdiv1-random", w: 4, run: async (op) => "vdiv1=" + (await selRandom(op, "vdiv1")) },
  { name: "vdiv2-random", w: 3, run: async (op) => "vdiv2=" + (await selRandom(op, "vdiv2")) },
  { name: "cpl1-random", w: 2, run: async (op) => "cpl1=" + (await selRandom(op, "cpl1")) },
  { name: "cpl2-random", w: 2, run: async (op) => "cpl2=" + (await selRandom(op, "cpl2")) },
  { name: "probe1-random", w: 1, run: async (op) => "probe1=" + (await selRandom(op, "probe1")) },
  { name: "probe2-random", w: 1, run: async (op) => "probe2=" + (await selRandom(op, "probe2")) },
  { name: "trig-level", w: 6, run: async (op) => { const v = rfloat(-3.5, 4.5).toFixed(2); await slider(op, "lvl", v); return "lvl=" + v; } },
  { name: "trig-slope", w: 3, run: async (op) => clickId(op, "slope") },
  { name: "trig-source", w: 3, run: async (op) => clickId(op, "source") },
  { name: "trig-mode", w: 3, hold: true, run: async (op) => clickId(op, "mode") }, // NORM with no matching trigger may hold
  { name: "trig-type", w: 3, run: async (op) => "ttype=" + (await selRandom(op, "ttype")) },
  { name: "trig-pos", w: 3, run: async (op) => { const v = rfloat(0, 1).toFixed(2); await slider(op, "tpos", v); return "tpos=" + v; } },
  { name: "holdoff", w: 1, run: async (op) => { const v = pick(["0", "0.0005", "0.01", "0.1"]); await op.page.evaluate((x) => { const e = document.getElementById("holdoff"); e.value = x; e.dispatchEvent(new Event("change")); }, v); return "holdoff=" + v; } },
  { name: "ch1-toggle", w: 3, run: async (op) => clickId(op, "tC1") },
  { name: "ch2-toggle", w: 3, run: async (op) => clickId(op, "tC2") },
  { name: "offset1", w: 3, run: async (op) => { const v = rfloat(-3.5, 3.5).toFixed(2); await slider(op, "off1", v); return "off1=" + v; } },
  { name: "offset2", w: 2, run: async (op) => { const v = rfloat(-3.5, 3.5).toFixed(2); await slider(op, "off2", v); return "off2=" + v; } },
  { name: "run-stop", w: 4, hold: true, run: async (op) => clickId(op, "run") },
  { name: "single", w: 2, hold: true, run: async (op) => clickId(op, "single") },
  { name: "freeze", w: 2, hold: true, run: async (op) => clickId(op, "freeze") },
  { name: "autoset", w: 1, run: async (op) => { await clickId(op, "autoset"); await op.page.waitForTimeout(4000); return "autoset"; } },
  { name: "view-yt", w: 2, run: async (op) => clickId(op, "mYT") },
  { name: "view-xy", w: 1, run: async (op) => clickId(op, "mXY") },
  { name: "view-fft", w: 2, run: async (op) => clickId(op, "mFFT") },
  { name: "math-fn", w: 2, run: async (op) => "mathFn=" + (await selRandom(op, "mathFn")) },
  { name: "acq-mode", w: 2, run: async (op) => "acq=" + (await selRandom(op, "acq")) },
  { name: "ets-toggle", w: 1, run: async (op) => clickId(op, "ets") },
  { name: "persist", w: 1, run: async (op) => clickId(op, "tPersist") },
  { name: "cursors", w: 2, run: async (op) => clickId(op, "tCursors") },
  { name: "memdepth", w: 2, run: async (op) => "memdepth=" + (await selRandom(op, "memdepth")) },
  { name: "decode-proto", w: 4, run: async (op) => "decProto=" + (await selRandom(op, "decProto")) },
  { name: "decode-fmt", w: 1, run: async (op) => "decFmt=" + (await selRandom(op, "decFmt")) },
  { name: "decode-auto", w: 1, run: async (op) => clickId(op, "decDetect") },
  {
    name: "zone-cycle", w: 1, hold: true, run: async (op) => {
      // draw-free zone flow: pick a mode, then straight back off (drawing needs
      // canvas drags; mode churn alone must never error)
      const v = await selRandom(op, "zmMode");
      await op.page.waitForTimeout(300);
      await op.page.evaluate(() => { const e = document.getElementById("zmMode"); e.value = "0"; e.dispatchEvent(new Event("change")); });
      return "zmMode=" + v + "->0";
    },
  },
  {
    name: "eye-arm-disarm", w: 2, run: async (op) => {
      const r = await clickId(op, "ejArm");
      if (r) return r;
      await op.page.waitForTimeout(1500);
      const armed = await op.page.evaluate(() => typeof ej !== "undefined" && ej && ej.armed);
      if (armed) await clickId(op, "ejArm");
      return "eye cycled";
    },
  },
  {
    name: "superres-arm-disarm", w: 2, run: async (op) => {
      await selRandom(op, "srPreset").catch(() => {});
      const r = await clickId(op, "srArm");
      if (r) return r;
      await op.page.waitForTimeout(1500);
      const armed = await op.page.evaluate(() => typeof sr !== "undefined" && sr && sr.armed);
      if (armed) await clickId(op, "srArm");
      return "superres cycled";
    },
  },
  {
    name: "spectrogram-arm-disarm", w: 2, run: async (op) => {
      const r = await clickId(op, "spgArm");
      if (r) return r;
      await op.page.waitForTimeout(1500);
      const on = await op.page.evaluate(() => { const b = document.getElementById("spgArm"); return b && b.classList.contains("on"); });
      if (on) await clickId(op, "spgArm");
      return "spectrogram cycled";
    },
  },
  {
    name: "bode-arm-disarm", w: 1, hold: true, run: async (op) => {
      const r = await clickId(op, "bodeArm");
      if (r) return r;
      await op.page.waitForTimeout(1200);
      const on = await op.page.evaluate(() => { const b = document.getElementById("bodeArm"); return b && b.classList.contains("on"); });
      if (on) await clickId(op, "bodeArm");
      return "bode cycled";
    },
  },
  {
    name: "serialtrig-arm-disarm", w: 2, hold: true, run: async (op) => {
      // random byte pattern; frames may legitimately hold while armed
      await op.page.evaluate((b) => { const e = document.getElementById("stBytes"); if (e) { e.value = b; e.dispatchEvent(new Event("change")); } }, rint(0, 255).toString(16).padStart(2, "0"));
      const r = await clickId(op, "stArm");
      if (r) return r;
      await op.page.waitForTimeout(1200);
      const on = await op.page.evaluate(() => { const b = document.getElementById("stArm"); return b && b.classList.contains("on"); });
      if (on) await clickId(op, "stArm");
      return "serialtrig cycled";
    },
  },
  {
    name: "zoom-wheel", w: 4, run: async (op) => {
      const box = await op.page.evaluate(() => { const c = document.getElementById("scope") || document.querySelector("canvas"); const r = c.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; });
      await op.page.mouse.move(box.x, box.y);
      const dir = rng() < 0.5 ? -1 : 1;
      for (let k = 0; k < rint(1, 4); k++) await op.page.mouse.wheel(0, dir * 120);
      return "wheel " + (dir < 0 ? "in" : "out");
    },
  },
  {
    name: "pan-drag", w: 3, run: async (op) => {
      const box = await op.page.evaluate(() => { const c = document.getElementById("scope") || document.querySelector("canvas"); const r = c.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2, w: r.width }; });
      const dx = rint(-Math.floor(box.w / 3), Math.floor(box.w / 3));
      await op.page.mouse.move(box.x, box.y);
      await op.page.mouse.down();
      await op.page.mouse.move(box.x + dx, box.y, { steps: 5 });
      await op.page.mouse.up();
      return `drag dx=${dx}`;
    },
  },
  {
    name: "dblclick-home", w: 2, run: async (op) => {
      const box = await op.page.evaluate(() => { const c = document.getElementById("scope") || document.querySelector("canvas"); const r = c.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; });
      await op.page.mouse.dblclick(box.x, box.y);
      return "dblclick";
    },
  },
  {
    name: "ctrl-wheel-tdiv", w: 2, run: async (op) => {
      const box = await op.page.evaluate(() => { const c = document.getElementById("scope") || document.querySelector("canvas"); const r = c.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; });
      await op.page.mouse.move(box.x, box.y);
      await op.page.keyboard.down("Control");
      await op.page.mouse.wheel(0, rng() < 0.5 ? -120 : 120);
      await op.page.keyboard.up("Control");
      return "ctrl-wheel";
    },
  },
  { name: "fftN1", w: 1, run: async (op) => { const v = rint(1, 64); await op.page.evaluate((x) => { const e = document.getElementById("fftN1"); if (e) { e.value = x; e.dispatchEvent(new Event("change")); } }, v); return "fftN1=" + v; } },
  { name: "ref-save", w: 1, run: async (op) => clickId(op, pick(["refSaveA", "refSaveB"])) },
];
const weighted = [];
for (const a of ACTIONS) for (let k = 0; k < a.w; k++) weighted.push(a);

// ---------- invariants ----------
async function checkInvariants(op, act, iter) {
  const errs = [];
  // I1 page errors
  if (op.pageErrors.length) {
    errs.push({ inv: "I1-pageerror", detail: op.pageErrors.join(" | ") });
    op.pageErrors.length = 0;
  }
  // I4 status healthy
  let st = null;
  try { st = await op.status(); } catch (e) { errs.push({ inv: "I4-status", detail: "status fetch failed: " + e.message }); }
  if (st && st.wedged) errs.push({ inv: "I4-wedged", detail: "engine wedged" });
  // I7 stuck acquisition (ONE-SHOT): the persistent half-record FSM state needs a
  // power-cycle, so every later iteration would just repeat the report — record
  // only the FIRST transition, with the action tail, so the tipping sequence is
  // captured for the root-cause hunt (task: vendor full-record op).
  if (st && st.stuck_suspect && !globalThis.__stuckSeen) {
    globalThis.__stuckSeen = true;
    errs.push({ inv: "I7-stuck", detail: `stuck_suspect first seen (degraded_run=${st.degraded_run || "?"}) — FPGA half-record state; power-cycle needed` });
  }
  // I2 frames flow (when they should). NORM is exempt: a NORM engine with no
  // qualifying trigger legitimately holds frames indefinitely (the UI shows
  // WAIT) — only AUTO promises a continuously updating display.
  if (st && st.running && !st.norm && !act.hold) {
    const frozen = await op.page.evaluate(() => typeof frozen !== "undefined" && frozen);
    if (!frozen) {
      const s0 = await op.page.evaluate(() => (typeof frame !== "undefined" && frame ? frame.seq : -1));
      let advanced = false;
      for (let k = 0; k < 25; k++) {
        await op.page.waitForTimeout(200);
        const s1 = await op.page.evaluate(() => (typeof frame !== "undefined" && frame ? frame.seq : -1));
        if (s1 > s0) { advanced = true; break; }
      }
      if (!advanced) errs.push({ inv: "I2-stalled", detail: `frame.seq stuck at ${s0} for 5s (running, unfrozen)` });
    }
  }
  // I3 no NaN/undefined/Infinity in visible readouts
  const bad = await op.page.evaluate(() => {
    const out = [];
    const els = document.querySelectorAll("span,td,th,div.readout,button,label,output");
    for (const el of els) {
      if (!el.offsetParent) continue; // hidden
      if (el.children.length) continue; // only leaf text
      const t = el.textContent;
      if (!t) continue;
      if (/\bNaN\b|\bundefined\b|\bInfinity\b|\bnull\b/.test(t)) out.push(el.id ? "#" + el.id + ":" + t.trim().slice(0, 60) : t.trim().slice(0, 60));
      if (out.length > 4) break;
    }
    return out;
  });
  if (bad.length) errs.push({ inv: "I3-badtext", detail: bad.join(" ; ") });
  // I5 no horizontal scroll
  const hscroll = await op.page.evaluate(() => document.body.scrollWidth > window.innerWidth + 2);
  if (hscroll) errs.push({ inv: "I5-hscroll", detail: "body scrolls horizontally" });
  return errs;
}

// ---------- driver ----------
const op = await launch(URL);
if (!op) { console.log("SKIP: playwright not installed"); process.exit(0); }
// I6: 5xx watcher
const http5xx = [];
op.page.on("response", (r) => { if (r.status() >= 500) http5xx.push(`${r.status()} ${r.url().slice(-60)}`); });

const log = [];
let findings = 0;
console.log(`fuzz: ${N} iterations, seed ${SEED}, url ${URL}`);
const DEBUG_AT = parseInt(process.env.DEBUG_AT || "0", 10);
for (let i = 1; i <= N; i++) {
  const act = weighted[Math.floor(rng() * weighted.length)];
  if (i === DEBUG_AT) {
    // pre-action diagnostics: is the target of a click stable/clickable?
    console.log(`\n[DEBUG_AT ${i}] next action=${act.name}; sampling layout stability…`);
    const track = await op.page.evaluate(async () => {
      const ids = ["decDetect", "decProto", "stArm", "ejArm", "srArm"];
      const out = {};
      for (let k = 0; k < 20; k++) {
        for (const id of ids) {
          const e = document.getElementById(id);
          if (!e) continue;
          const r = e.getBoundingClientRect();
          (out[id] = out[id] || []).push(Math.round(r.y));
        }
        await new Promise((res) => setTimeout(res, 150));
      }
      return out;
    });
    for (const [id, ys] of Object.entries(track)) {
      const u = [...new Set(ys)];
      if (u.length > 1) console.log(`   #${id} y UNSTABLE: ${u.join(",")}`);
      else console.log(`   #${id} y stable at ${u[0]}`);
    }
    const hs1 = await op.page.evaluate(() => { const o = {}; for (const el of document.querySelectorAll("[id]")) { if (!el.offsetParent) continue; const r = el.getBoundingClientRect(); if (r.height > 0) o[el.id] = Math.round(r.height); } return o; });
    await op.page.waitForTimeout(1200);
    const hs2 = await op.page.evaluate(() => { const o = {}; for (const el of document.querySelectorAll("[id]")) { if (!el.offsetParent) continue; const r = el.getBoundingClientRect(); if (r.height > 0) o[el.id] = Math.round(r.height); } return o; });
    const ch = Object.keys(hs1).filter((k) => hs2[k] !== undefined && hs2[k] !== hs1[k]);
    console.log("   height-changers:", ch.map((k) => `#${k}:${hs1[k]}->${hs2[k]}`).join(" ") || "none");
  }
  let detail = "";
  let actErr = null;
  try {
    detail = (await act.run(op)) || "";
  } catch (e) {
    actErr = e.message;
  }
  log.push(`${i}:${act.name}${detail ? "(" + detail + ")" : ""}`);
  const errs = await checkInvariants(op, act, i);
  if (actErr) errs.unshift({ inv: "ACTION", detail: actErr });
  if (http5xx.length) { errs.push({ inv: "I6-5xx", detail: http5xx.join(" | ") }); http5xx.length = 0; }
  if (errs.length) {
    findings++;
    const shot = path.join(OUT, `f${String(findings).padStart(2, "0")}-i${i}.png`);
    await op.page.screenshot({ path: shot }).catch(() => {});
    const rec = { iter: i, seed: SEED, action: act.name, detail, errs, tail: log.slice(-8) };
    fs.appendFileSync(findingsPath, JSON.stringify(rec) + "\n");
    console.log(`\n[FINDING ${findings}] iter ${i} action=${act.name} ${detail}`);
    for (const e of errs) console.log(`   ${e.inv}: ${e.detail}`);
    // recover to a sane baseline so subsequent iterations stay meaningful
    try { await op.reset(); } catch (e) {
      console.log("   reset failed (" + e.message + ") — reloading page");
      try { await op.page.reload({ waitUntil: "domcontentloaded", timeout: 20000 }); await op.page.waitForTimeout(2500); } catch {}
    }
  }
  if (i % 20 === 0) process.stdout.write(` ${i}`);
  // small pacing so the device isn't hammered unrealistically
  await op.page.waitForTimeout(80);
}
if (process.env.LOG_ACTIONS) console.log("\nactions:\n" + log.join("\n"));
console.log(`\n\ndone: ${N} iterations, ${findings} findings -> ${findingsPath}`);
await op.close();
process.exit(findings ? 1 : 0);
