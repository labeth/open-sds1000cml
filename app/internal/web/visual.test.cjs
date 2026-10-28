// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-278, REQ-SDS-279, REQ-SDS-280
// The BUS Visual view's state (visual.js) and digital audio (audio_decode.js)
// from the page's own decoders: MIDI and DMX512 as UART waveforms, the scope's
// recorded PS/2, IR NEC, Wiegand, caliper and USB PD captures, PWM and a
// servo, a keyboard, a mouse, an LED strip, DCF77, a stepper, a terminal, CAN
// with a DBC file, a layer's notes; and I2S, S/PDIF and PDM carrying a sine,
// read back sample for sample.
"use strict";
const fs = require("node:fs"), vm = require("node:vm"), zlib = require("node:zlib"), assert = require("node:assert/strict");
const files = ["decode.js", "decode_manchester.js", "decode_sent.js", "decode_canfd.js", "decode_mil1553.js", "decode_arinc429.js", "decode_usbls.js",
  "decode_flexray.js", "decode_uartproto.js", "decode_pulsewire.js", "decode_ir.js", "decode_biphase.js", "decode_clocked.js", "decode_motion.js",
  "decode_rfcodes.js", "decode_rfid.js", "decode_wire1.js", "decode_wire2.js", "decode_irmp.js", "visual.js", "audio_decode.js"];
const c = { console, performance };
vm.createContext(c);
vm.runInContext(files.map((f) => fs.readFileSync(f, "utf8")).join("\n;\n") + "\n;globalThis.__ = { VIS_PROTOS, VIS_KINDS, VIS_KEEP, VIS_GAP, PS2_KEYS, PS2_LAYOUT };", c);
// every state the tests make, kept to draw at the end (a picture of real data)
const made = [];
{
  const orig = c.visNew;
  // TRLC-LINKS: REQ-SDS-278
  c.visNew = (p) => { const st = orig(p); made.push(st); return st; };
}

// deq compares by value across the vm's realm (its arrays are not this one's).
// TRLC-LINKS: REQ-SDS-278
function deq(a, b, m) { assert.equal(JSON.stringify(a), JSON.stringify(b), m); }

// TRLC-LINKS: REQ-SDS-278
function capture(name) {
  const d = JSON.parse(zlib.gunzipSync(fs.readFileSync("../decode/testdata/" + name)));
  return { c1: d.C1 || d.c1, c2: d.C2 || d.c2, dt: d.sample_s };
}

// uartWave is 8N1 (or 8N2) characters at baud sampled every dt, idle high, a
// break of breakS before each packet when breaks are asked for.
// TRLC-LINKS: REQ-SDS-278
function uartWave(packets, baud, dt, stop, breakS) {
  const out = [], spb = 1 / (baud * dt);
  // TRLC-LINKS: REQ-SDS-278
  const level = (v, bits) => { for (let i = 0; i < Math.round(bits * spb); i++) out.push(v ? 230 : 25); };
  level(1, 30);
  for (const p of packets) {
    if (breakS) { level(0, breakS * baud); level(1, 3); }
    for (const b of p) { level(0, 1); for (let k = 0; k < 8; k++) level((b >> k) & 1, 1); level(1, stop); }
    level(1, 6);
  }
  level(1, 30);
  return out;
}

// ---- MIDI: notes held and released, running status, controllers, the synth's events
{
  const dt = 1e-6;
  const w = uartWave([[0x90, 60, 100], [64, 90], [0x80, 60, 0], [0xB0, 7, 80], [0xC0, 5], [0x99, 36, 120], [0xE0, 0, 0x60]], 31250, dt, 1, 0);
  const r = c.decodeMIDI(w, dt, {});
  assert.ok(r.ok, "the MIDI waveform decodes: " + r.error);
  const s = c.visNew("midi");
  c.visIngest(s, r, 100, dt);
  deq([...s.held.values()].map((n) => [n.ch, n.note, n.vel]).sort(), [[0, 64, 90], [9, 36, 120]], "C4 released, E4 (running status) and the kick held");
  assert.equal(s.roll.length, 3, "three notes in the roll");
  assert.equal(s.cc[0][7], 80, "channel 1's volume");
  assert.equal(s.prog[0], 5, "channel 1's program");
  assert.ok(s.bend[0] > 0.4 && s.bend[0] < 0.6, "pitch bend up by half: " + s.bend[0]);
  deq(s.out.map((e) => e.type), ["on", "on", "off", "cc", "pc", "on", "bend"], "the synth's events in order");
  assert.equal(c.midiNoteName(60), "C4");
  c.midiExpire(s, 100 + 10);
  assert.equal(s.held.size, 0, "notes held past the limit end (a note-off lost to a gap)");
}

// ---- DMX512: a packet's levels from its break, start code and slots
{
  const dt = 0.5e-6;
  const vals = [0, 255, 128, 0, 42];
  const w = uartWave([vals, vals.map((v, i) => i === 2 ? 7 : v)], 250000, dt, 2, 120e-6);
  const r = c.decodeDMX512(w, dt, {});
  assert.ok(r.ok, "the DMX waveform decodes: " + r.error);
  const s = c.visNew("dmx512");
  c.visIngest(s, r, 0, dt);
  deq([...s.levels.slice(0, 4)], [255, 7, 0, 42], "channels 1-4 from the second packet");
  assert.equal(s.slots, 4);
  assert.equal(s.frames, 2);
  assert.equal(s.sc, 0, "start code 0: levels");
  // the rate is the bus's: packets 4.3 ms apart within a capture, captures 80 ms apart
  const q = c.visNew("dmx512");
  // TRLC-LINKS: REQ-SDS-278
  const pk = (i0) => [{ i0, i1: i0, kind: "sof", text: "BRK", val: 0 }, { i0, i1: i0, kind: "data", text: "SC00", val: 0 }, { i0, i1: i0, kind: "data", text: "7", val: 7 }];
  for (let cap = 0; cap < 6; cap++) c.visIngest(q, { ok: true, spans: [...pk(0), ...pk(4300), ...pk(8600)] }, cap * 0.08, 1e-6);
  assert.ok(Math.abs(c.dmxRate(q) - 1 / 0.0043) < 1, "233 packets/s, not the capture rate: " + c.dmxRate(q));
}

