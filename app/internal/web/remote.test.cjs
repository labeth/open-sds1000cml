// ENGMODEL-OWNER-UNIT: FU-WEB-REMOTE
// remote.test.cjs — the phone remote's pure logic (remote_core.js): numbers
// as the scope writes them, the value a detent reaches, typed-entry and
// match-byte hints, search with everyday words, the history's diff, merge
// and undo targets, and the knob-job line. Run by TestRemoteCoreJS.
"use strict";
const assert = require("node:assert/strict");
const RC = require("./remote_core.js");

let n = 0;
// TRLC-LINKS: REQ-SDS-220
function test(name, fn) {
  fn();
  n++;
  console.log("ok  - " + name);
}

// A small sheet model in the device's shape (/api/sheets).
// TRLC-LINKS: REQ-SDS-220
function sheets() {
  const ladder = [];
  for (let d = 1e-9; d <= 50.0001; d *= 10) ladder.push(d, 2 * d, 5 * d);
  return {
    Seq: 3,
    Tabs: [
      { ID: "trig", Name: "TRIG", Title: "Trigger", Key: "Trig Menu", Rows: [
        { ID: "trig.edge", Label: "Source/slope", Kind: "enum", Available: true, Help: "Channel the trigger watches.", Value: 0, Text: "C1 Rise",
          Options: [{ Value: 0, Text: "C1 Rise" }, { Value: 1, Text: "C1 Fall" }, { Value: 2, Text: "C2 Rise" }, { Value: 3, Text: "C2 Fall" }] },
        { ID: "trig.level", Label: "Level", Kind: "number", Available: true, Help: "Voltage the trigger source must cross.", Value: 0, Text: "0V",
          Unit: "V", Min: -4, Max: 4, Step: 0.04, Presets: ["0 V", "50 %"] },
        { ID: "trig.holdoff", Label: "Holdoff", Kind: "number", Available: true, Help: "Minimum time between triggers.", Value: 0, Text: "Off",
          Unit: "s", Min: 0, Max: 10, Ladder: [0, 1e-4, 1e-3, 1e-2, 0.1, 1], Presets: ["Off", "100 us", "1 ms"] },
        { ID: "trig.pulse_when", Label: "Pulse when", Kind: "enum", Available: false, Why: "Type = Pulse", Section: "PULSE  (used when Type = Pulse)",
          Help: "Which pulse widths trigger.", Value: 0, Text: "Any", Options: [{ Value: 0, Text: "Any" }, { Value: 1, Text: "< min" }] },
        { ID: "trig.pulse_level", Label: "Pulse level", Kind: "number", Available: false, Why: "Type = Pulse", Help: "", Value: 50, Text: "50 %",
          Unit: "%", Min: 5, Max: 95, Step: 5 },
      ] },
      { ID: "ch1", Name: "CH1", Title: "Channel 1", Key: "1", Rows: [
        { ID: "ch1.coupling", Label: "Coupling", Kind: "enum", Available: true, Help: "DC, AC (blocks DC) or ground.", Value: 0, Text: "DC",
          Options: [{ Value: 0, Text: "DC" }, { Value: 1, Text: "AC" }, { Value: 2, Text: "GND" }] },
        { ID: "ch1.probe", Label: "Probe", Kind: "enum", Available: true, Help: "Probe attenuation; scales every reading.", Value: 1, Text: "1x",
          Options: [{ Value: 1, Text: "1x" }, { Value: 10, Text: "10x" }, { Value: 100, Text: "100x" }] },
        { ID: "ch1.vdiv", Label: "V/div", Kind: "number", Available: true, Help: "Vertical scale of the channel.", Value: 1, Text: "1V", Unit: "V",
          Min: 0.002, Max: 10, Ladder: [0.002, 0.005, 0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10] },
        { ID: "ch1.offset", Label: "Offset", Kind: "number", Available: true, Help: "Vertical position of the channel (volts).", Value: 0, Text: "0V",
          Unit: "V", Min: -40, Max: 40, Step: 0.2, Presets: ["0 V"] },
        { ID: "ch1.bwl", Label: "BW limit 20M", Kind: "enum", Available: true, Help: "Bandwidth limit.", Value: 0, Text: "Off",
          Options: [{ Value: 0, Text: "Off" }, { Value: 1, Text: "On" }] },
      ] },
      { ID: "time", Name: "TIME", Title: "Horizontal", Key: "Hori Menu", Rows: [
        { ID: "time.tdiv", Label: "Time/div", Kind: "number", Available: true, Help: "Seconds per horizontal division.", Value: 5e-4, Text: "500us",
          Unit: "s", Min: 1e-9, Max: 50, Ladder: ladder },
      ] },
      { ID: "acq", Name: "ACQ", Title: "Acquire", Key: "Acquire", Rows: [
        { ID: "acq.mode", Label: "Mode", Kind: "enum", Available: true, Help: "Normal, average, enhanced resolution or peak detect.", Value: 0, Text: "Normal",
          Options: [{ Value: 0, Text: "Normal" }, { Value: 1, Text: "Average" }, { Value: 2, Text: "ERes" }, { Value: 3, Text: "Peak" }] },
        { ID: "acq.averages", Label: "Averages", Kind: "enum", Available: false, Why: "Mode = Average", Help: "Captures averaged.", Value: 16, Text: "16",
          Options: [{ Value: 4, Text: "4" }, { Value: 16, Text: "16" }, { Value: 64, Text: "64" }] },
        { ID: "acq.run", Label: "Acquisition", Kind: "enum", Available: true, Section: "RUN CONTROL", Help: "", Value: 0, Text: "Run",
          Options: [{ Value: 0, Text: "Run" }, { Value: 1, Text: "Stop" }, { Value: 2, Text: "Single" }] },
      ] },
      { ID: "bus", Name: "BUS", Title: "Bus decode, serial trigger", Key: "Utility", Rows: [
        { ID: "bus.baud", Label: "Baud", Kind: "enum", Available: true, Section: "UART AND LINE BUSES", Help: "UART bit rate.", Value: 115200, Text: "115k2",
          Options: [{ Value: 9600, Text: "9k6" }, { Value: 115200, Text: "115k2" }] },
        { ID: "bus.match", Label: "Match bytes", Kind: "bytes", Available: false, Section: "SERIAL TRIGGER", Why: "a Protocol (decode on)",
          Help: "Bytes the serial trigger waits for.", Value: "", Text: "any byte" },
        { ID: "bus.i2c_address", Label: "I2C address", Kind: "number", Available: false, Help: "", Value: -1, Text: "Any", Min: -1, Max: 127, Step: 1 },
      ] },
      { ID: "file", Name: "FILE", Title: "Setups", Key: "Save/Recall", Rows: [
        { ID: "file.setup3", Label: "Setup  3", Kind: "action", Available: true, Help: "", Text: "empty",
          Actions: [{ ID: "save", Text: "Save", Available: true }, { ID: "recall", Text: "Recall", Available: false }] },
      ] },
      { ID: "sys", Name: "SYS", Title: "System and help", Key: "Help", Rows: [
        { ID: "sys.print", Label: "Print screen", Kind: "action", Available: true, Help: "Save the LCD as a PNG.", Text: "",
          Actions: [{ ID: "print", Text: "Save PNG", Available: true }] },
        { ID: "sys.default", Label: "Default setup", Kind: "action", Available: true, Help: "Factory settings.", Text: "",
          Actions: [{ ID: "default", Text: "Factory reset", Available: true }] },
      ] },
    ],
  };
}

