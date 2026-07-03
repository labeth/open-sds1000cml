// End-to-end test for the FFT peak-detection + selection logic that ui.html
// runs in the browser. It loads the SAME peaks.js the page loads, feeds it
// synthetic scope frames, and checks the two things the user hit: selection
// must survive frame-to-frame magnitude jitter, and you must be able to pick a
// different peak afterwards. Run: node peaks.test.cjs  (exit 0 = pass).
const { spectrum, detectPeaks, nearestPeak, component } = require("./peaks.js");

let failed = 0;
function ok(cond, msg) { if (!cond) { console.error("FAIL:", msg); failed++; } else { console.log("ok  -", msg); } }
function near(a, b, tol, msg) { ok(Math.abs(a - b) <= tol, `${msg} (got ${a}, want ${b}±${tol})`); }

// A scope frame: 1024 byte-codes (0..255) around 128, = two tones + noise.
// N=1024 samples over col_span_s = 1.024 ms  ->  1 MS/s  ->  Nyquist 500 kHz.
const N = 1024, SR = 1e6, NYQ = SR / 2, BIN = SR / N; // 976.5625 Hz/bin
// Bin-centred tones (no Hann sidelobes above the −50 dB floor): clean peaks.
const F1 = 52 * BIN;   // ~50.78 kHz
const F2 = 123 * BIN;  // ~120.12 kHz

function frame(amp1, amp2, seed) {
  const c1 = new Array(N);
  let s = seed >>> 0;
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

console.log(failed ? `\n${failed} FAILED` : "\nALL PASS");
process.exit(failed ? 1 : 0);