// ---- PS/2: the scope's keyboard capture, a typed word, a mouse
{
  const k = capture("scope_ps2_samples_500us.json.gz");
  const r = c.decodePS2(k.c1, k.c2, k.dt, {});
  assert.ok(r.ok, "the PS/2 capture decodes: " + r.error);
  const s = c.visNew("ps2");
  c.visIngest(s, r, 0, k.dt);
  assert.equal(s.lastKey, "", "F0 1C: A released, never pressed on screen");
  assert.ok(!s.keys.has("1C"), "A is not held");
  const t = c.visNew("ps2");
  for (const b of [0x12, 0x33, 0xF0, 0x33, 0xF0, 0x12, 0x24, 0xF0, 0x24, 0x4B, 0xF0, 0x4B, 0x4B, 0xF0, 0x4B, 0x44, 0xF0, 0x44, 0xE0, 0x75]) c.ps2Byte(t, b, false, 0);
  assert.equal(t.typed, "Hello", "Shift+h, e, l, l, o");
  assert.ok(t.keys.has("E075"), "Up held (an extended key)");
  assert.equal(t.kind, "kbd");
  const m = c.visNew("ps2");
  c.ps2Byte(m, 0xF2, true, 0); c.ps2Byte(m, 0xFA, false, 0); c.ps2Byte(m, 0x00, false, 0);
  for (const p of [[0x09, 5, 0], [0x28, 3, 0xFE], [0x08, 0, 0]]) for (const b of p) c.ps2Byte(m, b, false, 1);
  assert.equal(m.kind, "mouse", "Read ID answered 00: a mouse");
  assert.equal(m.x, 8); assert.equal(m.y, -2, "moves summed, Y's sign bit taken");
  assert.equal(m.btn, 0, "the left button released in the last packet");
  assert.equal(m.trail.length, 3);
  assert.ok(c.__.PS2_LAYOUT.length > 100 && c.__.PS2_KEYS.get("29").label === "Space", "the layout's keys");
}

// ---- PS/2: the device chosen stays chosen, whatever its bytes vote
{
  const s = c.visNew("ps2");
  s.kind = "mouse"; s.forced = true;
  for (const b of [0x1c, 0xf0, 0x1c, 0x1c, 0xf0, 0x1c, 0xe0, 0x74, 0xe0, 0xf0, 0x74]) c.ps2Byte(s, b, false, 0);
  assert.equal(s.kind, "mouse", "forced: a mouse");
  const u = c.visNew("ps2");
  for (const b of [0x08, 0x05, 0x02, 0x08, 0x03, 0x01]) c.ps2Byte(u, b, false, 0);
  for (const b of [0x1c, 0xf0, 0x1c, 0x1c, 0xf0, 0x1c, 0x1c, 0xf0, 0x1c, 0x1c, 0xf0, 0x1c]) c.ps2Byte(u, b, false, 1);
  assert.equal(u.kind, "kbd");
  assert.ok(u.trail.length === 0 && u.x === 0, "a keyboard known: the mouse parse of its bytes before is gone");
}

// ---- IR NEC from the scope: the address and command, then a repeat
{
  const k = capture("scope_irnec_search_20ms.json.gz");
  const r = c.decodeIRNEC(k.c1, k.dt, {});
  assert.ok(r.ok);
  const s = c.visNew("irnec");
  c.visIngest(s, r, 0, k.dt);
  assert.equal(s.lastBtn.addr, 4); assert.equal(s.lastBtn.cmd, 0x16, "A04 C16");
  assert.equal(s.buttons.size, 1);
}

// ---- LIN: each identifier's newest response; a failed checksum counts apart
{
  // TRLC-LINKS: REQ-SDS-278
  const sp = (kind, text, val) => ({ i0: 0, i1: 1, kind, text, val });
  const s = c.visNew("lin");
  c.visIngest(s, { ok: true, spans: [sp("sof", "BRK", 0), sp("rw", "SYNC", 0x55), sp("data", "ID02", 0xc2), sp("data", "12", 0x12), sp("data", "34", 0x34), sp("data", "CKCB", 0xcb),
    sp("sof", "BRK", 0), sp("rw", "SYNC", 0x55), sp("data", "ID00", 0x80), sp("data", "AB", 0xab), sp("frame-error", "!CK12", 0x12),
    sp("sof", "BRK", 0), sp("rw", "SYNC", 0x55), sp("data", "ID02", 0xc2), sp("data", "56", 0x56), sp("data", "CK77", 0x77)] }, 0, 1e-6);
  assert.equal(s.frames.size, 1, "one identifier with a good frame");
  deq(s.frames.get(2).bytes, [0x56]); assert.equal(s.frames.get(2).count, 2);
  assert.equal(s.errs, 1, "the bad checksum counted");
  c.visIngest(s, { ok: true, spans: [sp("sof", "BRK", 0), sp("rw", "SYNC", 0x55), sp("data", "ID02", 0xc2), sp("data", "56", 0x56), sp("data", "CK77", 0x77),
    sp("sof", "BRK", 0), sp("rw", "SYNC", 0x55), sp("data", "ID00", 0x80), sp("frame-error", "!CK12", 0x12)] }, 0, 1e-6);
  assert.equal(s.errs, 1, "the record's last frame, cut by its end, is no failure");
  assert.equal(s.frames.get(2).count, 3);
}

// ---- Sony sends the command before the address: the button has both
{
  // TRLC-LINKS: REQ-SDS-278
  const sp = (kind, text, val) => ({ i0: 0, i1: 1, kind, text, val });
  const s = c.visNew("irsirc");
  c.visIngest(s, { ok: true, spans: [sp("sof", "SIRC"), sp("data", "C15", 0x15), sp("data", "A01", 1), sp("sof", "SIRC"), sp("data", "C12", 0x12), sp("data", "A01", 1),
    sp("sof", "SIRC"), sp("frame-error", "!2bits")] }, 0, 1e-6);
  assert.equal(s.buttons.size, 2, "two buttons");
  assert.equal(s.lastBtn.addr, 1); assert.equal(s.lastBtn.cmd, 0x12, "A01 C12, the address after the command");
}

