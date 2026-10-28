// ENGMODEL-OWNER-UNIT: FU-WEB-VISUAL
// visual.js — what a decoded bus means, as state the BUS Visual view draws
// (app_visual.js): a MIDI keyboard's notes, a DMX universe's levels, a PS/2
// keyboard's keys and a mouse's moves, an LED strip's colours, an IR remote's
// buttons, a PWM line's duty or a servo's angle, an encoder's or a stepper's
// position, a caliper's reading, the DCF77 time, a UART's terminal, the USB PD
// contract, Wiegand cards, CAN signals from a DBC file and the values a
// protocol layer's notes carry. Each protocol's state takes a decode's spans
// (a frame) or the scope stream's units (gap-free), stamped in seconds.
// Pure: no DOM; classic script sharing the page's scope, and a module for
// the node tests.
"use strict";

// visHex2 writes a byte as two upper-case hex digits.
// TRLC-LINKS: REQ-SDS-278
function visHex2(v) { return (v & 0xff).toString(16).toUpperCase().padStart(2, "0"); }

// VIS_KINDS are the buses the Visual view draws: for each, by its decoder's
// protocol name (aliases share one entry), its title, its empty state
// (make), how a frame's decode goes in (spans: the spans in time order, tOf
// a sample index's seconds, r the whole decode, dt a sample's seconds) and,
// for a bus the scope's stream carries byte for byte, how a stream line's
// units go in (units: read gap-free). A new bus is an entry here, its
// picture and figures in VIS_VIEWS (app_visual_draw.js) and its tests in
// visual.test.cjs, which also checks every entry has a picture that draws.
const VIS_KINDS = {};

// visKind enters a bus in VIS_KINDS under its name and its aliases; key is
// the name, the one VIS_VIEWS knows it by.
// TRLC-LINKS: REQ-SDS-278
function visKind(names, def) {
  def.key = names[0];
  for (const n of names) VIS_KINDS[n] = def;
}

visKind(["midi"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "MIDI keyboard", make: () => midiNew(),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans, tOf) => { for (const sp of spans) if (sp.kind === "data" || sp.kind === "frame-error") midiByte(s, sp.kind === "data" ? sp.val : -1, tOf(sp.i0)); },
  // TRLC-LINKS: REQ-SDS-278
  units: (s, units, t) => { for (const u of units) midiByte(s, u & 0xff, t); },
});
visKind(["dmx512"], {
  title: "DMX512 levels",
  // TRLC-LINKS: REQ-SDS-278
  make: () => ({ levels: new Uint8Array(512), changed: new Float64Array(512), slots: 0, sc: -1, frames: 0, times: [] }),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans, tOf) => dmxSpans(s, spans, tOf),
  // TRLC-LINKS: REQ-SDS-278
  units: (s, units, t) => dmxPacket(s, units, t),
});
visKind(["ps2"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "PS/2 keyboard and mouse", make: () => ps2New(),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans, tOf) => { for (const sp of spans) if (sp.kind === "data") ps2Byte(s, sp.val, sp.text.startsWith("H"), tOf(sp.i0)); },
  // TRLC-LINKS: REQ-SDS-278
  units: (s, units, t) => { for (const u of units) ps2Byte(s, u & 0xff, false, t); },
});
visKind(["ws281x"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "LED strip", make: () => ({ leds: [], cur: null, order: "GRB", frames: 0 }),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans) => wsSpans(s, spans),
});
visKind(["irnec", "irsirc", "irrc5", "irrc6", "irmp"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "IR remote", make: () => ({ lastBtn: null, buttons: new Map(), cur: null }),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans, tOf) => irSpans(s, spans, tOf),
});
visKind(["pwm"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "PWM duty", make: () => ({ duty: NaN, freq: NaN, periodS: NaN, hist: [], min: NaN, max: NaN }),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans, tOf, r, dt) => pwmSpans(s, spans, r, dt, tOf),
});
visKind(["quad"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "Encoder position", make: () => ({ pos: 0, fwd: true, speed: 0, hist: [] }),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans, tOf) => stepSpans(s, spans, tOf),
});
visKind(["stepper"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "Stepper position", make: () => ({ pos: 0, fwd: true, speed: 0, hist: [] }),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans, tOf) => stepSpans(s, spans, tOf),
});
visKind(["caliper"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "Caliper reading", make: () => ({ text: "", value: NaN, unit: "", hist: [] }),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans, tOf) => caliperSpans(s, spans, tOf),
});
visKind(["dcf77"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "DCF77 clock", make: () => ({ fields: {}, time: "", bad: false, secS: 0 }),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans, tOf, r) => { dcfSpans(s, spans); if (r.meta && r.meta.baud > 0) s.secS = 1 / r.meta.baud; },
});
visKind(["uart"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "Terminal", make: () => ({ text: "", esc: "", cr: false }),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans) => { for (const sp of spans) if (sp.kind === "data") termByte(s, sp.val); },
  // TRLC-LINKS: REQ-SDS-278
  units: (s, units) => { for (const u of units) termByte(s, u & 0xff); },
});
visKind(["lin"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "LIN frames", make: () => ({ frames: new Map(), errs: 0 }),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans, tOf) => linSpans(s, spans, tOf),
});
visKind(["usbpd"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "USB PD contract", make: () => ({ caps: [], request: null, contract: "", msgs: [] }),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans, tOf) => pdSpans(s, spans, tOf),
});
visKind(["wiegand"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "Wiegand cards", make: () => ({ lastCard: null, cards: new Map() }),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans, tOf) => wiegandSpans(s, spans, tOf),
});
visKind(["can", "canfd"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "CAN signals", make: () => ({ dbc: null, sigs: new Map(), frames: new Map() }),
  // TRLC-LINKS: REQ-SDS-278
  spans: (s, spans, tOf) => canSpans(s, spans, tOf),
});
visKind(["spdif"], {
  // TRLC-LINKS: REQ-SDS-278
  title: "S/PDIF audio", make: () => ({}),
  // TRLC-LINKS: REQ-SDS-278
  spans: () => {}, // its audio is read from a whole record (Digital audio)
});