// set returns a copy of a model with row id at value v (and text t).
// TRLC-LINKS: REQ-SDS-220
function withRow(model, id, v, t) {
  const m = { rows: new Map(model.rows), tabs: model.tabs };
  m.rows.set(id, Object.assign({}, m.rows.get(id), { Value: v, Text: t }));
  return m;
}

// ---- formatting --------------------------------------------------------------------------
test("si writes three figures with an SI prefix, as the LCD does", () => {
  assert.equal(RC.si(500e-6, "s"), "500us");
  assert.equal(RC.si(1.25, "V"), "1.25V");
  assert.equal(RC.si(-0.02, "V"), "-20mV");
  assert.equal(RC.si(0, "V"), "0V");
  assert.equal(RC.si(0.0439078, "V"), "43.9mV");
  assert.equal(RC.si(999.6, "Hz"), "1kHz", "the prefix boundary is rounding-aware");
  assert.equal(RC.si(115200, "b/s"), "115kb/s");
  assert.equal(RC.si(2e-9, "s"), "2ns");
  assert.equal(RC.sgnSI(0.2, "V"), "+200mV");
  assert.equal(RC.sgnSI(-0.2, "V"), "-200mV");
  assert.equal(RC.g3(0.5), "0.5");
  assert.equal(RC.g3(100), "100");
});
test("fmtValue follows each row's own format", () => {
  const m = RC.index(sheets());
  assert.equal(RC.fmtValue(m.rows.get("trig.holdoff"), 0), "Off");
  assert.equal(RC.fmtValue(m.rows.get("trig.holdoff"), 1e-3), "1ms");
  assert.equal(RC.fmtValue(m.rows.get("ch1.offset"), 0.4), "+400mV");
  assert.equal(RC.fmtValue(m.rows.get("trig.pulse_level"), 55), "55 %");
  assert.equal(RC.fmtValue(m.rows.get("bus.i2c_address"), -1), "Any");
  assert.equal(RC.fmtValue(m.rows.get("bus.i2c_address"), 80), "0x50");
  assert.equal(RC.fmtValue({ ID: "curs.a", Unit: "div" }, 3.6), "3.60 div");
  assert.equal(RC.fmtValue({ ID: "trig.video_line", Unit: "" }, 10), "10");
});
test("pretty spaces numbers from units; compact drops /div and trailing zeros", () => {
  assert.equal(RC.pretty("500us"), "500 us");
  assert.equal(RC.pretty("+200mV"), "+200 mV");
  assert.equal(RC.pretty("115kb/s"), "115 kb/s");
  assert.equal(RC.pretty("0V"), "0 V");
  for (const same of ["50 %", "3.50 div", "Off", "C1 Rise", "0x50", "any byte", "Any"]) assert.equal(RC.pretty(same), same);
  assert.equal(RC.compact("3.50 div"), "3.5 div");
  assert.equal(RC.compact("500 us/div"), "500 us");
  assert.equal(RC.compact("1.0 V"), "1 V");
});

