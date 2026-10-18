// ENGMODEL-OWNER-UNIT: FU-WEB-PACKETS
// layers.js — the application layers a decoded bus carries (the BUS sheet's
// Layer row): each reads the bus's packets (packets.js) as a chip's or a
// stacked protocol's transactions and writes a note per packet ("READ
// 0x001000, 64 bytes", "EEPROM 50 read @0x0010, 2 bytes: 12 34", "ITM ch0
// 'hello'"). The scope's twin is internal/analysis (layer*.go); the notes are
// the same text (TestJSLayerParity). The tables come from layer_tables.js.
// Pure: no DOM; classic script sharing the page's scope.
"use strict";

// LAYERS lists the layers in the scope's order: [id, name, bus, line].
const LAYERS = [
  ["", "None", "", ""],
  ["spiflash", "SPI flash", "spi", "MOSI"], ["sdcard", "SD card", "spi", "MOSI"], ["avrisp", "AVR ISP", "spi", "MOSI"],
  ["nrf24l01", "nRF24L01", "spi", "MOSI"], ["nrf905", "nRF905", "spi", "MOSI"], ["cc1101", "CC1101", "spi", "MOSI"],
  ["rfm12", "RFM12", "spi", "MOSI"], ["mrf24j40", "MRF24J40", "spi", "MOSI"], ["enc28j60", "ENC28J60", "spi", "MOSI"],
  ["st25r39xx", "ST25R39xx", "spi", "MOSI"], ["adxl345", "ADXL345", "spi", "MOSI"], ["adns5020", "ADNS-5020", "spi", "MOSI"],
  ["ade77xx", "ADE7758", "spi", "MOSI"], ["adf435x", "ADF4350/1", "spi", "MOSI"], ["ad5626", "AD5626 DAC", "spi", "MOSI"],
  ["ad79x0", "AD7910/20 ADC", "spi", "MISO"], ["ltc242x", "LTC2421/2 ADC", "spi", "MISO"], ["max72xx", "MAX7219/21", "spi", "MOSI"],
  ["x2444m", "X2444M/P", "spi", "MOSI"], ["rgbled", "RGB LEDs", "spi", "MOSI"], ["nesgamepad", "NES gamepad", "spi", "MISO"],
  ["ssi32", "SSI32", "spi", "MOSI"],
  ["edid", "EDID", "i2c", ""], ["xfp", "XFP module", "i2c", ""], ["eeprom24xx", "24xx EEPROM", "i2c", ""],
  ["ds1307", "DS1307 RTC", "i2c", ""], ["rtc8564", "RTC-8564", "i2c", ""], ["atsha204a", "ATSHA204A", "i2c", ""],
  ["mlx90614", "MLX90614", "i2c", ""], ["nunchuk", "Wii Nunchuk", "i2c", ""], ["hdcp", "HDCP", "i2c", ""],
  ["lm75", "LM75", "i2c", ""], ["ltc26x7", "LTC2607/17/27", "i2c", ""], ["tca6408a", "TCA6408A", "i2c", ""],
  ["pca9571", "PCA9571", "i2c", ""], ["mcp23008", "MCP23008", "i2c", ""], ["mcp23017", "MCP23017", "i2c", ""],
  ["mxc6225xu", "MXC6225XU", "i2c", ""], ["i2cdemux", "By address", "i2c", ""], ["i2cfilter", "Address filter", "i2c", ""],
  ["sbus", "S.BUS", "uart", ""], ["arm_itm", "ARM ITM", "uart", ""], ["arm_tpiu", "ARM TPIU", "uart", ""],
  ["arm_etmv3", "ARM ETMv3", "uart", ""], ["pan1321", "PAN1321", "uart", ""], ["amulet", "Amulet ASCII", "uart", ""],
  ["ds28ea00", "DS28EA00", "onewire", ""], ["ds2408", "DS2408", "onewire", ""], ["ds243x", "DS243x", "onewire", ""],
  ["onewire_net", "1-Wire ROM", "onewire", ""],
  ["cfp", "CFP module", "mdio", ""],
  ["usb_request", "USB requests", "usbls", ""],
];

// ---- formatting (as the scope's layers format) ----

// TRLC-LINKS: REQ-SDS-241
function lyHx(v, n) { return v.toString(16).toUpperCase().padStart(n, "0"); }

// lyHexList writes bytes "12 34 56", at most max and " .." after.
// TRLC-LINKS: REQ-SDS-241
function lyHexList(b, max) {
  let s = "";
  for (let i = 0; i < b.length; i++) {
    if (i === max) { s += " .."; break; }
    if (i > 0) s += " ";
    s += lyHx(b[i], 2);
  }
  return s;
}

// lyQuoted writes bytes as text in single quotes, '.' for a byte that does not print.
// TRLC-LINKS: REQ-SDS-241
function lyQuoted(b, max) {
  let s = "'";
  for (let i = 0; i < b.length; i++) {
    if (i === max) return s + "'..";
    s += b[i] >= 0x20 && b[i] < 0x7f ? String.fromCharCode(b[i]) : ".";
  }
  return s + "'";
}

// lyFixed writes num/den with places decimals, half away from zero, in integers.
// TRLC-LINKS: REQ-SDS-241
function lyFixed(num, den, places) {
  if (den < 0) { num = -num; den = -den; }
  if (den === 0) return "?";
  const neg = num < 0;
  if (neg) num = -num;
  let scale = 1;
  for (let i = 0; i < places; i++) scale *= 10;
  const q = Math.floor((num * scale * 2 + den) / (den * 2));
  let s = String(Math.floor(q / scale));
  if (places > 0) s += "." + String(q % scale).padStart(places, "0");
  if (neg && q !== 0) s = "-" + s;
  return s;
}

// lyPrintable writes device text with '.' for a character that does not print (ASCII notes).
// TRLC-LINKS: REQ-SDS-241
function lyPrintable(s) { return s.replace(/[^\x20-\x7e]/g, "."); }
// TRLC-LINKS: REQ-SDS-241
function lyPlural(n, w) { return n + " " + w + (n === 1 ? "" : "s"); }
// lyBE is bytes most significant first as a number (arithmetic: past 31 bits).
// TRLC-LINKS: REQ-SDS-241
function lyBE(b) { let v = 0; for (const x of b) v = v * 256 + x; return v; }
// TRLC-LINKS: REQ-SDS-241
function lyLE(b) { let v = 0; for (let i = b.length - 1; i >= 0; i--) v = v * 256 + b[i]; return v; }
// TRLC-LINKS: REQ-SDS-241
function lyReg(t, r) { const n = t && t[r]; return n ? n : "0x" + lyHx(r, 2); }
// TRLC-LINKS: REQ-SDS-241
function lyAnyBad(p) { return (p.byteErr || []).some(Boolean) || !!p.err; }
// TRLC-LINKS: REQ-SDS-241
function lyInt8(v) { return (v << 24) >> 24; }
// TRLC-LINKS: REQ-SDS-241
function lyInt16(v) { return (v << 16) >> 16; }
// TRLC-LINKS: REQ-SDS-242
function lyJoin(parts) { return parts.join("; "); }
// lyEach writes every packet's note with one transaction reader.
// TRLC-LINKS: REQ-SDS-242
function lyEach(ps, f) { for (const p of ps) { const r = f(p.bytes); p.note = r[0]; p.noteErr = r[1]; } }
// TRLC-LINKS: REQ-SDS-242
function lyRegData(write, d) {
  if (!d.length) return "";
  if (write) return " = " + lyHexList(d, 8);
  return ", " + lyPlural(d.length, "byte");
}
// TRLC-LINKS: REQ-SDS-242
function lyRev(b) { return b.slice().reverse(); }

// ---- SPI ----

// lyFlashNote reads one flash transaction (the host's side, MOSI).
// TRLC-LINKS: REQ-SDS-242
function lyFlashNote(b) {
  if (!b.length) return ["", false];
  const c = LAYER_TABLES.flashCmds[b[0]];
  if (!c) return ["? 0x" + lyHx(b[0], 2) + ", " + lyPlural(b.length, "byte"), true];
  const [name, addr, dummy, rest, erase] = c;
  if (name === "RES" && b.length === 1) return ["RES (release)", false];
  const head = 1 + (addr ? 3 : 0);
  if (b.length < head + dummy) return [name + " (cut)", true];
  let note = name;
  if (addr) {
    const a = lyBE(b.slice(1, 4));
    if (erase > 0) {
      note = name + " " + (erase / 1024) + "K 0x" + lyHx(a, 6);
      if (a % erase !== 0) return [note + " (not aligned)", true];
    } else note = name + " 0x" + lyHx(a, 6);
  }
  const r = b.slice(head + dummy);
  if (rest === 1) { if (r.length) note += ", " + lyPlural(r.length, "byte") + ": " + lyHexList(r, 8); }
  else if (rest === 2) { if (r.length) note += ", " + lyPlural(r.length, "byte"); }
  else if (r.length) return [note + " (+" + lyPlural(r.length, "byte") + ")", true];
  return [note, false];
}

// TRLC-LINKS: REQ-SDS-242
function lySdCRC7(b) {
  let crc = 0;
  for (const v of b) for (let k = 7; k >= 0; k--) {
    const fb = ((crc >> 6) & 1) ^ ((v >> k) & 1);
    crc = (crc << 1) & 0x7f;
    if (fb) crc ^= 0x09;
  }
  return crc;
}

// lySdNotes reads a record's SD transactions (command tokens, waits, data blocks).
// TRLC-LINKS: REQ-SDS-242
function lySdNotes(ps) {
  let app = false;
  lyEach(ps, (b) => {
    const parts = [];
    let bad = false;
    for (let i = 0; i < b.length;) {
      const v = b[i];
      if (v === 0xff) { i++; continue; }
      if ((v & 0xc0) === 0x40 && i + 6 <= b.length) {
        const t = b.slice(i, i + 6), idx = v & 0x3f;
        let name = LAYER_TABLES.sdCmdNames[idx], pre = "CMD";
        if (app) { name = LAYER_TABLES.sdACmdNames[idx]; pre = "ACMD"; }
        app = !app && idx === 55;
        let s = pre + idx;
        if (name) s += " " + name;
        const arg = lyBE(t.slice(1, 5));
        if (arg !== 0) s += " 0x" + lyHx(arg, 8);
        const c = t[5];
        if (c !== ((lySdCRC7(t.slice(0, 5)) << 1) | 1) && c !== 0xff && c !== 0x01) { s += " (CRC7)"; bad = true; }
        parts.push(s);
        i += 6;
      } else if (v === 0xfe || v === 0xfc) {
        parts.push("data block, " + lyPlural(b.length - i - 1, "byte"));
        i = b.length;
      } else if (v === 0xfd) { parts.push("stop token"); i++; }
      else {
        parts.push((v & 0xc0) === 0x40 ? "CMD" + (v & 0x3f) + " (cut)" : "? 0x" + lyHx(v, 2));
        bad = true;
        i = b.length;
      }
    }
    return [lyJoin(parts), bad];
  });
}

