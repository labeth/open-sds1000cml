// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// End-to-end test for the FFT peak-detection + selection logic that ui.html
// runs in the browser. It loads the SAME peaks.js the page loads, feeds it
// synthetic scope frames, and checks the two things the user hit: selection
// must survive frame-to-frame magnitude jitter, and you must be able to pick a
// different peak afterwards. Run: node peaks.test.cjs  (exit 0 = pass).
const { spectrum, detectPeaks, nearestPeak, component, toneAmplitude } = require("./peaks.js");

let failed = 0;
// TRLC-LINKS: REQ-SDS-070
function ok(cond, msg) { if (!cond) { console.error("FAIL:", msg); failed++; } else { console.log("ok  -", msg); } }
// TRLC-LINKS: REQ-SDS-070
function near(a, b, tol, msg) { ok(Math.abs(a - b) <= tol, `${msg} (got ${a}, want ${b}±${tol})`); }

// A scope frame: 1024 byte-codes (0..255) around 128, = two tones + noise.
// N=1024 samples over col_span_s = 1.024 ms  ->  1 MS/s  ->  Nyquist 500 kHz.
const N = 1024, SR = 1e6, NYQ = SR / 2, BIN = SR / N; // 976.5625 Hz/bin
// Bin-centred tones (no Hann sidelobes above the −50 dB floor): clean peaks.
const F1 = 52 * BIN;   // ~50.78 kHz
const F2 = 123 * BIN;  // ~120.12 kHz

// TRLC-LINKS: REQ-SDS-070
function frame(amp1, amp2, seed) {
  const c1 = new Array(N);
  let s = seed >>> 0;
  // TRLC-LINKS: REQ-SDS-070
  const rnd = () => { s = (s * 1103515245 + 12345) & 0x7fffffff; return s / 0x7fffffff - 0.5; };
  for (let i = 0; i < N; i++) {
    let v = 128
      + amp1 * Math.sin(2 * Math.PI * F1 * i / SR)
      + amp2 * Math.sin(2 * Math.PI * F2 * i / SR)
      + 2 * rnd();
    c1[i] = Math.max(0, Math.min(255, Math.round(v)));
  }
  return c1;
}

// --- 1. detection: both tones found at the right frequencies ----------------
{
  const peaks = detectPeaks(spectrum(frame(40, 30, 1), NYQ), { floorDb: -50, maxPeaks: 8 });
  ok(peaks.length >= 2, `found ${peaks.length} peaks (>=2)`);
  // list is frequency-ordered
  const asc = peaks.every((p, i) => i === 0 || p.freq >= peaks[i - 1].freq);
  ok(asc, "peak list is frequency-ordered");
  const two = [...peaks].sort((a, b) => b.db - a.db).slice(0, 2).sort((a, b) => a.freq - b.freq);
  near(two[0].freq, F1, 2000, "strongest-two low tone ~= F1");
  near(two[1].freq, F2, 2000, "strongest-two high tone ~= F2");
}

// --- 2. list ORDER is stable when magnitudes flip ---------------------------
// Frame A: low tone louder. Frame B: high tone louder. The freq-ordered list
// must present the SAME sequence of frequencies both times (rows don't jump).
{
  const a = detectPeaks(spectrum(frame(50, 20, 2), NYQ), {});
  const b = detectPeaks(spectrum(frame(20, 50, 3), NYQ), {});
  const fa = a.map(p => Math.round(p.freq / BIN));
  const fb = b.map(p => Math.round(p.freq / BIN));
  ok(JSON.stringify(fa) === JSON.stringify(fb), `stable order across frames: ${fa} vs ${fb}`);
}