// VIS_PROTOS name the view's title for each protocol it draws.
const VIS_PROTOS = Object.fromEntries(Object.entries(VIS_KINDS).map(([n, k]) => [n, k.title]));

// visStreamed reports a bus the view reads gap-free from the scope's stream.
// TRLC-LINKS: REQ-SDS-278
function visStreamed(proto) { const k = VIS_KINDS[proto]; return !!(k && k.units); }

// visNew is a protocol's empty state: what the view draws, before any data;
// a bus the view has no picture of keeps a protocol layer's values.
// TRLC-LINKS: REQ-SDS-278
function visNew(proto) {
  const k = VIS_KINDS[proto];
  return Object.assign({ proto, updates: 0, last: 0 }, k ? k.make() : { notes: new Map() });
}

// visIngest takes a frame's decode r into s: t0 the seconds the record began
// at, dt a sample's seconds. The spans are the decode's, in time order.
// TRLC-LINKS: REQ-SDS-278
function visIngest(s, r, t0, dt) {
  const k = VIS_KINDS[s.proto];
  if (!k || !r || !r.spans || !r.spans.length) return;
  // TRLC-LINKS: REQ-SDS-278
  const tOf = (i) => t0 + i * (dt > 0 ? dt : 0);
  k.spans(s, r.spans, tOf, r, dt);
  s.updates++;
  s.last = tOf(r.spans[r.spans.length - 1].i1);
}

// visIngestUnits takes one line of the scope's stream (its units, the first
// at t seconds) into a bus read gap-free.
// TRLC-LINKS: REQ-SDS-278
function visIngestUnits(s, units, t) {
  const k = VIS_KINDS[s.proto];
  if (!k || !k.units || !units || !units.length) return;
  k.units(s, units, t);
  s.updates++;
  s.last = t;
}

// ---- MIDI ----------------------------------------------------------------------

// MIDI_NOTE_NAMES name the twelve notes of an octave.
const MIDI_NOTE_NAMES = ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"];

// MIDI_FAMILIES name General MIDI's sixteen families of eight programs.
const MIDI_FAMILIES = ["Piano", "Chromatic perc", "Organ", "Guitar", "Bass", "Strings", "Ensemble", "Brass",
  "Reed", "Pipe", "Synth lead", "Synth pad", "Synth FX", "Ethnic", "Percussive", "Sound FX"];

// midiInstrument names a channel's instrument: its program's family, drums
// on channel 10.
// TRLC-LINKS: REQ-SDS-278
function midiInstrument(ch, prog) { return ch === 9 ? "Drums" : MIDI_FAMILIES[(prog >> 3) & 15]; }

// midiNoteName names a MIDI note number ("C4" is 60).
// TRLC-LINKS: REQ-SDS-278
function midiNoteName(n) { return MIDI_NOTE_NAMES[n % 12] + (Math.floor(n / 12) - 1); }

// midiNew is a MIDI parser's state: running status, the notes held (key
// channel*128+note), the piano roll, each channel's program, controllers and
// pitch bend, the newest messages, and the synth's queue of events.
// TRLC-LINKS: REQ-SDS-278, REQ-SDS-279
function midiNew() {
  return {
    run: 0, need: 0, data: [], sysex: null,
    held: new Map(), roll: [], log: [], out: [],
    prog: new Uint8Array(16), cc: Array.from({ length: 16 }, () => new Uint8Array(128)), bend: new Float32Array(16),
    clocks: 0, msgs: 0, errs: 0,
  };
}

// MIDI_ROLL is how many notes the piano roll keeps; MIDI_LOG how many messages.
const MIDI_ROLL = 600;
const MIDI_LOG = 60;

// midiDataLen is the data bytes a status byte takes (-1: sysex, to F7).
// TRLC-LINKS: REQ-SDS-278
function midiLen(st) {
  switch (st >> 4) {
    case 0x8: case 0x9: case 0xA: case 0xB: case 0xE: return 2;
    case 0xC: case 0xD: return 1;
  }
  if (st === 0xF0) return -1;
  if (st === 0xF1 || st === 0xF3) return 1;
  if (st === 0xF2) return 2;
  return 0;
}

// midiByte takes one byte of the stream at t seconds (b < 0: a framing
// error, which ends running status).
// TRLC-LINKS: REQ-SDS-278, REQ-SDS-279
function midiByte(m, b, t) {
  if (b < 0) { m.run = 0; m.need = 0; m.data = []; m.sysex = null; m.errs++; return; }
  if (b >= 0xF8) { // real time: between any two bytes
    if (b === 0xF8) m.clocks++;
    else midiLog(m, t, { F8: "CLK", FA: "START", FB: "CONT", FC: "STOP", FE: "SENS", FF: "RESET" }[visHex2(b)] || "RT " + visHex2(b));
    if (b === 0xFF) midiAllOff(m, t);
    return;
  }
  if (b >= 0x80) {
    if (m.sysex && b === 0xF7) { midiLog(m, t, "SYSEX " + m.sysex.length + " bytes: " + m.sysex.slice(0, 12).map(visHex2).join(" ") + (m.sysex.length > 12 ? " .." : "")); m.sysex = null; m.run = 0; return; }
    m.sysex = b === 0xF0 ? [] : null;
    m.run = b; m.need = midiLen(b); m.data = [];
    if (m.need === 0 && b >= 0xF0) { midiMsg(m, b, [], t); m.run = 0; }
    return;
  }
  if (m.sysex) { if (m.sysex.length < 4096) m.sysex.push(b); return; }
  if (!m.run || m.need <= 0) { m.errs++; return; } // data with no status before it
  m.data.push(b);
  if (m.data.length < m.need) return;
  midiMsg(m, m.run, m.data, t);
  m.data = [];
  if (m.run >= 0xF0) m.run = 0; // system common: no running status
}

// midiLog keeps a message for the view's list.
// TRLC-LINKS: REQ-SDS-278
function midiLog(m, t, text) {
  m.log.push({ t, text });
  if (m.log.length > MIDI_LOG) m.log.splice(0, m.log.length - MIDI_LOG);
}

