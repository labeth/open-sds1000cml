// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// The Listen player's jitter buffer against a scope whose replies come late
// now and then: the stream arrives in 40 ms pieces, every few seconds one
// 160 ms late (the LCD's SOUND view beside Listen). The player grows its
// target before running dry and then plays through such gaps.
"use strict";
const fs = require("fs");
const vm = require("vm");
const ctx = { Math, Float32Array, console };
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(__dirname + "/sound_worklet.js", "utf8") + "\nthis.SoundPlayerCore = SoundPlayerCore;", ctx);
const fsrc = 61035.15625, out = 48000;
const p = new ctx.SoundPlayerCore({ srcRate: fsrc, targetMs: 120 }, out, () => {});
const block = 128;
let t = 0, sent = 0, late = 0;
const piece = Math.round(0.04 * fsrc);
// TRLC-LINKS: REQ-SDS-265
const src = (n) => { const l = new Float32Array(n); for (let i = 0; i < n; i++) l[i] = 0.1 * Math.sin(2 * Math.PI * 1000 * (sent + i) / fsrc); return l; };
let k = 0, prevArr = 0, undersAfter10s = -1;
// TRLC-LINKS: REQ-SDS-265
const arrival = (k) => {
  // the scope seals a piece every 40 ms; every fourth second one reply is 160 ms late and the next follow it at once
  const sched = 0.04 * (k + 1), isLate = k % 100 === 50;
  if (isLate) late++;
  return Math.max(prevArr, sched + (isLate ? 0.16 : 0));
};
let nextArr = arrival(0);
while (t < 60) {
  while (nextArr <= t) {
    p.onMsg({ l: src(piece) });
    sent += piece;
    prevArr = nextArr;
    nextArr = arrival(++k);
  }
  const oL = new Float32Array(block), oR = new Float32Array(block);
  p.render(oL, oR, t);
  t += block / out;
  if (undersAfter10s < 0 && t >= 10) undersAfter10s = p.under;
}
const fails = [];
if (late < 10) fails.push("late replies " + late);
if (p.under - undersAfter10s !== 0) fails.push(`underruns after the first 10 s: ${p.under - undersAfter10s} (target ${p.target} ms)`);
// a new target (targetMs) starts a step above it
p.onMsg({ targetMs: 120 });
if (p.target !== 160) fails.push("a new target did not reset it: " + p.target);
if (fails.length) { console.log("FAIL " + fails.join("; ")); process.exit(1); }
console.log("PASS underruns", p.under, "late", late);