// --- 3. SELECTION survives magnitude jitter (the core bug) ------------------
// Select the high tone in frame A, then re-locate it in frame B where the low
// tone is now the strongest. nearestPeak(by freq) must still point at F2 — an
// index-based selection would have flipped to F1.
{
  const a = detectPeaks(spectrum(frame(50, 20, 4), NYQ), {});
  let selFreq = a[nearestPeak(a, F2)].freq;      // user clicks the high tone
  const b = detectPeaks(spectrum(frame(20, 50, 5), NYQ), {}); // next frame, tones swapped
  const idxB = nearestPeak(b, selFreq);
  near(b[idxB].freq, F2, 2000, "selection stays on high tone after magnitude flip");
  ok(Math.abs(b[idxB].freq - F1) > 10000, "selection did NOT jump to the low tone");
}

// --- 4. RE-selecting a different peak works ---------------------------------
{
  const p = detectPeaks(spectrum(frame(40, 40, 6), NYQ), {});
  let selFreq = p[nearestPeak(p, F2)].freq;      // first pick: high tone
  const i1 = nearestPeak(p, selFreq);
  selFreq = p[nearestPeak(p, F1)].freq;          // now click the low tone
  const i2 = nearestPeak(p, selFreq);
  ok(i1 !== i2, `picked a different peak (idx ${i1} -> ${i2})`);
  near(p[i2].freq, F1, 2000, "second pick landed on the low tone");
}

// --- 5. no selection -> nothing selected ------------------------------------
{
  const p = detectPeaks(spectrum(frame(40, 30, 7), NYQ), {});
  ok(nearestPeak(p, -1) === -1, "selFreq<0 -> no selection");
  ok(nearestPeak([], F1) === -1, "empty peak list -> no selection");
}

// --- 6. flat input -> no peaks, no crash ------------------------------------
{
  const flat = new Array(N).fill(128);
  const p = detectPeaks(spectrum(flat, NYQ), {});
  ok(p.length === 0, `flat signal -> ${p.length} peaks (0)`);
}

// --- 7. component(): isolate one tone, correct amplitude, other tone removed --
// x = 128 + 40*cos(2π·8n/M) + 30*cos(2π·20n/M). Reconstructing the 8-cycle tone
// must return ~128 + 40*cos(...) — amplitude ~80 ptp, and the 20-cycle tone gone.
{
  const M = 512, C1 = 8, C2 = 20;
  const x = new Array(M);
  for (let n = 0; n < M; n++) x[n] = 128 + 40 * Math.cos(2 * Math.PI * C1 * n / M) + 30 * Math.cos(2 * Math.PI * C2 * n / M);
  const r = component(x, C1);
  let mn = Infinity, mx = -Infinity, err = 0;
  for (let n = 0; n < M; n++) {
    const pure = 128 + 40 * Math.cos(2 * Math.PI * C1 * n / M); // 20-cycle tone excluded
    err = Math.max(err, Math.abs(r[n] - pure));
    if (r[n] < mn) mn = r[n]; if (r[n] > mx) mx = r[n];
  }
  near(mx - mn, 80, 2, "component() recovers the tone amplitude (ptp)");
  ok(err < 1.5, `component() isolates the tone, other frequency removed (max err ${err.toFixed(2)})`);
  // gaps (-1) are tolerated
  const xg = x.slice(); for (let n = 0; n < 20; n++) xg[n] = -1;
  ok(component(xg, C1) != null, "component() tolerates gaps (-1)");
  // NON-INTEGER cycle count must still fit accurately (least-squares, not a naive
  // 2/N DFT coefficient which overshoots and would break carrier subtraction).
  const y = new Array(M);
  for (let n = 0; n < M; n++) y[n] = 128 + 40 * Math.sin(2 * Math.PI * 9.8 * n / M);
  const ry = component(y, 9.8); let e2 = 0;
  for (let n = 0; n < M; n++) e2 = Math.max(e2, Math.abs(ry[n] - y[n]));
  ok(e2 < 2, `component() fits a non-integer (9.8) cycle count (max err ${e2.toFixed(2)})`);
}