// ---- the value a detent reaches ----------------------------------------------------------
test("a ladder row steps to its next ladder value; right is clockwise (toward ns and mV)", () => {
  const m = RC.index(sheets());
  const t = m.rows.get("time.tdiv");
  assert.equal(RC.turn(t, +1).text, "200us", "clockwise: the faster timebase");
  assert.equal(RC.turn(t, -1).text, "1ms");
  assert.equal(RC.turn(t, +1, 2).text, "100us", "two detents");
  const v = m.rows.get("ch1.vdiv");
  assert.equal(RC.turn(v, +1).text, "500mV", "clockwise toward mV, as the printed arc");
  assert.equal(RC.turn(Object.assign({}, v, { Value: 0.002 }), +1), null, "the end of the ladder");
  const h = m.rows.get("trig.holdoff");
  assert.equal(RC.turn(h, +1).text, "100us", "holdoff counts up clockwise");
  assert.equal(RC.turn(h, -1), null);
  assert.equal(RC.turn(Object.assign({}, h, { Value: 0.0015 }), -1).text, "1ms", "a value between steps goes to the step below");
});
test("a stepped row moves by its step and stops at its range", () => {
  const m = RC.index(sheets());
  const l = m.rows.get("trig.level");
  assert.equal(RC.turn(l, +1).value, 0.04);
  assert.equal(RC.turn(l, -1).text, "-40mV");
  assert.equal(RC.turn(Object.assign({}, l, { Value: 3.99 }), +1).value, 4, "clamped to the range, as the knob");
  assert.equal(RC.turn(Object.assign({}, l, { Value: 4 }), +1), null, "at the end");
  const o = m.rows.get("ch1.offset");
  assert.equal(RC.turn(o, +1, 3).value, 0.6, "three steps, no float noise");
  assert.equal(RC.turn(o, +1, 3).text, "+600mV");
});
test("an option row steps to the next option", () => {
  const m = RC.index(sheets());
  assert.equal(RC.neighbour(m.rows.get("acq.mode"), +1).text, "Average");
  assert.equal(RC.neighbour(m.rows.get("acq.mode"), -1), null);
  assert.equal(RC.neighbour(m.rows.get("trig.edge"), +1).value, 1);
});

// ---- typed entry -------------------------------------------------------------------------
test("parseNumber reads SI text in the row's unit, with the device's errors", () => {
  assert.deepEqual(RC.parseNumber("1.25V", "V"), { ok: true, value: 1.25 });
  assert.deepEqual(RC.parseNumber("1.25", "V"), { ok: true, value: 1.25 });
  assert.equal(RC.parseNumber("500u", "s").value, 500e-6);
  assert.equal(RC.parseNumber("500 us", "s").value, 500e-6);
  assert.equal(RC.parseNumber("-20 mV", "V").value, -0.02);
  assert.equal(RC.parseNumber("2k", "").value, 2000);
  assert.equal(RC.parseNumber("50%", "%").value, 50);
  assert.equal(RC.parseNumber("0x1F", "").value, 31);
  assert.equal(RC.parseNumber("1e-3", "s").value, 1e-3);
  assert.equal(RC.parseNumber("2 volts", "V").value, 2);
  assert.equal(RC.parseNumber("abc", "V").error, 'not a number: "abc"');
  assert.equal(RC.parseNumber("1.25Hz", "V").error, 'unit "Hz" does not fit (want V)');
  assert.equal(RC.parseNumber("5 mA", "V").error, 'unit "A" does not fit (want V)');
  assert.equal(RC.parseNumber("", "V").error, "empty value");
  assert.equal(RC.parseNumber("3x", "").error, 'unit "x" does not fit (want a plain number)');
  assert.equal(RC.parseNumber("2,5V", "V").value, 2.5, "a comma is the decimal point on a European keyboard");
  assert.equal(RC.parseNumber("115,2k", "b/s").value, 115200);
  assert.equal(RC.parseNumber("1,2,3", "").error, 'unit ",2,3" does not fit (want a plain number)', "two commas are not a decimal point");
  assert.equal(RC.parseNumber("57,600", "Bd").error, 'the comma in "57,600" reads two ways: type it without the comma, or with a point',
    "a comma before three digits could be a thousands separator: refused, not guessed");
  assert.equal(RC.parseNumber("0,125", "V").value, 0.125, "after 0 it is a decimal point");
});
test("a negative value typed reads as itself in the refusal (not Off, not 0x-1)", () => {
  const m = RC.index(sheets());
  assert.equal(RC.numberHint(m.rows.get("trig.holdoff"), "-5").error, "holdoff -5s is outside Off .. 10s");
  assert.equal(RC.numberHint(m.rows.get("bus.i2c_address"), "-2").error, "i2c address -0x02 is outside Any .. 0x7F");
  assert.equal(RC.fmtValue({ ID: "bus.stream_value", Unit: "" }, -1), "-0x01");
  assert.equal(RC.fmtValue({ ID: "bus.stream_value", Unit: "" }, 255), "0xFF");
  assert.equal(RC.fmtValue(m.rows.get("trig.holdoff"), 0), "Off");
});
test("numberHint checks the range and says when the scope takes the nearest step", () => {
  const m = RC.index(sheets());
  const l = m.rows.get("trig.level");
  assert.deepEqual(RC.numberHint(l, "1.25"), { ok: true, value: 1.25, text: "1.25V", note: "" });
  assert.equal(RC.numberHint(l, "9V").error, "level 9V is outside -4V .. 4V", "never clamped: refused as the device does");
  assert.equal(RC.numberHint(l, "fast").error, 'Level: not a number: "fast"');
  assert.ok(RC.numberHint(l, "").empty);
  const t = m.rows.get("time.tdiv");
  assert.equal(RC.numberHint(t, "200us").note, "", "a step");
  assert.equal(RC.numberHint(t, "300us").note, "the scope takes the nearest step, 200 us");
  assert.equal(RC.numberHint(m.rows.get("trig.holdoff"), "off").value, 0, "holdoff takes Off");
  assert.equal(RC.numberHint(m.rows.get("bus.i2c_address"), "any").value, -1, "the I2C address takes Any");
});
test("numKey edits the keypad text; unit keys replace the unit; digits go in before it", () => {
  const units = ["mV", "V"];
  let b = "";
  for (const k of ["1", ".", "2", ".", "5"]) b = RC.numKey(b, k, units);
  assert.equal(b, "1.25", "a second point is ignored");
  b = RC.numKey(b, "mV", units);
  assert.equal(b, "1.25mV");
  b = RC.numKey(b, "V", units);
  assert.equal(b, "1.25V", "a unit replaces the unit");
  b = RC.numKey(b, "0", units);
  assert.equal(b, "1.250V", "digits go before the unit");
  b = RC.numKey(b, "back", units);
  assert.equal(b, "1.250", "Del takes the whole unit");
  b = RC.numKey(b, "-", units);
  assert.equal(b, "-1.250");
  b = RC.numKey(b, "-", units);
  assert.equal(b, "1.250");
  assert.equal(RC.numKey("1.250", "clear", units), "");
  assert.deepEqual(RC.unitKeys({ Unit: "s" }), ["ns", "us", "ms", "s"]);
  assert.deepEqual(RC.unitKeys({ Unit: "", Max: 100000 }), ["k"]);
  assert.deepEqual(RC.unitKeys({ Unit: "Hz", Max: 125e6 }), ["k", "M"], "a rate in Hz takes k and M, not nine digits");
  assert.equal(RC.parseNumber("104M", "Hz").value, 104e6);
  assert.deepEqual(RC.unitKeys({ Unit: "", Max: 127 }), []);
});