// TRLC-LINKS: REQ-SDS-242
function lyNrf24Note(b) {
  if (!b.length) return ["", false];
  const c = b[0], d = b.slice(1), T = LAYER_TABLES;
  if ((c & 0xe0) === 0x00 || (c & 0xe0) === 0x20) {
    const w = (c & 0xe0) === 0x20, name = lyReg(T.nrf24Regs, c & 0x1f);
    if (w) {
      if (d.length > 1) return ["W_REGISTER " + name + " = " + lyHexList(lyRev(d), 8), false];
      return ["W_REGISTER " + name + lyRegData(true, d), false];
    }
    return ["R_REGISTER " + name + lyRegData(false, d), false];
  }
  if (c === 0x50) return ["ACTIVATE" + lyRegData(true, d), d.length !== 1 || d[0] !== 0x73];
  if (c === 0x61) return ["R_RX_PAYLOAD" + lyRegData(false, d), false];
  if (c === 0x60) return ["R_RX_PL_WID", false];
  if (c === 0xa0 || c === 0xb0) return [(c === 0xa0 ? "W_TX_PAYLOAD" : "W_TX_PAYLOAD_NOACK") + " " + lyPlural(d.length, "byte") + " " + lyQuoted(d, 16), false];
  if ((c & 0xf8) === 0xa8) return ["W_ACK_PAYLOAD pipe " + (c & 7) + " " + lyPlural(d.length, "byte") + " " + lyQuoted(d, 16), false];
  const one = { 0xe1: "FLUSH_TX", 0xe2: "FLUSH_RX", 0xe3: "REUSE_TX_PL", 0xff: "NOP" }[c];
  if (one) return [one, false];
  return ["? 0x" + lyHx(c, 2), true];
}

// TRLC-LINKS: REQ-SDS-242
function lyNrf905Note(b) {
  if (!b.length) return ["", false];
  const c = b[0], d = b.slice(1);
  if ((c & 0xf0) === 0x00) return ["W_CONFIG " + (c & 0x0f) + lyRegData(true, d), (c & 0x0f) > 9];
  if ((c & 0xf0) === 0x10) return ["R_CONFIG " + (c & 0x0f) + lyRegData(false, d), (c & 0x0f) > 9];
  const fixed = { 0x20: ["W_TX_PAYLOAD", true], 0x21: ["R_TX_PAYLOAD", false], 0x22: ["W_TX_ADDRESS", true], 0x23: ["R_TX_ADDRESS", false], 0x24: ["R_RX_PAYLOAD", false] }[c];
  if (fixed) return [fixed[0] + lyRegData(fixed[1], d), false];
  if ((c & 0xf0) === 0x80) {
    if (d.length < 1) return ["CHANNEL_CONFIG (cut)", true];
    const ch = ((c & 1) << 8) | d[0];
    return ["CHANNEL_CONFIG CH " + ch + " " + ((c & 2) ? "868/915MHz" : "433MHz") + " " + ["-10dBm", "-2dBm", "+6dBm", "+10dBm"][(c >> 2) & 3], false];
  }
  return ["? 0x" + lyHx(c, 2), true];
}

// TRLC-LINKS: REQ-SDS-242
function lyCc1101Note(b) {
  if (!b.length) return ["", false];
  const c = b[0], d = b.slice(1), a = c & 0x3f, T = LAYER_TABLES;
  if (a >= 0x30 && a <= 0x3d) {
    if ((c & 0x40) === 0) {
      const s = T.cc1101Strobes[a];
      return s ? ["STROBE " + s, false] : ["STROBE 0x" + lyHx(a, 2), true];
    }
    if (c & 0x80) return ["STATUS READ " + lyReg(T.cc1101Regs, a) + lyRegData(false, d), false];
    return ["? 0x" + lyHx(c, 2), true];
  }
  return [["WRITE ", "BURST WRITE ", "READ ", "BURST READ "][c >> 6] + lyReg(T.cc1101Regs, a) + lyRegData((c & 0x80) === 0, d), false];
}

// TRLC-LINKS: REQ-SDS-242
function lyRfm12Note(b) {
  const parts = [];
  let bad = false;
  for (let i = 0; i < b.length; i += 2) {
    if (i + 1 >= b.length) { parts.push("(cut)"); bad = true; break; }
    const c = b[i], v = b[i + 1];
    let s;
    if (c === 0x80) s = "CONFIG " + ["315", "433", "868", "915"][(v >> 4) & 3] + "MHz " + lyFixed(85 + (v & 0xf) * 5, 10, 1) + "pF";
    else if (c === 0x82) s = "POWER 0x" + lyHx(v, 2);
    else if ((c & 0xf0) === 0xa0) s = "FREQ " + (((c & 0xf) << 8) | v);
    else if (c === 0xc6) s = "DATA RATE " + lyFixed(10000, 29 * ((v & 0x7f) + 1) * (1 + 7 * (v >> 7)), 3) + "kbps";
    else if ((c & 0xf8) === 0x90) s = "RX CTRL BW " + ["Reserved", "400kHz", "340kHz", "270kHz", "200kHz", "134kHz", "67kHz", "Reserved"][v >> 5];
    else if (c === 0xc2) s = "DATA FILTER";
    else if (c === 0xca) s = "FIFO RESET level " + (v >> 4);
    else if (c === 0xce) s = "SYNC 0x" + lyHx(v, 2);
    else if (c === 0xb0) s = "FIFO READ";
    else if (c === 0xc4) s = "AFC " + ["Off", "Once", "During receiving", "Always"][v >> 6];
    else if ((c & 0xfe) === 0x98) s = "TX CTRL " + 15 * (v >> 4) + "kHz";
    else if (c === 0xcc) s = "PLL";
    else if (c === 0xb8) s = "TX BYTE 0x" + lyHx(v, 2);
    else if (c === 0xfe) s = "RESET";
    else if ((c & 0xe0) === 0xe0) s = "WAKE-UP TIMER";
    else if (c === 0xc8) s = "LOW DUTY CYCLE";
    else if (c === 0xc0) s = "LOW BATTERY " + lyFixed(225 + (v & 0xf) * 10, 100, 2) + "V";
    else if (c === 0x00) s = "STATUS READ";
    else { s = "? 0x" + lyHx(c, 2) + lyHx(v, 2); bad = true; }
    parts.push(s);
  }
  return [lyJoin(parts), bad];
}

// TRLC-LINKS: REQ-SDS-242
function lyMrf24Note(b) {
  const parts = [], T = LAYER_TABLES;
  for (let i = 0; i < b.length;) {
    if ((b[i] & 0x80) === 0) {
      if (i + 2 > b.length) { parts.push("(cut)"); return [lyJoin(parts), true]; }
      const r = (b[i] >> 1) & 0x3f, w = (b[i] & 1) === 1;
      parts.push((w ? "W " : "R ") + lyReg(T.mrf24Short, r) + lyRegData(w, b.slice(i + 1, i + 2)));
      i += 2;
      continue;
    }
    if (i + 3 > b.length) { parts.push("(cut)"); return [lyJoin(parts), true]; }
    const dw = (b[i] << 8) | b[i + 1], w = (dw & 0x10) !== 0, r = (dw >> 5) & 0x3ff;
    let name;
    if (r >= 0x300) name = "RX[0x" + lyHx(r - 0x300, 2) + "]";
    else if (r >= 0x2c0) name = "Reserved 0x" + lyHx(r, 3);
    else if (r >= 0x280) name = "KEY[0x" + lyHx(r - 0x280, 2) + "]";
    else if (r >= 0x200) name = lyReg(T.mrf24Long, r);
    else if (r >= 0x180) name = "TX GTS2[0x" + lyHx(r - 0x180, 2) + "]";
    else if (r >= 0x100) name = "TX GTS1[0x" + lyHx(r - 0x100, 2) + "]";
    else if (r >= 0x80) name = "TX beacon[0x" + lyHx(r - 0x80, 2) + "]";
    else name = "TX[0x" + lyHx(r, 2) + "]";
    parts.push((w ? "W " : "R ") + name + lyRegData(w, b.slice(i + 2, i + 3)));
    i += 3;
  }
  return [lyJoin(parts), false];
}

// lyEncNotes reads a record's ENC28J60 opcodes, the bank ECON1 selects carried on.
// TRLC-LINKS: REQ-SDS-242
function lyEncNotes(ps) {
  const T = LAYER_TABLES, banks = [T.enc28j60Bank0, T.enc28j60Bank1, T.enc28j60Bank2, T.enc28j60Bank3];
  let bank = -1;
  // TRLC-LINKS: REQ-SDS-242
  const reg = (a) => (a >= 0x1b ? T.enc28j60Bank0[a] : bank < 0 ? "bank? 0x" + lyHx(a, 2) : lyReg(banks[bank], a));
  lyEach(ps, (b) => {
    if (!b.length) return ["", false];
    const c = b[0], d = b.slice(1), a = c & 0x1f;
    switch (c >> 5) {
      case 0: return ["RCR " + reg(a) + lyRegData(false, d), false];
      case 1: if (a === 0x1a) return ["RBM" + lyRegData(false, d), false]; break;
      case 2: {
        const s = "WCR " + reg(a) + lyRegData(true, d);
        if (a === 0x1f && d.length) bank = d[0] & 3;
        return [s, d.length === 0];
      }
      case 3: if (a === 0x1a) return ["WBM " + lyPlural(d.length, "byte") + lyRegData(true, d), false]; break;
      case 4: {
        const s = "BFS " + reg(a) + lyRegData(true, d);
        if (a === 0x1f && d.length && bank >= 0) bank |= d[0] & 3;
        return [s, d.length === 0];
      }
      case 5: {
        const s = "BFC " + reg(a) + lyRegData(true, d);
        if (a === 0x1f && d.length && bank >= 0) bank &= ~(d[0] & 3);
        return [s, d.length === 0];
      }
      case 7: if (c === 0xff) return ["SRC (soft reset)", false]; break;
    }
    return ["? 0x" + lyHx(c, 2), true];
  });
}

// lySt25Notes reads a record's ST25R39xx transactions, a Space-B or test prefix carried on.
// TRLC-LINKS: REQ-SDS-242
function lySt25Notes(ps) {
  const T = LAYER_TABLES;
  let space = 0;
  lyEach(ps, (b) => {
    const parts = [];
    let bad = false;
    for (let i = 0; i < b.length; i++) {
      const c = b[i], d = b.slice(i + 1);
      if (c <= 0x7f) {
        let t = T.st25RegsA, sp = "";
        if (space === 1) { t = T.st25RegsB; sp = "B "; }
        if (space === 2) { t = T.st25RegsTest; sp = "T "; }
        space = 0;
        const w = (c & 0x40) === 0;
        parts.push((w ? "WRITE " : "READ ") + sp + lyReg(t, c & 0x3f) + lyRegData(w, d));
        break;
      }
      if (c === 0x80) parts.push("FIFO LOAD" + lyRegData(true, d));
      else if (c === 0x9f) parts.push("FIFO READ" + lyRegData(false, d));
      else if (c === 0xa0 || c === 0xa8 || c === 0xac) parts.push({ 0xa0: "PT LOAD A", 0xa8: "PT LOAD F", 0xac: "PT LOAD TSN" }[c] + lyRegData(true, d));
      else if (c === 0xbf) parts.push("PT READ" + lyRegData(false, d));
      else if (c === 0xfb) { space = 1; parts.push("SPACE B"); continue; }
      else if (c === 0xfc) { space = 2; parts.push("TEST ACCESS"); continue; }
      else if (T.st25Cmds[c] && c >= 0xc0) { parts.push("CMD " + T.st25Cmds[c]); continue; }
      else { parts.push("? 0x" + lyHx(c, 2)); bad = true; }
      break;
    }
    return [lyJoin(parts), bad];
  });
}

// TRLC-LINKS: REQ-SDS-242
function lyAdxl345Note(b) {
  if (!b.length) return ["", false];
  const c = b[0], d = b.slice(1), w = (c & 0x80) === 0, a = c & 0x3f;
  const s = (w ? "W " : "R ") + lyReg(LAYER_TABLES.adxl345Regs, a);
  if (w && d.length === 1) return [s + " = " + lyHx(d[0], 2) + lyAdxl345Value(a, d[0]), false];
  return [s + lyRegData(w, d), (c & 0x40) === 0 && d.length > 1];
}

