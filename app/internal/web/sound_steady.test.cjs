// ENGMODEL-OWNER-UNIT: FU-WEB-APP-SOUND
// Run by TestSoundSteadyJS (sound_steady_node_test.go): the SOUND readout
// boxes show each number's median over the view's last readings (each
// harmonic's own), the newest text, and start again on a reading not ok.
const assert = require("node:assert/strict"), fs = require("node:fs"), vm = require("node:vm");
const src = fs.readFileSync("app_sound.js", "utf8");
const code = src.slice(src.indexOf("function sndSteady("), src.indexOf("// sndAdsrRows are"));
const c = { snd: {}, Number, Array, Object, Float32Array, Float64Array };
vm.createContext(c);
vm.runInContext(code, c);
// TRLC-LINKS: REQ-SDS-270
const harm = (thd, h2, ch) => ({ ok: true, f0: 440, thdPct: thd, character: ch, h: [{ n: 1, relDB: 0, found: true }, { n: 2, relDB: h2, found: true }] });
let shown;
for (const [thd, h2, ch] of [[1, -40, "a"], [9, -10, "b"], [2, -41, "c"], [3, -39, "d"], [2.5, -40.5, "e"]]) shown = c.sndSteady("harm", harm(thd, h2, ch));
assert.equal(shown.thdPct, 2.5, "THD is the median, not the newest jump");
assert.equal(shown.h[1].relDB, -40, "each harmonic its own median");
assert.equal(shown.character, "e", "the newest text");
const same = c.sndSteady("harm", c.snd.steady.harm.last);
assert.equal(same.thdPct, 2.5, "the same reading again is not counted twice");
const gone = c.sndSteady("harm", { ok: false, why: "waiting" });
assert.equal(gone.why, "waiting", "a reading not ok shows at once");
assert.equal(c.sndSteady("harm", harm(7, -20, "z")).thdPct, 7, "and the median starts again");
// harmonics found in some readings only: the median of those present
const a = c.sndSteady("h2", { ok: true, h: [{ relDB: -30 }] }), b = c.sndSteady("h2", { ok: true, h: [{ relDB: -32 }, { relDB: -50 }] });
assert.equal(b.h.length, 2);
assert.equal(b.h[1].relDB, -50);
console.log("sound steady ok", a.h.length);
