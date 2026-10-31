// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// webdec_check.mjs — the browser's stream decode (webdec.js) run in node on
// the Go tests' streams, for their parity checks.
//
//   node webdec_check.mjs stream <cases.json>
//     each case: the words of a digital stream (a little-endian file), the
//     pieces the Go stream decoder was pushed in, the JS decoder and its
//     settings; writes the frames WebDecStream hands out (JSON) to its out
//     path. Called by internal/decode/webdec_jsparity_test.go.
//   node webdec_check.mjs session <cases.json>
//     each case: /api/stream/words.bin replies as the engine codes them;
//     decodes them as the page's worker does (WebDecSession) and writes the
//     /api/stream/frames.bin body to its out path. Called by
//     internal/engine/soft_client_test.go.
//
// Prints "WEBDEC OK" and exits 0 when every case ran.

import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const [mode, casesPath] = process.argv.slice(2);
if (!mode || !casesPath) {
  console.error("usage: node webdec_check.mjs stream|session <cases.json>");
  process.exit(2);
}

// The decoders and webdec.js in one script scope, as the worker loads them.
const files = ["decode.js", "decode_sent.js", "decode_canfd.js", "decode_arinc429.js", "decode_manchester.js", "decode_mil1553.js",
  "decode_usbls.js", "decode_flexray.js", "decode_uartproto.js", "decode_pulsewire.js", "decode_ir.js", "decode_biphase.js",
  "decode_clocked.js", "decode_motion.js", "decode_rfcodes.js", "decode_rfid.js", "decode_wire1.js", "decode_wire2.js", "decode_irmp.js",
  "webdec.js"];
// Run in this context, as a worker runs them: a vm context's globals are
// reached through interceptors, a hundred times slower than a browser's.
const src = files.map((f) => fs.readFileSync(path.join(here, f), "utf8")).join("\n;\n");
globalThis.module = { exports: {} };
vm.runInThisContext(src, { filename: "webdec-bundle.js" });
const ctx = globalThis;
const W = globalThis.module.exports;
W.webdecInstall(ctx);

// words reads a little-endian word file as a Uint32Array.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function words(p) {
  const b = fs.readFileSync(p);
  return new Uint32Array(b.buffer.slice(b.byteOffset, b.byteOffset + (b.length & ~3)));
}

// streamCase decodes one case's pieces with WebDecStream.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function streamCase(c) {
  const w = words(c.words);
  const a = new W.WebDecLine();
  const b = new W.WebDecLine();
  a.appendWords(w, 0, w.length, 0);
  if (c.two) b.appendWords(w, 0, w.length, 1);
  const fn = ctx[c.fn];
  if (!fn) throw new Error("no decoder " + c.fn);
  const ss = c.ss;
  const cfg = c.cfg;
  // edec decodes a window's lines: through their edges, or (c.samples) as samples.
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
  function edec(x, y) {
    if (c.samples) {
      x = x.samples();
      y = y ? y.samples() : undefined;
    }
    return c.two ? fn(x, y || undefined, ss, cfg) : fn(x, ss, cfg);
  }
  const d = new W.WebDecStream(edec, c.closeN, c.maxN);
  d.leadN = c.leadN;
  d.subframes = !!c.subframes;
  const out = [];
  const t0 = performance.now();
  for (const [from, to, skip] of c.pieces) {
    for (const f of d.pushEdges(a, c.two ? b : null, from, to)) out.push(f);
    if (skip > 0) d.skip(skip);
  }
  for (const f of d.flush()) out.push(f);
  const ms = performance.now() - t0;
  fs.writeFileSync(c.out, JSON.stringify({ ms, edges: a.len + (c.two ? b.len : 0), frames: out.map((f) => [f.i0, f.i1, f.spans.map((s) => [s.i0, s.i1, s.kind, s.val, s.text])]) }));
}

// sessionCase decodes one case's replies with WebDecSession.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function sessionCase(c) {
  let sess = null;
  for (const p of c.replies) {
    const b = fs.readFileSync(p);
    const { hdr, chunks } = W.webdecParse(b.buffer.slice(b.byteOffset, b.byteOffset + b.length));
    if (!sess) sess = new W.WebDecSession(hdr, ctx);
    for (const ch of chunks) sess.feedChunk(ch);
  }
  // the stream's end: what is queued, then the window's last frames
  sess.push();
  for (const f of sess.dec.flush()) sess.frames.push(f);
  const body = W.webdecFramesBody(sess.info.session, sess.position(), sess.takeFrames(), sess.losses);
  fs.writeFileSync(c.out, Buffer.from(body));
}

const cases = JSON.parse(fs.readFileSync(casesPath, "utf8"));
let fails = 0;
for (const c of cases) {
  try {
    if (mode === "stream") streamCase(c);
    else sessionCase(c);
  } catch (e) {
    fails++;
    console.log("FAIL " + (c.name || c.out) + ": " + (e && e.stack ? e.stack : e));
  }
}
console.log(fails ? fails + " FAILED" : "WEBDEC OK");
process.exit(fails ? 1 : 0);
