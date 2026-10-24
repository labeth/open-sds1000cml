// ENGMODEL-OWNER-UNIT: FU-WEB-DECODE
// decode_irmp.js — the IR multi protocols (Samsung32, Samsung, JVC, NEC16,
// NEC42, Kaseikyo, Matsushita, Denon), a classic script mirroring
// decode_irmp.go step for step. Uses sliceChannel and fail from decode.js,
// irMarks from decode_ir.js, wireOut from decode_wire1.js and bitsInt from
// decode_wire2.js.

const IRMP_NAMES = { 4: "MATSUSH", 5: "KASEIKYO", 3: "SAMSUNG", 8: "DENON", 10: "SAMSG32", 20: "JVC", 27: "NEC16", 28: "NEC42" };
const IRMP_TIMINGS = { 10: [550, 550, 1650], 3: [550, 550, 1650], 20: [560, 560, 1690], 27: [560, 560, 1690],
  28: [560, 560, 1690], 5: [423, 423, 1269], 4: [872, 872, 2616], 8: [310, 745, 1780] };

// irWithin mirrors decode_irmp.go irWithin.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
function irWithin(d, want, frac) { return d >= want * (1 - frac) && d <= want * (1 + frac); }

// irmpBits mirrors decode_irmp.go irmpBits.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
function irmpBits(m, i, tm, max, us) {
  const bits = [];
  for (; i + 1 < m.length && bits.length < max; i++) {
    if (!irWithin(us(m[i].x1 - m[i].x0), tm[0], 0.4)) break;
    const sp = us(m[i + 1].x0 - m[i].x1);
    if (irWithin(sp, tm[1], 0.3)) bits.push(0);
    else if (irWithin(sp, tm[2], 0.3)) bits.push(1);
    else return { bits, j: i };
  }
  return { bits, j: i };
}

// lsbOf mirrors decode_irmp.go lsbOf.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
function lsbOf(b, at, n) {
  let v = 0;
  for (let k = 0; k < n; k++) v |= b[at + k] << k;
  return v;
}

