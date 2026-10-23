// ENGMODEL-OWNER-UNIT: FU-WEB-SOUND
// sound_analysis.js — SOUND's heavy page analyses in a Web Worker, off the
// page's thread: the tuner's pitch, the modulation page's four-second demodulation and its
// spectrum, the harmonics page's spectra, A/B's comparison and spectra. On
// the page's thread they held it 70 to 90 ms at a time, and Listen, fed from
// that thread, dropped out. Loads sound.js (no DOM); one reply a job.
"use strict";
importScripts("sound.js");

// TRLC-LINKS: REQ-SDS-269, REQ-SDS-270, REQ-SDS-271
onmessage = (e) => {
  const j = e.data;
  let r = {};
  if (j.kind === "pitch") {
    r = { p: j.enough ? sndPitch(j.x, j.fs, j.range) : { ok: false, hz: 0, clarity: 0 } };
  } else if (j.kind === "mod") {
    if (j.hz > 0) {
      const demod = sndDemod(j.x, j.fs, j.hz, 1000), sp = sndSpectrum(j.x, j.fs, 32768);
      r = { modDemod: demod, mod: sndModulation(demod), sb: sndSidebands(sp.dbv, sp.binHz), lfo: null };
    } else r = { modDemod: null, mod: null, lfo: sndLFO(j.x, j.fs) };
  } else if (j.kind === "spec") {
    r = { sp: j.xs.map((x) => sndSpectrum(x, j.fs, 16384)) };
  } else if (j.kind === "ab") {
    r = { ab: sndCompareAB(j.a, j.b, j.fs, j.hz), specA: sndSpectrum(j.a, j.fs, 16384), specB: sndSpectrum(j.b, j.fs, 16384) };
  }
  postMessage({ kind: j.kind, gen: j.gen, r });
};
