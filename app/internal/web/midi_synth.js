// ENGMODEL-OWNER-UNIT: FU-WEB-APP-VISUAL
// midi_synth.js — the BUS Visual view's MIDI sound: the decoded notes played
// in the browser by a small synthesizer (Web Audio's oscillators, filters and
// gains: no AudioWorklet, so it plays on the scope's plain-http pages too).
// A program's family picks the voice (keys, organ, bass, strings, leads),
// channel 10 plays drums from noise and a falling sine; volume, expression,
// sustain and pitch bend act per channel; at most SYNTH_VOICES notes sound,
// the oldest giving way. The events carry the bus's time: they play that far
// apart, a short latency after they arrive. Classic script.
"use strict";

// SYNTH_VOICES is the notes that sound at once; SYNTH_LATENCY the seconds
// between an event's arrival and its sound (the scheduling margin).
const SYNTH_VOICES = 32;
const SYNTH_LATENCY = 0.12;

// synthNew is a synthesizer, silent until synthStart (a click must start audio).
// TRLC-LINKS: REQ-SDS-279
function synthNew() {
  return { ctx: null, out: null, voices: new Map(), order: [], vol: new Float32Array(16).fill(100 / 127), expr: new Float32Array(16).fill(1),
    sustain: new Uint8Array(16), held: new Set(), prog: new Uint8Array(16), bend: new Float32Array(16), offset: NaN, lead: 0, gain: 0.5, played: 0, noise: null };
}

// synthStart makes (or resumes) the audio graph: master gain into a limiter
// into the speakers. It resolves to whether it plays.
// TRLC-LINKS: REQ-SDS-279
async function synthStart(sy) {
  if (!sy.ctx) {
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return false;
    sy.ctx = new AC({ latencyHint: "interactive" });
    const lim = sy.ctx.createDynamicsCompressor();
    lim.threshold.value = -10; lim.knee.value = 6; lim.ratio.value = 12; lim.attack.value = 0.003; lim.release.value = 0.15;
    sy.out = sy.ctx.createGain();
    sy.out.gain.value = sy.gain;
    sy.out.connect(lim); lim.connect(sy.ctx.destination);
    const len = sy.ctx.sampleRate; // a second of white noise for the drums
    sy.noise = sy.ctx.createBuffer(1, len, sy.ctx.sampleRate);
    const d = sy.noise.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = Math.random() * 2 - 1;
  }
  await sy.ctx.resume();
  sy.offset = NaN;
  return sy.ctx.state === "running";
}

// synthStop silences every note and suspends the audio.
// TRLC-LINKS: REQ-SDS-279
function synthStop(sy) {
  synthPanic(sy);
  if (sy.ctx) sy.ctx.suspend();
}

// synthPanic ends every note at once (a note-off lost to a capture's gap).
// TRLC-LINKS: REQ-SDS-279
function synthPanic(sy) {
  if (!sy.ctx) return;
  const now = sy.ctx.currentTime;
  for (const v of sy.voices.values()) synthRelease(sy, v, now, 0.02);
  sy.voices.clear(); sy.order = []; sy.held.clear();
}

// synthWhen maps an event's bus time to the audio clock. With a lead (the
// seconds a picture's note falls before it lands) the event sounds exactly
// that long after its time on the page's clock, as the picture shows it.
// Without, the first event sets the offset a latency ahead; one that falls
// behind (a pause, a gap) sets it again; one far ahead (the clocks drift) too.
// TRLC-LINKS: REQ-SDS-279
function synthWhen(sy, t) {
  const now = sy.ctx.currentTime;
  if (sy.lead > 0) return Math.max(now + 0.005, now + t + sy.lead - performance.now() / 1000);
  if (!Number.isFinite(sy.offset)) sy.offset = now + SYNTH_LATENCY - t;
  let at = t + sy.offset;
  if (at < now || at > now + 1.5) { sy.offset = now + SYNTH_LATENCY - t; at = now + SYNTH_LATENCY; }
  return at;
}