// ---- Wiegand and the caliper from the scope; USB PD's offer
{
  let k = capture("scope_wiegand_samples_5ms.json.gz");
  let r = c.decodeWiegand(k.c1, k.c2, k.dt, {});
  let s = c.visNew("wiegand");
  c.visIngest(s, r, 0, k.dt);
  assert.equal(s.lastCard.card, 4660); assert.equal(s.lastCard.fac, 42); assert.equal(s.lastCard.bits, 26);
  k = capture("scope_caliper_search_5ms.json.gz");
  r = c.decodeCaliper(k.c1, k.c2, k.dt, {});
  s = c.visNew("caliper");
  c.visIngest(s, r, 0, k.dt);
  assert.equal(s.text, "12.36mm"); assert.equal(s.value, 12.36); assert.equal(s.hist.length, 3);
  k = capture("scope_usbpd_raw_200us.json.gz");
  r = c.decodeUSBPD(k.c1, k.dt, {});
  s = c.visNew("usbpd");
  c.visIngest(s, r, 0, k.dt);
  deq(s.caps.map((x) => x.text), ["5 V 3 A"], "the source offers 5 V at 3 A");
  assert.equal(c.pdRDO((1 << 28) | (150 << 10) | 300, s.caps).text, "5 V 1.5 A", "a request of object 1 at 1.5 A");
  assert.equal(c.pdPDO((3 << 30) | (110 << 17) | (33 << 8) | 60).text, "3.3-11 V 3 A", "a PPS offer");
}

// ---- PWM: a duty and its frequency; a servo's angle
{
  const dt = 1e-6;
  // TRLC-LINKS: REQ-SDS-278
  const pwm = (periodS, highS, n) => { const w = []; for (let k = 0; k < n; k++) for (let i = 0; i < periodS / dt; i++) w.push(i < highS / dt ? 230 : 25); return w; };
  let r = c.decodePWM(pwm(1e-3, 0.3e-3, 8), dt, {});
  let s = c.visNew("pwm");
  c.visIngest(s, r, 0, dt);
  assert.ok(Math.abs(s.duty - 30) < 0.6, "30 % duty: " + s.duty);
  assert.ok(Math.abs(s.freq - 1000) < 5, "1 kHz: " + s.freq);
  assert.ok(Number.isNaN(c.pwmServo(s)), "no servo at 1 kHz");
  r = c.decodePWM(pwm(20e-3, 1.5e-3, 5), dt, {});
  s = c.visNew("pwm");
  c.visIngest(s, r, 0, dt);
  assert.ok(Math.abs(c.pwmServo(s) - 90) < 2, "a 1.5 ms pulse every 20 ms: the servo's centre, " + c.pwmServo(s));
}

// ---- an LED strip, DCF77, a stepper, a terminal
{
  const s = c.visNew("ws281x");
  const sp = [{ kind: "sof" }, ...[255, 0, 0, 0, 255, 0, 0, 0, 255].map((v) => ({ kind: "data", val: v })), { kind: "rw", text: "RST" }];
  c.visIngest(s, { spans: sp.map((x) => Object.assign({ i0: 0, i1: 0, text: "" }, x, x.text ? {} : { text: "" })) }, 0, 1);
  deq(c.wsColours(s.leds, "GRB"), [[0, 255, 0], [255, 0, 0], [0, 0, 255]], "GRB: green, red, blue");
  deq(c.wsColours([10, 20, 30, 5], "GRBW"), [[25, 15, 35]], "GRBW folds the white in");
  const d = c.visNew("dcf77");
  const f = [["MIN", 0x05], ["HR", 0x14], ["DAY", 0x27], ["DOW", 2], ["MON", 0x10], ["YR", 0x26]];
  c.visIngest(d, { spans: f.map(([n, v]) => ({ i0: 0, i1: 0, kind: "data", text: n + v.toString(16).toUpperCase(), val: v })) }, 0, 1);
  assert.equal(d.time, "14:05 Tue 27.10.2026");
  const st = c.visNew("stepper");
  c.visIngest(st, { spans: ["F1", "F2", "F3", "R2"].map((t, i) => ({ i0: i * 1000, i1: i * 1000, kind: "data", text: t, val: 1 })) }, 0, 1e-6);
  assert.equal(st.pos, 2); assert.equal(st.fwd, false, "the last step backward");
  const t = c.visNew("uart");
  for (const b of Buffer.from("Hi\r\nthere\x1b[31m!\x08?\x01")) c.visIngestUnits(t, [b], 0);
  assert.equal(t.text, "Hi\nthere?·", "CR LF one line, the escape dropped, backspace, a dot for a control byte");
}

// ---- CAN with a DBC file: Intel and Motorola signals, signed, scaled
{
  const dbc = 'BO_ 291 Engine: 8 ECU\n SG_ Rpm : 0|16@1+ (0.25,0) [0|16383] "rpm" X\n SG_ Temp : 23|8@0- (1,-40) [-40|215] "C" X\n';
  const msgs = c.dbcParse(dbc);
  assert.equal(msgs.get(291).signals.length, 2);
  const bytes = [0x10, 0x27, 0xF0, 0x05, 0, 0, 0, 0];
  assert.equal(c.dbcRaw(msgs.get(291).signals[0], bytes), 0x2710, "Intel: bytes 0-1 little-endian");
  assert.equal(c.dbcRaw(msgs.get(291).signals[1], bytes), -16, "Motorola: byte 2, signed");
  const s = c.visNew("can");
  s.dbc = msgs;
  c.visIngest(s, { spans: [{ kind: "sof", i0: 0, i1: 0 }, { kind: "id", i0: 1, i1: 1, val: 291 }, ...bytes.map((v) => ({ kind: "data", i0: 2, i1: 2, val: v })), { kind: "crc", i0: 3, i1: 3 }] }, 0, 1e-6);
  assert.equal(s.sigs.get("Engine.Rpm").v, 2500, "10000 x 0.25 rpm");
  assert.equal(s.sigs.get("Engine.Temp").v, -56, "-16 - 40 C");
}