// TRLC-LINKS: REQ-SDS-242
function lyAdxl345Value(a, v) {
  if (a === 0x2c) return " (" + ["0.10", "0.20", "0.39", "0.78", "1.56", "3.13", "6.25", "12.5", "25", "50", "100", "200", "400", "800", "1600", "3200"][v & 0xf] + " Hz)";
  if (a === 0x31) return " (+-" + (2 << (v & 3)) + "g)";
  if (a === 0x2d) return (v & 0x08) ? " (measure)" : " (standby)";
  return "";
}

// TRLC-LINKS: REQ-SDS-242
function lyAdnsNote(b) {
  const parts = [];
  for (let i = 0; i < b.length; i += 2) {
    const c = b[i], r = c & 0x7f;
    let name = lyReg(LAYER_TABLES.adns5020Regs, r);
    if (r > 0x63) name = "Unknown 0x" + lyHx(r, 2);
    if (i + 1 >= b.length) { parts.push(name + " (cut)"); return [lyJoin(parts), true]; }
    parts.push((c & 0x80) ? "W " + name + " = " + lyHx(b[i + 1], 2) : "R " + name);
  }
  return [lyJoin(parts), false];
}

// TRLC-LINKS: REQ-SDS-242
function lyAde7758Note(b) {
  if (!b.length) return ["", false];
  const c = b[0], d = b.slice(1), r = c & 0x7f, T = LAYER_TABLES;
  const name = T.ade7758Regs[r];
  if (name === undefined) return ["? register 0x" + lyHx(r, 2), true];
  const want = Math.floor((T.ade7758Bits[r] + 7) / 8), w = (c & 0x80) !== 0, op = w ? "W " : "R ";
  if (d.length < want) return [op + name + " (short)", true];
  if (w) return [op + name + " = 0x" + lyHx(lyBE(d.slice(0, want)), 2 * want), d.length !== want];
  return [op + name + ", " + lyPlural(want, "byte"), d.length !== want];
}

// TRLC-LINKS: REQ-SDS-242
function lyAdf435xNote(b) {
  if (b.length % 4 !== 0 || !b.length) return ["? " + lyPlural(b.length, "byte") + " (32-bit words)", true];
  const parts = [];
  for (let i = 0; i < b.length; i += 4) {
    const w = lyBE(b.slice(i, i + 4));
    // TRLC-LINKS: REQ-SDS-242
    const f = (off, n) => (w >>> off) & ((1 << n) - 1);
    let s;
    switch (w & 7) {
      case 0: s = "R0 INT " + f(15, 16) + " FRAC " + f(3, 12); break;
      case 1: s = "R1 MOD " + f(3, 12) + " phase " + f(15, 12) + " prescaler " + ["4/5", "8/9"][f(27, 1)]; break;
      case 2: s = "R2 R " + f(14, 10) + " CP " + f(9, 4) + " MUXOUT " + f(26, 3); break;
      case 3: s = "R3 clock divider " + f(3, 12); break;
      case 4: s = "R4 RF divider /" + (1 << f(20, 3)) + " power " + [-4, -1, 2, 5][f(3, 2)] + "dBm out " + ["off", "on"][f(5, 1)]; break;
      case 5: s = "R5 LD pin " + f(22, 2); break;
      default: s = "R" + (w & 7) + " 0x" + lyHx(w, 8);
    }
    parts.push(s);
  }
  return [lyJoin(parts), false];
}

// TRLC-LINKS: REQ-SDS-242
function lyAd5626Note(b) {
  if (b.length !== 2) return ["? " + lyPlural(b.length, "byte") + " (16-bit loads)", true];
  const v = lyBE(b) & 0xfff;
  return ["DAC " + lyFixed(v, 1000, 3) + " V (0x" + lyHx(v, 3) + ")", false];
}

// TRLC-LINKS: REQ-SDS-242
function lyAd79x0Note(b) {
  if (b.length === 1) return ["ADC power-down", false];
  if (b.length !== 2) return ["? " + lyPlural(b.length, "byte"), true];
  const v = lyBE(b);
  if (v === 0xfff) return ["ADC power-up (invalid)", false];
  return ["ADC " + (v & 0xfff) + "/4095 (" + lyFixed(v & 0xfff, 4095, 4) + " Vref)", v > 0xfff];
}

// TRLC-LINKS: REQ-SDS-242
function lyLtc242xNote(b) {
  if (b.length !== 3) return ["? " + lyPlural(b.length, "byte") + " (24-bit conversions)", true];
  const v = lyBE(b), x = (v & 0x3fffff) - (1 << 21);
  return ["CH" + ((v >> 22) & 1) + " " + lyFixed(x, 0xfffff, 6) + " Vref", (v & 0x800000) !== 0];
}

// TRLC-LINKS: REQ-SDS-242
function lyMax72xxNote(b) {
  if (!b.length || b.length % 2 !== 0) return ["? " + lyPlural(b.length, "byte") + " (pairs)", b.length > 0];
  const parts = [];
  let bad = false;
  for (let i = 0; i < b.length; i += 2) {
    const r = b[i] & 0x0f, v = b[i + 1];
    let s;
    if (r >= 1 && r <= 8) s = "Digit " + r + " = " + lyHx(v, 2);
    else if (r === 10) s = "Intensity " + (v & 0xf);
    else if (r === 11) s = "Scan limit " + (1 + (v & 7));
    else if (r === 12) s = "Shutdown " + ((v & 1) === 1 ? "off" : "on");
    else if (r === 15) s = "Display test " + ((v & 1) === 1 ? "on" : "off");
    else if (r === 9) s = "Decode " + lyHx(v, 2);
    else if (r === 0) s = "No-op";
    else { s = "? register " + lyHx(r, 2); bad = true; }
    if (b.length > 2) s = "chip " + (i / 2 + 1) + ": " + s;
    parts.push(s);
  }
  return [lyJoin(parts), bad];
}

// TRLC-LINKS: REQ-SDS-242
function lyAvrNote(b) {
  const parts = [];
  let bad = false;
  for (let i = 0; i < b.length; i += 4) {
    if (i + 4 > b.length) { parts.push("(cut)"); return [lyJoin(parts), true]; }
    const c = b.slice(i, i + 4);
    let s;
    if (c[0] === 0xac && c[1] === 0x53) s = "PROGRAMMING ENABLE";
    else if (c[0] === 0xac && (c[1] & 0x80)) s = "CHIP ERASE";
    else if (c[0] === 0x50 && c[1] === 0x00 && c[2] === 0x00) s = "READ FUSE";
    else if (c[0] === 0x58 && c[1] === 0x08 && c[2] === 0x00) s = "READ HIGH FUSE";
    else if (c[0] === 0x50 && c[1] === 0x08 && c[2] === 0x00) s = "READ EXT FUSE";
    else if (c[0] === 0x30 && c[2] <= 2) s = "READ SIGNATURE " + c[2];
    else if (c[0] === 0x58 && c[1] === 0x00) s = "READ LOCK";
    else if (c[0] === 0xa0 && (c[1] & 0xc0) === 0) s = "READ EEPROM 0x" + lyHx(((c[1] & 1) << 8) | c[2], 3);
    else if ((c[0] === 0x20 || c[0] === 0x28) && (c[1] & 0xf0) === 0) s = "READ FLASH " + (c[0] === 0x28 ? "H" : "L") + " 0x" + lyHx(((c[1] & 0x0f) << 8) | c[2], 3);
    else if ((c[0] === 0x40 || c[0] === 0x48) && (c[1] & 0xf0) === 0) s = "LOAD PAGE " + (c[0] === 0x48 ? "H" : "L") + " 0x" + lyHx(c[2] & 0x1f, 2) + " = " + lyHx(c[3], 2);
    else if (c[0] === 0x4c && (c[1] & 0xf0) === 0) s = "WRITE PAGE 0x" + lyHx(((c[1] & 0x0f) << 3) + (c[2] << 5), 2);
    else { s = "? " + lyHexList(c, 4); bad = true; }
    parts.push(s);
  }
  return [lyJoin(parts), bad];
}

// TRLC-LINKS: REQ-SDS-242
function lyX2444Note(b) {
  if (!b.length) return ["", false];
  const c = b[0];
  const name = { 0x80: "WRDS", 0x81: "STO", 0x82: "SLEEP", 0x83: "WRITE", 0x84: "WREN", 0x85: "RCL", 0x86: "READ", 0x87: "READ" }[c & 0x87];
  if (!name) return ["? 0x" + lyHx(c, 2), true];
  if (b.length === 1) return [name, false];
  const a = (c >> 3) & 0x0f;
  if (name === "WRITE") return ["WRITE 0x" + a.toString(16).toUpperCase() + " = 0x" + lyHx(lyBE(b.slice(1, 3)), 4), b.length !== 3];
  if (name === "READ") return ["READ 0x" + a.toString(16).toUpperCase(), b.length !== 3];
  return [name, true];
}

// TRLC-LINKS: REQ-SDS-242
function lyRgbNote(b) {
  const parts = [];
  for (let i = 0; i + 3 <= b.length; i += 3) {
    if (parts.length === 6) { parts.push(".."); break; }
    parts.push("#" + lyHx(lyBE(b.slice(i, i + 3)), 6));
  }
  return [lyPlural(Math.floor(b.length / 3), "LED") + ": " + parts.join(" "), b.length % 3 !== 0];
}

// TRLC-LINKS: REQ-SDS-242
function lyNesNote(b) {
  const names = ["A", "B", "Select", "Start", "Up", "Down", "Left", "Right"];
  return [lyJoin(b.map((v) => {
    if (v === 0xff) return "no button";
    if (v === 0x00) return "not connected";
    return names.filter((_, k) => ((v >> (7 - k)) & 1) === 0).join("+");
  })), false];
}

// TRLC-LINKS: REQ-SDS-242
function lySsi32Note(b) {
  if (!b.length) return ["", false];
  if (b[0] & 0x80) return ["ACK 0x" + lyHx(b[0], 2), b.length !== 4];
  if (b.length < 4) return ["CTRL (cut)", true];
  const n = b[2];
  let s = "CTRL 0x" + lyHx(b[0], 2) + " LUN 0x" + lyHx(b[1], 2) + " SIZE " + n + " CRC 0x" + lyHx(b[3], 2);
  const d = b.slice(4, Math.min(b.length, 4 + n));
  if (d.length) s += ": " + lyHexList(d, 8);
  return [s, 4 + n > b.length];
}

// ---- I2C ----

// lyPtr is a register device's pointer: take reads a transfer with an
// n-byte pointer, moving it past the data.
// TRLC-LINKS: REQ-SDS-243
function lyPtr() {
  const r = { ptr: 0, known: false };
  // TRLC-LINKS: REQ-SDS-243
  r.take = (p, n) => {
    if (!p.read) {
      if (p.bytes.length < n) return { reg: 0, data: p.bytes, write: true, ptrOnly: false, known: false };
      const a = { reg: lyBE(p.bytes.slice(0, n)), data: p.bytes.slice(n), write: true, known: true };
      a.ptrOnly = a.data.length === 0;
      r.ptr = a.reg + a.data.length; r.known = true;
      return a;
    }
    const a = { reg: r.ptr, data: p.bytes, write: false, ptrOnly: false, known: r.known };
    if (r.known) r.ptr += p.bytes.length;
    return a;
  };
  return r;
}

// TRLC-LINKS: REQ-SDS-243
function lyBCD(v) { const hi = v >> 4, lo = v & 0x0f; return [hi * 10 + lo, hi < 10 && lo < 10]; }
// TRLC-LINKS: REQ-SDS-243
function lyDev(p, lo, hi) { return p.hasAddr && p.addr >= lo && p.addr <= hi; }
// TRLC-LINKS: REQ-SDS-243
function lyRW(p) { return p.read ? "read" : "write"; }