test("a row the scope writes in hex gets a hex keypad: A-F make it 0x.., 0x toggles the prefix", () => {
  const m = RC.index(sheets());
  const a = m.rows.get("bus.i2c_address");
  assert.ok(RC.isHex(a) && RC.isHex({ ID: "bus.stream_value" }) && !RC.isHex(m.rows.get("trig.level")));
  assert.deepEqual(RC.unitKeys({ ID: "bus.stream_value", Unit: "", Max: 65535 }), [], "no k key on a hex row");
  let b = "";
  for (const k of ["5", "0"]) b = RC.hexNumKey(b, k);
  assert.equal(b, "50", "digits alone stay decimal");
  b = RC.hexNumKey(b, "0x");
  assert.equal(b, "0x50", "0x makes them hex");
  assert.equal(RC.numberHint(a, b).text, "0x50");
  assert.equal(RC.numberHint(a, b).value, 80);
  b = RC.hexNumKey("", "A");
  assert.equal(b, "0xA", "a letter brings 0x");
  b = RC.hexNumKey(b, "F");
  assert.equal(b, "0xAF");
  assert.equal(RC.numberHint(a, b).error, "i2c address 0xAF is outside Any .. 0x7F", "refused as the device does");
  assert.equal(RC.hexNumKey("0xA", "back"), "0x");
  assert.equal(RC.hexNumKey("0x", "back"), "", "Del takes 0x back with the last digit");
  assert.equal(RC.hexNumKey("0x50", "0x"), "50");
  assert.equal(RC.hexNumKey("0x50", "clear"), "");
  assert.equal(RC.hexNumKey("0x12345678", "9"), "0x12345678", "ten characters at most");
});

// ---- match bytes ------------------------------------------------------------------------------
test("parseMatch reads hex tokens and quoted text as the device does", () => {
  assert.deepEqual(RC.parseMatch("55 AA 0F").tokens, ["55", "AA", "0F"]);
  assert.deepEqual(RC.parseMatch("55aa").tokens, ["55", "AA"]);
  assert.deepEqual(RC.parseMatch("55,AA").tokens, ["55", "AA"]);
  assert.deepEqual(RC.parseMatch("?? A? ERR").tokens, ["??", "A?", "ERR"]);
  assert.deepEqual(RC.parseMatch('"Hi"').tokens, ["48", "69"]);
  assert.deepEqual(RC.parseMatch('"Hi\\r\\n"').tokens, ["48", "69", "0D", "0A"]);
  assert.deepEqual(RC.parseMatch("'\\x41\\\\'").tokens, ["41", "5C"]);
  assert.deepEqual(RC.parseMatch("").tokens, [], "empty: any byte");
  assert.deepEqual(RC.parseMatch("any byte").tokens, []);
  assert.equal(RC.parseMatch("5").error, 'bad byte "5": use hex pairs like 55 AA, ?? or ERR, or "text" in quotes');
  assert.equal(RC.parseMatch("55 ZZ").error, 'bad byte "ZZ": use hex pairs like 55 AA, ?? or ERR, or "text" in quotes');
  assert.equal(RC.parseMatch('"\\q"').error, "unknown escape \\q");
  assert.equal(RC.parseMatch('""').error, "empty text");
  assert.equal(RC.parseMatch("00 ".repeat(33)).error, "33 bytes: 32 at most");
  assert.deepEqual(RC.parseMatch('"é"').tokens, ["C3", "A9"], "text is sent as UTF-8 bytes");
});
test("hexKey groups pairs, takes ?? as one byte, Del takes it back whole", () => {
  let b = "";
  for (const k of ["5", "5", "A", "A", "0"]) b = RC.hexKey(b, k);
  assert.equal(b, "55 AA 0");
  b = RC.hexKey(b, "??");
  assert.equal(b, "55 AA 0 ??");
  b = RC.hexKey(b, "back");
  assert.equal(b, "55 AA 0");
  b = RC.hexKey(b, "back");
  assert.equal(b, "55 AA");
  b = RC.hexKey(b, " ");
  assert.equal(b, "55 AA ");
  assert.equal(RC.hexKey(b, " "), "55 AA ", "one space");
  assert.equal(RC.hexKey("55", "clear"), "");
  assert.equal(RC.quoteText('Say "Hi"\\r\\n'), '"Say \\"Hi\\"\\r\\n"', "quotes escaped, escapes kept");
  assert.equal(RC.quoteText("a\\b"), '"a\\\\b"', "a stray backslash is escaped");
  assert.deepEqual(RC.parseMatch(RC.quoteText("Hi\\x0D")).tokens, ["48", "69", "0D"]);
});

