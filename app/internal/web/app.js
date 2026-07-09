// app.js — the client's shared foundation: global state, config constants, the
// palette/DOM handles, and a few small helpers. Loaded FIRST (after the JS
// libraries) so every feature module (app_*.js) and the wiring can close over
// these globals. Feature logic + its event wiring live in the per-feature files;
// app_init.js runs last (first paint + poll). See ui.html for the load order.
"use strict";
const $ = id => document.getElementById(id);
const scope = $("scope");
let ctx = null; // the scope's 2D-facade over WebGL — set by glInit() (app_gl.js)
let CW = 800, CH = 400, dpr = 1;
const DIVX = 10, DIVY = 8;

let st = null;        // last /api/status
let frame = null;     // last frame reply
let fftRaw = null, fftRawT = 0, fftRawBusy = false; // full-record RAW frame for FFT mode (native-fast display is an interpolated window)
let lastSeq = 0;
let lvlDragging = false, offDragging = false;
let frozen = false;
// Probe attenuation is a tip-referred display multiplier: every volts the
// client SHOWS is at the probe tip, but code/DAC math is electrical (at the
// scope input), so divide tip volts by the probe factor before sending.
const probeOf = (ch) => (st && (ch === 2 ? st.probe2 : st.probe1)) || 1;
const trigProbe = () => probeOf(st && st.trig_source === 1 ? 2 : 1);
const view = { mode: "YT", persist: false, cursors: false, c1: true, c2: true,
               win: { a: 0, b: 1 },     // visible column range as fractions of 0..cols-1
               vwin: { a: 0, b: 1 },    // visible VOLTAGE range as fractions of full scale (0=bottom)
               fwin: { a: 0, b: 1 } };  // visible FREQUENCY range as fractions of 0..Nyquist (FFT)
let userZoomed = false;   // true once the user pans/zooms → live frames stop re-homing
let lastSig = "";         // acquisition signature; a change re-homes even if zoomed
// Normalized cursor positions (fractions of width/height).
const cur = { t1: 0.33, t2: 0.66, v1: 0.4, v2: 0.6, drag: null };
// Super-res GATE markers: which region to super-res. a/b are fractions of the
// DISPLAY record (0..1), so they stay pinned to the signal through zoom/pan
// (the display is trigger-anchored). Turning the gate on AUTO-PLACES them on the
// best thing in the current view (active region, one period); after that the
// markers are the ONLY truth — arming stacks exactly what they span.
const srGate = { on: false, placed: false, a: 0.4, b: 0.6, drag: null };
let reqCols = 2048;   // full-resolution both channels (decode + navigator); client-side zoom

// ---- navigator / horizontal zoom ----
const nav = $("nav");
let navCtx = null; // WebGL 2D facade, set by glInit (the navigator is a GL canvas)
let NW = 0, NH = 0;
const NAVH_CSS = 56, MINSPAN = 0.004;
const navDrag = { active: false, grab: 0, a0: 0, b0: 0 };

// ---- protocol decode ----
const dcfg = {
  proto: "off", scl: 1, sda: 2, clk: 1, data: 2, line: 1,
  threshold: null, auto: true, baud: 115200, bits: 8, parity: "none",
  cpol: 0, cpha: 0, msb: true, fmt: "hex", result: null,
  stream: false, hist: [], lastStreamSeq: 0,   // stitched high-bandwidth decode + packet history
  // Watch/capture: save decoded windows matching a rule to a client buffer for
  // later review. watchErr = on any decode error; watchMatch = substring/regex on
  // the transcript. captures[] holds {seq,t,reason,text,snap}; reviewIdx pins one.
  watch: false, watchErr: true, watchMatch: "", captures: [], reviewIdx: -1, lastCapKey: "",
};
const DECCOL = {
  start: "#3fb950", stop: "#e8604c", addr: "#b98cff", rw: "#8fa6b8",
  ack: "#3fb950", nak: "#e8604c", data: "#35c8e8",
  "frame-error": "#e8604c", "parity-error": "#f5a24c", gap: "#7c8894", idle: "#3a444e",
};

const css = getComputedStyle(document.body);
const C1COL = css.getPropertyValue("--c1").trim();
const C2COL = css.getPropertyValue("--c2").trim();
const CURCOL = css.getPropertyValue("--cursor").trim();
const MATHCOL = css.getPropertyValue("--math").trim();
const TRIGCOL = css.getPropertyValue("--trigger").trim();