// lyEepromNotes reads 24xx EEPROM transfers at 0x50..0x57.
// TRLC-LINKS: REQ-SDS-243
function lyEepromNotes(ps) {
  const widths = {}, ptrs = {};
  for (let i = 0; i + 1 < ps.length; i++) {
    const p = ps[i], q = ps[i + 1];
    if (!p.hasAddr || p.read || !q.read || q.addr !== p.addr || p.err) continue;
    const n = p.bytes.length;
    if ((n === 1 || n === 2) && (widths[p.addr] || 0) < n) widths[p.addr] = n;
  }
  for (const p of ps) {
    if (!lyDev(p, 0x50, 0x57)) continue;
    const pre = "EEPROM " + lyHx(p.addr, 2);
    if (p.err === "NAK") { p.note = pre + " busy (no ACK)"; continue; }
    const n = Math.max(widths[p.addr] || 0, 1);
    if (!ptrs[p.addr]) ptrs[p.addr] = lyPtr();
    const a = ptrs[p.addr].take(p, n), at = "@0x" + lyHx(a.reg, 2 * n);
    if (a.write && !a.known) { p.note = pre + " write (no address)"; p.noteErr = true; }
    else if (a.ptrOnly) p.note = pre + " set address " + at;
    else if (a.write && a.data.length === 1) p.note = pre + " byte write " + at + ": " + lyHx(a.data[0], 2);
    else if (a.write) p.note = pre + " page write " + at + ", " + lyPlural(a.data.length, "byte") + ": " + lyHexList(a.data, 8);
    else if (!a.known) p.note = pre + " read (current address), " + lyPlural(a.data.length, "byte") + ": " + lyHexList(a.data, 8);
    else p.note = pre + " read " + at + ", " + lyPlural(a.data.length, "byte") + ": " + lyHexList(a.data, 8);
  }
}

const LY_EDID_HEADER = [0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00];

// TRLC-LINKS: REQ-SDS-243
function lyEdidHas(b, k) { for (let i = 0; i < 8; i++) if (b[k + i] !== LY_EDID_HEADER[i]) return false; return true; }

// lyEdidNotes reads EDID reads at 0x50; the read completing the base block names the display.
// TRLC-LINKS: REQ-SDS-243
function lyEdidNotes(ps) {
  const mem = new Array(256).fill(0), have = new Array(256).fill(false);
  let ptr = lyPtr();
  // TRLC-LINKS: REQ-SDS-243
  const complete = () => { for (let i = 0; i < 128; i++) if (!have[i]) return false; return true; };
  for (const p of ps) {
    if (!lyDev(p, 0x50, 0x50)) continue;
    const a = ptr.take(p, 1);
    if (a.write) { p.note = "EDID offset " + a.reg; continue; }
    if (!a.known) {
      if (a.data.length >= 8 && lyEdidHas(a.data, 0)) {
        a.reg = 0; a.known = true;
        ptr = lyPtr(); ptr.ptr = a.data.length; ptr.known = true;
      } else { p.note = "EDID read (no offset), " + lyPlural(a.data.length, "byte"); continue; }
    }
    const had = complete();
    a.data.forEach((v, k) => { const o = a.reg + k; if (o < 256) { mem[o] = v; have[o] = true; } });
    if (!had && complete()) { const r = lyEdidSummary(mem); p.note = r[0]; p.noteErr = r[1]; continue; }
    p.note = "EDID read @" + a.reg + ", " + lyPlural(a.data.length, "byte");
  }
}

// TRLC-LINKS: REQ-SDS-243
function lyEdidSummary(mem) {
  const m = mem.slice(0, 128);
  if (!lyEdidHas(m, 0)) return ["EDID (no header)", true];
  const id = String.fromCharCode(64 + ((m[8] >> 2) & 0x1f), 64 + (((m[8] & 3) << 3) | (m[9] >> 5)), 64 + (m[9] & 0x1f));
  let s = "EDID " + id + " " + lyHx((m[11] << 8) | m[10], 4);
  for (let d = 54; d + 18 <= 126; d += 18) {
    if (m[d] === 0 && m[d + 1] === 0 && m[d + 3] === 0xfc) {
      s += " '" + lyPrintable(String.fromCharCode(...m.slice(d + 5, d + 18)).split("\n")[0].replace(/ +$/, "")) + "'";
      break;
    }
  }
  const pc = (m[55] << 8) | m[54];
  if (pc !== 0) {
    const h = ((m[58] & 0xf0) << 4) | m[56], v = ((m[61] & 0xf0) << 4) | m[59];
    const hb = ((m[58] & 0x0f) << 8) | m[57], vb = ((m[61] & 0x0f) << 8) | m[60];
    s += " " + h + "x" + v;
    const tot = (h + hb) * (v + vb);
    if (tot > 0) s += "@" + lyFixed(pc * 10000, tot, 0) + "Hz";
  }
  s += " v" + m[18] + "." + m[19];
  const sum = m.reduce((x, y) => x + y, 0);
  if (sum % 256 !== 0) return [s + " checksum bad", true];
  return [s + " checksum ok", false];
}

const LY_WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const LY_DS1307 = { sec: 0, min: 1, hour: 2, wday: 3, day: 4, month: 5, year: 6, from: 1, regs: { 0: "Seconds", 1: "Minutes", 2: "Hours", 3: "Day", 4: "Date", 5: "Month", 6: "Year", 7: "Control" } };
const LY_RTC8564 = { sec: 2, min: 3, hour: 4, wday: 6, day: 5, month: 7, year: 8, from: 0, regs: { 0: "Control1", 1: "Control2", 2: "Seconds", 3: "Minutes", 4: "Hours", 5: "Days", 6: "Weekdays", 7: "Months", 8: "Years", 9: "Minute alarm", 10: "Hour alarm", 11: "Day alarm", 12: "Weekday alarm", 13: "CLKOUT", 14: "Timer control", 15: "Timer" } };

// lyClock reads the date and time out of a register image; null when not BCD in range.
// TRLC-LINKS: REQ-SDS-243
function lyClock(c, r) {
  // TRLC-LINKS: REQ-SDS-243
  const g = (k) => r[k] || 0;
  const [s, ok1] = lyBCD(g(c.sec) & 0x7f), [m, ok2] = lyBCD(g(c.min) & 0x7f);
  const hb = g(c.hour);
  let h, ok3, ampm = "";
  if (c.from === 1 && (hb & 0x40)) {
    [h, ok3] = lyBCD(hb & 0x1f);
    ampm = (hb & 0x20) ? " PM" : " AM";
    ok3 = ok3 && h >= 1 && h <= 12;
  } else {
    [h, ok3] = lyBCD(hb & 0x3f);
    ok3 = ok3 && h < 24;
  }
  const [d, ok4] = lyBCD(g(c.day) & 0x3f), [mo, ok5] = lyBCD(g(c.month) & 0x1f), [y, ok6] = lyBCD(g(c.year));
  const wd = (g(c.wday) & 7) - c.from;
  const century = c.from === 0 && (g(c.month) & 0x80) ? 1900 : 2000;
  if (!(ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && s < 60 && m < 60 && d >= 1 && d <= 31 && mo >= 1 && mo <= 12 && wd >= 0 && wd < 7)) return null;
  // TRLC-LINKS: REQ-SDS-243
  const p2 = (v) => String(v).padStart(2, "0");
  return String(century + y).padStart(4, "0") + "-" + p2(mo) + "-" + p2(d) + " " + p2(h) + ":" + p2(m) + ":" + p2(s) + ampm + " " + LY_WEEKDAYS[wd];
}

// lyRtcNotes reads a clock's register accesses at its address.
// TRLC-LINKS: REQ-SDS-243
function lyRtcNotes(ps, c, addr) {
  const r = lyPtr(), img = {};
  for (const p of ps) {
    if (!lyDev(p, addr, addr)) continue;
    const a = r.take(p, 1);
    if (!a.known) { p.note = "RTC " + lyRW(p) + " (no register), " + lyPlural(a.data.length, "byte"); continue; }
    if (a.ptrOnly) { p.note = "RTC set register " + lyReg(c.regs, a.reg); continue; }
    a.data.forEach((v, k) => { img[a.reg + k] = v; });
    const lo = Math.min(c.sec, c.year), hi = Math.max(c.sec, c.year);
    if (a.reg <= lo && a.reg + a.data.length > hi) {
      const t = lyClock(c, img);
      if (t !== null) p.note = "RTC " + lyRW(p) + " " + t;
      else { p.note = "RTC " + lyRW(p) + " time not BCD: " + lyHexList(a.data, 8); p.noteErr = true; }
      continue;
    }
    const parts = [];
    for (let k = 0; k < a.data.length; k++) {
      if (parts.length === 4) { parts.push(".."); break; }
      const name = c.regs[a.reg + k] !== undefined ? c.regs[a.reg + k] : "0x" + lyHx(a.reg + k, 2);
      parts.push(name + "=" + lyHx(a.data[k], 2));
    }
    p.note = "RTC " + lyRW(p) + " " + parts.join(" ");
  }
}

// TRLC-LINKS: REQ-SDS-243
function lyLm75Notes(ps) {
  const ptrs = {}, T = LAYER_TABLES.lm75Regs;
  for (const p of ps) {
    if (!lyDev(p, 0x48, 0x4f)) continue;
    const pre = "LM75 " + lyHx(p.addr, 2) + " ";
    let data = p.bytes;
    if (!p.read && data.length) {
      ptrs[p.addr] = data[0] & 3;
      data = data.slice(1);
      if (!data.length) { p.note = pre + "pointer " + lyReg(T, ptrs[p.addr]); continue; }
    }
    const reg = ptrs[p.addr] || 0;
    if (reg === 1 && data.length >= 1) {
      const c = data[0];
      p.note = pre + lyRW(p) + " Config " + lyHx(c, 2) + ": " + ((c & 1) ? "shutdown" : "normal") + " " + ((c & 2) ? "interrupt" : "comparator");
    } else if (reg !== 1 && data.length >= 2) {
      p.note = pre + lyRW(p) + " " + lyReg(T, reg) + " " + lyFixed(lyInt8(data[0]) * 2 + (data[1] >> 7), 2, 1) + " C";
    } else p.note = pre + lyRW(p) + " " + lyReg(T, reg) + ", " + lyPlural(data.length, "byte");
  }
}

// TRLC-LINKS: REQ-SDS-243
function lySmbusPEC(b) {
  let crc = 0;
  for (const v of b) {
    crc ^= v;
    for (let k = 0; k < 8; k++) crc = (crc & 0x80) ? ((crc << 1) ^ 0x07) & 0xff : (crc << 1) & 0xff;
  }
  return crc;
}

// TRLC-LINKS: REQ-SDS-243
function lyMlxNotes(ps) {
  const T = LAYER_TABLES.mlxRegs;
  let cmd = -1;
  for (const p of ps) {
    if (!p.hasAddr || (p.addr !== 0x5a && p.addr !== 0x00)) continue;
    const b = p.bytes;
    if (!p.read) {
      if (b.length === 1) { cmd = b[0]; p.note = "MLX90614 command " + lyReg(T, cmd & 0x3f); }
      else if (b.length >= 3) { p.note = "MLX90614 write " + lyReg(T, b[0] & 0x3f) + " = 0x" + lyHx((b[2] << 8) | b[1], 4); cmd = -1; }
      continue;
    }
    if (cmd < 0 || b.length < 3) { p.note = "MLX90614 read, " + lyPlural(b.length, "byte"); continue; }
    const w = (b[1] << 8) | b[0];
    const pec = lySmbusPEC([(p.addr << 1) & 0xff, cmd, ((p.addr << 1) | 1) & 0xff, b[0], b[1]]);
    const c = cmd & 0x3f;
    let v;
    if (cmd < 0x20 && c >= 6 && c <= 8) v = (w & 0x8000) ? "error flag" : lyFixed(w * 2 - 27315, 100, 2) + " C";
    else v = "0x" + lyHx(w, 4);
    p.note = "MLX90614 " + lyReg(T, c) + " " + v;
    if (pec !== b[2]) { p.note += " PEC bad"; p.noteErr = true; }
    cmd = -1;
  }
}