// synthEvents plays a MIDI state's queued events (visual.js midiMsg), and
// empties the queue.
// TRLC-LINKS: REQ-SDS-279
function synthEvents(sy, events) {
  if (!sy.ctx || sy.ctx.state !== "running") { events.length = 0; return; }
  for (const e of events) {
    const at = synthWhen(sy, e.t);
    if (e.type === "on") synthNoteOn(sy, e.ch, e.note, e.vel, at);
    else if (e.type === "off") synthNoteOff(sy, e.ch, e.note, at);
    else if (e.type === "pc") sy.prog[e.ch] = e.prog;
    else if (e.type === "bend") synthBend(sy, e.ch, e.val, at);
    else if (e.type === "cc") synthCC(sy, e.ch, e.cc, e.val, at);
  }
  events.length = 0;
}

// synthCC acts on a controller: volume (7), expression (11), sustain (64,
// held notes end on its release), all sound or notes off (120, 123).
// TRLC-LINKS: REQ-SDS-279
function synthCC(sy, ch, cc, val, at) {
  if (cc === 7) sy.vol[ch] = val / 127;
  else if (cc === 11) sy.expr[ch] = val / 127;
  else if (cc === 64) {
    sy.sustain[ch] = val >= 64 ? 1 : 0;
    if (!sy.sustain[ch]) for (const k of [...sy.held]) if (Math.floor(k / 128) === ch) { sy.held.delete(k); synthNoteOff(sy, ch, k % 128, at); }
  } else if (cc === 120 || cc === 123) {
    for (const [k, v] of [...sy.voices]) if (Math.floor(k / 128) === ch) { synthRelease(sy, v, at, 0.03); sy.voices.delete(k); }
  }
}

// synthBend bends a channel's notes (±2 semitones).
// TRLC-LINKS: REQ-SDS-279
function synthBend(sy, ch, val, at) {
  sy.bend[ch] = val;
  for (const [k, v] of sy.voices) if (Math.floor(k / 128) === ch && v.osc) v.osc.detune.setValueAtTime(val * 200, at);
}

// SYNTH_FAMILIES picks a voice by the General MIDI program's family (eight
// programs a family): [oscillator, filter cutoff as a multiple of the
// note's frequency, attack, decay, sustain level, release].
const SYNTH_FAMILIES = [
  ["triangle", 8, 0.004, 0.6, 0.25, 0.3], ["triangle", 10, 0.002, 0.3, 0.2, 0.2], ["square", 4, 0.01, 0.05, 0.85, 0.08],
  ["sawtooth", 6, 0.005, 0.4, 0.3, 0.3], ["triangle", 4, 0.004, 0.5, 0.4, 0.15], ["sawtooth", 3, 0.08, 0.2, 0.8, 0.4],
  ["sawtooth", 3, 0.1, 0.3, 0.85, 0.5], ["sawtooth", 5, 0.03, 0.2, 0.7, 0.25], ["sawtooth", 4, 0.04, 0.2, 0.7, 0.25],
  ["square", 3, 0.03, 0.2, 0.7, 0.25], ["sawtooth", 6, 0.01, 0.2, 0.7, 0.2], ["sawtooth", 2, 0.2, 0.5, 0.8, 0.8],
  ["square", 3, 0.05, 0.5, 0.6, 0.6], ["triangle", 6, 0.004, 0.4, 0.3, 0.3], ["triangle", 8, 0.002, 0.3, 0.0, 0.2],
  ["sawtooth", 2, 0.1, 0.5, 0.5, 0.5],
];