// midiMsg acts on a whole message: notes held and their roll, controllers,
// programs and bends, and an event for the synth.
// TRLC-LINKS: REQ-SDS-278, REQ-SDS-279
function midiMsg(m, st, d, t) {
  m.msgs++;
  const ch = st & 0xf, kind = st >> 4;
  if (kind === 0x9 && d[1] > 0) {
    midiNoteOff(m, ch, d[0], t);
    const n = { ch, note: d[0], vel: d[1], t0: t, t1: NaN };
    m.held.set(ch * 128 + d[0], n);
    m.roll.push(n);
    if (m.roll.length > MIDI_ROLL) m.roll.splice(0, m.roll.length - MIDI_ROLL);
    m.out.push({ type: "on", ch, note: d[0], vel: d[1], t });
    return;
  }
  if (kind === 0x8 || kind === 0x9) { midiNoteOff(m, ch, d[0], t); return; }
  if (kind === 0xB) {
    m.cc[ch][d[0]] = d[1];
    m.out.push({ type: "cc", ch, cc: d[0], val: d[1], t });
    midiLog(m, t, "CC" + (ch + 1) + " " + d[0] + " = " + d[1] + (MIDI_CC_NAMES[d[0]] ? " (" + MIDI_CC_NAMES[d[0]] + ")" : ""));
    if (d[0] === 120 || d[0] === 123) midiAllOff(m, t, ch);
    return;
  }
  if (kind === 0xC) { m.prog[ch] = d[0]; m.out.push({ type: "pc", ch, prog: d[0], t }); midiLog(m, t, "PROGRAM" + (ch + 1) + " " + (d[0] + 1)); return; }
  if (kind === 0xE) {
    m.bend[ch] = ((d[0] | (d[1] << 7)) - 8192) / 8192;
    m.out.push({ type: "bend", ch, val: m.bend[ch], t });
    return;
  }
  if (kind === 0xA) { midiLog(m, t, "AFTERTOUCH" + (ch + 1) + " " + midiNoteName(d[0]) + " " + d[1]); return; }
  if (kind === 0xD) { midiLog(m, t, "PRESSURE" + (ch + 1) + " " + d[0]); return; }
  if (st === 0xF2) { midiLog(m, t, "SONG POS " + (d[0] | (d[1] << 7))); return; }
  if (st === 0xF3) { midiLog(m, t, "SONG " + d[0]); return; }
  if (st === 0xF1) return; // timecode quarter frames: too many to list
  midiLog(m, t, "SYS " + visHex2(st));
}

// MIDI_CC_NAMES names the common controllers.
const MIDI_CC_NAMES = { 1: "mod", 7: "volume", 10: "pan", 11: "expression", 64: "sustain", 91: "reverb", 93: "chorus", 120: "sound off", 121: "reset", 123: "notes off" };

// midiNoteOff ends a note held on a channel.
// TRLC-LINKS: REQ-SDS-278, REQ-SDS-279
function midiNoteOff(m, ch, note, t) {
  const k = ch * 128 + note, n = m.held.get(k);
  if (!n) return;
  n.t1 = t;
  m.held.delete(k);
  m.out.push({ type: "off", ch, note, t });
}

// midiAllOff ends every note held (on one channel, or all).
// TRLC-LINKS: REQ-SDS-278, REQ-SDS-279
function midiAllOff(m, t, ch) {
  for (const n of [...m.held.values()]) if (ch === undefined || n.ch === ch) midiNoteOff(m, n.ch, n.note, t);
}

// MIDI_HELD_MAX is how long a note stays held with no note-off seen: a frame's
// gap can swallow the note-off, and the key would hang lit (and sounding).
const MIDI_HELD_MAX = 6;

// midiExpire ends the notes held longer than MIDI_HELD_MAX at t.
// TRLC-LINKS: REQ-SDS-278, REQ-SDS-279
function midiExpire(m, t) {
  for (const n of [...m.held.values()]) if (t - n.t0 > MIDI_HELD_MAX) midiNoteOff(m, n.ch, n.note, t);
}

// ---- DMX512 --------------------------------------------------------------------

// dmxSpans takes a frame's DMX packets: after each break the start code, then
// the slots in order (a slot read wrong keeps its last level).
// TRLC-LINKS: REQ-SDS-278
function dmxSpans(s, spans, tOf) {
  let slot = -1, t = 0, vals = null;
  // TRLC-LINKS: REQ-SDS-278
  const done = () => { if (vals) dmxPacket(s, vals, t); vals = null; };
  for (const sp of spans) {
    if (sp.kind === "sof") { done(); slot = 0; vals = []; t = tOf(sp.i0); continue; }
    if (slot < 0 || (sp.kind !== "data" && sp.kind !== "frame-error")) continue;
    vals.push(sp.kind === "data" ? sp.val : -1);
    slot++;
  }
  done();
}

// dmxPacket takes one packet: its start code and slots (-1: read wrong).
// Only a start code of 0 carries levels; another (RDM, text) is counted.
// TRLC-LINKS: REQ-SDS-278
function dmxPacket(s, vals, t) {
  if (!vals.length) return;
  s.sc = vals[0];
  s.frames++;
  s.times.push(t);
  if (s.times.length > 50) s.times.shift();
  if (vals[0] !== 0) return;
  for (let i = 1; i < vals.length && i <= 512; i++) {
    const v = vals[i];
    if (v < 0) continue;
    if (s.levels[i - 1] !== v) { s.levels[i - 1] = v; s.changed[i - 1] = t; }
  }
  s.slots = Math.max(s.slots, Math.min(512, vals.length - 1));
}

// dmxRate is the packets a second the stamps show (0: too few).
// TRLC-LINKS: REQ-SDS-278
function dmxRate(s) {
  const n = s.times.length;
  if (n < 3) return 0;
  const span = s.times[n - 1] - s.times[0];
  return span > 0 ? (n - 1) / span : 0;
}

// ---- PS/2 ------------------------------------------------------------------------

// ps2New is the state of a PS/2 line: a keyboard's keys held and text typed,
// a mouse's buttons, position and trail, which of the two the device is
// (votes, or the host's commands), and the parse of each.
// TRLC-LINKS: REQ-SDS-278
function ps2New() {
  return {
    kind: "", kbdVotes: 0, mouseVotes: 0, lastHost: -1, idNext: false, wheel: false,
    ext: false, brk: false, skip: 0, keys: new Set(), typed: "", lastKey: "", shift: false, caps: false,
    pkt: [], x: 0, y: 0, btn: 0, trail: [], wheelPos: 0,
  };
}