// TRLC-LINKS: REQ-SDS-243
function lyAtshaCRC(b) {
  let crc = 0;
  for (const v of b) for (let k = 0; k < 8; k++) {
    const bit = (v >> k) & 1, top = (crc >> 15) & 1;
    crc = (crc << 1) & 0xffff;
    if (bit !== top) crc ^= 0x8005;
  }
  return crc;
}

// TRLC-LINKS: REQ-SDS-243
function lyAtshaOK(b) {
  if (b.length < 3 || b[0] !== b.length) return false;
  const c = lyAtshaCRC(b.slice(0, b.length - 2));
  return b[b.length - 2] === (c & 0xff) && b[b.length - 1] === c >> 8;
}

// TRLC-LINKS: REQ-SDS-243
function lyAtshaNotes(ps) {
  const T = LAYER_TABLES;
  for (const p of ps) {
    if (!p.hasAddr || p.addr !== 0x64) continue;
    const b = p.bytes;
    if (p.read) {
      if (!b.length) p.note = "ATSHA204A read";
      else if (b.length === 4) p.note = "ATSHA204A status " + (T.atshaStatus[b[1]] !== undefined ? T.atshaStatus[b[1]] : "0x" + lyHx(b[1], 2));
      else p.note = "ATSHA204A response, " + lyPlural(Math.max(b.length - 3, 0), "byte");
      if (b.length && !lyAtshaOK(b)) { p.note += " CRC bad"; p.noteErr = true; }
      continue;
    }
    if (!b.length) { p.note = "ATSHA204A wake"; continue; }
    if (b[0] <= 2) { p.note = "ATSHA204A " + ["reset", "sleep", "idle"][b[0]]; continue; }
    if (b[0] !== 3) { p.note = "ATSHA204A word address 0x" + lyHx(b[0], 2); p.noteErr = true; continue; }
    const c = b.slice(1);
    if (c.length < 7) { p.note = "ATSHA204A command (cut)"; p.noteErr = true; continue; }
    const op = T.atshaOps[c[1]] !== undefined ? T.atshaOps[c[1]] : "0x" + lyHx(c[1], 2);
    p.note = "ATSHA204A " + op + " param1 " + lyHx(c[2], 2) + " param2 " + lyHx((c[4] << 8) | c[3], 4);
    if (c.length - 7 > 0) p.note += ", " + lyPlural(c.length - 7, "data byte");
    if (!lyAtshaOK(c)) { p.note += " CRC bad"; p.noteErr = true; }
  }
}

// TRLC-LINKS: REQ-SDS-243
function lyHdcpNotes(ps) {
  const T = LAYER_TABLES, r = lyPtr();
  for (const p of ps) {
    if (!lyDev(p, 0x3a, 0x3a)) continue;
    const a = r.take(p, 1);
    if (!a.known) { p.note = "HDCP " + lyRW(p) + ", " + lyPlural(a.data.length, "byte"); continue; }
    const name = lyReg(T.hdcpRegs, a.reg);
    if (a.ptrOnly) { p.note = "HDCP offset " + name; continue; }
    const d = a.data;
    let s = "HDCP " + lyRW(p) + " " + name;
    if ((a.reg === 0x60 || a.reg === 0x80) && d.length > 0) s += " " + (T.hdcpMsgs[d[0]] !== undefined ? T.hdcpMsgs[d[0]] : "message 0x" + lyHx(d[0], 2));
    else if (a.reg === 0x41 && d.length >= 2) {
      const v = (d[1] << 8) | d[0];
      s += " " + (v & 0x7f) + " devices depth " + ((v >> 8) & 7);
      if (v & 0x1000) s += " HDMI";
    } else if (a.reg === 0x70 && d.length >= 2) {
      const v = (d[1] << 8) | d[0];
      s += " length " + (v & 0x3ff);
      if (v & 0x400) s += " ready";
      if (v & 0x800) s += " reauth";
    } else if (a.reg === 0x50 && d.length >= 1) s += (d[0] & 4) ? " HDCP2" : " not HDCP2";
    else s += ": " + lyHexList(d, 8);
    p.note = s;
  }
}

// TRLC-LINKS: REQ-SDS-243
function lyLtcNotes(ps) {
  const T = LAYER_TABLES;
  for (const p of ps) {
    if (!p.hasAddr || p.read || !p.bytes.length) continue;
    const c = p.bytes[0], cmd = T.ltcCmds[c >> 4], dac = T.ltcDACs[c & 0xf];
    if (cmd === undefined || dac === undefined) { p.note = "LTC26x7 ? 0x" + lyHx(c, 2); p.noteErr = true; continue; }
    let s = "LTC26x7 " + cmd + " " + dac;
    if (p.bytes.length >= 3) { const v = lyBE(p.bytes.slice(1, 3)); s += " 0x" + lyHx(v, 4) + " (" + lyFixed(v, 0xffff, 4) + " Vref)"; }
    p.note = s;
  }
}

// TRLC-LINKS: REQ-SDS-243
function lyPcaNotes(ps) {
  for (const p of ps) {
    if (!lyDev(p, 0x25, 0x25) || !p.bytes.length) continue;
    p.note = (p.read ? "PCA9571 read " : "PCA9571 outputs ") + lyHexList(p.bytes, 8);
  }
}

// lyRegNotes reads a simple register device's accesses at lo..hi.
// TRLC-LINKS: REQ-SDS-243
function lyRegNotes(ps, lo, hi, chip, t) {
  const ptrs = {};
  for (const p of ps) {
    if (!lyDev(p, lo, hi)) continue;
    if (!ptrs[p.addr]) ptrs[p.addr] = lyPtr();
    const a = ptrs[p.addr].take(p, 1), pre = chip + " " + lyHx(p.addr, 2) + " ";
    if (!a.known) p.note = pre + lyRW(p) + " (no register), " + lyPlural(a.data.length, "byte");
    else if (a.ptrOnly) p.note = pre + "register " + lyReg(t, a.reg);
    else {
      const parts = [];
      for (let k = 0; k < a.data.length; k++) {
        if (parts.length === 4) { parts.push(".."); break; }
        parts.push(lyReg(t, a.reg + k) + "=" + lyHx(a.data[k], 2));
      }
      p.note = pre + lyRW(p) + " " + parts.join(" ");
    }
  }
}

// TRLC-LINKS: REQ-SDS-243
function lyMxcNotes(ps) {
  const T = LAYER_TABLES.mxcRegs, r = lyPtr();
  for (const p of ps) {
    if (!lyDev(p, 0x15, 0x15)) continue;
    const a = r.take(p, 1);
    if (!a.known) p.note = "MXC6225XU " + lyRW(p) + ", " + lyPlural(a.data.length, "byte");
    else if (a.ptrOnly) p.note = "MXC6225XU register " + lyReg(T, a.reg);
    else p.note = "MXC6225XU " + lyRW(p) + " " + a.data.map((v, k) => (a.reg + k <= 1 ? lyReg(T, a.reg + k) + "=" + lyInt8(v) : lyReg(T, a.reg + k) + "=" + lyHx(v, 2))).join(" ");
  }
}

// TRLC-LINKS: REQ-SDS-243
function lyNunchukNotes(ps) {
  for (const p of ps) {
    if (!lyDev(p, 0x52, 0x52)) continue;
    const b = p.bytes;
    if (!p.read) {
      if (b.length === 2 && b[0] === 0x40 && b[1] === 0x00) p.note = "Nunchuk init";
      else if (b.length === 2 && (b[0] === 0xf0 || b[0] === 0xfb)) p.note = "Nunchuk init (unencrypted) " + lyHexList(b, 2);
      else if (b.length === 1 && b[0] === 0) p.note = "Nunchuk request report";
      else p.note = "Nunchuk write " + lyHexList(b, 8);
      continue;
    }
    if (b.length < 6) { p.note = "Nunchuk read, " + lyPlural(b.length, "byte"); continue; }
    const ax = (b[2] << 2) | ((b[5] >> 2) & 3), ay = (b[3] << 2) | ((b[5] >> 4) & 3), az = (b[4] << 2) | ((b[5] >> 6) & 3);
    let s = "Nunchuk stick " + b[0] + "/" + b[1] + " accel " + ax + "/" + ay + "/" + az;
    if ((b[5] & 1) === 0) s += " Z";
    if ((b[5] & 2) === 0) s += " C";
    p.note = s;
  }
}

// TRLC-LINKS: REQ-SDS-243
function lyXfpModule(id) {
  const n = { 1: "GBIC", 3: "SFP", 5: "XENPAK", 6: "XFP", 7: "XFF", 8: "XFP-E", 9: "XPAK", 10: "X2" }[id];
  return n || "module 0x" + lyHx(id, 2);
}

// TRLC-LINKS: REQ-SDS-243
function lyXfpText(b) { return lyPrintable(String.fromCharCode(...b).replace(/\x00+$/, "").replace(/ +$/, "")); }

// TRLC-LINKS: REQ-SDS-243
function lyXfpNotes(ps) {
  const mem = new Array(256).fill(0), r = lyPtr();
  for (const p of ps) {
    if (!lyDev(p, 0x50, 0x50)) continue;
    const a = r.take(p, 1);
    if (a.write) { p.note = a.ptrOnly ? "XFP offset " + a.reg : "XFP write @" + a.reg + ": " + lyHexList(a.data, 8); continue; }
    if (!a.known) { p.note = "XFP read (no offset), " + lyPlural(a.data.length, "byte"); continue; }
    a.data.forEach((v, k) => { if (a.reg + k < 256) mem[a.reg + k] = v; });
    // TRLC-LINKS: REQ-SDS-243
    const covers = (lo, hi) => a.reg <= lo && a.reg + a.data.length > hi;
    const parts = [];
    if (covers(0, 0)) parts.push(lyXfpModule(mem[0]));
    if (covers(96, 105)) {
      const t = lyInt16((mem[96] << 8) | mem[97]), bias = (mem[100] << 8) | mem[101], tx = (mem[102] << 8) | mem[103], rx = (mem[104] << 8) | mem[105];
      parts.push(lyFixed(t, 256, 1) + " C bias " + lyFixed(bias * 2, 10000, 2) + " mA TX " + lyFixed(tx, 10000, 3) + " mW RX " + lyFixed(rx, 10000, 3) + " mW");
    }
    if (covers(128, 128)) parts.push(lyXfpModule(mem[128]));
    if (covers(148, 163)) parts.push("'" + lyXfpText(mem.slice(148, 164)) + "'");
    if (covers(168, 183)) parts.push("PN '" + lyXfpText(mem.slice(168, 184)) + "'");
    if (covers(186, 187)) parts.push(lyFixed((mem[186] << 8) | mem[187], 20, 2) + " nm");
    if (covers(196, 211)) parts.push("SN '" + lyXfpText(mem.slice(196, 212)) + "'");
    p.note = parts.length ? "XFP " + parts.join(" ") : "XFP read @" + a.reg + ", " + lyPlural(a.data.length, "byte");
  }
}