// ---- responsive canvas ----
function resize() {
  dpr = window.devicePixelRatio || 1;
  const box = $("scopebox");
  const w = Math.max(240, Math.floor(box.clientWidth));
  const h = Math.max(120, Math.floor(box.clientHeight));
  // The canvas ELEMENT is sized purely by CSS (#scope is position:absolute
  // inset:0, so it's bound to #scopebox's edges and can NEVER exceed the flex
  // layout / overflow the side panel). We only set the BACKING store here (device
  // px) for crisp rendering — never an explicit style width/height, which (being
  // absolute) would paint outside #scopebox if the measurement were ever stale.
  scope.width = Math.round(w * dpr); scope.height = Math.round(h * dpr);
  CW = scope.width; CH = scope.height;
  // Navigator canvas: full width, fixed CSS height.
  nav.width = Math.round(w * dpr); nav.height = Math.round(NAVH_CSS * dpr);
  NW = nav.width; NH = nav.height;
  if (GLR) GLR.resize(); // size the scope's WebGL backing store to the CSS box
  if (NAVR) NAVR.resize();
  // 2048 is 1:1 with real samples on decimated bands (the µs–ms/div range where
  // I2C/UART/slow-SPI live); native-fast bands downsample the window, tolerable
  // for mid-level thresholding. Single source of truth for the fetch width.
  reqCols = 2048;
  clearPersist();
  redraw();
}

// ---- formatting ----
function eng(x, unit, digits) {
  digits = digits || 3;
  // a non-finite / undefined value (e.g. an eye metric not yet available)
  // must render as a dash, not crash on x.toExponential(...) below.
  if (typeof x !== "number" || !isFinite(x)) return "— " + unit;
  const a = Math.abs(x);
  if (a === 0) return "0 " + unit;
  const pfx = [["G",1e9],["M",1e6],["k",1e3],["",1],["m",1e-3],["µ",1e-6],["n",1e-9],["p",1e-12]];
  for (let i = 0; i < pfx.length; i++) {
    const [p, s] = pfx[i];
    if (a >= s) {
      const str = (x / s).toPrecision(digits);
      // toPrecision emits SCIENTIFIC notation when rounding pushes the mantissa
      // to 1000 at a prefix boundary (e.g. 999.9 ns -> "1.00e+3 ns"). Promote
      // to the next-larger prefix so the operator reads "1.00 µs", never
      // "1.00e+3 ns", on any measurement just under a 1000x boundary.
      if (str.includes("e") && i > 0) {
        const [p2, s2] = pfx[i - 1];
        return (x / s2).toPrecision(digits) + " " + p2 + unit;
      }
      return str + " " + p + unit;
    }
  }
  return x.toExponential(digits - 1) + " " + unit;
}

// ---- persistence / afterglow lives in a WebGL accumulation framebuffer inside
// the scope renderer (GLR.persistFade/Composite/Clear) — no 2D canvas ----





// component() is the per-redraw hot path on big stacks: a full-record tone fit
// (2 trig/sample over up to ~1.3M samples) per selected peak, run by BOTH the
// residual math and the Y-T peak overlay. On a static stack neither the source
// data nor the picked frequencies change as you pan/zoom/hover — only the view
// window — so memoize each fitted tone by (source-array identity, cycles-per-
// record). All the interactive redraws then hit the cache; it self-clears when
// the source array changes (new live frame, re-stack, or leaving stack view),
// and the shared key dedups the overlay and residual calls onto one fit.
let compMemo = { src: null, map: new Map() };

// Math trace: arithmetic on C1/C2, or a RESIDUAL — a channel minus its selected
// FFT peaks — so you can null out a carrier and see the minor waves underneath.
let mathFn = "off";
// The math trace rebuilds a full-length array (a copy + a subtraction loop per
// selected tone for the residual) — cheap live, but ~1.3M points on a stack, and
// drawMath() runs it every redraw. It only changes when the source arrays, the
// math mode, or (for the residual) the picked frequencies change, so memoize the
// result and let pan/zoom/hover reuse it. Same self-clearing (by-identity) scheme
// as componentMemo.
let mathMemo = { c1: null, c2: null, fn: null, sel: "", out: null };