// ---- the model and search ---------------------------------------------------------------------
test("index carries sections down and splits their notes", () => {
  const m = RC.index(sheets());
  assert.equal(m.tabs.length, 7);
  assert.equal(m.rows.get("trig.pulse_when").section, "PULSE");
  assert.equal(m.rows.get("trig.pulse_when").sectionNote, "Used when Type = Pulse");
  assert.equal(m.rows.get("trig.pulse_level").section, "PULSE", "carried down to the rows below");
  assert.equal(m.rows.get("trig.level").section, "");
  assert.ok(RC.isSwitch(m.rows.get("ch1.bwl")) && !RC.isSwitch(m.rows.get("ch1.coupling")));
  assert.ok(RC.isSlot(m.rows.get("file.setup3")) && !RC.isSlot(m.rows.get("sys.print")));
  assert.equal(RC.label(m.rows.get("ch1.vdiv")), "CH1 V/div");
  assert.equal(RC.label(m.rows.get("trig.level")), "Trigger level");
  assert.equal(RC.label(m.rows.get("file.setup3")), "Setup 3", "double spaces of the LCD label folded");
});
test("search ranks names first and finds rows by everyday words", () => {
  const m = RC.index(sheets());
  // TRLC-LINKS: REQ-SDS-220
  const ids = (q) => RC.search(m, q).map((x) => x.id);
  assert.equal(ids("holdoff")[0], "trig.holdoff");
  assert.equal(ids("hold")[0], "trig.holdoff", "a prefix");
  assert.equal(ids("coupling")[0], "ch1.coupling");
  assert.equal(ids("averaging")[0], "acq.averages", "averaging -> average");
  assert.ok(ids("attenuation").indexOf("ch1.probe") >= 0, "attenuation -> probe");
  assert.ok(ids("screenshot").indexOf("sys.print") >= 0, "screenshot -> print");
  assert.ok(ids("factory").indexOf("sys.default") >= 0, "factory -> default");
  assert.ok(ids("sensitivity").indexOf("ch1.vdiv") >= 0, "sensitivity -> V/div");
  assert.ok(ids("timebase").indexOf("time.tdiv") >= 0, "timebase -> time/div");
  assert.ok(ids("baudrate").indexOf("bus.baud") >= 0);
  assert.ok(ids("bandwidth").indexOf("ch1.bwl") >= 0, "bandwidth -> BW");
  assert.ok(ids("glitch").indexOf("trig.pulse_when") >= 0, "glitch -> pulse");
  assert.deepEqual(ids("trig level"), ["trig.level", "trig.pulse_level"], "the name match first");
  assert.deepEqual(ids("trig coupling"), [], "every word must match");
  assert.deepEqual(ids("ch1 ac"), ["ch1.coupling"], "an option word and the tab");
  assert.deepEqual(ids("zzz"), []);
  assert.ok(RC.matchPlace({ label: "History", words: "undo changes log" }, "undo"));
  assert.ok(!RC.matchPlace({ label: "History", words: "undo changes log" }, "undo zz"));
});