// TRLC-LINKS: REQ-SDS-243
function lyDemuxNotes(ps) {
  const devs = {}, count = {};
  let n = 0;
  for (const p of ps) {
    if (!p.hasAddr) continue;
    if (devs[p.addr] === undefined) devs[p.addr] = ++n;
    count[p.addr] = (count[p.addr] || 0) + 1;
    p.note = "dev " + devs[p.addr] + " (0x" + lyHx(p.addr, 2) + ") #" + count[p.addr] + " " + lyRW(p) + ", " + lyPlural(p.bytes.length, "byte");
  }
}

// lyFilterI2C keeps the transfers to one address (-1 any) in one direction (0 both, 1 read, 2 write).
// TRLC-LINKS: REQ-SDS-243
function lyFilterI2C(ps, addr, dir) {
  return ps.filter((p) => p.hasAddr && !(addr >= 0 && p.addr !== addr) && !(dir === 1 && !p.read) && !(dir === 2 && p.read)).map((p) => {
    p.note = "0x" + lyHx(p.addr, 2) + " " + lyRW(p) + ", " + lyPlural(p.bytes.length, "byte");
    return p;
  });
}

// ---- UART ----

// TRLC-LINKS: REQ-SDS-244
function lySbusNote(p) {
  const b = p.bytes;
  if (b.length !== 25 || b[0] !== 0x0f) return ["S.BUS? " + lyPlural(b.length, "byte") + " (25 from 0x0F)", true];
  let s = "S.BUS", bit = 0;
  for (let k = 0; k < 16; k++) {
    let v = 0;
    for (let j = 0; j < 11; j++) { v |= ((b[1 + (bit >> 3)] >> (bit & 7)) & 1) << j; bit++; }
    s += " " + v;
  }
  const f = b[23];
  if (f & 1) s += " ch17";
  if (f & 2) s += " ch18";
  if (f & 4) s += " lost";
  if (f & 8) s += " failsafe";
  const end = b[24], ok = end === 0x00 || (end & 0x0f) === 0x04;
  if (!ok) s += " end " + lyHx(end, 2);
  if (lyAnyBad(p)) return [s + " (parity)", true];
  return [s, !ok];
}

// TRLC-LINKS: REQ-SDS-244
function lyItmText(v) { return (v >= 0x20 && v < 0x7f) || (v >= 9 && v <= 13); }

// lyItmParse reads ITM packets; returns [items, bad].
// TRLC-LINKS: REQ-SDS-244
function lyItmParse(b) {
  const items = [], T = LAYER_TABLES;
  let bad = false, textCh = -1, text = [];
  // TRLC-LINKS: REQ-SDS-244
  const flush = () => { if (textCh >= 0) { items.push("ch" + textCh + " " + lyQuoted(text, 24)); textCh = -1; text = []; } };
  for (let i = 0; i < b.length;) {
    const h = b[i];
    if (h === 0x00) {
      let j = i;
      while (j < b.length && b[j] === 0) j++;
      flush();
      if (j < b.length && b[j] === 0x80 && j - i >= 5) { items.push("SYNC"); i = j + 1; continue; }
      items.push("(zeros)"); i = j; continue;
    }
    if (h === 0x70) { flush(); items.push("OVERFLOW"); i++; continue; }
    if ((h & 0x0f) === 0x00) {
      flush();
      if ((h & 0x80) === 0) { items.push("TS +" + (h >> 4)); i++; continue; }
      let ts = 0, j = i + 1;
      for (let k = 0; j < b.length && k < 4; k++) { ts += (b[j] & 0x7f) * Math.pow(2, 7 * k); j++; if ((b[j - 1] & 0x80) === 0) break; }
      if (b[j - 1] & 0x80) { items.push("TS (cut)"); return [items, true]; }
      items.push("TS +" + ts); i = j; continue;
    }
    if ((h & 0x0b) === 0x08) {
      flush();
      let j = i + 1;
      while ((h & 0x80) && j < b.length && (b[j - 1] & 0x80)) j++;
      items.push("EXT"); i = j; continue;
    }
    if ((h & 0x0f) === 0x04) { flush(); items.push("? 0x" + lyHx(h, 2)); bad = true; i++; continue; }
    const n = [0, 1, 2, 4][h & 3];
    if (i + 1 + n > b.length) { flush(); items.push("(cut)"); return [items, true]; }
    const d = b.slice(i + 1, i + 1 + n), id = h >> 3;
    i += 1 + n;
    if ((h & 0x04) === 0) {
      if (n === 1 && lyItmText(d[0])) {
        if (textCh !== id) { flush(); textCh = id; }
        text.push(d[0]);
        continue;
      }
      flush();
      items.push("ch" + id + " 0x" + lyHx(lyLE(d), 2 * n));
      continue;
    }
    flush();
    if (id === 0 && n === 1) items.push("DWT events 0x" + lyHx(d[0], 2));
    else if (id === 1 && n === 2) {
      const exc = ((d[1] & 1) << 8) | d[0];
      const name = T.itmExceptions[exc] !== undefined ? T.itmExceptions[exc] : "IRQ " + (exc - 16);
      const ev = { 1: "enter", 2: "exit", 3: "resume" }[d[1] >> 4] || "";
      items.push((ev + " " + name).trim());
    } else if (id === 2) items.push(n === 4 ? "PC 0x" + lyHx(lyLE(d), 8) : "PC sleep");
    else if ((h & 0xc4) === 0x84) items.push("WP" + ((h >> 4) & 3) + " " + ((h & 0x08) ? "write" : "read") + " 0x" + lyHx(lyLE(d), 2 * n));
    else if ((h & 0xcf) === 0x47) items.push("WP" + ((h >> 4) & 3) + " PC 0x" + lyHx(lyLE(d), 8));
    else if ((h & 0xcf) === 0x4e) items.push("WP" + ((h >> 4) & 3) + " addr 0x" + lyHx(lyLE(d), 4));
    else items.push("HW" + id + " 0x" + lyHx(lyLE(d), 2 * n));
  }
  flush();
  return [items, bad];
}

// lyTpiuDemux reads TPIU frames into the bytes of each stream: [ids, data, cut].
// TRLC-LINKS: REQ-SDS-244
function lyTpiuDemux(b) {
  let start = 0;
  for (let k = 0; k + 4 <= b.length; k++) if (b[k] === 0xff && b[k + 1] === 0xff && b[k + 2] === 0xff && b[k + 3] === 0x7f) start = k + 4;
  const ids = [], data = [];
  let cur = -1;
  // TRLC-LINKS: REQ-SDS-244
  const emit = (v) => {
    if (cur < 0) return;
    if (!ids.length || ids[ids.length - 1] !== cur) { ids.push(cur); data.push([]); }
    data[data.length - 1].push(v);
  };
  let i = start;
  for (; i + 16 <= b.length; i += 16) {
    const f = b.slice(i, i + 16), low = f[15];
    for (let k = 0; k < 15; k += 2) {
      const lb = (low >> (k / 2)) & 1;
      let delayed = -1;
      if (f[k] & 1) {
        if (lb) delayed = f[k] >> 1;
        else cur = f[k] >> 1;
      } else emit(f[k] | lb);
      if (k < 14) emit(f[k + 1]);
      if (delayed >= 0) cur = delayed;
    }
  }
  return [ids, data, i < b.length];
}

// lyEtmParse reads ETMv3 packets with the branch state st ({addr, thumb}); BigInt for the 35-bit addresses.
// TRLC-LINKS: REQ-SDS-244
function lyEtmParse(st, b) {
  const items = [];
  // TRLC-LINKS: REQ-SDS-244
  const varint = (from, max) => {
    let v = 0n, n = 0;
    while (from + n < b.length && n < max) {
      v |= BigInt(b[from + n] & 0x7f) << BigInt(7 * n);
      n++;
      if ((b[from + n - 1] & 0x80) === 0) return [v, n, true];
    }
    return [v, n, false];
  };
  for (let i = 0; i < b.length;) {
    const h = b[i];
    if (h === 0x00) {
      let j = i;
      while (j < b.length && b[j] === 0) j++;
      if (j < b.length && b[j] === 0x80 && j - i >= 4) { items.push("A-SYNC"); i = j + 1; } else { items.push("(zeros)"); i = j; }
    } else if (h & 1) {
      let [a, n, ok] = varint(i, 5);
      if (!ok) { items.push("B (cut)"); return [items, true]; }
      const bits = BigInt(7 * n - 1);
      const exc = n >= 2 && ((a >> bits) & 1n) !== 0n;
      if (exc) a &= ~(1n << bits);
      if (n === 5) st.thumb = (b[i + 4] & 0xb0) === 0x10;
      let full = bits;
      if (st.thumb) a &= ~1n;
      else { a = (a & ~1n) << 1n; full += 1n; }
      if (n < 5) a |= st.addr & ~((1n << full) - 1n);
      a &= 0xffffffffn;
      st.addr = a;
      let s = "B 0x" + lyHx(Number(a), 8);
      i += n;
      if (exc) {
        const [e, m, eok] = varint(i, 3);
        if (!eok) { items.push(s + " (cut)"); return [items, true]; }
        s += " exc " + (Number((e >> 1n) & 0x0fn) | Number((e >> 7n) & 0x1f0n));
        i += m;
      }
      items.push(s);
    } else if (h === 0x08) {
      if (i + 6 > b.length) { items.push("I-SYNC (cut)"); return [items, true]; }
      const info = b[i + 1], a = lyLE(b.slice(i + 2, i + 6));
      st.thumb = a % 2 === 1;
      st.addr = BigInt(a - (a % 2));
      items.push("I-SYNC 0x" + lyHx(Number(st.addr), 8) + " " + (st.thumb ? "thumb" : "arm") + " " + ["periodic", "trace on", "overflow", "debug exit"][(info >> 5) & 3]);
      i += 6;
    } else if (h === 0x0c) { items.push("TRIGGER"); i++; }
    else if (h === 0x76) { items.push("EXC EXIT"); i++; }
    else if (h === 0x7e) { items.push("EXC ENTRY"); i++; }
    else if (h === 0x66) i++;
    else if ((h & 0x83) === 0x80) {
      const e = (h >> 2) & 15, nn = (h >> 6) & 1;
      items.push(e + "E" + (nn > 0 ? " " + nn + "N" : ""));
      i++;
    } else if ((h & 0xf3) === 0x82) { items.push("2 atoms " + ((h & 8) ? "N" : "E") + ((h & 4) ? "N" : "E")); i++; }
    else if ((h & 0xfb) === 0x42) {
      const [, n, ok] = varint(i + 1, 9);
      if (!ok) { items.push("TS (cut)"); return [items, true]; }
      items.push("TS"); i += 1 + n;
    } else if (h === 0x04) {
      const [, n, ok] = varint(i + 1, 5);
      if (!ok) { items.push("CYC (cut)"); return [items, true]; }
      items.push("CYC"); i += 1 + n;
    } else { items.push("? 0x" + lyHx(h, 2)); return [items, true]; }
  }
  return [items, false];
}

// TRLC-LINKS: REQ-SDS-244
function lyAmuletHex(b) {
  let v = 0;
  for (const c of b) {
    let d;
    if (c >= 0x30 && c <= 0x39) d = c - 0x30;
    else if (c >= 0x41 && c <= 0x46) d = c - 0x41 + 10;
    else if (c >= 0x61 && c <= 0x66) d = c - 0x61 + 10;
    else return [0, false];
    v = v * 16 + d;
  }
  return [v, true];
}