// Reference waveforms: saved snapshots (codes + their volts/code + offset)
// overlaid for comparison. A ref stays at its ABSOLUTE voltage — it is
// re-mapped to the current V/div/offset when drawn, not pinned to screen codes.
const refs = { A: null, B: null };
const REFCOL = { A: "#b48ead", B: "#a3be8c" };




// FFT + peak detection live in peaks.js (shared with the node e2e test).
// SELECTION IS TRACKED BY FREQUENCY, not by list index: peak magnitudes jitter
// with noise, so the strongest-first ranking (and hence any index) reshuffles
// frame-to-frame. The picked frequencies are the source of truth; every frame we
// re-locate each one to its nearest peak with nearestPeak().
// PER-CHANNEL MULTI-SELECT peak model. Each channel keeps its own peak list +
// selection so C1 and C2 spectra are inspected and picked independently (two
// boxes). SELECTION IS TRACKED BY FREQUENCY (magnitudes jitter -> a strongest-
// first index reshuffles frame-to-frame): fftCh[ch].sel holds the picked FREQs,
// re-anchored to the nearest current peak every frame; selIdx maps them to the
// current peak indices for highlighting. Click a peak/row to toggle; per-box
// "clear" empties that channel.
const fftCh = { 1: { peaks: [], sel: [], selIdx: new Set() }, 2: { peaks: [], sel: [], selIdx: new Set() } };
let maxPeaks = 8;
// Distinct palettes so a channel's selected-component overlays are attributable
// on the waveform: C1 warm, C2 cool.
const COMPCOLS = { 1: ["#ff9f1c", "#ffe14f", "#ff5ec4", "#ffb86b", "#ffd24f"], 2: ["#4fd8ff", "#8cff5a", "#b98cff", "#5ad9ff", "#6bffb8"] };
// Recompute one channel's peaks + re-anchor its selection to the nearest current
// peak (tracks drift, dedupes, keeps a freq whose peak momentarily vanished).
// Returns that channel's spectrum, or null if the channel is off/absent/flat.
// Per-channel spectrum memo keyed by the frame array IDENTITY (every frame
// allocates fresh arrays, so identity is an exact cache key): interaction
// redraws (wheel/drag/cursor) and the peak-list refresh reuse the spectrum
// instead of re-running a full-record FFT on the same data.
const specMemo = { 1: { src: null, spec: null }, 2: { src: null, spec: null } };
// The FFT input is capped to FFT_MAX points. Frequency RESOLUTION is set by
// the record's time span, not the point count, so decimating a huge array
// (a superres stack reaches K× the raw rate = >1M points) only lowers the
// axis' top Nyquist to FFT_MAX/(2·span) ≈ 400 MHz — which is above the raw
// Nyquist, so it keeps every real spectral line while dropping the pure
// interpolation-artifact region above it, at ~40× the FFT speed. Normal
// records (≤20480 samples) are already under the cap → unchanged.
const FFT_MAX = 32768;

// Pointer readout state (FFT mode): frequency at x, dB at y, and each
// visible channel's curve level at that frequency.
const fftHover = { on: false, x: 0, y: 0 };


// Y-T overlay: for each channel, reconstruct EACH selected tone from THAT
// channel's trace and draw it over the waveform (one palette-coloured curve per
// selected frequency) so picked sub-frequencies are visible as their own curves.
let peakListLastT = 0;







// Measurement rows. A compact default set plus an expandable "more" group
// (timing/pulse) so the panel stays scannable but the depth is one click away.
const MEAS_CORE = ["Vpp", "Vmax", "Vmin", "Vmean", "Vrms", "Freq", "Period", "Duty"];
const MEAS_MORE = ["Vtop", "Vbase", "Vampl", "Rise", "Fall", "+Width", "-Width", "Overshoot"];
let measExpanded = false;
// The row STRUCTURE only changes on expand/collapse or a clip-flag flip; the
// VALUES change every frame. Rebuild the table on structure changes, update
// cell textContent in place otherwise — an innerHTML re-parse at 20 fps was
// measurable DOM churn for identical markup.
let measDomSig = "", measCells = null;



// ---- DIRECT MANIPULATION of the on-screen markers ----
// You drag the TRIGGER-LEVEL handle (right edge) or a channel's GROUND/offset
// arrow (left edge) right on the display — a vertical quantity moved vertically,
// in the direction you drag. Far better than a horizontal slider for an up/down
// value. (The footer sliders remain for fine keyboard/number entry.)
let mk = null;