// ---- a layer's notes: one row a reading, its numbers graphed
{
  const s = c.visNew("layer");
  c.layerNotes(s, [{ note: "LM75 48 read Temperature 25.5 C" }], 1);
  c.layerNotes(s, [{ note: "LM75 48 read Temperature 26 C" }], 2);
  assert.equal(s.notes.size, 1);
  const e = [...s.notes.values()][0];
  assert.equal(e.v, 26); assert.equal(e.hist.length, 2); assert.equal(e.count, 2);
}

// ---- I2S read off WS and SD: a 1 kHz sine at 48 kHz, 32-bit slots, both channels
{
  const fs = 48000, slot = 32, dt = 20e-9, bitS = 1 / (fs * 2 * slot), frames = 200;
  const ws = [], sd = [];
  // TRLC-LINKS: REQ-SDS-280
  const word = (k, ch) => Math.round((ch ? -0.25 : 0.5) * Math.sin(2 * Math.PI * 1000 * k / fs) * (2 ** 31 - 1));
  const bits = [];
  for (let k = 0; k < frames; k++) for (let ch = 0; ch < 2; ch++) {
    const w = word(k, ch) >>> 0;
    for (let b = 0; b < slot; b++) bits.push({ ws: ch, d: (w >>> (31 - b)) & 1 });
  }
  // I2S: the word's MSB one bit after WS changes
  const n = Math.floor(bits.length * bitS / dt);
  for (let i = 0; i < n; i++) {
    const t = i * dt / bitS, j = Math.floor(t);
    ws.push(bits[j].ws ? 230 : 25);
    const dj = j - 1;
    sd.push(dj >= 0 && bits[dj].d ? 230 : 25);
  }
  const a = c.audioFromI2S(ws, sd, dt, {});
  assert.ok(a.ok, "I2S decodes: " + a.error);
  assert.equal(a.slot, 32);
  assert.ok(Math.abs(a.fs - fs) < 50, "48 kHz: " + a.fs);
  const st = c.audioStats(a.ch[0], a.raw[0], a.bits);
  assert.ok(Math.abs(st.peakDb - 20 * Math.log10(0.5)) < 0.3, "the left channel's peak at -6 dBFS: " + st.peakDb);
  const k0 = 10;
  let best = Infinity;
  for (let off = 0; off < 3; off++) best = Math.min(best, Math.abs(a.ch[0][k0] - 0.5 * Math.sin(2 * Math.PI * 1000 * (k0 + off) / fs)));
  assert.ok(best < 1e-6, "the left samples are the sine's");
  const r = c.audioStats(a.ch[1], a.raw[1], a.bits);
  assert.ok(Math.abs(r.peakDb - 20 * Math.log10(0.25)) < 0.3, "the right channel's peak at -12 dBFS: " + r.peakDb);
  // digital silence: the data line held low is silence at the word select's rate, not "not I2S"
  const q = c.audioFromI2S(ws, ws.map(() => 25), dt, {});
  assert.ok(q.ok, "silence decodes: " + q.error);
  assert.ok(Math.abs(q.fs - fs) < 50 && q.still && q.ch[0].every((v) => v === 0), "silence at 48 kHz, said so: " + q.still);
  assert.equal(st.clip, 0);
}

// ---- S/PDIF: biphase-mark subframes of a 24-bit sine, read back exactly
{
  const fs = 48000, dt = 20e-9, ui = 1 / (fs * 128), frames = 96; // a half cell: 64 cells a frame
  const halves = []; // the line in half cells
  let lvl = 0;
  // TRLC-LINKS: REQ-SDS-280
  const put = (n) => { for (let i = 0; i < n; i++) halves.push(lvl); };
  // TRLC-LINKS: REQ-SDS-280
  const pre = (name) => { // the preambles' half-cell runs: B 3 1 1 3, M 3 3 1 1, W 3 2 1 2
    for (const r of { B: [3, 1, 1, 3], M: [3, 3, 1, 1], W: [3, 2, 1, 2] }[name]) { lvl ^= 1; put(r); }
  };
  // TRLC-LINKS: REQ-SDS-280
  const bit = (b) => { lvl ^= 1; put(1); if (b) lvl ^= 1; put(1); };
  const want = [];
  for (let k = 0; k < frames; k++) for (let ch = 0; ch < 2; ch++) {
    const v = Math.round((ch ? 0.1 : 0.7) * Math.sin(2 * Math.PI * 997 * k / fs) * 8388607);
    want.push(v);
    pre(ch ? "W" : k % 192 === 0 ? "B" : "M");
    const u = v & 0xffffff, bitsOut = [];
    for (let b = 0; b < 24; b++) bitsOut.push((u >> b) & 1);
    bitsOut.push(0, 0, 0); // V U C
    bitsOut.push(bitsOut.reduce((x, y) => x + y, 0) % 2); // even parity
    for (const b of bitsOut) bit(b);
  }
  const codes = [];
  for (let i = 0; i < halves.length * ui / dt; i++) codes.push(halves[Math.floor(i * dt / ui)] ? 230 : 25);
  const r = c.decodeSPDIF(codes, dt, {});
  assert.ok(r.ok, "S/PDIF decodes: " + r.error);
  const a = c.audioFromSPDIF(r);
  assert.ok(a.ok, a.error);
  assert.ok(Math.abs(a.fs - fs) < 1, "48 kHz: " + a.fs);
  assert.ok(a.ch[0].length >= frames - 2, "a sample a frame: " + a.ch[0].length);
  // the first whole frame's left and right, as sent (the record opens on a preamble)
  const L = a.raw[0], R = a.raw[1];
  let o = -1;
  for (let k = 0; k < frames && o < 0; k++) if ([1, 2, 3, 4, 5].every((j) => want[2 * (k + j - 1)] === L[j])) o = k - 1;
  assert.ok(o >= -1 && [1, 2, 3, 4, 5].every((j) => want[2 * (o + j)] === L[j]), "five left samples read back exactly");
  assert.ok([1, 2, 3, 4, 5].every((j) => want[2 * (o + j) + 1] === R[j]), "and their right samples");
  assert.equal(c.audioStats(a.ch[0], a.raw[0], 24).bitsUsed, 24, "all 24 bits in use");
}