// TRLC-LINKS: REQ-SDS-244
function lyAmuletNote(b) {
  const parts = [], T = LAYER_TABLES.amuletCmds;
  for (let i = 0; i < b.length;) {
    const c = T[b[i]];
    if (!c) { parts.push("? 0x" + lyHx(b[i], 2)); return [lyJoin(parts), true]; }
    const [name, form] = c;
    i++;
    let need = { r: 2, b: 4, w: 6, c: 16, "-": 0, p: 4, s: 2, a: 2 }[form];
    if (i + need > b.length) { parts.push(name + " (cut)"); return [lyJoin(parts), true]; }
    const f = b.slice(i, i + need);
    let [addr, aok] = lyAmuletHex(f.slice(0, Math.min(2, f.length)));
    let s = name;
    if (form === "r") s += " 0x" + lyHx(addr, 2);
    else if (form === "b" || form === "w") {
      const [v, vok] = lyAmuletHex(f.slice(2));
      aok = aok && vok;
      s += " 0x" + lyHx(addr, 2) + " = 0x" + lyHx(v, f.length - 2);
    } else if (form === "c") {
      const xy = [];
      for (let k = 0; k < 4; k++) { const [v, vok] = lyAmuletHex(f.slice(4 * k, 4 * k + 4)); aok = aok && vok; xy.push(String(v)); }
      s += " " + xy.join(",");
    } else if (form === "p") {
      const sum = (0xa0 + f[0] + f[1] + f[2] + f[3]) & 0xff;
      const [page, pok] = lyAmuletHex(f.slice(1, 3));
      aok = f[0] === 0x02 && sum === 0 && pok;
      s += " 0x" + lyHx(page, 2);
    } else if (form === "s" || form === "a") {
      let j = i + 2;
      while (j < b.length && b[j] !== 0) j++;
      if (j >= b.length) { parts.push(name + " (no NUL)"); return [lyJoin(parts), true]; }
      s += " 0x" + lyHx(addr, 2) + " " + lyQuoted(b.slice(i + 2, j), 24);
      need = j + 1 - i;
    }
    if (form === "-") aok = true;
    if (!aok) { parts.push(s + " (bad)"); return [lyJoin(parts), true]; }
    parts.push(s);
    i += need;
  }
  return [lyJoin(parts), false];
}

// lyPanNotes reads a record's PAN1321 lines (CR LF ended, spanning bursts).
// TRLC-LINKS: REQ-SDS-244
function lyPanNotes(ps) {
  let line = [];
  lyEach(ps, (b) => {
    const parts = [];
    let bad = false;
    for (const v of b) {
      line.push(v);
      const n = line.length;
      if (n < 2 || line[n - 2] !== 13 || v !== 10) continue;
      const s = String.fromCharCode(...line.slice(0, n - 2));
      line = [];
      if (s.startsWith("AT+JSLN")) { const k = s.indexOf(","); parts.push("name '" + lyPrintable(k >= 0 ? s.slice(k + 1) : s) + "'"); }
      else if (s.startsWith("AT+JSEC")) parts.push("PIN " + lyPrintable(s.length >= 4 ? s.slice(s.length - 4) : s));
      else if (s.startsWith("AT+JAAC=")) { parts.push(s.slice(8) === "1" ? "auto-accept on" : "auto-accept off"); bad = bad || (s.slice(8) !== "0" && s.slice(8) !== "1"); }
      else if (s.startsWith("AT+JPRO=")) parts.push("production mode " + lyPrintable(s.slice(8)));
      else if (s === "AT+JRES") parts.push("reset");
      else if (s.startsWith("AT+JSDA=")) {
        const r = s.slice(8), k = r.indexOf(","), l = k >= 0 ? r.slice(0, k) : r, d = k >= 0 ? r.slice(k + 1) : "";
        parts.push("send " + lyPrintable(l) + ": " + lyQuoted([...d].map((ch) => ch.charCodeAt(0)), 16));
        bad = bad || String(d.length) !== l;
      } else if (s === "ROK") parts.push("ready");
      else if (s === "OK") parts.push("OK");
      else if (s.startsWith("ERR")) { parts.push("ERR " + lyPrintable(s.slice(3).replace(/^=/, ""))); bad = true; }
      else parts.push(lyQuoted([...s].map((ch) => ch.charCodeAt(0)), 24));
    }
    return parts.length ? ["PAN1321 " + lyJoin(parts), bad] : ["", false];
  });
}

// ---- 1-Wire ----

// lyOwRead reads a reset-opened packet as {cmd, rom, romBad, fn, ok}.
// TRLC-LINKS: REQ-SDS-245
function lyOwRead(p) {
  const b = p.bytes;
  if (!b.length) return { cmd: "", rom: null, romBad: false, fn: [], ok: false };
  const c = LAYER_TABLES.owROMCmds[b[0]];
  if (!c) return { cmd: "ROM 0x" + lyHx(b[0], 2), rom: null, romBad: false, fn: b.slice(1), ok: false };
  const t = { cmd: c[0], rom: null, romBad: false, fn: b.slice(1), ok: true };
  if (c[1]) {
    if (b.length < 9) { t.rom = b.slice(1); t.fn = []; t.romBad = true; return t; }
    t.rom = b.slice(1, 9); t.fn = b.slice(9);
    for (let k = 1; k < 9 && k < (p.byteErr || []).length; k++) t.romBad = t.romBad || !!p.byteErr[k];
  }
  return t;
}

// TRLC-LINKS: REQ-SDS-245
function lyOwROM(r) {
  if (r.length < 8) return "(cut ROM)";
  return lyHx(r[0], 2) + "-" + [6, 5, 4, 3, 2, 1].map((k) => lyHx(r[k], 2)).join("");
}

// lyOwNotes writes each 1-Wire transaction, dev reading its function bytes.
// TRLC-LINKS: REQ-SDS-245
function lyOwNotes(ps, dev) {
  for (const p of ps) {
    if (!p.bytes.length) { if (!p.err) p.note = "RESET"; continue; }
    const t = lyOwRead(p);
    let s = t.cmd;
    if (t.rom !== null) { s += " " + lyOwROM(t.rom); if (t.romBad) s += " (CRC)"; }
    let bad = t.romBad || !t.ok;
    if (t.fn.length) {
      let f = "", fb = false;
      if (dev) [f, fb] = dev(t.fn);
      if (!f) f = lyHexList(t.fn, 10);
      s += ": " + f;
      bad = bad || fb;
    }
    p.note = s; p.noteErr = bad;
  }
}

// TRLC-LINKS: REQ-SDS-245
function lyCrc8Maxim(b) {
  let crc = 0;
  for (const v of b) { crc ^= v; for (let k = 0; k < 8; k++) crc = (crc & 1) ? (crc >> 1) ^ 0x8c : crc >> 1; }
  return crc;
}

// TRLC-LINKS: REQ-SDS-245
function lyCrc16Maxim(b) {
  let crc = 0;
  for (let x of b) for (let k = 0; k < 8; k++) { crc = ((x ^ crc) & 1) ? (crc >> 1) ^ 0xa001 : crc >> 1; x >>= 1; }
  return crc ^ 0xffff;
}

// TRLC-LINKS: REQ-SDS-245
function lyDs28ea00Fn(fn) {
  const names = { 0x4e: "Write scratchpad", 0xbe: "Read scratchpad", 0x48: "Copy scratchpad", 0x44: "Convert T", 0xb4: "Read power mode", 0xb8: "Recall EEPROM", 0xf5: "PIO read", 0xa5: "PIO write", 0x99: "Chain" };
  const name = names[fn[0]];
  if (!name) return ["? function 0x" + lyHx(fn[0], 2), true];
  const d = fn.slice(1);
  if (fn[0] === 0xbe) {
    if (d.length < 2) return [name, false];
    let s = name + " " + lyFixed(lyInt16((d[1] << 8) | d[0]), 16, 4) + " C";
    if (d.length >= 9) {
      if (lyCrc8Maxim(d.slice(0, 8)) !== d[8]) return [s + " CRC bad", true];
      s += " CRC ok";
    }
    return [s, false];
  }
  if (fn[0] === 0x4e) return [name + " " + lyHexList(d, 3), false];
  if (fn[0] === 0xa5 && d.length >= 2) return [name + " 0x" + lyHx(d[0], 2), d[1] !== (~d[0] & 0xff)];
  return d.length ? [name + " " + lyHexList(d, 8), false] : [name, false];
}

// TRLC-LINKS: REQ-SDS-245
function lyDs2408Fn(fn) {
  const d = fn.slice(1);
  if (fn[0] === 0xf0 || fn[0] === 0xcc) {
    const name = fn[0] === 0xf0 ? "Read PIO registers" : "Write cond. search register";
    if (d.length < 2) return [name, false];
    let s = name + " @0x" + lyHx((d[1] << 8) | d[0], 4);
    if (d.length > 2) s += ": " + lyHexList(d.slice(2), 8);
    return [s, false];
  }
  if (fn[0] === 0xf5) return ["Channel access read " + lyHexList(d, 8), false];
  if (fn[0] === 0x5a) {
    if (d.length < 2) return ["Channel access write", false];
    let s = "Channel access write 0x" + lyHx(d[0], 2);
    const bad = d[1] !== (~d[0] & 0xff);
    if (bad) s += " (complement bad)";
    if (d.length >= 3) s += d[2] === 0xaa ? " done" : " failed";
    return [s, bad];
  }
  if (fn[0] === 0xc3) return ["Reset activity latches" + (d.length ? (d[d.length - 1] === 0xaa ? " done" : " ?") : ""), false];
  if (fn[0] === 0x3c) return ["Disable test mode", false];
  return ["? function 0x" + lyHx(fn[0], 2), true];
}

// TRLC-LINKS: REQ-SDS-245
function lyDs243xFn(fn) {
  const d = fn.slice(1);
  const ta = d.length < 2 ? "" : " @0x" + lyHx((d[1] << 8) | d[0], 4);
  // TRLC-LINKS: REQ-SDS-245
  const crc = (upto) => {
    if (fn.length < upto + 2) return ["", false];
    return lyCrc16Maxim(fn.slice(0, upto)) !== (fn[upto] | (fn[upto + 1] << 8)) ? [" CRC bad", true] : [" CRC ok", false];
  };
  if (fn[0] === 0x0f) {
    let s = "Write scratchpad" + ta;
    if (d.length >= 10) s += ": " + lyHexList(d.slice(2, 10), 8);
    const c = crc(11);
    return [s + c[0], c[1]];
  }
  if (fn[0] === 0xaa) {
    let s = "Read scratchpad" + ta;
    if (d.length >= 3) s += " E/S " + lyHx(d[2], 2);
    if (d.length >= 11) s += ": " + lyHexList(d.slice(3, 11), 8);
    const c = crc(12);
    return [s + c[0], c[1]];
  }
  if (fn[0] === 0x55) return ["Copy scratchpad" + ta + (d.length >= 3 ? " E/S " + lyHx(d[2], 2) : ""), false];
  if (fn[0] === 0xf0) return ["Read memory" + ta + (d.length > 2 ? ", " + lyPlural(d.length - 2, "byte") + ": " + lyHexList(d.slice(2), 8) : ""), false];
  if (fn[0] === 0x5a) return ["Load first secret" + ta, false];
  if (fn[0] === 0x33) return ["Compute next secret" + ta, false];
  if (fn[0] === 0xa5) return ["Read authenticated page" + ta, false];
  return ["? function 0x" + lyHx(fn[0], 2), true];
}

// ---- MDIO: CFP ----

// TRLC-LINKS: REQ-SDS-246
function lyCfpRegion(a) {
  const R = [[0x8000, 0x807f, "NVR1"], [0x8080, 0x80ff, "NVR2"], [0x8100, 0x817f, "NVR3"], [0x8180, 0x81ff, "NVR4"], [0x8400, 0x847f, "Vendor NVR1"], [0x8480, 0x84ff, "Vendor NVR2"], [0x8800, 0x887f, "User NVR1"], [0x8880, 0x88ff, "User NVR2"], [0xa000, 0xa07f, "Module VR1"], [0xa080, 0xa0ff, "MLG VR1"]];
  for (const [lo, hi, n] of R) if (a >= lo && a <= hi) return n;
  return "";
}