test("search knows scope words, phrases and one-letter misspellings", () => {
  // TRLC-LINKS: REQ-SDS-220
  const row = (id, label, extra) => Object.assign({ ID: id, Label: label, Kind: "enum", Available: true, Help: "", Value: 0, Text: "" }, extra);
  const onoff = { Options: [{ Value: 0, Text: "Off" }, { Value: 1, Text: "On" }] };
  const m = RC.index({ Tabs: [
    { ID: "trig", Name: "TRIG", Title: "Trigger", Rows: [row("trig.edge", "Source/slope", { Options: [{ Value: 0, Text: "C1 Rise" }, { Value: 1, Text: "C1 Fall" }] }),
      row("trig.level", "Level", { Kind: "number", Unit: "V" })] },
    { ID: "ch1", Name: "CH1", Title: "Channel 1", Rows: [row("ch1.offset", "Offset", { Kind: "number", Unit: "V" }), row("ch1.coupling", "Coupling", { Options: [{ Value: 0, Text: "DC" }, { Value: 1, Text: "AC" }] })] },
    { ID: "time", Name: "TIME", Title: "Horizontal", Rows: [row("time.tdiv", "Time/div", { Kind: "number", Unit: "s" })] },
    { ID: "acq", Name: "ACQ", Title: "Acquire", Rows: [row("acq.mode", "Mode", { Options: [{ Value: 0, Text: "Normal" }, { Value: 1, Text: "Average" }] }),
      row("acq.depth", "Memory depth", { Options: [{ Value: 2048, Text: "2k" }] })] },
    { ID: "view", Name: "VIEW", Title: "Display", Rows: [row("view.persist", "Persistence", onoff)] },
    { ID: "meas", Name: "MEAS", Title: "Measure", Rows: [row("meas.pkpk", "Peak-peak"), row("meas.rise", "Rise"), row("meas.fall", "Fall")] },
    { ID: "curs", Name: "CURS", Title: "Cursors", Rows: [row("curs.mode", "Cursors")] },
    { ID: "sys", Name: "SYS", Title: "System and help", Rows: [row("sys.print", "Print screen", { Kind: "action", Actions: [{ ID: "print", Text: "Save PNG", Available: true }] })] },
  ] });
  // TRLC-LINKS: REQ-SDS-220
  const top = (q) => (RC.search(m, q)[0] || {}).id;
  // TRLC-LINKS: REQ-SDS-220
  const all = (q) => RC.search(m, q).map((x) => x.id);
  for (const [q, id] of [["vpp", "meas.pkpk"], ["pk-pk", "meas.pkpk"], ["peak to peak", "meas.pkpk"], ["rise time", "meas.rise"], ["record length", "acq.depth"],
    ["screen-shot", "sys.print"], ["screen shot", "sys.print"], ["persistance", "view.persist"], ["cusor", "curs.mode"], ["offest", "ch1.offset"]]) {
    assert.equal(top(q), id, q);
  }
  assert.ok(all("rising").includes("trig.edge") && all("rising").includes("meas.rise"), "rising: the rising edge and the rise time");
  assert.ok(all("falling").includes("trig.edge") && all("falling").includes("meas.fall"));
  assert.deepEqual(all("sample rate").sort(), ["acq.depth", "time.tdiv"], "the sample rate follows time/div and the memory depth");
  assert.ok(all("averge").includes("acq.mode"), "averge, one letter short of Average");
  assert.ok(all("tigger").includes("trig.level"), "tigger, one letter short of trigger");
  assert.deepEqual(all("zzzzz"), [], "nothing like it");
  assert.deepEqual(all("trig coupling"), [], "every word must still match");
  assert.deepEqual(all("rose"), [], "a word under 5 letters is not guessed at");
  assert.ok(RC.within1("offest", "offset") && RC.within1("averge", "average") && RC.within1("cusor", "cursor") && !RC.within1("cursor", "curves"));
});