// Rubber-band box zoom: drag a rectangle → zoom into it. In Y-T each axis
// applies only if the box has real extent there (a flat drag zooms time
// only); in FFT the x-extent sets the frequency window. Esc/short drags
// cancel; double-click resets as before.
const boxZoom = { active: false, moved: false, sx: 0, sy: 0, ex: 0, ey: 0 };
let fftHoverRaf = 0;

// Navigator: drag to pan the viewport (click outside it first recenters it);
// double-click resets to the trigger-centered "home" slice. Separate from the
// scope's own pointer handlers, so cursor-drag / FFT-pick are untouched.
const navWin = () => view.mode === "FFT" ? view.fwin : view.win; // which window the strip controls

// ---- frame transport: /api/frame.bin long-poll (the ONE transport) ----
// The server PARKS the request (waitms) until a new frame publishes, then
// replies with a small JSON header + raw uint8 payload that binframe.js expands
// to Int16Array — decode is ~0 vs the 50-150 ms the device burned JSON-encoding
// int16 arrays, and there's no client-side poll gap (request-when-ready is the
// pacing). Any failure (bad reply, OTA app restart) retries with jittered
// backoff — the endpoint comes back at full speed; there is no second transport.
let binFailures = 0;

// Buttons that are ON/OFF toggles get an aria-pressed mirror of their .on class.
const PRESSED = ["mYT", "mXY", "mFFT", "tPersist", "tCursors", "tC1", "tC2", "freeze",
  "mode", "ets", "single", "decAuto", "decWatch", "decStream"];


let lastLineHTML = "", lastAria = "";


// AUTOSET — one click fits the whole scope to the live signal. It delegates to
// the DEVICE autoset routine (the same one the front-panel AUTO button runs):
// a single, robust implementation that sweeps the timebase to find the signal's
// NATIVE band (so the frequency is never read off an aliased slow band), fits
// each channel's vertical, and sets an edge trigger. The web UI used to carry a
// second, divergent autoset that mis-read aliased frequencies from slow/roll
// timebases — delegating removes that whole class of bug.
let autosetBusy = false;
const sendPulse = () => sendParams("pulseparams", { lvl: +$("p-lvl").value / 100, min: +$("p-min").value * 1000, max: +$("p-max").value * 1000, cond: +$("p-cond").value });
const sendSlope = () => sendParams("slopeparams", { lo: +$("s-lo").value / 100, hi: +$("s-hi").value / 100, min: +$("s-min").value * 1000, max: +$("s-max").value * 1000, cond: +$("s-cond").value });
const sendVideo = () => sendParams("videoparams", { std: +$("v-std").value, line: +$("v-line").value, neg: +$("v-neg").value === 1 });

// ---- superres: stack-and-crunch (align → lucky → drizzle → stack) ----
// A dedicated raw long-poll (?raw=1) feeds superres.js while armed — the
// display transport is untouched. The result is reviewed through the same
// frozen-synthetic-frame path as captures, so zoom/cursors/CSV/PNG all work
// on the stacked waveform, and "fit model" writes the analytic sum-of-
// sinusoids reconstruction into REF B for overlay comparison.
const sr = { st: null, armed: false, gen: 0, lastSeq: 0, meta: null, t0: 0, stopMode: "bits", stopVal: 4, lastBits: 0, lockRef: false, gateDt: null, lastUi: 0, ch: 0, alignCh: 0,
  showing: false, savedWin: null, // stack-view toggle state + remembered zoom
  comp: false, compFbw: "auto", compSpend: 0.8, compInfo: null, // analog-falloff compensation (de-embed the measured chain rolloff; auto target from the stack's bit budget)
  ets: false, etsF: 0, etsSt: null, etsInfo: null, // phase-coherent equivalent-time reconstruction of a free-run/untriggerable clock
  evt: false, evtByte: NaN, evtMargin: 0, // decode-triggered super-res: stack a decoded protocol byte event
  // Offset dither: the 8-bit quantizer's staircase survives averaging when
  // the front-end noise is sub-LSB. Sweeping the offset DAC by sub-LSB steps
  // across frames (and subtracting the COMMANDED offset back in code space)
  // slides the code thresholds across the signal, so the staircase averages
  // out. dither.pending skips the one frame after each step — the DAC write
  // is staged between captures, so that frame's true offset is ambiguous.
  dither: { on: false, base: 0, steps: 8, idx: 0, pending: 0, framesAtStep: 0 } };