// ---- PDM: a first-order sigma-delta of a sine at 3.072 MHz, filtered to 48 kHz
{
  const fclk = 3.072e6, dt = 25e-9, per = 1 / fclk, nClk = 64 * 300;
  const clk = [], data = [];
  let acc = 0;
  const bitsL = [];
  for (let k = 0; k < nClk; k++) {
    const x = 0.5 * Math.sin(2 * Math.PI * 1000 * k / fclk);
    const y = acc >= 0 ? 1 : -1;
    acc += x - y;
    bitsL.push(y > 0 ? 1 : 0);
  }
  const n = Math.floor(nClk * per / dt);
  for (let i = 0; i < n; i++) {
    const t = i * dt, k = Math.floor(t / per), ph = t / per - k;
    clk.push(ph < 0.5 ? 230 : 25);
    data.push(bitsL[Math.min(k, nClk - 1)] ? 230 : 25); // held a whole clock: read on both edges alike
  }
  const a = c.audioFromPDM(clk, data, dt);
  assert.ok(a.ok, "PDM decodes: " + a.error);
  assert.ok(Math.abs(a.fs - 48000) < 400, "48 kHz: " + a.fs);
  const st = c.audioStats(a.ch[0], null, 0);
  assert.ok(Math.abs(st.peakDb - 20 * Math.log10(0.5)) < 1.5, "the sine's level comes through: " + st.peakDb);
}

// ---- every bus's state, fed random spans and stream bytes, never throws and
// keeps its lists, tables and text bounded (a noisy bus runs for hours)
{
  const KINDS = ["data", "data", "data", "sof", "rw", "id", "frame-error", "parity-error", "crc-error"];
  const TEXTS = ["", "H12", "A04", "C16", "REP", "LDR", "ID02", "CK12", "!CK12", "26b", "F42", "C4660", "12.34mm", "R-5", "F7", "MIN12", "HR3", "DAY9", "SrcCap", "Request", "BRK", "SC00", "RST", "M", "W", "B", "50.0%"];
  let seed = 11;
  // TRLC-LINKS: REQ-SDS-278
  const rnd = (n) => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed % n; };
  // TRLC-LINKS: REQ-SDS-278
  const size = (v, path, out, depth) => {
    if (depth > 4 || v === null || typeof v !== "object") { if (typeof v === "string") out.push([path, v.length, 25000]); return; }
    if (v instanceof Map || v instanceof Set) { out.push([path, v.size, 2048]); for (const x of v.values()) size(x, path + "[]", out, depth + 1); return; }
    if (ArrayBuffer.isView(v)) return;
    if (Array.isArray(v)) { out.push([path, v.length, 4096]); for (const x of v.slice(0, 50)) size(x, path + "[]", out, depth + 1); return; }
    for (const k of Object.keys(v)) size(v[k], path + "." + k, out, depth + 1);
  };
  for (const proto of Object.keys(c.__.VIS_KINDS)) {
    const s = c.visNew(proto);
    for (let f = 0; f < 3000; f++) {
      const spans = [];
      for (let k = 0, n = 1 + rnd(30); k < n; k++)
        spans.push({ i0: k * 10, i1: k * 10 + 9, kind: KINDS[rnd(KINDS.length)], text: rnd(3) ? TEXTS[rnd(TEXTS.length)] : String(rnd(1e6)), val: rnd(4) ? rnd(256) : rnd(1 << 29) });
      c.visIngest(s, { ok: true, spans, meta: { baud: rnd(3) ? 0 : 1 + rnd(1e6), spb: 1 + rnd(100) } }, f * 0.01, 1e-6);
      if (rnd(3) === 0) c.visIngestUnits(s, Array.from({ length: 1 + rnd(40) }, () => rnd(256)), f * 0.01 + 0.005);
    }
    const out = [];
    size(s, proto, out, 0);
    for (const [path, n, max] of out) assert.ok(n <= max, path + " grew to " + n + " (bounded to " + max + ")");
  }
}

// ---- a table of what a bus sent keeps its newest VIS_KEEP: 5000 CAN ids
{
  const s = c.visNew("can"), spans = [];
  for (let id = 0; id < 5000; id++) spans.push({ i0: id, i1: id, kind: "sof", text: "SOF", val: 0 }, { i0: id, i1: id, kind: "id", text: "", val: id }, { i0: id, i1: id, kind: "data", text: "", val: id & 255 });
  c.visIngest(s, { ok: true, spans }, 0, 1e-6);
  assert.equal(s.frames.size, c.__.VIS_KEEP, "the CAN ids kept are bounded");
  assert.ok(s.frames.has(4999) && !s.frames.has(0), "the newest kept, the oldest gone");
}

// ---- MIDI from captures: each capture's own pushes and releases, its span;
// the stream, gap-free, keeps none (every note's length is seen)
{
  const s = c.visNew("midi");
  // TRLC-LINKS: REQ-SDS-278
  const d = (i0, val) => ({ i0, i1: i0 + 1, kind: "data", text: "", val });
  c.visIngest(s, { ok: true, spans: [d(10, 0x90), d(11, 60), d(12, 100), d(20, 0x80), d(21, 64), d(22, 0)] }, 5, 1e-3);
  deq(s.cap.map((e) => [e.on, e.note]), [[true, 60], [false, 64]], "a push of C4, a release of E4 (pushed before the capture)");
  assert.ok(Math.abs(s.capT0 - 5) < 1e-9 && s.capT1 > s.cap[1].t, "the capture's span holds its events");
  c.visIngest(s, { ok: true, spans: [d(5, 0x80), d(6, 60), d(7, 0)] }, 9, 1e-3);
  deq(s.cap.map((e) => [e.on, e.note]), [[false, 60]], "the next capture's own: the one before is gone");
  c.visIngestUnits(s, [0x90, 62, 90], 12);
  assert.equal(s.cap, null, "read gap-free: no capture's events");
}