// ---- history ----------------------------------------------------------------------------------
test("History books a change with old and new values, newest first", () => {
  const m0 = RC.index(sheets()), h = new RC.History();
  const a = RC.snapshot(m0, "scope");
  const m1 = withRow(withRow(m0, "ch1.probe", 10, "10x"), "ch1.vdiv", 10, "10V");
  const b = RC.snapshot(m1, "scope");
  const rows = RC.diff(a, b);
  assert.deepEqual(rows.map((r) => r.id).sort(), ["ch1.probe", "ch1.vdiv"]);
  const e = h.add("phone", rows, { primary: "ch1.probe" }, 1000);
  assert.equal(e.rows[0].id, "ch1.probe", "the row the user changed first, its side effect after it");
  assert.equal(e.rows[0].old.text, "1x");
  assert.equal(e.rows[0].neu.text, "10x");
  assert.ok(e.undoable && e.primary && !e.single);
  assert.equal(h.list.length, 1);
});
test("steps on one row from one source within 3 s are one entry; back to the start removes it", () => {
  const h = new RC.History();
  // TRLC-LINKS: REQ-SDS-220
  const step = (from, to, t, src) => h.add(src || "phone", [{ id: "time.tdiv", tab: "TIME", label: "Time/div", kind: "number",
    old: { value: from, text: RC.si(from, "s"), key: JSON.stringify(from) }, neu: { value: to, text: RC.si(to, "s"), key: JSON.stringify(to) } }], {}, t);
  step(5e-4, 2e-4, 0);
  step(2e-4, 1e-4, 1000);
  step(1e-4, 5e-5, 2500);
  assert.equal(h.list.length, 1, "merged");
  assert.equal(h.list[0].n, 3);
  assert.equal(h.list[0].rows[0].old.text, "500us");
  assert.equal(h.list[0].rows[0].neu.text, "50us");
  step(5e-5, 2e-5, 6000);
  assert.equal(h.list.length, 2, "more than 3 s later: a new entry");
  step(2e-5, 1e-5, 6500, "scope");
  assert.equal(h.list.length, 3, "another source: a new entry");
  const k = new RC.History();
  // TRLC-LINKS: REQ-SDS-220
  const s2 = (from, to, t) => k.add("phone", [{ id: "x", tab: "T", label: "x", kind: "number", old: { value: from, text: "" + from, key: "" + from },
    neu: { value: to, text: "" + to, key: "" + to } }], {}, t);
  s2(1, 2, 0);
  s2(2, 1, 500);
  assert.equal(k.list.length, 0, "stepped back to where it started");
});
test("a row the scope sets itself joins the change that caused it; undo reverts both", () => {
  const h = new RC.History();
  // TRLC-LINKS: REQ-SDS-220
  const row = (id, label, from, fromText, to, toText) => ({ id, tab: "TIME", label, kind: "number",
    old: { value: from, text: fromText, key: JSON.stringify(from) }, neu: { value: to, text: toText, key: JSON.stringify(to) } });
  // TRLC-LINKS: REQ-SDS-220
  const tdiv = (from, to, t) => h.add("phone", [row("time.tdiv", "Time/div", from, RC.si(from, "s"), to, RC.si(to, "s"))], {}, t);
  // TRLC-LINKS: REQ-SDS-220
  const depth = (from, to, t) => h.add("scope", [row("acq.depth", "Memory depth", from, from + " (auto)", to, to + " (auto)")], {}, t);
  tdiv(2e-5, 2e-4, 0);
  depth("500k", "1M", 400);   // booked a read later, as the scope's
  assert.equal(h.list.length, 1, "the depth is part of the timebase change");
  assert.deepEqual(h.list[0].rows.map((r) => r.id), ["time.tdiv", "acq.depth"]);
  tdiv(2e-4, 5e-4, 900);
  depth("1M", "2M", 1200);
  assert.equal(h.list.length, 1, "knob steps still merge with the depth inside");
  assert.equal(h.list[0].rows[0].neu.text, "500us");
  assert.equal(h.list[0].rows[1].neu.text, "2M (auto)");
  const t = h.targets([h.list[0]]);
  assert.deepEqual(t.rows.map((r) => r.id), ["time.tdiv"], "undo puts the timebase back; the scope derives the depth again");
  assert.equal(t.rows[0].text, "20us");
  depth("2M", "4M", 9000);
  assert.equal(h.list.length, 2, "a depth change long after any change: its own entry");
  const m = new RC.History();
  m.add("phone", [row("time.tdiv", "Time/div", 2e-5, "20us", 2e-4, "200us")], {}, 0);
  m.add("scope", [row("acq.depth", "Memory depth", "500k", "500k", "1M", "1M (auto)")], {}, 300);
  assert.deepEqual(m.targets([m.list[0]]).rows.map((r) => r.id), ["time.tdiv", "acq.depth"], "a depth set by hand before is put back");
});
test("undo targets restore old values; back-to-before takes each row's oldest value", () => {
  const h = new RC.History();
  // TRLC-LINKS: REQ-SDS-220
  const r = (id, a, b) => ({ id, tab: "T", label: id, kind: "number", old: { value: a, text: "" + a, key: "" + a }, neu: { value: b, text: "" + b, key: "" + b } });
  const e1 = h.add("phone", [r("a", 1, 2)], { label: "first" }, 0);
  const e2 = h.add("scope", [r("a", 2, 3), r("b", 10, 20)], {}, 100);
  const e3 = h.add("phone", [], { ws: { from: "scope", to: "bus" } }, 200);
  assert.ok(e3.undoable && e3.kind === "ws", "a workspace switch undoes");
  const one = h.targets([e2]);
  assert.deepEqual(one.rows.map((x) => [x.id, x.value]).sort(), [["a", 2], ["b", 10]]);
  const all = h.targets(h.since(e1));
  assert.deepEqual(all.rows.map((x) => [x.id, x.value]).sort(), [["a", 1], ["b", 10]], "the oldest old value per row");
  assert.equal(all.ws, "scope", "and the workspace it started in");
  assert.deepEqual(h.since(e2).map((e) => e.id), [e3.id, e2.id]);
  // an undo and the entry it undid cancel out: back to before them keeps the row as it was then
  const k = new RC.History();
  const c1 = k.add("phone", [r("cpl", "DC", "AC")], {}, 0);
  const anchor = k.add("phone", [r("proto", "Off", "UART")], {}, 5000);
  const u = k.add("phone", [r("cpl", "AC", "DC")], { kind: "undo", label: "Undo: c1" }, 10000);
  c1.undone = true;
  k.add("phone", [r("cpl", "DC", "AC")], { kind: "undo", label: "Undo: undo" }, 15000);
  u.undone = true;
  const back = k.targets(k.since(anchor));
  assert.deepEqual(back.rows.map((x) => [x.id, x.value]).sort(), [["cpl", "AC"], ["proto", "Off"]], "coupling stays AC, the protocol goes back");
  const act = h.add("phone", [], { kind: "action", label: "FILE · Setup 3: Save" }, 300);
  assert.ok(!act.undoable, "a save is listed without undo");
  const def = h.add("phone", [r("a", 3, 1)], { kind: "action", label: "Default setup", undoable: true }, 400);
  assert.ok(def.undoable, "Default setup undoes");
  h.extend(act, [r("c", 0, 1)]);
  assert.equal(act.rows.length, 1, "side effects found later join their entry");
  const late = h.add("phone", [r("side", 0, 1)], { primary: "main" }, 450);
  assert.ok(!late.primary, "the row the user changed has not shown yet");
  h.extend(late, [r("main", 5, 6)]);
  assert.ok(late.primary && late.rows[0].id === "main" && late.rows.length === 2, "when it shows it goes first, its side effect after it");
  const restored = new RC.History(JSON.parse(JSON.stringify(h.list)));
  assert.equal(restored.list.length, h.list.length, "kept across a reload");
  assert.ok(restored.add("phone", [r("z", 0, 1)], {}, 500).id > def.id, "ids continue");
});
test("an undo entry keeps the ids of the entries it undid (a redo puts them back in effect)", () => {
  const h = new RC.History();
  // TRLC-LINKS: REQ-SDS-220
  const r = (id, a, b) => ({ id, tab: "T", label: id, kind: "enum", old: { value: a, text: a, key: JSON.stringify(a) }, neu: { value: b, text: b, key: JSON.stringify(b) } });
  const e = h.add("phone", [r("cpl", "DC", "AC")], {}, 0);
  const u = h.add("phone", [r("cpl", "AC", "DC")], { kind: "undo", label: "Undo: c", undid: [e.id] }, 5000);
  assert.deepEqual(u.undid, [e.id]);
  assert.equal(u.kind, "undo");
  assert.ok(u.undoable, "an undo can itself be undone");
  assert.equal(e.undid, undefined, "a plain change undid nothing");
  const back = new RC.History(JSON.parse(JSON.stringify(h.list)));
  assert.deepEqual(back.list[0].undid, [e.id], "kept across a reload");
});
test("stored History of another shape is dropped when it loads", () => {
  const row = { id: "a", tab: "T", label: "a", kind: "enum", old: { key: "0", text: "0" }, neu: { key: "1", text: "1" } };
  const good = { id: 3, t: 1, src: "phone", rows: [row] };
  const h = new RC.History([good, { id: 1, t: 1, src: "phone" }, { id: 2, t: 1, src: "phone", rows: [{ id: "time.tdiv" }], undoable: true }, null, 5, "x",
    Object.assign({}, good, { id: 4, ws: "bus" }), Object.assign({}, good, { id: 6, rows: [Object.assign({}, row, { old: null })] })]);
  assert.deepEqual(h.list.map((e) => e.id), [3], "only the entry of the shape the page shows");
  assert.equal(new RC.History({ list: [] }).list.length, 0, "not a list");
  assert.equal(h.add("scope", [row], {}, 2).id, 4, "ids go on after the kept ones");
  assert.ok(RC.okEntry(good) && !RC.okEntry({ id: 1 }));
});
test("History keeps at most 100 entries; ago reads like a phone", () => {
  const h = new RC.History();
  for (let i = 0; i < 120; i++) h.add("scope", [{ id: "r" + i, tab: "T", label: "r", kind: "enum", old: { key: "0" }, neu: { key: "1" } }], {}, i * 10000);
  assert.equal(h.list.length, 100);
  assert.equal(RC.ago(2000), "now");
  assert.equal(RC.ago(12000), "12 s ago");
  assert.equal(RC.ago(180000), "3 min ago");
  assert.equal(RC.ago(7200000), "2 h ago");
});
test("snapshot follows settings and setup slots, not the run state or live statuses", () => {
  const m = RC.index(sheets());
  const s = RC.snapshot(m, "bus");
  assert.ok(s.rows.has("trig.level") && s.rows.has("bus.match") && s.rows.has("file.setup3"));
  assert.ok(!s.rows.has("acq.run") && !s.rows.has("sys.print"));
  assert.equal(s.rows.get("file.setup3").kind, "slot");
  assert.equal(s.ws, "bus");
});