// TRLC-LINKS: REQ-SDS-246
function lyCfpNotes(ps) {
  const addr = {}, T = LAYER_TABLES.cfpModules;
  // TRLC-LINKS: REQ-SDS-246
  const reg = (a) => { const r = lyCfpRegion(a); return (r ? r + " " : "") + "0x" + lyHx(a, 4); };
  for (const p of ps) {
    if (p.bytes.length < 4) continue;
    const h = lyBE(p.bytes.slice(0, 2)), st = (h >> 12) & 3, op = (h >> 10) & 3, port = (h >> 5) & 0x1f, dev = h & 0x1f, data = lyBE(p.bytes.slice(2, 4));
    if (st !== 0) continue;
    const key = (port << 5) | dev, known = addr[key] !== undefined, a = addr[key], pre = "CFP " + port + "." + dev + " ";
    if (op === 0) { addr[key] = data; p.note = pre + "address " + reg(data); }
    else if (op === 1) p.note = known ? pre + "write " + reg(a) + " = 0x" + lyHx(data, 4) : pre + "write (no address) 0x" + lyHx(data, 4);
    else {
      if (!known) { p.note = pre + "read (no address) 0x" + lyHx(data, 4); continue; }
      let s = pre + "read " + reg(a) + " = 0x" + lyHx(data, 4);
      if (a === 0x8000 && T[data & 0xff] !== undefined) s += " (" + T[data & 0xff] + ")";
      p.note = s; p.noteErr = lyAnyBad(p);
      if (op === 2) addr[key] = a + 1;
    }
  }
}

// ---- USB requests ----

// TRLC-LINKS: REQ-SDS-246
function lyUsbSetup(d) {
  const T = LAYER_TABLES, rt = d[0], req = d[1], val = lyLE(d.slice(2, 4)), idx = lyLE(d.slice(4, 6)), n = lyLE(d.slice(6, 8));
  const dir = (rt & 0x80) ? "IN" : "OUT";
  if (rt & 0x60) return { 1: "class", 2: "vendor", 3: "reserved" }[(rt >> 5) & 3] + " request 0x" + lyHx(req, 2) + " " + dir + " value 0x" + lyHx(val, 4) + " index 0x" + lyHx(idx, 4) + " len " + n;
  const name = T.usbRequests[req] !== undefined ? T.usbRequests[req] : "request 0x" + lyHx(req, 2);
  if (req === 6 || req === 7) {
    const dn = T.usbDescs[val >> 8] !== undefined ? T.usbDescs[val >> 8] : "type 0x" + lyHx(val >> 8, 2);
    let s = name + " " + dn;
    if ((val & 0xff) !== 0 || val >> 8 === 3) s += " " + (val & 0xff);
    return s + " len " + n;
  }
  if (req === 5 || req === 9 || req === 11) return name + " " + val;
  return name + " value 0x" + lyHx(val, 4) + " index 0x" + lyHx(idx, 4) + " len " + n;
}

// lyUsbNotes reads a record's USB packets in their transactions.
// TRLC-LINKS: REQ-SDS-246
function lyUsbNotes(ps) {
  let tok = 0, addr = 0, ep = 0;
  const setup = {};
  for (const p of ps) {
    if (!p.hasAddr) continue;
    const pid = p.addr, b = p.bytes;
    let r;
    if (pid === 0x1 || pid === 0x9 || pid === 0xd) {
      if (b.length !== 2) r = ["token (cut)", true];
      else {
        const w = lyLE(b);
        tok = pid; addr = w & 0x7f; ep = (w >> 7) & 0xf;
        r = [{ 1: "OUT", 9: "IN", 13: "SETUP" }[pid] + " " + addr + "." + ep, !!p.err];
      }
    } else if (pid === 0x5) r = b.length === 2 ? ["SOF " + (lyLE(b) & 0x7ff), !!p.err] : ["SOF", true];
    else if (pid === 0x3 || pid === 0xb || pid === 0x7 || pid === 0xf) {
      if (b.length < 2) r = ["data (cut)", true];
      else {
        const d = b.slice(0, b.length - 2), key = (addr << 4) | ep, t = tok;
        tok = 0;
        const su = setup[key];
        if (p.err) r = ["data CRC bad", true];
        else if (t === 0xd && d.length === 8) { setup[key] = d.slice(); r = [lyUsbSetup(d), false]; }
        else if (t === 0x9 && d.length >= 12 && d[1] === 1 && su && su.length === 8 && su[1] === 6 && su[3] === 1) r = ["Device USB " + d[3].toString(16).toUpperCase() + "." + lyHx(d[2], 2) + " class " + lyHx(d[4], 2) + " VID " + lyHx(lyLE(d.slice(8, 10)), 4) + " PID " + lyHx(lyLE(d.slice(10, 12)), 4), false];
        else if (!d.length) r = ["zero-length data", false];
        else r = ["data " + lyPlural(d.length, "byte") + ": " + lyHexList(d, 8), false];
      }
    } else {
      const hs = { 2: "ACK", 10: "NAK", 14: "STALL", 6: "NYET", 12: "PRE" }[pid];
      r = hs ? [hs, false] : ["PID 0x" + lyHx(pid, 1), !!p.err];
    }
    p.note = r[0]; p.noteErr = r[1];
  }
}

// ---- dispatch ----

// TRLC-LINKS: REQ-SDS-243
function lyDs1307Notes(ps) { lyRtcNotes(ps, LY_DS1307, 0x68); }
// TRLC-LINKS: REQ-SDS-243
function lyRtc8564Notes(ps) { lyRtcNotes(ps, LY_RTC8564, 0x51); }
// TRLC-LINKS: REQ-SDS-243
function lyTcaNotes(ps) { lyRegNotes(ps, 0x20, 0x21, "TCA6408A", LAYER_TABLES.tcaRegs); }
// TRLC-LINKS: REQ-SDS-243
function lyMcp23008Notes(ps) { lyRegNotes(ps, 0x20, 0x27, "MCP23008", LAYER_TABLES.mcp23008Regs); }
// TRLC-LINKS: REQ-SDS-243
function lyMcp23017Notes(ps) { lyRegNotes(ps, 0x20, 0x27, "MCP23017", LAYER_TABLES.mcp23017Regs); }
// TRLC-LINKS: REQ-SDS-244
function lySbusNotes(ps) { for (const p of ps) { const r = lySbusNote(p); p.note = r[0]; p.noteErr = r[1]; } }
// TRLC-LINKS: REQ-SDS-244
function lyItmNote(b) { const r = lyItmParse(b); return ["ITM " + r[0].join(" "), r[1]]; }

// lyTpiuNote reads a burst's TPIU streams: stream 1 as ITM, the others as bytes.
// TRLC-LINKS: REQ-SDS-244
function lyTpiuNote(b) {
  const [ids, data, cut] = lyTpiuDemux(b), parts = [];
  let bad = cut;
  for (let k = 0; k < ids.length; k++) {
    if (ids[k] === 1) { const [it, ib] = lyItmParse(data[k]); bad = bad || ib; parts.push("ITM " + it.join(" ")); }
    else parts.push("id" + ids[k] + " " + lyHexList(data[k], 8));
  }
  if (!parts.length) parts.push("(no stream)");
  return ["TPIU " + parts.join("; ") + (cut ? " (frame cut)" : ""), bad];
}

// TRLC-LINKS: REQ-SDS-244
function lyEtmNotes(ps) {
  const st = { addr: 0n, thumb: false };
  for (const p of ps) { const r = lyEtmParse(st, p.bytes); p.note = "ETM " + r[0].join(" "); p.noteErr = r[1]; }
}

// TRLC-LINKS: REQ-SDS-245
function lyDs28ea00Notes(ps) { lyOwNotes(ps, lyDs28ea00Fn); }
// TRLC-LINKS: REQ-SDS-245
function lyDs2408Notes(ps) { lyOwNotes(ps, lyDs2408Fn); }
// TRLC-LINKS: REQ-SDS-245
function lyDs243xNotes(ps) { lyOwNotes(ps, lyDs243xFn); }
// TRLC-LINKS: REQ-SDS-245
function lyOwNetNotes(ps) { lyOwNotes(ps, null); }

// LY_EACH are the layers that read each packet alone (a transaction reader).
const LY_EACH = {
  spiflash: lyFlashNote, avrisp: lyAvrNote, nrf24l01: lyNrf24Note, nrf905: lyNrf905Note, cc1101: lyCc1101Note,
  rfm12: lyRfm12Note, mrf24j40: lyMrf24Note, adxl345: lyAdxl345Note, adns5020: lyAdnsNote, ade77xx: lyAde7758Note,
  adf435x: lyAdf435xNote, ad5626: lyAd5626Note, ad79x0: lyAd79x0Note, ltc242x: lyLtc242xNote, max72xx: lyMax72xxNote,
  x2444m: lyX2444Note, rgbled: lyRgbNote, nesgamepad: lyNesNote, ssi32: lySsi32Note, arm_itm: lyItmNote,
  arm_tpiu: lyTpiuNote, amulet: lyAmuletNote,
};

// LY_NOTES are the layers that read a record's packets in order.
const LY_NOTES = {
  sdcard: lySdNotes, enc28j60: lyEncNotes, st25r39xx: lySt25Notes, edid: lyEdidNotes, xfp: lyXfpNotes,
  eeprom24xx: lyEepromNotes, ds1307: lyDs1307Notes, rtc8564: lyRtc8564Notes, atsha204a: lyAtshaNotes,
  mlx90614: lyMlxNotes, nunchuk: lyNunchukNotes, hdcp: lyHdcpNotes, lm75: lyLm75Notes, ltc26x7: lyLtcNotes,
  tca6408a: lyTcaNotes, pca9571: lyPcaNotes, mcp23008: lyMcp23008Notes, mcp23017: lyMcp23017Notes,
  mxc6225xu: lyMxcNotes, i2cdemux: lyDemuxNotes, sbus: lySbusNotes, arm_etmv3: lyEtmNotes, pan1321: lyPanNotes,
  ds28ea00: lyDs28ea00Notes, ds2408: lyDs2408Notes, ds243x: lyDs243xNotes, onewire_net: lyOwNetNotes,
  cfp: lyCfpNotes, usb_request: lyUsbNotes,
};

// layerBus names the bus a decoder's packets come from as the layers name it.
// TRLC-LINKS: REQ-SDS-241
function layerBus(proto) { return proto === "usb" ? "usbls" : proto; }

// layersOn lists the layers a decode can carry, none first: [[id, name], ...].
// TRLC-LINKS: REQ-SDS-241
function layersOn(proto) { return LAYERS.filter((l) => l[0] === "" || l[2] === layerBus(proto)).map((l) => [l[0], l[1]]); }

// layerApply writes each packet's note (and noteErr) with layer id's reading
// and returns the packets the table lists (the I2C filter keeps its address's).
// TRLC-LINKS: REQ-SDS-241
function layerApply(id, proto, ps, args) {
  for (const p of ps) { p.note = ""; p.noteErr = false; }
  const l = LAYERS.find((x) => x[0] === id);
  if (!l || !id || l[2] !== layerBus(proto)) return ps;
  if (id === "i2cfilter") return lyFilterI2C(ps, args && args.addr !== undefined ? args.addr : -1, (args && args.dir) || 0);
  if (LY_EACH[id]) lyEach(ps, LY_EACH[id]);
  else if (LY_NOTES[id]) LY_NOTES[id](ps);
  return ps;
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { LAYERS, layerApply, layersOn, lyFixed, lyHexList, lyQuoted };
}