// ---- gap-free: the scope's stream lines (as the bench's buses streamed them)
// gathered into frames and read by each bus that streams
{
  // TRLC-LINKS: REQ-SDS-278
  const feed = (proto, lines) => {
    const s = c.visNew(proto), S = {}, out = [], bw = !!c.__.VIS_KINDS[proto].bytewise;
    lines.forEach((l, i) => c.visStreamLine(S, { i, n: l[0], v: l[1] }, i * 0.01, out, bw));
    c.visStreamFlush(S, true, out);
    for (const f of out) c.visIngestUnits(s, f.units, f.t, f.note);
    return s;
  };
  // DMX: a packet goes on past a line's sixteen units, unnoted
  const slots = Array.from({ length: 21 }, (_, k) => 2 + 7 * k);
  let s = feed("dmx512", [["BRK", [0, ...slots.slice(0, 15)]], ["", slots.slice(15)], ["BRK", [0, ...slots.slice(0, 15)]], ["", slots.slice(15)]]);
  assert.equal(s.slots, 21, "a packet across two lines is one: 21 slots");
  assert.equal(s.sc, 0); assert.equal(s.levels[20], slots[20], "its last slot's level");
  // LIN: identifier, response, checksum
  s = feed("lin", [["BRK", [0xc1, 0x12, 0x34, 0xab, 0x4c]], ["BRK", [0x80, 0x12, 0x34, 0xab, 0x8d]], ["BRK", [0x42, 0x12, 0x34, 0xab, 0xcb]], ["BRK", [0xc1, 0x12, 0x34, 0xab, 0x4c]], ["BRK", [0x42, 0x12, 0x34, 0xab, 0x00]]]);
  deq([...s.frames.keys()].sort(), [0, 1, 2], "identifiers 0, 1, 2");
  deq(s.frames.get(1).bytes, [0x12, 0x34, 0xab]); assert.equal(s.errs, 1, "a bad checksum counted");
  assert.ok(Math.abs(s.frames.get(1).period - 0.03) < 1e-9, "identifier 1 every 30 ms (gap-free)");
  // IR: NEC with its repeats, Sony's command first, RC-5's address first
  s = feed("irnec", [["LDR", [0x00, 0xff, 0x16, 0xe9]], ["REP", []], ["REP", []], ["LDR", [0x04, 0xfb, 0x16, 0xe9]]]);
  assert.equal(s.lastBtn.addr, 4); assert.equal(s.lastBtn.cmd, 0x16); assert.equal(s.buttons.get("0/22").repeats, 2, "two repeats of A0 C16");
  s = feed("irsirc", [["SIRC", [0x15, 0x01]]]);
  assert.ok(s.lastBtn.addr === 1 && s.lastBtn.cmd === 0x15, "Sony: command, then address");
  s = feed("irrc5", [["RC5", [0x05, 0x10]]]);
  assert.ok(s.lastBtn.addr === 5 && s.lastBtn.cmd === 0x10, "RC-5: address, then command");
  assert.ok(!c.visStreamed("irrc6") && c.visStreamed("irnec"), "only the IR buses whose stream is read stream");
  // Wiegand, the caliper, steps
  s = feed("wiegand", [["", [0x2a, 0x1234]], ["", [0x2a, 0x4321]], ["", [0x2a, 0x1234]]]);
  assert.ok(s.lastCard.fac === 42 && s.lastCard.card === 0x1234 && s.cards.get("42/4660").count === 2, "cards 4660 twice, 17185 once");
  s = feed("caliper", [["", [0xd4, 0x04, 0x00]]]);
  assert.equal(s.text, "12.36mm");
  s = feed("quad", [["", Array(16).fill(1)], ["", Array(4).fill(1)], ["", Array(16).fill(0)], ["", Array(14).fill(0)]]);
  assert.ok(s.pos === 4 * 20 - 4 * 30 && s.gapless && !s.fwd, "quadrature: 20 cycles on, 30 back: -40 counts, every one");
  s = feed("stepper", [["", Array(14).fill(1)], ["", [...Array(15).fill(0), 1]]]);
  assert.equal(s.pos, 14 - 15 + 1, "a stepper's steps one by one");
  // a byte stream's lines go in at once (MIDI's lines are all full)
  s = feed("midi", [["", [0x90, 60, 100, 0x80, 60, 0, 0x90, 64, 90, 0x90, 67, 90, 0x90, 72, 90, 0x80]], ["", [64, 0]]]);
  assert.equal(s.msgs, 6, "every message, the last across two lines");
}

// ---- from captures: PS/2's keys are the capture's own; a terminal's and the
// typed text's captures parted by a gap mark
{
  const s = c.visNew("ps2");
  // TRLC-LINKS: REQ-SDS-278
  const d = (val) => ({ i0: 0, i1: 1, kind: "data", text: "", val });
  c.visIngest(s, { ok: true, spans: [d(0x1c)] }, 0, 1e-3); // A pressed (its release in the gap)
  c.visIngest(s, { ok: true, spans: [d(0x32), d(0xf0), d(0x32)] }, 1, 1e-3); // B pressed and released
  deq(s.cap.map((e) => [e.code, e.down]), [["32", true], ["32", false]], "the capture's own presses and releases");
  assert.ok(s.typed.includes(c.__.VIS_GAP), "the typed text parted where a capture's gap was: " + JSON.stringify(s.typed));
  const u = c.visNew("uart");
  c.visIngest(u, { ok: true, spans: [d(0x48), d(0x69)] }, 0, 1e-3);
  c.visIngest(u, { ok: true, spans: [d(0x21)] }, 1, 1e-3);
  assert.equal(u.text, "Hi" + c.__.VIS_GAP + "!", "the terminal's captures parted");
  c.visIngestUnits(u, [0x3f], 2, "");
  assert.equal(u.text, "Hi" + c.__.VIS_GAP + "!?", "the stream, gap-free: no mark");
}