// decodeIRMP mirrors decode_irmp.go DecodeIRMP.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
function decodeIRMP(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("irmp", S.reason);
  if (!(colTimeS > 0)) return fail("irmp", "no sample time");
  const { marks: m, high } = irMarks(S, colTimeS);
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
  const us = (d) => d * colTimeS * 1e6;
  const o = wireOut(S.n);
  let good = false, first = 0, unit = 0, lastJVC = -1;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
  const frame = (proto, x0, x1, addr, cmd, ok, name) => {
    o.add(x0, x0, name, "sof", proto, false);
    let kind = "data", ka = "A", kc = "C";
    if (!ok) { kind = "frame-error"; ka = "!A"; kc = "!C"; } else good = true;
    const mid = (x0 + x1) / 2;
    o.add(x0, mid, ka + addr.toString(16).toUpperCase(), kind, addr, true);
    o.add(mid, x1, kc + cmd.toString(16).toUpperCase(), kind, cmd, true);
    if (first === 0) { first = proto; unit = IRMP_TIMINGS[proto][0]; }
  };
  for (let i = 0; i + 1 < m.length; i++) {
    const lead = us(m[i].x1 - m[i].x0), sp = us(m[i + 1].x0 - m[i].x1);
    const x0 = m[i].x0;
    if (irWithin(lead, 9000, 0.12) && irWithin(sp, 4500, 0.2)) {
      const tm = IRMP_TIMINGS[20];
      let r = irmpBits(m, i + 1, tm, 8, us);
      if (r.bits.length === 8 && r.j + 1 < m.length && irWithin(us(m[r.j].x1 - m[r.j].x0), tm[0], 0.4) &&
        irWithin(us(m[r.j + 1].x0 - m[r.j].x1), 4500, 0.2)) {
        const c = irmpBits(m, r.j + 1, tm, 8, us);
        if (c.bits.length === 8) {
          frame(27, x0, m[c.j].x1, lsbOf(r.bits, 0, 8), lsbOf(c.bits, 0, 8), true, IRMP_NAMES[27]);
          i = c.j;
        }
        continue;
      }
      r = irmpBits(m, i + 1, tm, 42, us);
      const b = r.bits;
      if (b.length === 16) {
        frame(20, x0, m[r.j].x1, lsbOf(b, 0, 4), lsbOf(b, 4, 12), true, IRMP_NAMES[20]);
        lastJVC = m[r.j].x1;
      } else if (b.length === 42) {
        const a = lsbOf(b, 0, 13), ai = lsbOf(b, 13, 13), c = lsbOf(b, 26, 8), ci = lsbOf(b, 34, 8);
        frame(28, x0, m[r.j].x1, a, c, (a ^ ai) === 0x1fff && (c ^ ci) === 0xff, IRMP_NAMES[28]);
      } else continue;
      i = r.j;
    } else if (irWithin(lead, 4500, 0.15) && irWithin(sp, 4500, 0.2)) {
      const tm = IRMP_TIMINGS[10];
      let r = irmpBits(m, i + 1, tm, 16, us);
      if (r.bits.length === 16 && r.j + 1 < m.length && irWithin(us(m[r.j].x1 - m[r.j].x0), tm[0], 0.4) &&
        irWithin(us(m[r.j + 1].x0 - m[r.j].x1), 4500, 0.2)) {
        const c = irmpBits(m, r.j + 1, tm, 20, us);
        if (c.bits.length === 20) {
          const id = lsbOf(c.bits, 0, 4), cmd = lsbOf(c.bits, 4, 8), inv = lsbOf(c.bits, 12, 8);
          frame(3, x0, m[c.j].x1, lsbOf(r.bits, 0, 16), cmd | (id << 8), (cmd ^ inv) === 0xff, IRMP_NAMES[3]);
          i = c.j;
        }
        continue;
      }
      r = irmpBits(m, i + 1, tm, 32, us);
      if (r.bits.length === 32) {
        frame(10, x0, m[r.j].x1, lsbOf(r.bits, 0, 16), lsbOf(r.bits, 16, 16), true, IRMP_NAMES[10]);
        i = r.j;
      }
    } else if (irWithin(lead, 3380, 0.15) && irWithin(sp, 1690, 0.2)) {
      const r = irmpBits(m, i + 1, IRMP_TIMINGS[5], 48, us);
      if (r.bits.length !== 48) continue;
      const by = [];
      for (let k = 0; k < 6; k++) by.push(lsbOf(r.bits, 8 * k, 8));
      const maker = by[0] | (by[1] << 8);
      const par = (maker ^ (maker >> 4) ^ (maker >> 8) ^ (maker >> 12)) & 15;
      const ok = (by[2] & 15) === par && by[5] === (by[2] ^ by[3] ^ by[4]);
      frame(5, x0, m[r.j].x1, maker, lsbOf(r.bits, 28, 12) | (lsbOf(r.bits, 20, 4) << 12), ok, IRMP_NAMES[5]);
      i = r.j;
    } else if (irWithin(lead, 3488, 0.15) && irWithin(sp, 3488, 0.2)) {
      const r = irmpBits(m, i + 1, IRMP_TIMINGS[4], 24, us);
      if (r.bits.length !== 24) continue;
      frame(4, x0, m[r.j].x1, lsbOf(r.bits, 12, 12), lsbOf(r.bits, 0, 12), true, IRMP_NAMES[4]);
      i = r.j;
    } else if (lastJVC >= 0 && irWithin(lead, 560, 0.4) && us(x0 - lastJVC) < 30000) {
      const r = irmpBits(m, i, IRMP_TIMINGS[20], 16, us);
      if (r.bits.length !== 16) continue;
      frame(20, x0, m[r.j].x1, lsbOf(r.bits, 0, 4), lsbOf(r.bits, 4, 12), true, "REP");
      lastJVC = m[r.j].x1;
      i = r.j;
    } else if (irWithin(lead, 310, 0.4) && (i === 0 || us(x0 - m[i - 1].x1) > 10000)) {
      const tm = IRMP_TIMINGS[8];
      const r = irmpBits(m, i, tm, 15, us);
      let j = r.j;
      if (r.bits.length !== 15 || j + 1 >= m.length || us(m[j + 1].x0 - m[j].x1) > 70000) continue;
      const r2 = irmpBits(m, j + 1, tm, 15, us);
      const a = bitsInt(r.bits.slice(0, 5)), c = bitsInt(r.bits.slice(5));
      const ok = r2.bits.length === 15 && bitsInt(r2.bits.slice(0, 5)) === a &&
        (bitsInt(r2.bits.slice(5)) ^ c) === 0x3ff && (c & 1) === 0;
      if (r2.bits.length === 15) j = r2.j;
      frame(8, x0, m[j].x1, a, c, ok, IRMP_NAMES[8]);
      i = j;
    }
  }
  if (!o.spans.length) return fail("irmp", "no IR multi frame");
  const res = { ok: good, proto: "irmp", spans: o.spans, text: o.toks.join(" "), bytes: o.units,
    meta: { threshold: S.threshold, inverted: high, frameBits: first } };
  if (unit > 0) { res.meta.spb = unit * 1e-6 / colTimeS; res.meta.baud = Math.round(1e6 / unit); }
  if (!good) res.error = "no IR multi frame whose checks hold";
  return res;
}

if (typeof module !== "undefined" && module.exports) module.exports = { decodeIRMP };
