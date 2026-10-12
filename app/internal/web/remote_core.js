// ENGMODEL-OWNER-UNIT: FU-WEB-REMOTE
// remote_core.js — the phone remote's pure logic: no DOM, no network. Numbers
// formatted as the scope writes them, the value a knob detent reaches, the
// hints for typed numbers and match bytes, search with everyday words, the
// change history (diff, merge, undo targets) and the knob-job line. Loaded by
// remote.html (window.RC) and require()d by remote.test.cjs. The scope stays
// the judge: every hint here is checked again by the device, whose own
// answer is what the remote shows.
// TRLC-LINKS: REQ-SDS-220
"use strict";

(function (root) {
  // ---- numbers as the scope writes them (panel/format.go) ---------------------

  const SI = [[1e9, "G"], [1e6, "M"], [1e3, "k"], [1, ""], [1e-3, "m"], [1e-6, "u"], [1e-9, "n"], [1e-12, "p"]];

  // g3 is three significant figures without trailing zeros (Go's %.3g).
  // TRLC-LINKS: REQ-SDS-220
  function g3(x) {
    if (x === 0) return "0";
    let s = Number(x).toPrecision(3);
    const e = s.indexOf("e");
    if (e >= 0) {
      let m = s.slice(0, e), ex = Number(s.slice(e + 1));
      if (m.indexOf(".") >= 0) m = m.replace(/0+$/, "").replace(/\.$/, "");
      return m + "e" + (ex < 0 ? "-" : "+") + String(Math.abs(ex)).padStart(2, "0");
    }
    if (s.indexOf(".") >= 0) s = s.replace(/0+$/, "").replace(/\.$/, "");
    return s;
  }

  // si writes v with an SI prefix and unit at three figures ("1.25V", "500us").
  // TRLC-LINKS: REQ-SDS-220
  function si(v, unit) {
    if (!Number.isFinite(v)) return "---";
    if (v === 0) return "0" + unit;
    const neg = v < 0 ? "-" : "", a = Math.abs(v);
    for (const [sc, pre] of SI) if (a >= sc * 0.9995) return neg + g3(a / sc) + pre + unit;
    return neg + g3(a / 1e-12) + "p" + unit;
  }

  // sgnSI is si with a + on positive values (positions and offsets).
  // TRLC-LINKS: REQ-SDS-220
  function sgnSI(v, unit) { return v > 0 ? "+" + si(v, unit) : si(v, unit); }

  // TRLC-LINKS: REQ-SDS-220
  function hex2(v) { return Math.max(0, Math.round(v)).toString(16).toUpperCase().padStart(2, "0"); }

  // hexText writes a whole number as the hex rows do (panel hexText): 0x50,
  // and -0x02 for a negative one typed; rounded half away from zero, as Go.
  // TRLC-LINKS: REQ-SDS-220
  function hexText(v) {
    const n = Math.sign(v) * Math.round(Math.abs(v));
    return n < 0 ? "-0x" + hex2(-n) : "0x" + hex2(n);
  }

  // The rows the device formats in their own way; the rest follow their unit.
  const FMT = {
    "trig.holdoff": (v) => (v === 0 ? "Off" : si(v, "s")),
    "ch1.offset": (v) => sgnSI(v, "V"),
    "ch2.offset": (v) => sgnSI(v, "V"),
    "time.zoom_pos": (v) => String(Math.round(v * 10) / 10) + " %",
    "bus.stream_value": (v) => hexText(v),
    "bus.i2c_address": (v) => (v === -1 ? "Any" : hexText(v)),
    "acq.sr_target": (v) => g3(v),
  };

  // fmtValue writes a number row's value as the scope would.
  // TRLC-LINKS: REQ-SDS-220
  function fmtValue(row, v) {
    const f = FMT[row.ID];
    if (f) return f(v);
    switch (row.Unit || "") {
      case "%": return String(v) + " %";
      case "div": return v.toFixed(2) + " div";
      case "": return String(Math.round(v));
      default: return si(v, row.Unit);
    }
  }

  // pretty spaces a number from its unit for reading on a phone: "500us" ->
  // "500 us", "+200mV" -> "+200 mV". Other texts stay as the scope writes them.
  // TRLC-LINKS: REQ-SDS-220
  function pretty(text) {
    const s = String(text == null ? "" : text);
    const m = /^([+-]?\d+(?:\.\d+)?(?:e[+-]\d+)?)([pnuµmkMG]?(?:V|s|Hz|b\/s|Bd))$/.exec(s);
    return m ? m[1] + " " + m[2] : s;
  }

  // compact is pretty without "/div" or trailing zeros, for a small button.
  // TRLC-LINKS: REQ-SDS-220
  function compact(text) {
    return pretty(text).replace(/\s*\/div$/, "").replace(/(\d)\.0+(?!\d)/g, "$1").replace(/(\.\d*?[1-9])0+(?!\d)/g, "$1");
  }

  // ---- the value a knob detent reaches ------------------------------------------

  // The knobs whose clockwise turn lowers the value (toward ns and mV, as
  // their printed arcs show): on the phone "right is clockwise" everywhere.
  const CW_DOWN = { "time.tdiv": 1, "ch1.vdiv": 1, "ch2.vdiv": 1 };

  // TRLC-LINKS: REQ-SDS-220
  function near(a, b) { return Math.abs(a - b) <= 1e-9 * Math.max(Math.abs(a), Math.abs(b), 1e-12); }

  // neighbour is the value one step up (dir +1) or down (-1) from a row's
  // value: the next ladder value, the value plus its step (the device clamps
  // a knob step to the range, so does this), or the next option. null at the
  // end, or when the row has no steps.
  // TRLC-LINKS: REQ-SDS-220
  function neighbour(row, dir) {
    if (!row) return null;
    if (row.Kind === "enum") {
      const o = row.Options || [];
      const i = o.findIndex((x) => x.Value === row.Value);
      const j = i < 0 ? (dir > 0 ? 0 : o.length - 1) : i + dir;
      return j >= 0 && j < o.length && j !== i ? { value: o[j].Value, text: o[j].Text } : null;
    }
    if (row.Kind !== "number") return null;
    const v = Number(row.Value);
    let t;
    if (row.Step) {
      const lo = row.Min == null ? -Infinity : row.Min, hi = row.Max == null ? Infinity : row.Max;
      t = +Math.min(hi, Math.max(lo, v + dir * row.Step)).toPrecision(12);
      if (near(t, v) || (dir > 0 ? t < v : t > v)) return null;
    } else if (row.Ladder && row.Ladder.length) {
      const L = row.Ladder.slice().sort((a, b) => a - b);
      t = dir > 0 ? L.find((x) => x > v && !near(x, v)) : L.slice().reverse().find((x) => x < v && !near(x, v));
      if (t === undefined) return null;
    } else return null;
    return { value: t, text: fmtValue(row, t) };
  }

  // turn is the value a clockwise (cw = +1) or counter-clockwise (-1) detent
  // reaches, n detents on.
  // TRLC-LINKS: REQ-SDS-220
  function turn(row, cw, n) {
    const dir = CW_DOWN[row && row.ID] ? -cw : cw;
    let r = row, out = null;
    for (let k = 0; k < (n || 1); k++) {
      const x = neighbour(r, dir);
      if (!x) break;
      out = x;
      r = Object.assign({}, r, { Value: x.value, Text: x.text });
    }
    return out;
  }

  // ---- typed numbers (panel/textparse.go) ---------------------------------------

  const PREFIX = { p: 1e-12, n: 1e-9, u: 1e-6, "µ": 1e-6, m: 1e-3, k: 1e3, K: 1e3, M: 1e6, G: 1e9 };

  // TRLC-LINKS: REQ-SDS-220
  function unitIs(u, unit) {
    if (!unit) return false;
    const l = u.toLowerCase();
    if (l === unit.toLowerCase()) return true;
    switch (unit) {
      case "Bd": return l === "baud" || l === "bps" || l === "b/s";
      case "s": return l === "sec";
      case "V": return l === "volt" || l === "volts";
    }
    return false;
  }

  // parseNumber reads "1.25V", "500u", "-20 mV", "2k", "50%", "0x55" in the
  // row's unit, with the device's error texts. A comma is the decimal point
  // when the text has no point and one comma ("2,5V": a European keyboard);
  // before exactly three digits after a whole number ("57,600") it could be
  // a thousands separator too, so that is refused rather than guessed.
  // TRLC-LINKS: REQ-SDS-220
  function parseNumber(text, unit) {
    const s0 = String(text == null ? "" : text).trim();
    const want = unit ? unit : "a plain number";
    if (!s0) return { ok: false, error: "empty value" };
    let s = s0;
    if (s0.indexOf(".") < 0 && s0.split(",").length === 2) {
      const i = s0.indexOf(","), whole = s0.slice(0, i).replace(/^[+-]+/, "");
      if (/^\d+$/.test(whole) && /[1-9]/.test(whole) && /^\d{3}(\D|$)/.test(s0.slice(i + 1))) {
        return { ok: false, error: 'the comma in "' + String(text) + '" reads two ways: type it without the comma, or with a point' };
      }
      s = s0.slice(0, i) + "." + s0.slice(i + 1);
    }
    const l = s.toLowerCase();
    if (l.startsWith("0x") || l.startsWith("-0x")) {
      const neg = l[0] === "-", h = l.replace(/^-?0x/, "");
      if (!/^[0-9a-f]+$/.test(h)) return { ok: false, error: 'bad hex number "' + s0 + '"' };
      const v = parseInt(h, 16);
      return { ok: true, value: neg ? -v : v };
    }
    // the numeric part, scanned as the device does: digits, points, a
    // leading sign, and an exponent that has digits after it
    const dig = (c) => c >= "0" && c <= "9";
    let i = 0;
    while (i < s.length) {
      const c = s[i];
      if (dig(c) || c === "." || ((c === "+" || c === "-") && i === 0)) i++;
      else if ((c === "e" || c === "E") && i > 0 && i + 1 < s.length &&
        (dig(s[i + 1]) || ((s[i + 1] === "-" || s[i + 1] === "+") && i + 2 < s.length && dig(s[i + 2])))) i += 2;
      else break;
    }
    const num = s.slice(0, i), rest = s.slice(i).trim();
    const v = /^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/.test(num) ? Number(num) : NaN;
    if (!Number.isFinite(v)) return { ok: false, error: 'not a number: "' + s0 + '"' };
    if (!rest || unitIs(rest, unit)) return { ok: true, value: v };
    const p = PREFIX[rest[0]];
    if (p !== undefined) {
      const u = rest.slice(1).trim();
      if (!u || unitIs(u, unit)) return { ok: true, value: v * p };
      return { ok: false, error: 'unit "' + u + '" does not fit (want ' + want + ")" };
    }
    return { ok: false, error: 'unit "' + rest + '" does not fit (want ' + want + ")" };
  }

  // The rows whose typed value the device moves to the nearest step.
  const SNAPS = { "time.tdiv": 1, "ch1.vdiv": 1, "ch2.vdiv": 1 };

  // numberHint checks typed text for a number row the way the device will:
  // {ok, value, text, note} or {ok: false, error}. Empty text: {empty: true}.
  // TRLC-LINKS: REQ-SDS-220
  function numberHint(row, text) {
    const t = String(text == null ? "" : text).trim();
    if (!t) return { ok: false, empty: true, error: "" };
    let v;
    if (row.ID === "bus.i2c_address" && /^any$/i.test(t)) v = -1;
    else if (row.ID === "trig.holdoff" && /^off$/i.test(t)) v = 0;
    else {
      const p = parseNumber(t, row.Unit || "");
      if (!p.ok) return { ok: false, error: row.Label + ": " + p.error };
      v = p.value;
    }
    const lo = row.Min, hi = row.Max;
    if (lo != null && hi != null) {
      const tol = 1e-9 * Math.max(Math.abs(lo), Math.abs(hi));
      if (v < lo - tol || v > hi + tol) {
        return { ok: false, error: row.Label.toLowerCase() + " " + fmtValue(row, v) + " is outside " + fmtValue(row, lo) + " .. " + fmtValue(row, hi) };
      }
    }
    let note = "";
    if (SNAPS[row.ID] && row.Ladder && row.Ladder.length) {
      const n = nearestLadder(row.Ladder, v);
      if (!near(n, v)) note = "the scope takes the nearest step, " + pretty(fmtValue(row, n));
    }
    return { ok: true, value: v, text: fmtValue(row, v), note };
  }

  // nearestLadder is the ladder value closest to v on a log scale.
  // TRLC-LINKS: REQ-SDS-220
  function nearestLadder(L, v) {
    let best = L[0];
    for (const x of L) if (Math.abs(Math.log(x / v)) < Math.abs(Math.log(best / v))) best = x;
    return best;
  }

  // The unit keys a number row's keypad gets (none for a row the scope
  // writes in hex: its keypad has A-F and 0x instead).
  const UNIT_KEYS = { V: ["mV", "V"], s: ["ns", "us", "ms", "s"], "%": ["%"], "b/s": ["k", "M"] };

  // TRLC-LINKS: REQ-SDS-220
  function unitKeys(row) {
    if (isHex(row)) return [];
    const u = UNIT_KEYS[row.Unit || ""];
    if (u) return u;
    return !row.Unit && row.Max >= 1000 ? ["k"] : [];
  }

  // The rows the scope writes in hex ("0x50").
  const HEX = { "bus.i2c_address": 1, "bus.stream_value": 1 };

  // TRLC-LINKS: REQ-SDS-220
  function isHex(row) { return !!row && !!HEX[row.ID]; }

  // hexNumKey edits a hex keypad's number ("0x50"): a letter A-F makes it
  // hex (0x goes in front), 0x puts the prefix in or takes it out, Del takes
  // the last digit (and 0x with the last one).
  // TRLC-LINKS: REQ-SDS-220
  function hexNumKey(buf, k) {
    const hex = buf.slice(0, 2) === "0x";
    if (k === "clear") return "";
    if (k === "back") return buf === "0x" ? "" : buf.slice(0, -1);
    if (k === "0x") return hex ? buf.slice(2) : "0x" + buf;
    if (buf.length >= 10) return buf;
    if (/^[A-F]$/.test(k)) return (hex ? buf : "0x" + buf) + k;
    return /^[0-9]$/.test(k) ? buf + k : buf;
  }

  // numKey edits a number keypad's text: digits, the sign, Del (a whole unit
  // at once), Clear, and a unit key that replaces the unit typed before.
  // TRLC-LINKS: REQ-SDS-220
  function numKey(buf, k, units) {
    const UNIT_TAIL = /\s*(?:mV|V|ns|us|ms|s|%|k|M)$/;
    if (k === "back") return UNIT_TAIL.test(buf) ? buf.replace(UNIT_TAIL, "") : buf.slice(0, -1);
    if (k === "clear") return "";
    if (k === "-") return buf[0] === "-" ? buf.slice(1) : "-" + buf;
    if (units && units.indexOf(k) >= 0) return buf.replace(UNIT_TAIL, "") + k;
    const m = UNIT_TAIL.exec(buf), unit = m ? m[0] : "", num = buf.slice(0, buf.length - unit.length);
    if (k === "." && num.indexOf(".") >= 0) return buf;
    return buf.length < 24 ? num + k + unit : buf;   // digits go in before a unit typed already
  }

  // ---- match bytes (panel/textparse.go parseMatchText) --------------------------

  const MATCH_MAX = 32;

  // utf8 is the UTF-8 bytes of s (match text is sent byte for byte).
  // TRLC-LINKS: REQ-SDS-220
  function utf8(s) {
    const out = [];
    for (const ch of s) {
      let c = ch.codePointAt(0);
      if (c < 0x80) out.push(c);
      else if (c < 0x800) out.push(0xc0 | (c >> 6), 0x80 | (c & 63));
      else if (c < 0x10000) out.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
      else out.push(0xf0 | (c >> 18), 0x80 | ((c >> 12) & 63), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
    }
    return out;
  }

  // unescapeBytes resolves \r \n \t \0 \\ \" \' and \xNN in quoted text.
  // TRLC-LINKS: REQ-SDS-220
  function unescapeBytes(s) {
    const b = utf8(s), out = [];
    for (let i = 0; i < b.length; i++) {
      const c = b[i];
      if (c !== 92) { out.push(c); continue; }
      i++;
      if (i >= b.length) return { ok: false, error: "text ends in a backslash" };
      const e = String.fromCharCode(b[i]);
      if (e === "r") out.push(13);
      else if (e === "n") out.push(10);
      else if (e === "t") out.push(9);
      else if (e === "0") out.push(0);
      else if (e === "\\" || e === '"' || e === "'") out.push(b[i]);
      else if (e === "x") {
        const h = String.fromCharCode(b[i + 1] || 0, b[i + 2] || 0);
        if (i + 2 >= b.length || !/^[0-9a-fA-F]{2}$/.test(h)) return { ok: false, error: "\\x needs two hex digits" };
        out.push(parseInt(h, 16));
        i += 2;
      } else return { ok: false, error: "unknown escape \\" + e };
    }
    return { ok: true, bytes: out };
  }

  // parseMatch reads match bytes as the device does: hex tokens ("55 AA",
  // "55AA", "??", "A?", "ERR") or quoted text ("\"Hi\\r\\n\""); empty = any byte.
  // TRLC-LINKS: REQ-SDS-220
  function parseMatch(text) {
    const s = String(text == null ? "" : text).trim();
    if (!s || /^any( byte)?$/i.test(s)) return { ok: true, tokens: [] };
    if (s.length >= 2 && ((s[0] === '"' && s[s.length - 1] === '"') || (s[0] === "'" && s[s.length - 1] === "'"))) {
      const u = unescapeBytes(s.slice(1, -1));
      if (!u.ok) return u;
      if (!u.bytes.length) return { ok: false, error: "empty text" };
      if (u.bytes.length > MATCH_MAX) return { ok: false, error: u.bytes.length + " bytes: " + MATCH_MAX + " at most" };
      return { ok: true, tokens: u.bytes.map(hex2) };
    }
    const toks = [];
    for (const f of s.split(/[\s,]+/).filter(Boolean)) {
      const bad = { ok: false, error: 'bad byte "' + f + '": use hex pairs like 55 AA, ?? or ERR, or "text" in quotes' };
      const u = f.toUpperCase();
      if (u === "ERR" || u === "??") { toks.push(u); continue; }
      if (u.length % 2) return bad;
      for (let i = 0; i < u.length; i += 2) {
        const t = u.slice(i, i + 2);
        if (!/^[0-9A-F?]{2}$/.test(t)) return bad;
        toks.push(t);
      }
    }
    if (toks.length > MATCH_MAX) return { ok: false, error: toks.length + " bytes: " + MATCH_MAX + " at most" };
    return { ok: true, tokens: toks };
  }

  // quoteText turns typed text into the device's quoted form: quotes and
  // stray backslashes escaped, the \r \n \t \0 \xNN escapes kept.
  // TRLC-LINKS: REQ-SDS-220
  function quoteText(t) {
    if (!t) return "";
    return '"' + t.replace(/\\(?![rnt0\\"'x])/g, "\\\\").replace(/"/g, '\\"') + '"';
  }

  // hexKey edits the hex keypad's text: digits group in pairs ("55 AA"), ??
  // is one wildcard byte, Del takes a whole ?? back.
  // TRLC-LINKS: REQ-SDS-220
  function hexKey(buf, k) {
    const digits = (b) => b.replace(/\s/g, "").length;
    if (k === "back") {
      let b = buf.replace(/\s+$/, "");
      b = /\?\?$/.test(b) ? b.slice(0, -2) : b.slice(0, -1);
      return b.replace(/\s+$/, "");
    }
    if (k === "clear") return "";
    if (k === " ") return buf && !/\s$/.test(buf) ? buf + " " : buf;
    if (digits(buf) >= 2 * MATCH_MAX) return buf;
    if (k === "??") return buf + (buf && !/\s$/.test(buf) ? " " : "") + "??";
    if (/^[0-9A-F]$/.test(k)) {
      const last = buf.split(" ").pop();
      return buf + (last.length >= 2 ? " " : "") + k;
    }
    return buf;
  }

  // ---- the model --------------------------------------------------------------------

  // index flattens /api/sheets: rows by id with their tab, the section they
  // sit in (carried down from the header row) and the section's note.
  // TRLC-LINKS: REQ-SDS-220
  function index(sheets) {
    const rows = new Map(), tabs = [];
    for (const t of (sheets && sheets.Tabs) || []) {
      const ids = [];
      let sec = "", note = "";
      for (const r of t.Rows || []) {
        if (r.Section) {
          const m = /^(.*?)\s*\((.*)\)\s*$/.exec(r.Section);
          sec = m ? m[1] : r.Section;
          note = m ? m[2].charAt(0).toUpperCase() + m[2].slice(1) : "";
        }
        rows.set(r.ID, Object.assign({}, r, { tab: t.ID, tabName: t.Name, section: sec, sectionNote: note, sectionHead: !!r.Section }));
        ids.push(r.ID);
      }
      tabs.push({ id: t.ID, name: t.Name, title: t.Title, key: t.Key, rows: ids });
    }
    return { rows, tabs };
  }

  // isSwitch: an Off/On enum, shown as a switch.
  // TRLC-LINKS: REQ-SDS-220
  function isSwitch(row) {
    const o = row && row.Kind === "enum" ? row.Options || [] : [];
    return o.length === 2 && /^off$/i.test(o[0].Text) && /^on$/i.test(o[1].Text);
  }

  // isSlot: a FILE setup slot (Save / Recall).
  // TRLC-LINKS: REQ-SDS-220
  function isSlot(row) { return !!row && row.Kind === "action" && /^file\.setup\d+$/.test(row.ID); }

  // ---- search -------------------------------------------------------------------------

  // Everyday words for what the sheets call otherwise (each word also
  // matches as itself).
  const SYN = {
    averaging: ["average"], avg: ["average"], screenshot: ["print"], capture: ["print"], attenuation: ["probe"],
    attenuator: ["probe"], x10: ["probe"], "10x": ["probe"], sensitivity: ["v/div"], vdiv: ["v/div"], volts: ["v/div"],
    tdiv: ["time/div"], timebase: ["time/div"], sweep: ["time/div"], horizontal: ["time/div", "horizontal"],
    delay: ["trigger pos"], position: ["offset", "trigger pos"], reset: ["default"], factory: ["default"],
    preset: ["default"], bandwidth: ["bw"], baudrate: ["baud"], rate: ["baud", "bit rate"], stack: ["super-res", "master"],
    superres: ["super-res"], glitch: ["pulse"], golden: ["mask", "master"], marker: ["fft"], harmonics: ["fft"],
    slope: ["source/slope", "slope"], edge: ["source/slope"], decode: ["protocol"], uart: ["protocol", "baud"],
    i2c: ["protocol", "i2c"], spi: ["protocol", "spi"], serial: ["serial", "match", "protocol"], save: ["setup"],
    recall: ["setup"], load: ["setup"], persistence: ["persistence"], afterglow: ["persistence"], xy: ["view"],
    spectrogram: ["view"], bode: ["view"], invert: ["invert"], ac: ["coupling"], dc: ["coupling"], gnd: ["coupling"],
    ground: ["coupling"], holdoff: ["holdoff"], zoom: ["zoom"], cursor: ["cursor"], measure: ["meas"],
    // scope words and the usual misspellings
    vpp: ["peak-peak"], "pk-pk": ["peak-peak"], pkpk: ["peak-peak"], "p-p": ["peak-peak"], rising: ["rise"], falling: ["fall"],
    risetime: ["rise"], falltime: ["fall"], samplerate: ["time/div", "memory depth"], recordlength: ["memory depth"],
    record: ["memory depth"], "screen-shot": ["print"], persistance: ["persistence"], averge: ["average"], tigger: ["trigger", "trig"],
    cusor: ["cursor"], offest: ["offset"],
  };

  // Phrases that are one word to the search ("peak to peak" is pkpk).
  const PHRASES = [[/\bpeak[ -]to[ -]peak\b/g, "pkpk"], [/\brise time\b/g, "risetime"], [/\bfall time\b/g, "falltime"],
    [/\bsample rate\b/g, "samplerate"], [/\brecord length\b/g, "recordlength"], [/\bscreen shot\b/g, "screenshot"]];

  // The friendlier names of rows whose sheet label needs its tab.
  const NAME = {
    "time.tdiv": "Time/div", "trig.level": "Trigger level", "trig.edge": "Trigger", "trig.mode": "Trigger mode",
    "trig.type": "Trigger type", "trig.holdoff": "Trigger holdoff", "time.trig_pos": "Trigger position",
    "ch1.offset": "CH1 position", "ch2.offset": "CH2 position", "bus.match": "Match bytes", "bus.trigger_on": "Serial trigger",
    "bus.protocol": "Protocol", "view.fft_source": "FFT source", "view.view": "View", "curs.mode": "Cursors",
    "acq.mode": "Acquisition", "cmp.mask": "Mask test", "cmp.math": "Math", "time.zoom": "Zoom",
  };

  // label is the name a row has on a tile, in History and in search.
  // TRLC-LINKS: REQ-SDS-220
  function label(row) {
    if (!row) return "";
    if (NAME[row.ID]) return NAME[row.ID];
    return (row.tab === "ch1" ? "CH1 " : row.tab === "ch2" ? "CH2 " : "") + row.Label.replace(/\s+/g, " ");
  }

  // words splits a query into lower-case words (a known phrase is one).
  // TRLC-LINKS: REQ-SDS-220
  function words(q) {
    let s = String(q || "").toLowerCase();
    for (const [re, w] of PHRASES) s = s.replace(re, w);
    return s.split(/[\s,]+/).filter(Boolean);
  }

  // within1: a and b differ by at most one letter: one changed, added,
  // dropped, or two neighbours swapped ("offest", "offset").
  // TRLC-LINKS: REQ-SDS-220
  function within1(a, b) {
    if (a === b) return true;
    const la = a.length, lb = b.length;
    if (Math.abs(la - lb) > 1) return false;
    let i = 0;
    while (i < la && i < lb && a[i] === b[i]) i++;
    if (la === lb) {
      return a.slice(i + 1) === b.slice(i + 1) || (a[i] === b[i + 1] && a[i + 1] === b[i] && a.slice(i + 2) === b.slice(i + 2));
    }
    return la > lb ? a.slice(i + 1) === b.slice(i) : a.slice(i) === b.slice(i + 1);
  }

  // vocabulary is every word the sheets use (names, tabs, sections, options,
  // presets, actions) and the everyday words above.
  // TRLC-LINKS: REQ-SDS-220
  function vocabulary(model) {
    const v = new Set(Object.keys(SYN));
    const put = (t) => { for (const w of String(t || "").toLowerCase().split(/[^a-z0-9]+/)) if (w.length >= 4) v.add(w); };
    for (const t of model.tabs) {
      put(t.name); put(t.title);
      for (const id of t.rows) {
        const r = model.rows.get(id);
        put(label(r)); put(r.Label); put(r.section);
        for (const o of r.Options || []) put(o.Text);
        for (const p of r.Presets || []) put(p);
        for (const a of r.Actions || []) put(a.Text);
      }
    }
    return v;
  }

  // search finds rows by their name, tab, section, options and help, and by
  // the everyday words above. Every word must match; a name match ranks
  // first, the help text last. When nothing matches, a word of 5 letters or
  // more also matches the sheets' words one letter away from it ("averge",
  // "tigger"). [{id, score}], best first, in sheet order.
  // TRLC-LINKS: REQ-SDS-220
  function search(model, q, limit) {
    const ws = words(q);
    if (!ws.length) return [];
    let out = find(model, ws, (w) => [w].concat(SYN[w] || []));
    if (!out.length) {
      const vocab = vocabulary(model);
      out = find(model, ws, (w) => {
        const alts = w.length < 5 ? [] : [...vocab].filter((x) => x !== w && within1(w, x));
        return [w].concat(SYN[w] || [], ...alts.map((x) => [x].concat(SYN[x] || [])));
      });
    }
    out.sort((a, b) => b.score - a.score || a.order - b.order);
    return out.slice(0, limit || 40).map((x) => ({ id: x.id, score: x.score }));
  }

  // find scores every row against the words, each word standing for its
  // candidates (itself and its alternatives); rows that every word matches.
  // TRLC-LINKS: REQ-SDS-220
  function find(model, ws, cands) {
    const out = [];
    let order = 0;
    const alt = ws.map(cands);
    for (const t of model.tabs) {
      for (const id of t.rows) {
        const r = model.rows.get(id);
        order++;
        const name = (label(r) + " " + r.Label).toLowerCase();
        const where = [t.name, t.title, r.section, r.sectionNote, id, (r.Options || []).map((o) => o.Text).join(" "),
          (r.Presets || []).join(" "), (r.Actions || []).map((a) => a.Text).join(" ")].join(" ").toLowerCase();
        const help = String(r.Help || "").toLowerCase();
        let score = 0, all = true;
        for (let k = 0; k < ws.length; k++) {
          let best = 0;
          for (const x of alt[k]) {
            if (name.startsWith(x)) best = Math.max(best, 4);
            else if (new RegExp("(^|[\\s/(-])" + x.replace(/[.*+?^${}()|[\]\\/]/g, "\\$&")).test(name)) best = Math.max(best, 3);
            else if (name.indexOf(x) >= 0) best = Math.max(best, 2);
            else if (where.indexOf(x) >= 0) best = Math.max(best, 1);
            else if (x.length > 2 && help.indexOf(x) >= 0) best = Math.max(best, 0.5);
          }
          if (!best) { all = false; break; }
          score += best;
        }
        if (all) out.push({ id, score, order });
      }
    }
    return out;
  }

  // matchPlace reports whether a place (a workspace, a key, a tab of the
  // remote) matches every word of the query.
  // TRLC-LINKS: REQ-SDS-220
  function matchPlace(place, q) {
    const ws = words(q), hay = (place.label + " " + place.words).toLowerCase();
    return ws.length > 0 && ws.every((w) => [w].concat(SYN[w] || []).some((x) => hay.indexOf(x) >= 0));
  }

  // ---- history --------------------------------------------------------------------------

  const MERGE_MS = 3000, HIST_MAX = 100;

  // tracked: the rows History follows: settings (enum, number, bytes) and
  // the setup slots; not the run state, not live action statuses.
  // TRLC-LINKS: REQ-SDS-220
  function tracked(row) {
    if (!row || row.ID === "acq.run") return false;
    return row.Kind === "enum" || row.Kind === "number" || row.Kind === "bytes" || isSlot(row);
  }

  // snapshot is what History compares: each tracked row's value, and the
  // workspace.
  // TRLC-LINKS: REQ-SDS-220
  function snapshot(model, ws) {
    const rows = new Map();
    for (const [id, r] of model.rows) {
      if (!tracked(r)) continue;
      const kind = isSlot(r) ? "slot" : r.Kind;
      rows.set(id, { key: kind === "slot" ? r.Text : JSON.stringify(r.Value === undefined ? null : r.Value),
        value: r.Value === undefined ? null : r.Value, text: r.Text, kind, tab: r.tabName, label: r.Label.replace(/\s+/g, " ") });
    }
    return { rows, ws: ws || "" };
  }

  // diff lists the rows whose value differs between two snapshots.
  // TRLC-LINKS: REQ-SDS-220
  function diff(a, b) {
    const out = [];
    for (const [id, x] of b.rows) {
      const y = a.rows.get(id);
      if (y && y.key !== x.key) {
        out.push({ id, tab: x.tab, label: x.label, kind: x.kind, old: { value: y.value, text: y.text, key: y.key },
          neu: { value: x.value, text: x.text, key: x.key } });
      }
    }
    return out;
  }

  // okEntry: a stored entry of the shape this page shows. History survives
  // page updates and hand edits: anything else is dropped when it loads.
  // TRLC-LINKS: REQ-SDS-220
  function okEntry(e) {
    const str = (x) => typeof x === "string";
    const side = (x) => !!x && typeof x === "object" && str(x.key);
    const okRow = (r) => !!r && typeof r === "object" && str(r.id) && str(r.tab) && str(r.label) && side(r.old) && side(r.neu);
    return !!e && typeof e === "object" && Number.isFinite(e.id) && Number.isFinite(e.t) && str(e.src) && Array.isArray(e.rows) && e.rows.every(okRow) &&
      (e.ws == null || (typeof e.ws === "object" && str(e.ws.from) && str(e.ws.to))) && (e.undid === undefined || Array.isArray(e.undid));
  }

  // History is the change list, newest first: every setting change from the
  // phone or the scope; steps on one row from one source within 3 s are one
  // entry (stepping back to the start removes it); an undo is itself an
  // entry. At most 100 entries.
  // TRLC-LINKS: REQ-SDS-220
  class History {
    constructor(list) {
      this.list = Array.isArray(list) ? list.filter(okEntry).slice(0, HIST_MAX) : [];
      this.seq = this.list.reduce((m, e) => Math.max(m, e.id || 0), 0);
    }

    // add books a change: src "phone", "scope" or "remote" (another remote);
    // meta {primary, label, kind, undoable, ws, undid (an undo: the ids of
    // the entries it undid)}. Returns the entry (or null when a merge
    // removed it).
    add(src, rows, meta, now) {
      meta = meta || {};
      rows = rows.slice();
      const pi = meta.primary ? rows.findIndex((r) => r.id === meta.primary) : -1;
      if (pi > 0) rows.unshift(rows.splice(pi, 1)[0]);
      const single = rows.length === 1 && !meta.ws && !meta.kind && !meta.label;
      const last = this.list[0];
      if (single && last && last.single && last.src === src && last.rows[0].id === rows[0].id && !last.undone && now - last.t <= MERGE_MS) {
        last.rows[0].neu = rows[0].neu;
        last.t = now;
        last.n++;
        if (last.rows[0].neu.key === last.rows[0].old.key) { this.list.shift(); return null; }
        return last;
      }
      const settings = rows.some((r) => r.kind !== "slot");
      const undoable = meta.kind === "action" ? !!meta.undoable && settings : !!meta.ws || settings;
      const e = { id: ++this.seq, t: now, src, rows, single, primary: pi >= 0 || (rows.length > 0 && rows[0].id === meta.primary),
        pid: meta.primary || "", ws: meta.ws || null, label: meta.label || "", kind: meta.kind || (meta.ws ? "ws" : rows.length === 1 ? "set" : "batch"),
        undoable, undone: false, n: 1 };
      if (Array.isArray(meta.undid) && meta.undid.length) e.undid = meta.undid.slice();   // an undo: the entries it undid
      this.list.unshift(e);
      if (this.list.length > HIST_MAX) this.list.length = HIST_MAX;
      return e;
    }

    // extend adds rows a change brought with it (side effects found later);
    // the row the user changed goes first when it comes later.
    extend(e, rows, ws) {
      for (const r of rows) {
        const have = e.rows.find((x) => x.id === r.id);
        if (have) have.neu = r.neu;
        else e.rows.push(r);
      }
      const pi = e.pid ? e.rows.findIndex((r) => r.id === e.pid) : -1;
      if (pi >= 0) { e.rows.unshift(e.rows.splice(pi, 1)[0]); e.primary = true; }
      if (ws) e.ws = e.ws ? { from: e.ws.from, to: ws.to } : ws;
      e.single = e.single && e.rows.length === 1 && !e.ws;
      if (!e.undoable && e.kind !== "action") e.undoable = !!e.ws || e.rows.some((r) => r.kind !== "slot");
      return e;
    }

    // targets is what an undo of these entries sets back: each row's old
    // value from the oldest of them, and the workspace they started in.
    targets(entries) {
      const rows = new Map();
      let ws = null;
      for (const e of entries.slice().sort((a, b) => b.id - a.id)) {   // newest first: older entries override
        if (e.ws) ws = e.ws.from;
        for (const r of e.rows) if (r.kind !== "slot") rows.set(r.id, r);
      }
      return { rows: Array.from(rows.values()).map((r) => ({ id: r.id, tab: r.tab, label: r.label, kind: r.kind, value: r.old.value, text: r.old.text, key: r.old.key })), ws };
    }

    // since is entry e and every newer entry that changed a setting or the
    // workspace: what "back to before this" undoes. Undone entries count too,
    // so an undo and the entry it undid cancel out in the targets.
    since(e) {
      const at = this.list.indexOf(e);
      return at < 0 ? [] : this.list.slice(0, at + 1).filter((x) => x.ws || x.rows.some((r) => r.kind !== "slot"));
    }
  }

  // ago is a relative time: "now", "12 s ago", "3 min ago", "2 h ago".
  // TRLC-LINKS: REQ-SDS-220
  function ago(ms) {
    const s = Math.max(0, Math.round(ms / 1000));
    return s < 5 ? "now" : s < 60 ? s + " s ago" : s < 3600 ? Math.floor(s / 60) + " min ago" : Math.floor(s / 3600) + " h ago";
  }

  // ---- the knob-job line (status Tool) ------------------------------------------------

  // tools reads the scope's tool line ([{Knob, Job}]) into the Adjust and
  // Position jobs with their pushes, and the S/DIV push.
  // TRLC-LINKS: REQ-SDS-220
  function tools(segs) {
    const out = { adjust: { job: "", push: "" }, position: { job: "", push: "" }, zoom: "" };
    let cur = null;
    for (const s of segs || []) {
      const k = String(s.Knob || "");
      if (k === "Adjust") cur = out.adjust, cur.job = s.Job;
      else if (k === "Position") cur = out.position, cur.job = s.Job;
      else if (k === "push" && cur) cur.push = s.Job;
      else if (k === "Adjust push") out.adjust.push = s.Job, cur = null;
      else if (k === "Position push") out.position.push = s.Job, cur = null;
      else if (k === "Zoom" || k === "S/DIV push") out.zoom = s.Job, cur = null;
    }
    return out;
  }

  // turnWords names the two ways a knob job moves.
  // TRLC-LINKS: REQ-SDS-220
  function turnWords(job) {
    return /threshold/i.test(job) ? ["Down", "Up"] : /cursor|sample|position|fine|point|level/i.test(job) ? ["Left", "Right"] : ["Prev", "Next"];
  }

  const RC = {
    g3, si, sgnSI, hex2, fmtValue, pretty, compact, near, neighbour, turn, CW_DOWN, parseNumber, numberHint, nearestLadder,
    unitKeys, numKey, isHex, hexNumKey, parseMatch, quoteText, hexKey, MATCH_MAX, index, isSwitch, isSlot, SYN, NAME, label, words, within1, search,
    matchPlace, tracked, snapshot, diff, okEntry, History, ago, tools, turnWords, MERGE_MS,
  };
  if (typeof module !== "undefined" && module.exports) module.exports = RC;
  else root.RC = RC;
})(typeof window !== "undefined" ? window : globalThis);