let srFails = 0;

// ---- keyboard shortcuts + ? help overlay ----
// One declarative registry drives the keymap AND the help sheet, so adding a
// shortcut is a one-line change (the extensibility pattern from the ADR).
const KEYMAP = [
  { key: " ", label: "Space", desc: "Run / Stop", run: () => $("run").click() },
  { key: "s", label: "S", desc: "Single shot", run: () => $("single").click() },
  { key: "a", label: "A", desc: "AUTO / NORM trigger", run: () => $("mode").click() },
  { key: "t", label: "T", desc: "Trigger source C1/C2", run: () => $("source").click() },
  { key: "1", label: "1", desc: "Toggle channel 1", run: () => $("tC1").click() },
  { key: "2", label: "2", desc: "Toggle channel 2", run: () => $("tC2").click() },
  { key: "c", label: "C", desc: "Cursors", run: () => $("tCursors").click() },
  { key: "p", label: "P", desc: "Persist", run: () => $("tPersist").click() },
  { key: "z", label: "Z", desc: "Freeze", run: () => $("freeze").click() },
  { key: "y", label: "Y", desc: "Y-T view", run: () => setMode("YT") },
  { key: "x", label: "X", desc: "X-Y view", run: () => setMode("XY") },
  { key: "f", label: "F", desc: "FFT view", run: () => setMode("FFT") },
  { key: "?", label: "?", desc: "Show / hide this help", run: () => toggleHelp() },
];
// Mouse gestures — listed in the ? overlay so they're discoverable.
const MOUSEMAP = [
  { label: "Drag (empty area)", desc: "Box zoom — time and voltage (Y-T), frequency (FFT)" },
  { label: "Wheel", desc: "Zoom the time axis about the cursor (FFT: frequency axis)" },
  { label: "Shift+Wheel", desc: "Pan left / right through the record" },
  { label: "Ctrl+Wheel", desc: "Change time/div (zoom the acquisition)" },
  { label: "Double-click", desc: "Reset zoom (time + voltage; FFT: frequency)" },
  { label: "Drag ▸ handle (right)", desc: "Move the trigger level" },
  { label: "Drag ◂ arrow (left)", desc: "Move a channel's offset" },
  { label: "Shift+click", desc: "Set the trigger level where you click" },
];
function editableFocused() { const a = document.activeElement; return a && /^(INPUT|SELECT|TEXTAREA)$/.test(a.tagName); }
function toggleHelp() {
  const el = $("help");
  if (!el.classList.contains("show")) {
    const rows = arr => arr.map(x => `<tr><td><kbd>${x.label}</kbd></td><td>${x.desc}</td></tr>`).join("");
    $("helpBody").innerHTML =
      `<tr><th colspan="2" class="fcap">Keyboard</th></tr>` + rows(KEYMAP) +
      `<tr><th colspan="2" class="fcap">Mouse</th></tr>` + rows(MOUSEMAP);
  }
  el.classList.toggle("show");
}


// ---- eye diagram / jitter analysis (eyejitter.js engine) ----
// The serial-analysis package: software CDR over raw records, persistence eye,
// TIE jitter (histogram, RJ/DJ, spectrum). One raw-feed consumer at a time —
// arming the eye stops superres and vice versa.
const ej = { st: null, armed: false, gen: 0, lastSeq: 0, fails: 0, lastUi: 0, vpc: 1 / 25 };
const ejStatus = m => { $("ejStats").textContent = m; };


// ---- rendering (throttled) ----
let ejLastUi = 0, ejEyeCv = null;



// ---- large views: click any diagram (eye / histogram / spectrum) to open a
// full-screen live-refreshing render with proper axes ----
let ejBigKind = "eye";

// ---- Bode / Frequency-Response-Analysis (engine-accumulated; this is the
// control + render surface) ------------------------------------------------
const bode = { armed: false, lastN: -1, timer: 0 };
const GRIDCOL = css.getPropertyValue("--grid").trim() || "#243";
const AXISCOL = css.getPropertyValue("--axis").trim() || "#456";
const DIMCOL = css.getPropertyValue("--dim").trim() || "#9ab";

// ---- Spectrogram ("FFT over time") waterfall ------------------------------
const spg = { armed: false, sg: null, lastSeq: -1, ch: 1 };