// ---- a MIDI recording as a Standard MIDI File: read back, its messages at their times
{
  const s = c.visNew("midi");
  s.rec = [];
  // TRLC-LINKS: REQ-SDS-278
  const play = (t, bytes) => { for (const b of bytes) c.midiByte(s, b, t); };
  play(10.0, [0xc0, 5]); play(10.0, [0x90, 60, 100]); play(10.5, [64, 90]); play(11.0, [0x80, 60, 0]); play(12.25, [0xfe]); play(12.5, [0x80, 64, 0]);
  assert.equal(s.rec.length, 5, "the channel messages recorded (the running-status one too), not active sensing");
  const f = c.midiSMF(s.rec);
  // TRLC-LINKS: REQ-SDS-278
  const u32 = (i) => (f[i] << 24 | f[i + 1] << 16 | f[i + 2] << 8 | f[i + 3]) >>> 0;
  assert.equal(String.fromCharCode(...f.slice(0, 4)), "MThd"); assert.equal(u32(4), 6);
  assert.ok(f[9] === 0 && f[11] === 1 && (f[12] << 8 | f[13]) === 480, "format 0, one track, 480 a quarter");
  assert.equal(String.fromCharCode(...f.slice(14, 18)), "MTrk");
  const end = 22 + u32(18);
  assert.equal(end, f.length, "the track's length is its bytes");
  let i = 22, tick = 0;
  const got = [];
  while (i < end) {
    let d = 0;
    do { d = d * 128 + (f[i] & 0x7f); } while (f[i++] & 0x80);
    tick += d;
    if (f[i] === 0xff) { const type = f[i + 1], len = f[i + 2]; got.push(["meta", type, tick]); i += 3 + len; continue; }
    const st = f[i], n = (st & 0xf0) === 0xc0 || (st & 0xf0) === 0xd0 ? 1 : 2;
    got.push([tick, ...f.slice(i, i + 1 + n)]); i += 1 + n;
  }
  deq(got, [["meta", 0x51, 0], [0, 0xc0, 5], [0, 0x90, 60, 100], [480, 0x90, 64, 90], [960, 0x80, 60, 0], [2400, 0x80, 64, 0], ["meta", 0x2f, 2400]],
    "the tempo, then each message at its time (960 ticks a second), then the end");
}

// ---- I2S read at four samples a bit (the capture's plan): the same words
{
  const fs = 48000, slot = 16, bitS = 1 / (fs * 2 * slot), dt = bitS / 4, frames = 120, ws = [], sd = [], bits = [];
  for (let k = 0; k < frames; k++) for (let ch = 0; ch < 2; ch++) {
    const w = Math.round((ch ? 0.25 : 0.5) * Math.sin(2 * Math.PI * 1000 * k / fs) * 32767) & 0xffff;
    for (let b = 0; b < slot; b++) bits.push({ ws: ch, d: (w >> (15 - b)) & 1 });
  }
  for (let i = 0; i < Math.floor(bits.length * bitS / dt); i++) {
    const j = Math.floor(i * dt / bitS);
    ws.push(bits[j].ws ? 230 : 25); sd.push(j >= 1 && bits[j - 1].d ? 230 : 25);
  }
  const a = c.audioFromI2S(ws, sd, dt, {});
  assert.ok(a.ok && a.slot === 16 && Math.abs(a.fs - fs) < 100, "I2S at four samples a bit: " + (a.error || a.slot + " " + a.fs));
  const st = c.audioStats(a.ch[0], a.raw[0], a.bits);
  assert.ok(Math.abs(st.peakDb + 6.02) < 0.1, "its left at -6 dBFS: " + st.peakDb);
}

// ---- a short record's spectrum says so: no distortion figure from bins too wide
{
  const x = Float32Array.from({ length: 128 }, (_, i) => 0.5 * Math.sin(2 * Math.PI * 2010 * i / 48000));
  const a = c.audioSpectrum(x, 48000);
  assert.ok(a.ok && Number.isNaN(a.thdDb) && /too short/.test(a.why), "128 points at 48 kHz (375 Hz a bin): no THD, said why: " + a.why);
}

// ---- the audio's spectrum: a tone's frequency and level, its distortion and noise
{
  const fs = 48000, n = 8192;
  // TRLC-LINKS: REQ-SDS-280
  const tone = (h3) => Float32Array.from({ length: n }, (_, i) => {
    const v = 0.5 * Math.sin(2 * Math.PI * 1000 * i / fs) + h3 * Math.sin(2 * Math.PI * 3000 * i / fs);
    return Math.round(v * 32767) / 32768; // 16 bits
  });
  let a = c.audioSpectrum(tone(0), fs);
  assert.ok(a.ok && Math.abs(a.toneHz - 1000) < 2, "the tone at 1 kHz: " + a.toneHz);
  assert.ok(Math.abs(a.toneDb + 6.02) < 0.2, "at -6 dBFS: " + a.toneDb);
  assert.ok(a.thdDb < -80, "a clean tone's distortion: " + a.thdDb.toFixed(1) + " dB");
  assert.ok(a.enob > 13.5 && a.enob < 16.5, "16 bits at -6 dBFS: about 15 bits make: " + a.enob.toFixed(2));
  a = c.audioSpectrum(tone(0.005), fs); // a third harmonic 40 dB under the tone
  assert.ok(Math.abs(a.thdDb + 40) < 0.5, "THD -40 dB: " + a.thdDb.toFixed(2));
  assert.ok(Math.abs(a.sinadDb - 40) < 0.5, "SINAD 40 dB (the harmonic is the rest): " + a.sinadDb.toFixed(2));
}

