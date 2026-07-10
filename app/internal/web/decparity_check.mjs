// decparity_check.mjs — Go<->JS decoder parity checker.
//
// Loads the browser decode logic (decode.js + every protocol twin) into one
// shared script scope (the same way the browser concatenates them), then replays
// a battery of {proto, codes, colT, cfg, ok, bytes} vectors produced by the Go
// decoders (the ground truth) and asserts each JS twin returns the SAME ok + the
// SAME bytes. Prints "ALL PARITY OK" and exits 0 on full agreement, else exits 1.
//
// Invoked by internal/decode/decode_jsparity_test.go, which passes the vectors
// JSON path as argv[2] and self-skips when node is absent.

import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const vecPath = process.argv[2];
if (!vecPath) {
  console.error("usage: node decparity_check.mjs <vectors.json>");
  process.exit(2);
}

// Concatenate the decode scripts into ONE source and run it once, so the twins'
// references to sliceChannel/logicAt/fmtByte/fail (globals in the browser) all
// resolve within the shared script scope. A trailing line stashes the decoders.
const files = [
  "decode.js",
  "decode_sent.js",
  "decode_canfd.js",
  "decode_arinc429.js",
  "decode_manchester.js",
  "decode_mil1553.js",
  "decode_usbls.js",
  "decode_flexray.js",
];
let src = files.map((f) => fs.readFileSync(path.join(here, f), "utf8")).join("\n;\n");
src +=
  "\n;this.__D = { decodeManchester, decodeMIL1553, decodeFlexRay, decodeSENT, decodeCANFD, decodeARINC429, decodeUSBLS, decodeUART, decodeI2C, decodeSPI, autodetect };\n";

const ctx = { console, module: { exports: {} } };
vm.createContext(ctx);
vm.runInContext(src, ctx, { filename: "decode-bundle.js" });
const D = ctx.__D;

// codes2 carries the second channel for two-channel protocols (I2C, SPI) and
// for autodetect vectors; an empty/missing array means "channel off".
const c2of = (v) => (v.codes2 && v.codes2.length ? v.codes2 : undefined);
const dispatch = {
  manchester: (v) => D.decodeManchester(v.codes, v.colT, v.cfg),
  mil1553: (v) => D.decodeMIL1553(v.codes, v.colT, v.cfg),
  flexray: (v) => D.decodeFlexRay(v.codes, v.colT, v.cfg),
  sent: (v) => D.decodeSENT(v.codes, v.colT, v.cfg),
  canfd: (v) => D.decodeCANFD(v.codes, v.colT, v.cfg),
  arinc429: (v) => D.decodeARINC429(v.codes, v.colT, v.cfg),
  usbls: (v) => D.decodeUSBLS(v.codes, v.colT, v.cfg),
  uart: (v) => D.decodeUART(v.codes, v.colT, v.cfg),
  i2c: (v) => D.decodeI2C(v.codes, c2of(v), v.colT, v.cfg),
  spi: (v) => D.decodeSPI(v.codes, c2of(v), v.colT, v.cfg),
  // autodetect: the FINAL CHOICE must match Go byte-for-byte — both the chosen
  // protocol (v.det) and the winning hypothesis' decoded {ok, bytes, text}.
  autodetect: (v) => {
    const d = D.autodetect(
      { c1: v.codes, c2: c2of(v), col_span_s: v.colT * v.codes.length },
      { fmt: (v.cfg && v.cfg.fmt) || "hex" }
    );
    if (d.proto !== (v.det || "off"))
      return { ok: false, bytes: [], text: `PROTO MISMATCH: js chose ${d.proto}, go chose ${v.det || "off"}` };
    return d.result || { ok: false, bytes: [], text: "" };
  },
};

const arrEq = (a, b) => {
  a = a || [];
  b = b || [];
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if ((a[i] | 0) !== (b[i] | 0)) return false;
  return true;
};

const vecs = JSON.parse(fs.readFileSync(vecPath, "utf8"));
let pass = 0;
const fails = [];
vecs.forEach((v, idx) => {
  const fn = dispatch[v.proto];
  if (!fn) {
    fails.push(`#${idx} ${v.proto}: no JS dispatch`);
    return;
  }
  let r;
  try {
    r = fn(v);
  } catch (e) {
    fails.push(`#${idx} ${v.proto}: JS threw ${e && e.message}`);
    return;
  }
  const jsOK = !!r.ok;
  const jsBytes = r.bytes || [];
  const jsText = r.text || "";
  if (jsOK !== v.ok || !arrEq(jsBytes, v.bytes) || jsText !== (v.text || "")) {
    fails.push(
      `#${idx} ${v.proto}: go{ok=${v.ok},bytes=${JSON.stringify(v.bytes)},text=${JSON.stringify(v.text)}} ` +
        `js{ok=${jsOK},bytes=${JSON.stringify(jsBytes)},text=${JSON.stringify(jsText)}}`
    );
    return;
  }
  pass++;
});

if (fails.length) {
  console.log(`PARITY FAIL: ${pass}/${vecs.length} matched, ${fails.length} mismatched`);
  for (const f of fails) console.log("  " + f);
  process.exit(1);
}
console.log(`ALL PARITY OK: ${pass}/${vecs.length} vectors matched Go byte-for-byte`);