// synthNoteOn starts a note at the audio time at: an oscillator through a
// low-pass and an envelope (a drum on channel 10); a note already sounding
// on the key ends first, and past SYNTH_VOICES the oldest gives way.
// TRLC-LINKS: REQ-SDS-279
function synthNoteOn(sy, ch, note, vel, at) {
  const key = ch * 128 + note, old = sy.voices.get(key);
  if (old) { synthRelease(sy, old, at, 0.01); sy.voices.delete(key); }
  sy.held.delete(key);
  while (sy.voices.size >= SYNTH_VOICES && sy.order.length) {
    const k = sy.order.shift(), v = sy.voices.get(k);
    if (v) { synthRelease(sy, v, at, 0.01); sy.voices.delete(k); }
  }
  const c = sy.ctx, amp = (vel / 127) * sy.vol[ch] * sy.expr[ch] * 0.35;
  const g = c.createGain();
  g.gain.setValueAtTime(0, at);
  g.connect(sy.out);
  sy.played++;
  if (ch === 9) { synthDrum(sy, note, amp, at, g); return; }
  const fam = SYNTH_FAMILIES[sy.prog[ch] >> 3], f = 440 * 2 ** ((note - 69) / 12);
  const osc = c.createOscillator(), lp = c.createBiquadFilter();
  osc.type = fam[0];
  osc.frequency.setValueAtTime(f, at);
  osc.detune.setValueAtTime(sy.bend[ch] * 200, at);
  lp.type = "lowpass";
  lp.frequency.setValueAtTime(Math.min(18000, f * fam[1]), at);
  osc.connect(lp); lp.connect(g);
  g.gain.linearRampToValueAtTime(amp, at + fam[2]);
  g.gain.setTargetAtTime(amp * fam[4], at + fam[2], fam[3] / 3);
  osc.start(at);
  const v = { osc, g, rel: fam[5], end: Infinity };
  sy.voices.set(key, v);
  sy.order.push(key);
}

// synthDrum plays a drum: the kick (35, 36) a falling sine, the snares and
// claps noise through a band-pass, the cymbals and hats high-passed noise,
// the toms a falling sine pitched by the note.
// TRLC-LINKS: REQ-SDS-279
function synthDrum(sy, note, amp, at, g) {
  const c = sy.ctx;
  const tom = [41, 43, 45, 47, 48, 50].includes(note);
  if (note === 35 || note === 36 || tom) {
    const osc = c.createOscillator(), f0 = tom ? 80 + (note - 41) * 20 : 150;
    osc.frequency.setValueAtTime(f0, at);
    osc.frequency.exponentialRampToValueAtTime(Math.max(30, f0 / 3), at + 0.15);
    osc.connect(g);
    g.gain.linearRampToValueAtTime(amp * 1.6, at + 0.002);
    g.gain.exponentialRampToValueAtTime(0.0001, at + 0.35);
    osc.start(at); osc.stop(at + 0.4);
    return;
  }
  const src = c.createBufferSource(), flt = c.createBiquadFilter();
  src.buffer = sy.noise;
  const hat = note === 42 || note === 44 || note === 46 || note >= 49;
  flt.type = hat ? "highpass" : "bandpass";
  flt.frequency.setValueAtTime(hat ? 7000 : 1800, at);
  src.connect(flt); flt.connect(g);
  const len = note === 46 || note === 49 || note === 57 ? 0.45 : hat ? 0.06 : 0.18;
  g.gain.linearRampToValueAtTime(amp, at + 0.001);
  g.gain.exponentialRampToValueAtTime(0.0001, at + len);
  src.start(at); src.stop(at + len + 0.05);
}

// synthNoteOff ends a note (held on while the channel's sustain is down).
// TRLC-LINKS: REQ-SDS-279
function synthNoteOff(sy, ch, note, at) {
  const key = ch * 128 + note, v = sy.voices.get(key);
  if (!v) return;
  if (sy.sustain[ch]) { sy.held.add(key); return; }
  synthRelease(sy, v, at, v.rel);
  sy.voices.delete(key);
  const i = sy.order.indexOf(key);
  if (i >= 0) sy.order.splice(i, 1);
}

// synthRelease fades a voice out from at over rel seconds and stops it.
// TRLC-LINKS: REQ-SDS-279
function synthRelease(sy, v, at, rel) {
  const t = Math.max(at, sy.ctx.currentTime);
  v.g.gain.cancelScheduledValues(t);
  v.g.gain.setTargetAtTime(0, t, Math.max(0.005, rel / 4));
  if (v.osc) v.osc.stop(t + rel * 2 + 0.05);
}

// synthVolume sets the master gain (0..1).
// TRLC-LINKS: REQ-SDS-279
function synthVolume(sy, g) {
  sy.gain = g;
  if (sy.out) sy.out.gain.value = g;
}
