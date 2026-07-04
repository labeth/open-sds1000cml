// Node tests for binframe.js — golden byte fixtures built to the wire layout
// documented in web.go (encodeBinFrame). Run by binframe_node_test.go.
"use strict";
const { decodeBinFrame, BIN_MAGIC } = require("./binframe.js");

let fails = 0;
function check(name, ok) {
  if (!ok) {
    console.log("FAIL " + name);
    fails++;
  } else {
    console.log("ok   " + name);
  }
}

function msg(flags, hdrObj, payload) {
  const hdr = new TextEncoder().encode(JSON.stringify(hdrObj));
  const buf = new Uint8Array(8 + hdr.length + payload.length);
  buf[0] = BIN_MAGIC;
  buf[1] = flags;
  new DataView(buf.buffer).setUint32(4, hdr.length, true);
  buf.set(hdr, 8);
  buf.set(payload, 8 + hdr.length);
  return buf.buffer;
}

// native: 4 cols, no margins.
{
  const f = decodeBinFrame(msg(0, { seq: 5, cols: 4, vpc1: 1 / 32 }, new Uint8Array([1, 2, 3, 4, 9, 8, 7, 6])));
  check("native decodes", f && f.seq === 5);
  check("native c1", f && f.c1 instanceof Int16Array && [...f.c1].join() === "1,2,3,4");
  check("native c2", f && [...f.c2].join() === "9,8,7,6");
}

// deep: 8 cols, head 2, tail 1 → body 5 per channel, margins refilled as -1.
{
  const pay = new Uint8Array([10, 11, 12, 13, 14, 20, 21, 22, 23, 24]);
  const f = decodeBinFrame(msg(4, { seq: 6, cols: 8, depth: 8, head: 2, tail: 1 }, pay));
  check("deep decodes", !!f);
  check("deep c1 margins", f && [...f.c1].join() === "-1,-1,10,11,12,13,14,-1");
  check("deep c2 margins", f && [...f.c2].join() === "-1,-1,20,21,22,23,24,-1");
}

// empty: all margin, zero payload.
{
  const f = decodeBinFrame(msg(8, { seq: 7, cols: 4, head: 4, tail: 0 }, new Uint8Array(0)));
  check("empty all -1", f && [...f.c1].join() === "-1,-1,-1,-1");
}

// env: 4 segments of cols bytes.
{
  const pay = new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8]);
  const f = decodeBinFrame(msg(2, { seq: 8, cols: 2, is_env: true }, pay));
  check("env decodes", f && [...f.e1min].join() === "1,2" && [...f.e1max].join() === "3,4" &&
    [...f.e2min].join() === "5,6" && [...f.e2max].join() === "7,8");
  check("env typed", f && f.e2max instanceof Int16Array);
}

// unchanged: no payload allowed.
{
  const f = decodeBinFrame(msg(1, { seq: 9, unchanged: true }, new Uint8Array(0)));
  check("unchanged passes through", f && f.unchanged === true && f.seq === 9);
  const bad = decodeBinFrame(msg(1, { seq: 9, unchanged: true }, new Uint8Array(3)));
  check("unchanged rejects stray payload", bad === null);
}

// protocol failures → null, never garbage.
{
  const good = msg(0, { seq: 5, cols: 4 }, new Uint8Array(8));
  const badMagic = new Uint8Array(good.slice(0));
  badMagic[0] = 0x00;
  check("bad magic", decodeBinFrame(badMagic.buffer) === null);
  check("truncated", decodeBinFrame(good.slice(0, 6)) === null);
  check("short payload", decodeBinFrame(msg(0, { seq: 5, cols: 4 }, new Uint8Array(7))) === null);
  check("margins exceed cols", decodeBinFrame(msg(4, { seq: 5, cols: 4, head: 3, tail: 2 }, new Uint8Array(0))) === null);
  check("env payload mismatch", decodeBinFrame(msg(2, { seq: 5, cols: 4, is_env: true }, new Uint8Array(15))) === null);
  const hdrOverrun = new Uint8Array(12);
  hdrOverrun[0] = BIN_MAGIC;
  new DataView(hdrOverrun.buffer).setUint32(4, 4096, true);
  check("header overrun", decodeBinFrame(hdrOverrun.buffer) === null);
  check("bad header json", decodeBinFrame(msg(0, "not-an-object", new Uint8Array(0))) === null);
  check("missing seq", decodeBinFrame(msg(0, { cols: 4 }, new Uint8Array(8))) === null);
}

// fresh allocations: two decodes of the same bytes share nothing.
{
  const bytes = msg(0, { seq: 5, cols: 2 }, new Uint8Array([1, 2, 3, 4]));
  const a = decodeBinFrame(bytes.slice(0)), b = decodeBinFrame(bytes.slice(0));
  a.c1[0] = 99;
  check("no shared buffers", b.c1[0] === 1);
}

if (fails) {
  console.log(fails + " FAILURES");
  process.exit(1);
}
console.log("ALL PASS");