// ps2Byte takes one byte: from the host (a command: the keyboard's LEDs, the
// mouse's ID, sample rate) or from the device (scan codes or mouse packets).
// TRLC-LINKS: REQ-SDS-278
function ps2Byte(s, b, host, t) {
  if (host) {
    if (b === 0xED) s.kbdVotes += 4;
    if (b === 0xF3 || b === 0xE8 || b === 0xE6 || b === 0xE7) s.mouseVotes += 2;
    if (b === 0xF2) s.idNext = true;
    s.lastHost = b;
    return;
  }
  // acknowledge, resend, echo - between a mouse's packets (inside one they are its moves)
  if ((b === 0xFA || b === 0xFE || b === 0xEE) && !s.pkt.length) return;
  if (s.idNext) { // the device's ID after Read ID: AB 83 a keyboard, 00/03/04 a mouse
    if (b === 0xAB) { s.kbdVotes += 10; return; }
    if (b === 0x83 || b === 0x41 || b === 0xC1) { s.idNext = false; return; }
    if (b === 0x00 || b === 0x03 || b === 0x04) { s.mouseVotes += 10; s.wheel = b !== 0; s.idNext = false; return; }
    s.idNext = false;
  }
  if (b === 0xAA && !s.pkt.length) return; // self-test passed
  // the device's kind, by its bytes when no command said: a keyboard's break
  // prefix, a mouse's packets (bit 3 of each first byte set)
  if (b === 0xF0 || b === 0xE0) s.kbdVotes++;
  if (s.kind !== "kbd") ps2Mouse(s, b, t);
  if (s.kind !== "mouse") ps2Key(s, b, t);
  s.kind = s.kbdVotes >= s.mouseVotes + 3 ? "kbd" : s.mouseVotes >= s.kbdVotes + 3 ? "mouse" : s.kind;
}

// ps2Key reads a scan code (set 2): E0 an extended key's prefix, F0 a
// release's, E1 Pause's sequence of eight.
// TRLC-LINKS: REQ-SDS-278
function ps2Key(s, b, t) {
  if (s.skip > 0) { s.skip--; return; }
  if (b === 0xE1) { s.skip = 7; ps2Press(s, "E1", true, t); s.keys.delete("E1"); return; }
  if (b === 0xE0) { s.ext = true; return; }
  if (b === 0xF0) { s.brk = true; return; }
  const code = (s.ext ? "E0" : "") + visHex2(b), up = s.brk;
  s.ext = false; s.brk = false;
  if (code === "E012" || code === "E059") return; // Print Screen's fake shifts
  ps2Press(s, code, !up, t);
}

// ps2Press marks a key down or up, and types what it types.
// TRLC-LINKS: REQ-SDS-278
function ps2Press(s, code, down, t) {
  const k = PS2_KEYS.get(code);
  if (!down) {
    s.keys.delete(code);
    if (code === "12" || code === "59") s.shift = false;
    return;
  }
  if (!s.keys.has(code)) {
    s.lastKey = k ? k.label : code;
    if (code === "12" || code === "59") s.shift = true;
    if (code === "58") s.caps = !s.caps;
    const ch = k && k.chars ? k.chars[(s.shift !== (s.caps && /^[a-z]$/.test(k.chars[0]))) ? 1 : 0] : "";
    if (code === "66") s.typed = s.typed.slice(0, -1);
    else if (ch) s.typed = (s.typed + ch).slice(-2000);
  }
  s.keys.add(code);
  s.kbdVotes++;
}

// ps2Mouse reads mouse packets: three bytes (four with a wheel), the first
// with bit 3 set; a byte that cannot start one is dropped until one can.
// TRLC-LINKS: REQ-SDS-278
function ps2Mouse(s, b, t) {
  if (!s.pkt.length && !(b & 0x08)) { s.mouseVotes = Math.max(0, s.mouseVotes - 1); return; }
  s.pkt.push(b);
  if (s.pkt.length < (s.wheel ? 4 : 3)) return;
  const [b0, b1, b2, b3] = s.pkt;
  s.pkt = [];
  if (b0 & 0xC0) return; // an overflow: the move is not known
  const dx = b1 - ((b0 << 4) & 0x100), dy = b2 - ((b0 << 3) & 0x100);
  s.x += dx; s.y += dy;
  s.btn = b0 & 7;
  if (s.wheel && b3 !== undefined) s.wheelPos += (b3 & 0x8) ? (b3 & 0xf) - 16 : b3 & 0xf;
  s.trail.push({ x: s.x, y: s.y, t, btn: s.btn });
  if (s.trail.length > 400) s.trail.shift();
  s.mouseVotes++;
}