// ---- the synth's voices: random notes, retriggers and controllers keep each
// voice once in its order, never more than SYNTH_VOICES
{
  const sc = { console, performance, Math };
  vm.createContext(sc);
  vm.runInContext(fs.readFileSync("midi_synth.js", "utf8") + "\n;globalThis.__s = { synthNew, synthNoteOn, synthNoteOff, synthCC, synthPanic, SYNTH_VOICES };", sc);
  // TRLC-LINKS: REQ-SDS-279
  const nop = () => {};
  // TRLC-LINKS: REQ-SDS-279
  const param = () => ({ value: 0, setValueAtTime: nop, linearRampToValueAtTime: nop, setTargetAtTime: nop, exponentialRampToValueAtTime: nop, cancelScheduledValues: nop });
  // TRLC-LINKS: REQ-SDS-279
  const node = () => ({ gain: param(), frequency: param(), detune: param(), connect: nop, start: nop, stop: nop, type: "" });
  const S = sc.__s, sy = S.synthNew();
  sy.ctx = { currentTime: 0, createGain: node, createOscillator: node, createBiquadFilter: node, createBufferSource: node };
  sy.out = node();
  let seed = 7;
  // TRLC-LINKS: REQ-SDS-279
  const rnd = (n) => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed % n; };
  for (let i = 0; i < 20000; i++) {
    const ch = rnd(3), note = 40 + rnd(24), r = rnd(10);
    if (r < 5) S.synthNoteOn(sy, ch, note, 80, i * 0.01);
    else if (r < 9) S.synthNoteOff(sy, ch, note, i * 0.01);
    else S.synthCC(sy, ch, [64, 64, 123][rnd(3)], rnd(128), i * 0.01);
    if (sy.order.length !== new Set(sy.order).size) assert.fail("a key twice in the order at step " + i);
    for (const k of sy.order) if (!sy.voices.has(k)) assert.fail("the order holds a voice ended at step " + i);
    if (sy.voices.size > S.SYNTH_VOICES) assert.fail("more voices than SYNTH_VOICES at step " + i);
  }
  S.synthPanic(sy);
  assert.equal(sy.voices.size + sy.order.length, 0, "All notes off leaves none");
}

// ---- the decode strip's rows (app_decode.js decRows): a span enclosing others
// on the top row, its labelled parts on the bottom, the rest the whole band
{
  const src = fs.readFileSync("app_decode.js", "utf8").match(/\n(\/\/ TRLC-LINKS: REQ-SDS-207\nfunction decRows[\s\S]*?\n}\n)/)[1];
  const rc = {};
  vm.createContext(rc);
  vm.runInContext(src + ";globalThis.__r = decRows;", rc);
  // TRLC-LINKS: REQ-SDS-207
  const sp = (kind, text, i0, i1) => ({ kind, text, i0, i1 });
  const caliper = [sp("sof", "", 0, 5), sp("data", "D4", 0, 90), sp("data", "04", 100, 180), sp("data", "00", 190, 270), sp("rw", "12.34mm", 0, 270), sp("data", "AA", 400, 480)];
  deq(Array.from(rc.__r({ spans: caliper })), [0, 2, 2, 2, 1, 0], "the reading on top, its bytes under it, the start and a byte apart the whole band");
  const wiegand = [sp("sof", "", 0, 2), sp("rw", "26b", 0, 500), sp("data", "F42", 20, 200), sp("data", "C4660", 220, 480)];
  deq(Array.from(rc.__r({ spans: wiegand })), [0, 1, 2, 2], "Wiegand's length on top, its facility and card both shown under it");
}

// ---- every bus has a view, and every view draws ----------------------------------------
// app_visual_draw.js in the same realm with the page's few globals it reads; a
// 2D context that takes any call and fails one with a coordinate not finite
{
  vm.runInContext(`var dpr = 1, st = null, dcfg = { baud: 0 };
    var vis = { wsOrder: "GRB", midiLead: 0, canPick: new Set(), sound: false, sy: null, ps2Kind: "", dbcName: "", layer: { notes: new Map() } };
    function eng(x, u) { return Number.isFinite(x) ? x + " " + u : "- " + u; }
    function escHTML(s) { return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;"); }
    function fmtTdiv(s) { return s + " s"; }
    function $() { return null; }
    function toast() {}
    function visLag() { return 0; }`, c);
  vm.runInContext(fs.readFileSync("app_visual_draw.js", "utf8") + "\n;globalThis.__views = { VIS_VIEWS, VIS_KINDS, visView, visDrawLayer, visLayerTable };", c);
  const bad = [];
  // TRLC-LINKS: REQ-SDS-278
  const ctx = (who) => new Proxy({}, {
    // TRLC-LINKS: REQ-SDS-278
    get: (o, k) => (k in o ? o[k] : (...args) => {
      for (const v of args) if (typeof v === "number" && !Number.isFinite(v)) { bad.push(who + ": " + String(k) + "(" + args.join(", ") + ")"); break; }
      return { width: 10 };
    }),
    // TRLC-LINKS: REQ-SDS-278
    set: (o, k, v) => { o[k] = v; return true; },
  });
  const V = c.__views;
  for (const [name, k] of Object.entries(c.__views.VIS_KINDS)) {
    const v = V.visView(name);
    assert.ok(v && typeof v.draw === "function" && typeof v.figures === "function", name + " has a view: a picture and figures");
    assert.ok(k.title && typeof k.make === "function" && typeof k.spans === "function", name + " has a title, a state and its decode's way in");
  }
  for (const key of Object.keys(V.VIS_VIEWS)) assert.ok(V.VIS_KINDS[key] && V.VIS_KINDS[key].key === key, "the view " + key + " is a bus's of VIS_KINDS");
  // each bus's empty state, then every state the tests above filled
  const states = Object.keys(V.VIS_KINDS).map((p) => c.visNew(p)).concat(made);
  for (const s of states) {
    const v = V.visView(s.proto);
    if (!v) continue;
    for (const [W, H] of [[800, 400], [320, 180], [60, 60]]) {
      c.vis.midiLead = 0; v.draw(ctx(s.proto + " " + W + "x" + H), W, H, s);
      if (s.proto === "midi") { c.vis.midiLead = 3; v.draw(ctx("midi falling " + W + "x" + H), W, H, s); c.vis.midiLead = 0; }
    }
    assert.equal(typeof v.figures(s), "string", s.proto + "'s figures");
    if (v.controls) assert.equal(typeof v.controls(s), "string", s.proto + "'s controls");
  }
  V.visDrawLayer(ctx("layer"), 400, 200);
  assert.equal(typeof V.visLayerTable(), "string");
  assert.deepEqual(bad, [], "every picture draws with finite coordinates");
}

console.log("visual ok");
