"use strict";
// binframe.js — decoder for the /api/frame.bin binary transport (layout doc
// in web.go next to encodeBinFrame). Produces the SAME frame object shape as
// the /api/frame JSON reply, with c1/c2/e* as Int16Array and the contiguous
// -1 head/tail margins refilled from the header's head/tail counts, so every
// existing consumer (draw/zoom/decode/FFT/CSV/refs/captures) is untouched.
//
// Every call allocates FRESH typed arrays. Never pool or reuse buffers here:
// dcfg.captures and refs in app.js hold frames BY REFERENCE — a reused buffer
// would silently overwrite saved captures with later frames.
//
// Returns null on ANY protocol mismatch (bad magic, truncated header, payload
// size not matching the header's cols/head/tail). Callers treat null as
// "protocol failure" and retry the poll with backoff rather than render garbage.
const BIN_MAGIC = 0xf5;
const BIN_FLAG_RAW = 0x10;

function decodeBinFrame(buf) {
  const u8 = new Uint8Array(buf);
  if (u8.length < 8 || u8[0] !== BIN_MAGIC) return null;
  const flags = u8[1];
  const hlen = new DataView(buf).getUint32(4, true);
  if (hlen < 2 || 8 + hlen > u8.length) return null;
  let f;
  try {
    f = JSON.parse(new TextDecoder().decode(u8.subarray(8, 8 + hlen)));
  } catch (e) {
    return null;
  }
  if (typeof f !== "object" || f === null || typeof f.seq !== "number") return null;
  const pay = u8.subarray(8 + hlen);
  if (f.unchanged) return pay.length === 0 ? f : null;
  const cols = f.cols | 0, head = f.head | 0, tail = f.tail | 0;
  if (cols <= 0 || head < 0 || tail < 0 || head + tail > cols) return null;
  // RAW-shape payload is ALWAYS c1+c2 (even when the frame is an envelope —
  // is_env stays set in the header so the stacker can refuse the band, but
  // the layout must not switch to the 4-segment env decode).
  if (f.is_env && !(flags & BIN_FLAG_RAW)) {
    if (pay.length !== 4 * cols) return null;
    const seg = (i) => {
      const out = new Int16Array(cols);
      out.set(pay.subarray(i * cols, (i + 1) * cols)); // u8 -> i16 element-wise
      return out;
    };
    f.e1min = seg(0); f.e1max = seg(1); f.e2min = seg(2); f.e2max = seg(3);
    return f;
  }
  const body = cols - head - tail;
  if (pay.length !== 2 * body) return null;
  const chan = (off) => {
    const out = new Int16Array(cols);
    if (head || tail) out.fill(-1); // margins; body overwrites the middle
    out.set(pay.subarray(off, off + body), head);
    return out;
  };
  f.c1 = chan(0);
  f.c2 = chan(body);
  return f;
}

if (typeof module !== "undefined") module.exports = { decodeBinFrame, BIN_MAGIC, BIN_FLAG_RAW };