// ---- the knob-job line ------------------------------------------------------------------------
test("tools reads the scope's tool line into knob jobs and pushes", () => {
  const t = RC.tools([{ Knob: "Adjust", Job: "packet" }, { Knob: "push", Job: "pick bytes" }, { Knob: "Position", Job: "trigger position" },
    { Knob: "push", Job: "next match" }, { Knob: "Zoom", Job: "on packet" }]);
  assert.deepEqual(t, { adjust: { job: "packet", push: "pick bytes" }, position: { job: "trigger position", push: "next match" }, zoom: "on packet" });
  const m = RC.tools([{ Knob: "Adjust", Job: "packet" }, { Knob: "push", Job: "pick bytes" }, { Knob: "Position", Job: "next match" },
    { Knob: "push", Job: "trigger position" }, { Knob: "Zoom", Job: "on packet" }]);
  assert.deepEqual(m.position, { job: "next match", push: "trigger position" });
  const n = RC.tools([{ Knob: "Adjust push", Job: "try again" }, { Knob: "Position", Job: "trigger position" }, { Knob: "push", Job: "centre" },
    { Knob: "S/DIV push", Job: "zoom" }]);
  assert.equal(n.adjust.push, "try again");
  assert.deepEqual(n.position, { job: "trigger position", push: "centre" });
  assert.equal(n.zoom, "zoom");
  // the byte picker: the Adjust push triggers; Menu (backs out) is no knob job
  const p = RC.tools([{ Knob: "Adjust", Job: "byte" }, { Knob: "push", Job: "trigger on it" }, { Knob: "Position", Job: "byte" },
    { Knob: "push", Job: "back to start" }, { Knob: "Menu", Job: "back" }]);
  assert.deepEqual(p, { adjust: { job: "byte", push: "trigger on it" }, position: { job: "byte", push: "back to start" }, zoom: "" });
  assert.deepEqual(RC.turnWords("trigger position"), ["Left", "Right"]);
  assert.deepEqual(RC.turnWords("cursor A"), ["Left", "Right"]);
  assert.deepEqual(RC.turnWords("threshold"), ["Down", "Up"]);
  assert.deepEqual(RC.turnWords("packet"), ["Prev", "Next"]);
});

console.log("\n" + n + " tests\nALL PASS");