// PS2_LAYOUT is a keyboard's keys as [set-2 code, label, x, y, width, chars]:
// the main block, the cursor block and the number pad, in key units.
// TRLC-LINKS: REQ-SDS-278
const PS2_LAYOUT = (() => {
  const out = [];
  // TRLC-LINKS: REQ-SDS-278
  const row = (y, x0, keys) => {
    let x = x0;
    for (const k of keys) {
      if (k === null) { x += 0.5; continue; }
      const [code, label, w, chars] = k;
      out.push({ code, label, x, y, w: w || 1, chars });
      x += w || 1;
    }
  };
  row(0, 0, [["76", "Esc"], null, ["05", "F1"], ["06", "F2"], ["04", "F3"], ["0C", "F4"], null, ["03", "F5"], ["0B", "F6"], ["83", "F7"], ["0A", "F8"], null,
    ["01", "F9"], ["09", "F10"], ["78", "F11"], ["07", "F12"]]);
  row(0, 15.5, [["E07C", "PrtSc"], ["7E", "ScrLk"], ["E1", "Pause"]]);
  const digits = "1234567890";
  const shifted = "!@#$%^&*()";
  const dcodes = ["16", "1E", "26", "25", "2E", "36", "3D", "3E", "46", "45"];
  row(1.25, 0, [["0E", "`", 1, "`~"], ...dcodes.map((c, i) => [c, digits[i], 1, digits[i] + shifted[i]]), ["4E", "-", 1, "-_"], ["55", "=", 1, "=+"], ["66", "Bksp", 2]]);
  row(1.25, 15.5, [["E070", "Ins"], ["E06C", "Home"], ["E07D", "PgUp"]]);
  row(1.25, 19, [["77", "Num"], ["E04A", "/", 1, "//"], ["7C", "*", 1, "**"], ["7B", "-", 1, "--"]]);
  // TRLC-LINKS: REQ-SDS-278
  const letters = (s, codes) => s.split("").map((c, i) => [codes[i], c.toUpperCase(), 1, c + c.toUpperCase()]);
  row(2.25, 0, [["0D", "Tab", 1.5, "\t\t"], ...letters("qwertyuiop", ["15", "1D", "24", "2D", "2C", "35", "3C", "43", "44", "4D"]), ["54", "[", 1, "[{"], ["5B", "]", 1, "]}"], ["5D", "\\", 1.5, "\\|"]]);
  row(2.25, 15.5, [["E071", "Del"], ["E069", "End"], ["E07A", "PgDn"]]);
  row(2.25, 19, [["6C", "7", 1, "77"], ["75", "8", 1, "88"], ["7D", "9", 1, "99"], ["79", "+", 1, "++"]]);
  row(3.25, 0, [["58", "Caps", 1.75], ...letters("asdfghjkl", ["1C", "1B", "23", "2B", "34", "33", "3B", "42", "4B"]), ["4C", ";", 1, ";:"], ["52", "'", 1, "'\""], ["5A", "Enter", 2.25, "\n\n"]]);
  row(3.25, 19, [["6B", "4", 1, "44"], ["73", "5", 1, "55"], ["74", "6", 1, "66"]]);
  row(4.25, 0, [["12", "Shift", 2.25], ...letters("zxcvbnm", ["1A", "22", "21", "2A", "32", "31", "3A"]), ["41", ",", 1, ",<"], ["49", ".", 1, ".>"], ["4A", "/", 1, "/?"], ["59", "Shift", 2.75]]);
  row(4.25, 16.5, [["E075", "Up"]]);
  row(4.25, 19, [["69", "1", 1, "11"], ["72", "2", 1, "22"], ["7A", "3", 1, "33"], ["E05A", "Ent", 1, "\n\n"]]);
  row(5.25, 0, [["14", "Ctrl", 1.25], ["E01F", "Win", 1.25], ["11", "Alt", 1.25], ["29", "Space", 6.25, "  "], ["E011", "Alt", 1.25], ["E027", "Win", 1.25], ["E02F", "Menu", 1.25], ["E014", "Ctrl", 1.25]]);
  row(5.25, 15.5, [["E06B", "Left"], ["E072", "Down"], ["E074", "Right"]]);
  row(5.25, 19, [["70", "0", 2, "00"], ["71", ".", 1, ".."]]);
  return out;
})();

// PS2_KEYS maps a set-2 code to its key in PS2_LAYOUT.
const PS2_KEYS = new Map(PS2_LAYOUT.map((k) => [k.code, k]));

// ---- WS281x ------------------------------------------------------------------------

// wsSpans takes a frame's LED frames: the bytes from each frame start to its
// reset latch, three a LED; the strip shown is the newest frame latched.
// TRLC-LINKS: REQ-SDS-278
function wsSpans(s, spans) {
  for (const sp of spans) {
    if (sp.kind === "sof") { s.cur = []; continue; }
    if (sp.kind === "data" && s.cur) { s.cur.push(sp.val); continue; }
    if (sp.kind === "rw" && sp.text === "RST" && s.cur) { s.leds = s.cur; s.cur = null; s.frames++; }
  }
}

// wsColours are the LEDs' colours, [r, g, b] each, of bytes in order (GRB,
// RGB or BRG; GRBW and RGBW take four bytes a LED and fold the white in).
// TRLC-LINKS: REQ-SDS-278
function wsColours(bytes, order) {
  const n = order.length, out = [];
  for (let i = 0; i + n <= bytes.length; i += n) {
    const c = { R: 0, G: 0, B: 0, W: 0 };
    for (let k = 0; k < n; k++) c[order[k]] = bytes[i + k];
    out.push([Math.min(255, c.R + c.W), Math.min(255, c.G + c.W), Math.min(255, c.B + c.W)]);
  }
  return out;
}

// ---- IR remotes ----------------------------------------------------------------

// irSpans takes a frame's remote codes: each frame's address and command
// (spans "A.." and "C.."), a repeat code counting on the last button.
// TRLC-LINKS: REQ-SDS-278
function irSpans(s, spans, tOf) {
  // a frame's address and command come in either order (Sony's command first):
  // the button counts once both are in, or with its command alone at the next
  // frame or the record's end (a protocol without an address)
  // TRLC-LINKS: REQ-SDS-278
  const flush = () => { if (s.cur && s.cur.cmd >= 0) irButton(s, s.cur); s.cur = null; };
  for (const sp of spans) {
    if (sp.kind === "sof") {
      flush();
      if (sp.text === "REP") { if (s.lastBtn) { s.lastBtn.repeats++; s.lastBtn.t = tOf(sp.i0); } continue; }
      s.cur = { name: sp.text || s.proto, addr: -1, cmd: -1, t: tOf(sp.i0) };
      continue;
    }
    if (sp.kind === "frame-error") { s.cur = null; continue; }
    if (sp.kind !== "data" || !s.cur) continue;
    const m = /^([AC])([0-9A-F]+)$/.exec(sp.text);
    if (!m) continue;
    if (m[1] === "A") s.cur.addr = sp.val;
    else s.cur.cmd = sp.val;
    if (s.cur.addr >= 0 && s.cur.cmd >= 0) { irButton(s, s.cur); s.cur = null; }
  }
  flush();
}

// irButton counts a button pressed: the newest, and the table of all seen.
// TRLC-LINKS: REQ-SDS-278
function irButton(s, f) {
  const key = f.addr + "/" + f.cmd;
  const b = s.buttons.get(key) || { addr: f.addr, cmd: f.cmd, count: 0, t: 0, repeats: 0, name: f.name };
  b.count++; b.t = f.t; b.repeats = 0;
  s.buttons.set(key, b);
  s.lastBtn = b;
}

