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
vm.runInContext(files.map((f) => fs.readFileSync(f, "utf8")).join("\n;\n") + "\n;globalThis.__ = { VIS_PROTOS, PS2_KEYS, PS2_LAYOUT };", c);

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

console.log("visual ok");
