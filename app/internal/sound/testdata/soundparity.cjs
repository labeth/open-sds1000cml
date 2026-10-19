// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
// soundparity.cjs - runs the web's SOUND analysis (internal/web/sound.js) on
// the vectors of TestSoundJSParity. Usage: node soundparity.cjs <vectors.json>
// <web dir>. Prints, per vector, the readings the Go test compares.
"use strict";
const fs = require("node:fs");
const path = require("node:path");

const [vecPath, webDir] = process.argv.slice(2);
const S = require(path.resolve(webDir, "sound.js"));
const vecs = JSON.parse(fs.readFileSync(vecPath, "utf8"));

// TRLC-LINKS: REQ-SDS-266, REQ-SDS-267, REQ-SDS-268, REQ-SDS-269, REQ-SDS-270, REQ-SDS-271, REQ-SDS-272
function run(v) {
  const x = Float64Array.from(v.x || []);
  const y = Float64Array.from(v.y || []);
  switch (v.kind) {
    case "measure": return S.sndMeasure(x);
    case "pitch": return S.sndPitch(x, v.fs, S.sndPitchRange(v.range || ""));
    case "note": return S.sndNote(v.f, v.a4);
    case "adsr": {
      const e = v.cv ? S.sndBlockMeans(x, v.fs, 0.001) : S.sndEnvelope(x, v.fs, 0.02, 0.001);
      return { envN: e.env.length, envFs: e.fs, adsr: S.sndADSR(e.env, e.fs, v.on, v.off) };
    }
    case "mod": {
      const d = S.sndDemod(x, v.fs, v.fc, 1000);
      return { n: d.amp.length, amp: d.amp.slice(0, 50), freq: d.freq.slice(0, 50), mod: S.sndModulation(d) };
    }
    case "sidebands": {
      const sp = S.sndSpectrum(x, v.fs, v.n);
      return S.sndSidebands(sp.dbv, sp.binHz);
    }
    case "lfo": return S.sndLFO(x, v.fs);
    case "harm": {
      const sp = S.sndSpectrum(x, v.fs, v.n);
      const h = S.sndHarmonics(sp, v.f0);
      return { binHz: sp.binHz, dbv: Array.from(sp.dbv.slice(0, 64)), h };
    }
    case "ab": return S.sndCompareAB(x, y, v.fs, v.f0);
    case "voct": return S.sndFitVOct(v.pts, 440);
    case "cycle": {
      const [at, ok] = S.sndPlaceMark(x, v.mark, 0, true);
      const c = ok ? S.sndCycles(x, v.fs, at, v.period, 2, 64) : null;
      return { at, ok, c: c && Object.assign({}, c, { trace: Array.from(c.trace) }) };
    }
  }
  return null;
}

// JSON cannot carry -Infinity or NaN: they go as strings.
// TRLC-LINKS: REQ-SDS-266
function enc(k, val) {
  if (typeof val === "number" && !Number.isFinite(val)) return String(val);
  return val;
}

process.stdout.write(JSON.stringify(vecs.map(run), enc));