// ---- PWM, encoders, steppers -------------------------------------------------------

// pwmSpans takes a frame's periods: the duty cycle (mean, least, most), the
// period from the decode, and a servo's angle where the period is a servo's.
// TRLC-LINKS: REQ-SDS-278
function pwmSpans(s, spans, r, dt, tOf) {
  let sum = 0, n = 0, lo = Infinity, hi = -Infinity;
  for (const sp of spans) {
    if (sp.kind !== "data") continue;
    const d = parseFloat(sp.text);
    if (!Number.isFinite(d)) continue;
    sum += d; n++; lo = Math.min(lo, d); hi = Math.max(hi, d);
  }
  if (!n) return;
  s.duty = sum / n; s.min = lo; s.max = hi;
  if (r.meta && r.meta.spb > 0 && dt > 0) { s.periodS = r.meta.spb * dt; s.freq = 1 / s.periodS; }
  s.hist.push({ t: tOf(spans[spans.length - 1].i1), v: s.duty });
  if (s.hist.length > 300) s.hist.shift();
}

// pwmServo is a servo's angle in degrees for a PWM state (NaN: no servo): a
// period of 10 to 30 ms whose pulse is 0.5 to 2.5 ms, 1.5 ms the centre and
// 90 degrees a millisecond either way.
// TRLC-LINKS: REQ-SDS-278
function pwmServo(s) {
  if (!(s.periodS >= 0.01 && s.periodS <= 0.03)) return NaN;
  const pw = s.duty / 100 * s.periodS * 1000;
  if (!(pw >= 0.5 && pw <= 2.5)) return NaN;
  return Math.max(0, Math.min(180, 90 + (pw - 1.5) * 90));
}

// stepSpans takes a frame's steps: each frame's count runs from its start,
// so the steps it adds are its last count; the speed is the steps over the
// time they took.
// TRLC-LINKS: REQ-SDS-278
function stepSpans(s, spans, tOf) {
  let last = 0, first = NaN, end = NaN, n = 0;
  for (const sp of spans) {
    if (sp.kind !== "data") continue;
    const m = /^([FR])(-?\d+)$/.exec(sp.text);
    if (!m) continue;
    last = +m[2]; s.fwd = m[1] === "F"; n++;
    if (!Number.isFinite(first)) first = tOf(sp.i0);
    end = tOf(sp.i0);
  }
  if (!n) return;
  s.pos += last;
  s.speed = n > 1 && end > first ? (s.fwd ? 1 : -1) * (n - 1) / (end - first) : 0;
  s.hist.push({ t: end, v: s.pos });
  if (s.hist.length > 300) s.hist.shift();
}

// ---- caliper, DCF77, terminal ----------------------------------------------------

// caliperSpans takes a frame's readings ("12.34mm", "0.4860in").
// TRLC-LINKS: REQ-SDS-278
function caliperSpans(s, spans, tOf) {
  for (const sp of spans) {
    if (sp.kind !== "rw") continue;
    const m = /^(-?[\d.]+)\s*(mm|in)$/.exec(sp.text);
    if (!m) continue;
    s.text = sp.text; s.value = parseFloat(m[1]); s.unit = m[2];
    s.hist.push({ t: tOf(sp.i0), v: s.value });
    if (s.hist.length > 300) s.hist.shift();
  }
}

