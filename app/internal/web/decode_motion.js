// ENGMODEL-OWNER-UNIT: FU-WEB-DECODE
// decode_motion.js — quadrature encoders, stepper STEP/DIR and PWM, a
// classic script mirroring decode_motion.go step for step. Uses
// sliceChannel, logicAt and fail from decode.js and pspan from
// decode_pulsewire.js. cfg.threshold slices line a, cfg.thresholdB line b.

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
function lineOpts(thr) { return thr != null ? { threshold: thr } : {}; }

// medianOf mirrors decode_motion.go medianOf.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
function medianOf(v) {
  if (!v.length) return 0;
  // The middle element by selection (Hoare), not a full sort of a copy.
  const c = v.slice(), k = c.length >> 1;
  let lo = 0, hi = c.length - 1;
  while (lo < hi) {
    const p = c[(lo + hi) >> 1];
    let i = lo, j = hi;
    while (i <= j) {
      while (c[i] < p) i++;
      while (c[j] > p) j--;
      if (i <= j) { const t = c[i]; c[i] = c[j]; c[j] = t; i++; j--; }
    }
    if (k <= j) hi = j; else if (k >= i) lo = i; else break;
  }
  return c[k];
}

// stepPeriod mirrors decode_motion.go stepPeriod.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
function stepPeriod(A) {
  const per = [];
  let last = -1;
  for (const e of A.edges) {
    if (e.dir > 0) {
      if (last >= 0) per.push(e.x - last);
      last = e.x;
    }
  }
  return medianOf(per);
}

// decodeSteps mirrors decode_motion.go decodeSteps.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
function decodeSteps(proto, a, b, colTimeS, cfg, quad) {
  cfg = cfg || {};
  if (!b) return fail(proto, "needs both lines");
  const A = sliceChannel(a, lineOpts(cfg.threshold));
  if (!A.ok) return fail(proto, A.reason);
  const B = sliceChannel(b, lineOpts(cfg.thresholdB));
  if (!B.ok && quad) return fail(proto, B.reason);
  let dirFlat = 0;
  if (!B.ok) {
    let sum = 0;
    for (let i = 0; i < b.length; i++) sum += b[i];
    if (b.length > 0 && sum / b.length >= A.threshold) dirFlat = 1;
  }
  const T = stepPeriod(A);
  if (!(T > 0)) return fail(proto, "too few steps");
  const n = A.n, spans = [], toks = [], units = [];
  let count = 0;
  let la = A.level[0];
  let lb = B.ok ? B.level[0] : 0;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
  const phase = () => { const g = la | (lb << 1); return g ^ (g >> 1); };
  let ph = phase();
  let prevA = 0, open = false, ia = 0, ib = 0, steps = 0, pulse = Infinity, riseX = -1, lastA = -1, bMid = 0, bAll = 0;
  while (ia < A.edges.length || (quad && ib < B.edges.length)) {
    const useA = ia < A.edges.length && (!quad || ib >= B.edges.length || A.edges[ia].x <= B.edges[ib].x);
    if (!useA) {
      const e = B.edges[ib++];
      lb = e.dir > 0 ? 1 : 0;
      if (lastA >= 0) {
        bAll++;
        const f = (e.x - lastA) / (T / 2);
        if (f >= 0.25 && f <= 0.75) bMid++;
      }
      const np = phase();
      const d = (((np - ph + 1) % 4) + 4) % 4 - 1;
      ph = np;
      if (d === 2) { spans.push(pspan(e.x, e.x, n, "!", "frame-error", 0)); toks.push("!"); continue; }
      count += d;
      continue;
    }
    const e = A.edges[ia++];
    if (e.x - prevA >= 2 * T) open = true;
    if (e.dir > 0) riseX = e.x;
    else if (riseX >= 0) pulse = Math.min(pulse, e.x - riseX);
    lastA = e.x;
    prevA = e.x;
    la = e.dir > 0 ? 1 : 0;
    if (quad) {
      const np = phase();
      const d = (((np - ph + 1) % 4) + 4) % 4 - 1;
      ph = np;
      if (d === 2) { spans.push(pspan(e.x, e.x, n, "!", "frame-error", 0)); toks.push("!"); continue; }
      count += d;
    }
    if (e.dir < 0) continue;
    let fwd = false;
    if (quad) fwd = lb === 0;
    else {
      fwd = dirFlat === 1;
      if (B.ok) fwd = logicAt(B, e.x) === 1;
      count += fwd ? 1 : -1;
    }
    if (open) { spans.push(pspan(e.x, e.x, n, "", "sof", 0)); open = false; }
    const v = fwd ? 1 : 0;
    const txt = (fwd ? "F" : "R") + String(count);
    spans.push(pspan(e.x, e.x + T, n, txt, "data", v));
    toks.push(txt); units.push(v);
    steps++;
  }
  const res = { ok: steps >= 2, proto, spans, text: toks.join(" "), bytes: units,
    meta: { threshold: A.threshold, spb: T, baud: Math.round(1 / (T * colTimeS)) } };
  if (!quad && pulse !== Infinity) res.meta.feature = pulse;
  if (quad && bAll > 0 && 10 * B.edges.length >= 9 * A.edges.length) res.meta.cover = bMid / bAll;
  else if (!quad && B.ok && steps > 0) res.meta.cover = B.edges.length / steps;
  if (!res.ok) res.error = "too few steps";
  return res;
}

// decodeQuadrature mirrors decode_motion.go DecodeQuadrature.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
function decodeQuadrature(a, b, colTimeS, cfg) { return decodeSteps("quad", a, b, colTimeS, cfg, true); }

// decodeStepper mirrors decode_motion.go DecodeStepper.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
function decodeStepper(a, b, colTimeS, cfg) { return decodeSteps("stepper", a, b, colTimeS, cfg, false); }

// decodePWM mirrors decode_motion.go DecodePWM.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
function decodePWM(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("pwm", S.reason);
  const n = S.n, spans = [], toks = [], units = [], pers = [];
  let narrow = Infinity;
  const E = S.edges;
  for (let k = 0; k < E.length; k++) {
    if (E[k].dir < 0 || k + 2 >= E.length) continue;
    const r0 = E[k].x, f = E[k + 1].x, r1 = E[k + 2].x;
    const per = r1 - r0;
    const ratio = (f - r0) / per;
    const v = Math.min(255, Math.floor(ratio * 256));
    const d10 = Math.floor(ratio * 1000 + 0.5);
    const txt = Math.trunc(d10 / 10) + "." + (d10 % 10) + "%";
    spans.push(pspan(r0, r0, n, "", "sof", 0), pspan(r0, r1, n, txt, "data", v));
    toks.push(txt); units.push(v); pers.push(per);
    narrow = Math.min(narrow, Math.min(f - r0, r1 - f));
  }
  if (units.length < 2) return fail("pwm", "too few periods");
  const T = medianOf(pers);
  return { ok: true, proto: "pwm", spans, text: toks.join(" "), bytes: units,
    meta: { threshold: S.threshold, spb: T, baud: Math.round(1 / (T * colTimeS)), feature: narrow } };
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { decodeQuadrature, decodeStepper, decodePWM };