// --- 8. carrier removal: subtract the carrier component to reveal minor waves --
{
  const M = 2048, r = [];
  for (let n = 0; n < M; n++) r.push(128 + 40 * Math.sin(2 * Math.PI * 10 * n / M) + 6 * Math.sin(2 * Math.PI * 37 * n / M));
  const comp = component(r, 10), res = r.slice();
  for (let n = 0; n < M; n++) res[n] -= (comp[n] - 128); // remove the carrier's AC part
  let mn = Infinity, mx = -Infinity; for (const v of res) { if (v < mn) mn = v; if (v > mx) mx = v; }
  near(mx - mn, 12, 3, "removing a sine carrier reveals the ~12ptp minor wave underneath");
}

// --- 9. amplitude between bins: the fit, not the scalloped Hann bin ---------
// (bug hunt 2026-10-10: a 5 MHz clock half a bin off read 1.70 V for 1.98 V.)
{
  const M = 2048, r = [];
  for (let n = 0; n < M; n++) r.push(128 + 50 * Math.sin(2 * Math.PI * 20.5 * n / M + 0.3));
  const sp = spectrum(r, 1);
  const hann = 4 * sp.peak / (sp.half * 2);
  ok(hann < 50 * 0.9, `Hann bin reads low half a bin off (${hann.toFixed(1)} of 50)`);
  near(toneAmplitude(r, 20.5), 50, 0.5, "toneAmplitude() recovers the amplitude half a bin off");
}

// --- 10. harmonics + THD (SPECTRUM's peak table, REQ-SDS-216) ----------------
// The rules of analysis.Harmonics (the LCD's table): H1..H10 numbered, the THD
// over H2..H10 from the tone levels. A square wave's odd harmonics fall as 1/n:
// H3 at −9.5 dB, H5 at −14 dB…; with 8 peaks listed (H1..H15) the THD counts
// H3..H9: √(1/9+1/25+1/49+1/81) = 42.9 %. A pure sine has no harmonic.
{
  const { harmonics } = require("./peaks.js");
  const M = 8192, cyc = 64; // bin-centred fundamental, harmonics on bins too
  const sq = [];
  for (let n = 0; n < M; n++) sq.push(128 + 60 * (((n * cyc / M) % 1) < 0.5 ? 1 : -1));
  const sp = spectrum(sq, 0.5);
  const pk = detectPeaks(sp, { floorDb: -50, maxPeaks: 8 });
  const H = harmonics(pk, sp);
  near(H.f0, cyc / M, 1e-4, "f0 = the largest peak (the fundamental)");
  ok(H.fund === 0, "the fundamental is the first (lowest) peak");
  ok(pk.map((p, i) => H.harm[i]).join(",") === "1,3,5,7,9,0,0,0", "odd harmonics numbered 1,3,5,7,9; H11..H15 are past H10: " + pk.map((p, i) => H.harm[i]).join(","));
  const h3 = pk[H.harm.indexOf(3)];
  near(h3.lvl - H.topDb, 20 * Math.log10(1 / 3), 0.3, "H3 is 1/3 of H1 (−9.5 dB)");
  const sq9 = Math.sqrt(1 / 9 + 1 / 25 + 1 / 49 + 1 / 81);
  near(H.thd, sq9, 0.01, "THD over H3..H9 ≈ 42.9 %");
  ok(H.count === 4, "4 harmonics summed");
  const sine = [];
  for (let n = 0; n < M; n++) sine.push(128 + 60 * Math.sin(2 * Math.PI * cyc * n / M));
  const ss = spectrum(sine, 0.5), sk = detectPeaks(ss, { floorDb: -50, maxPeaks: 8 }), SH = harmonics(sk, ss);
  ok(SH.count === 0 && Number.isNaN(SH.thd), "a pure sine has no harmonic: THD is NaN");
  const e = harmonics([], null);
  ok(e.f0 === 0 && e.fund === -1 && e.harm.length === 0, "no peaks: an empty result");

  // analysis.TestPeaksAndTHDOfWaveforms' band-limited square, between bins:
  // (4/π)·80/n for odd n ≤ 15 at 31.7 cycles in 4096 samples, 8 peaks
  const N4 = 4096, sq4 = [];
  for (let i = 0; i < N4; i++) {
    let v = 128;
    for (let n = 1; n <= 15; n += 2) v += 4 / Math.PI * 80 / n * Math.sin(2 * Math.PI * 31.7 * n * i / N4);
    sq4.push(v);
  }
  const s4 = spectrum(sq4, 0.5), p4 = detectPeaks(s4, { floorDb: -50, maxPeaks: 8 }), H4 = harmonics(p4, s4);
  ok(p4.map((p, i) => H4.harm[i]).filter(Boolean).join(",") === "1,3,5,7,9", "band-limited square between bins: H1,H3..H9 numbered");
  near(100 * H4.thd, 100 * sq9, 1.5, "…THD 42.9 % ±1.5 (the Go test's tolerance)");

  // the rules on a hand-made list: 1.5 bins or 1 % of n·f0, the strongest of
  // two claimants, nothing past H10
  const L = [
    { k: 10, freq: 1000, db: 0, lvl: 0 },
    { k: 30, freq: 3005, db: -10, lvl: -10 },   // H3, 5 Hz off
    { k: 31, freq: 3100, db: -5, lvl: -5 },     // also near H3 (100 Hz ≤ 1.5 bins = 150 Hz): stronger, wins
    { k: 50, freq: 5200, db: -20, lvl: -20 },   // 200 Hz off H5: no harmonic
    { k: 120, freq: 12000, db: -30, lvl: -30 }, // H12: past H10
  ];
  const HL = harmonics(L);
  ok(HL.harm.join(",") === "1,0,3,0,0", "tolerance, strongest claimant and H10 limit: " + HL.harm.join(","));
  near(HL.thd, Math.pow(10, -5 / 20), 1e-12, "THD from the winning H3 only");
  // the tone level, not the bin level, picks the fundamental and sets Rel
  const T = [{ k: 10, freq: 1000, db: -1.0, lvl: 0.3 }, { k: 20, freq: 2000, db: 0, lvl: 0 }];
  const HT = harmonics(T);
  ok(HT.fund === 0 && HT.topDb === 0.3 && HT.harm.join(",") === "1,2", "the strongest tone level is the fundamental");
}