// DCF77_DOW names the weekdays, Monday 1.
const DCF77_DOW = ["", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

// dcfSpans takes a frame's minute fields (BCD) and writes the time they
// carry: "14:05 Tue 27.10.2026".
// TRLC-LINKS: REQ-SDS-278
function dcfSpans(s, spans) {
  let got = false;
  for (const sp of spans) {
    if (sp.kind !== "data" && sp.kind !== "parity-error") continue;
    const m = /^!?([A-Z]+)/.exec(sp.text);
    if (!m) continue;
    s.fields[m[1]] = { v: dcfBCD(sp.val), bad: sp.kind !== "data" };
    got = true;
  }
  if (!got) return;
  const f = s.fields;
  // TRLC-LINKS: REQ-SDS-278
  const two = (k) => f[k] ? String(f[k].v).padStart(2, "0") : "--";
  s.time = two("HR") + ":" + two("MIN") + (f.DOW ? " " + (DCF77_DOW[f.DOW.v] || "?") : "") +
    (f.DAY ? " " + two("DAY") + "." + two("MON") + ".20" + two("YR") : "");
  s.bad = Object.values(f).some((x) => x.bad);
}

// dcfBCD is a binary-coded decimal's value.
// TRLC-LINKS: REQ-SDS-278
function dcfBCD(v) { return (v >> 4) * 10 + (v & 0xf); }

// TERM_MAX is the characters the terminal keeps.
const TERM_MAX = 20000;

// termByte writes a byte to the terminal: CR LF, LF and a lone CR end a
// line, backspace takes one back, an ANSI escape sequence is dropped, a
// byte that does not print shows as a middle dot.
// TRLC-LINKS: REQ-SDS-278
function termByte(s, b) {
  if (s.esc) { // ESC [ parameters, then a final byte 0x40-0x7E; ESC and one other byte
    s.esc += String.fromCharCode(b);
    if (s.esc.length === 2) { if (b !== 0x5b) s.esc = ""; return; }
    if ((b >= 0x40 && b <= 0x7e) || s.esc.length > 16) s.esc = "";
    return;
  }
  if (b === 0x1b) { s.esc = "\x1b"; return; }
  if (b === 0x0a) { if (!s.cr) s.text += "\n"; s.cr = false; }
  else if (b === 0x0d) { s.text += "\n"; s.cr = true; return; }
  else if (b === 0x08) s.text = s.text.slice(0, -1);
  else if (b === 0x09) s.text += "\t";
  else if (b >= 0x20 && b < 0x7f) s.text += String.fromCharCode(b);
  else s.text += "·";
  s.cr = false;
  if (s.text.length > TERM_MAX) s.text = s.text.slice(-TERM_MAX);
}

// ---- USB Power Delivery ----------------------------------------------------------------

// pdSpans takes a frame's PD messages: a source's capabilities, a sink's
// request, and the contract the source's Accept and PS_RDY settle.
// TRLC-LINKS: REQ-SDS-278
function pdSpans(s, spans, tOf) {
  let msg = null;
  // TRLC-LINKS: REQ-SDS-278
  const done = () => { if (msg) pdMessage(s, msg); msg = null; };
  for (const sp of spans) {
    if (sp.kind === "sof") { done(); msg = { sop: sp.text, name: "", bytes: [], bad: false, t: tOf(sp.i0) }; continue; }
    if (!msg) continue;
    if (sp.kind === "data" || sp.kind === "frame-error") {
      if (!msg.bytes.length) msg.name = sp.text.replace(/^!/, "");
      msg.bytes.push(sp.val);
      if (sp.kind === "frame-error") msg.bad = true;
    }
  }
  done();
}

// pdMessage acts on a whole message (its header, data objects and CRC).
// TRLC-LINKS: REQ-SDS-278
function pdMessage(s, m) {
  if (m.bytes.length < 6 || m.bad) return;
  const head = m.bytes[0] | (m.bytes[1] << 8), count = (head >> 12) & 7;
  const objs = [];
  for (let k = 0; k < count && 2 + 4 * k + 4 <= m.bytes.length; k++) {
    const b = m.bytes.slice(2 + 4 * k, 6 + 4 * k);
    objs.push((b[0] | (b[1] << 8) | (b[2] << 16) | (b[3] << 24)) >>> 0);
  }
  s.msgs.push({ t: m.t, name: m.name, sop: m.sop });
  if (s.msgs.length > 30) s.msgs.shift();
  if (count > 0 && (head & 0xf) === 1) { s.caps = objs.map(pdPDO); return; }
  if (count > 0 && (head & 0xf) === 2 && objs.length) { s.request = pdRDO(objs[0], s.caps); s.contract = ""; return; }
  if (count === 0 && (head & 0xf) === 6 && s.request) s.contract = s.request.text; // PS_RDY
  if (count === 0 && (head & 0xf) === 4 && s.request) s.request.rejected = true; // Reject
}

// pdPDO reads a source's power data object: fixed, battery, variable or an
// augmented (PPS) one, as {type, text, mv, mvMin, ma, mw}.
// TRLC-LINKS: REQ-SDS-278
function pdPDO(p) {
  const kind = p >>> 30;
  if (kind === 0) {
    const mv = ((p >>> 10) & 0x3ff) * 50, ma = (p & 0x3ff) * 10;
    return { type: "Fixed", mv, ma, text: (mv / 1000) + " V " + (ma / 1000) + " A" };
  }
  if (kind === 1) {
    const mv = ((p >>> 20) & 0x3ff) * 50, mvMin = ((p >>> 10) & 0x3ff) * 50, mw = (p & 0x3ff) * 250;
    return { type: "Battery", mv, mvMin, mw, text: (mvMin / 1000) + "-" + (mv / 1000) + " V " + (mw / 1000) + " W" };
  }
  if (kind === 2) {
    const mv = ((p >>> 20) & 0x3ff) * 50, mvMin = ((p >>> 10) & 0x3ff) * 50, ma = (p & 0x3ff) * 10;
    return { type: "Variable", mv, mvMin, ma, text: (mvMin / 1000) + "-" + (mv / 1000) + " V " + (ma / 1000) + " A" };
  }
  const mv = ((p >>> 17) & 0xff) * 100, mvMin = ((p >>> 8) & 0xff) * 100, ma = (p & 0x7f) * 50;
  return { type: "PPS", mv, mvMin, ma, text: (mvMin / 1000) + "-" + (mv / 1000) + " V " + (ma / 1000) + " A" };
}

// pdRDO reads a sink's request against the capabilities it answers: the
// object it asks for (1-based) and its current, or a PPS one's voltage.
// TRLC-LINKS: REQ-SDS-278
function pdRDO(r, caps) {
  const pos = (r >>> 28) & 7, cap = caps[pos - 1];
  if (cap && cap.type === "PPS") {
    const mv = ((r >>> 9) & 0x7ff) * 20, ma = (r & 0x7f) * 50;
    return { pos, text: "PPS " + (mv / 1000) + " V " + (ma / 1000) + " A", rejected: false };
  }
  const ma = ((r >>> 10) & 0x3ff) * 10;
  const v = cap && cap.mv ? (cap.mv / 1000) + " V " : "object " + pos + " ";
  return { pos, text: v + (ma / 1000) + " A", rejected: false };
}

// ---- Wiegand ----------------------------------------------------------------------

// wiegandSpans takes a frame's cards: the bits, facility code and card number.
// TRLC-LINKS: REQ-SDS-278
function wiegandSpans(s, spans, tOf) {
  let cur = null;
  for (const sp of spans) {
    if (sp.kind === "sof") { cur = { bits: 0, fac: -1, card: -1, t: tOf(sp.i0) }; continue; }
    if (!cur) continue;
    if (sp.kind === "rw") cur.bits = sp.val;
    if (sp.kind === "data" && sp.text.startsWith("F")) cur.fac = sp.val;
    if (sp.kind === "data" && sp.text.startsWith("C")) {
      cur.card = sp.val;
      const key = cur.bits + "/" + cur.fac + "/" + cur.card;
      const c = s.cards.get(key) || Object.assign({ count: 0 }, cur);
      c.count++; c.t = cur.t;
      s.cards.set(key, c);
      s.lastCard = c;
      cur = null;
    }
  }
}

// ---- CAN with a DBC file -----------------------------------------------------------

// dbcParse reads a DBC file's messages and their signals: {id -> {name, dlc,
// signals: [{name, start, len, intel, signed, factor, offset, unit, mux}]}}
// (an extended id carries bit 31 in the file, cleared here).
// TRLC-LINKS: REQ-SDS-278
function dbcParse(text) {
  const msgs = new Map();
  let cur = null;
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    let m = /^BO_\s+(\d+)\s+(\w+)\s*:\s*(\d+)/.exec(line);
    if (m) {
      const id = Number(m[1]) & 0x1fffffff;
      cur = { id, name: m[2], dlc: +m[3], signals: [] };
      msgs.set(id, cur);
      continue;
    }
    m = /^SG_\s+(\w+)\s*(M|m\d+)?\s*:\s*(\d+)\|(\d+)@([01])([+-])\s*\(\s*([-+\d.eE]+)\s*,\s*([-+\d.eE]+)\s*\)\s*\[[^\]]*\]\s*"([^"]*)"/.exec(line);
    if (m && cur) {
      cur.signals.push({ name: m[1], mux: m[2] || "", start: +m[3], len: +m[4], intel: m[5] === "1", signed: m[6] === "-",
        factor: +m[7], offset: +m[8], unit: m[9] });
    }
  }
  return msgs;
}

// dbcRaw extracts a signal's raw value from a frame's bytes: Intel order from
// its least significant bit up, Motorola from its most significant bit
// (DBC's numbering) down through the bytes.
// TRLC-LINKS: REQ-SDS-278
function dbcRaw(sig, bytes) {
  // TRLC-LINKS: REQ-SDS-278
  const bit = (p) => (bytes[p >> 3] >> (p & 7)) & 1;
  let v = 0;
  if (sig.intel) {
    for (let k = sig.len - 1; k >= 0; k--) v = v * 2 + bit(sig.start + k);
  } else {
    let p = sig.start;
    for (let k = 0; k < sig.len; k++) {
      v = v * 2 + bit(p);
      p = p % 8 === 0 ? p + 15 : p - 1;
    }
  }
  if (sig.signed && sig.len > 0 && v >= 2 ** (sig.len - 1)) v -= 2 ** sig.len;
  return v;
}

// canSpans takes a frame's CAN frames: with a DBC file, each signal's newest
// value and its history; without one, each id's newest bytes.
// TRLC-LINKS: REQ-SDS-278
function canSpans(s, spans, tOf) {
  let cur = null;
  // TRLC-LINKS: REQ-SDS-278
  const done = () => {
    if (!cur || cur.bad || cur.id < 0) { cur = null; return; }
    s.frames.set(cur.id, { bytes: cur.bytes, t: cur.t, count: ((s.frames.get(cur.id) || {}).count || 0) + 1 });
    const msg = s.dbc && s.dbc.get(cur.id);
    if (msg) {
      const muxSig = msg.signals.find((g) => g.mux === "M");
      const muxVal = muxSig ? dbcRaw(muxSig, cur.bytes) : -1;
      for (const g of msg.signals) {
        if (g.mux.startsWith("m") && +g.mux.slice(1) !== muxVal) continue;
        const v = dbcRaw(g, cur.bytes) * g.factor + g.offset, key = msg.name + "." + g.name;
        const e = s.sigs.get(key) || { msg: msg.name, name: g.name, unit: g.unit, hist: [] };
        e.v = v; e.t = cur.t;
        e.hist.push({ t: cur.t, v });
        if (e.hist.length > 400) e.hist.shift();
        s.sigs.set(key, e);
      }
    }
    cur = null;
  };
  for (const sp of spans) {
    if (sp.kind === "sof") { done(); cur = { id: -1, bytes: [], bad: false, t: tOf(sp.i0) }; continue; }
    if (!cur) continue;
    if (sp.kind === "id") cur.id = sp.val;
    else if (sp.kind === "data") cur.bytes.push(sp.val);
    else if (sp.kind === "frame-error" || sp.kind === "crc-error" || sp.kind === "parity-error") cur.bad = true;
  }
  done();
}

// linSpans takes a frame's LIN frames: each identifier's newest response, its
// count, and the frames whose checksum or identifier parity failed (not the
// record's last: the record's end can cut its response short).
// TRLC-LINKS: REQ-SDS-278
function linSpans(s, spans, tOf) {
  let cur = null;
  // TRLC-LINKS: REQ-SDS-278
  const done = (last) => {
    if (cur && cur.id >= 0 && cur.ck) {
      if (cur.bad) { if (!last) s.errs++; }
      else s.frames.set(cur.id, { bytes: cur.bytes, t: cur.t, count: ((s.frames.get(cur.id) || {}).count || 0) + 1 });
    }
    cur = null;
  };
  for (const sp of spans) {
    if (sp.kind === "sof") { done(); cur = { id: -1, bytes: [], bad: false, ck: false, t: tOf(sp.i0) }; continue; }
    if (!cur || sp.kind === "rw") continue;
    const t = (sp.text || "").replace(/^!/, ""); // a failed field is marked "!"
    if (t.startsWith("ID")) { cur.id = parseInt(t.slice(2), 16); if (sp.kind !== "data") cur.bad = true; }
    else if (t.startsWith("CK")) { cur.ck = true; if (sp.kind !== "data") cur.bad = true; }
    else if (sp.kind === "data") cur.bytes.push(sp.val);
    else cur.bad = true;
  }
  done(true);
}

// ---- a protocol layer's values ----------------------------------------------------

// layerNotes takes packets' layer notes ("LM75 48 read Temperature 25.5 C"):
// each note's text without its numbers is its key, its numbers the values,
// so a sensor's readings keep one row that updates and a history to graph.
// TRLC-LINKS: REQ-SDS-278
function layerNotes(s, packets, t) {
  for (const p of packets) {
    if (!p.note) continue;
    const nums = (p.note.match(/-?\d+(?:\.\d+)?/g) || []).map(Number);
    const key = p.note.replace(/-?\d+(?:\.\d+)?/g, "#");
    const e = s.notes.get(key) || { key, hist: [], count: 0 };
    e.note = p.note; e.t = t; e.count++; e.err = !!p.noteErr;
    e.v = nums.length ? nums[nums.length - 1] : NaN;
    if (Number.isFinite(e.v)) { e.hist.push({ t, v: e.v }); if (e.hist.length > 300) e.hist.shift(); }
    s.notes.set(key, e);
  }
  if (s.notes.size > 200) s.notes.delete(s.notes.keys().next().value);
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { linSpans, midiInstrument, VIS_PROTOS, VIS_KINDS, visStreamed, visNew, visIngest, visIngestUnits, midiNoteName, midiByte, midiExpire, dmxRate, wsColours,
    pwmServo, dbcParse, dbcRaw, pdPDO, pdRDO, layerNotes, PS2_LAYOUT, PS2_KEYS };