// --- 11. a peak's tone level: the Hann bin corrected for scalloping ----------
// analysis.TestPeakLevelBetweenBins: a 60-code tone 100+off bins in, a 30-code
// tone on bin 300. The bin of a tone half a bin off reads ~1.42 dB low; the
// corrected level `lvl` stays within 0.06 dB of the true +6.02 dB at any offset.
{
  const N4 = 4096;
  for (const off of [0, 0.1, 0.2, 0.25, 0.3, 0.4, 0.45, 0.5, 0.6, 0.75, 0.9]) {
    const s = [];
    for (let i = 0; i < N4; i++) s.push(128 + 60 * Math.sin(2 * Math.PI * (100 + off) * i / N4) + 30 * Math.sin(2 * Math.PI * 300 * i / N4 + 0.4));
    const pk = detectPeaks(spectrum(s, 0.5), { floorDb: -50, maxPeaks: 4 });
    if (pk.length !== 2) { ok(false, `offset ${off}: ${pk.length} peaks, want 2`); continue; }
    const want = 20 * Math.log10(2);
    const lvlErr = pk[0].lvl - pk[1].lvl - want, binErr = pk[0].db - pk[1].lvl - want;
    ok(Math.abs(lvlErr) <= 0.06, `offset ${off} bins: tone level ${lvlErr.toFixed(3)} dB off (the bin ${binErr.toFixed(3)} dB)`);
    if (off === 0.5) ok(binErr < -1.4, `half a bin off, the bin reads ${binErr.toFixed(3)} dB (≈ −1.42)`);
    ok(Math.abs(pk[1].lvl - pk[1].db) < 1e-3, `offset ${off}: an on-bin tone's level is its bin level`);
  }
}

console.log(failed ? `\n${failed} FAILED` : "\nALL PASS");
process.exit(failed ? 1 : 0);
