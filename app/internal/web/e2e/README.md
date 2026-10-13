# Operator end-to-end workflows

Drives the REAL scope web UI the way a human does — clicking actual buttons and
reading results off the rendered measurement panel / device screen, never the
data APIs. Each workflow starts from an FPGA-generated signal and reaches the
result an operator wants (a measurement, a decode, an eye, a pass/fail).

## Run

Flash the source, then run its batch against the live unit:

    # tone1M — a 1 MHz square on C1
    (cd ~/ws/fpga/proj && ./build.sh 1)
    RUN_ALL=1 node run.mjs tone1M

    # prbs2M — PRBS7 data on C1 + bit clock on C2 (eye/jitter/math/X-Y)
    (cd ~/ws/fpga/proj && ./build-prbs.sh 2)
    RUN_ALL=1 node run.mjs prbs2M

    # maskviol — pulse train + width violation (pulse measure/trigger, mask, zone)
    (cd ~/ws/fpga/proj && ./build-maskviol.sh 400 7 12 100)
    RUN_ALL=1 node run.mjs maskviol

`SCOPE_URL` overrides the target (default http://192.168.1.209:8080).
Without `RUN_ALL=1` the runner STOPS at the first failing workflow so it can be
root-caused (the operator rule: a control that doesn't work is investigated,
not retried).

## Contract

`operator.mjs` primitives assert every control ACTUALLY responded and THROW
with a precise message otherwise — that is how UX/root-cause issues surface.
Results are read from the rendered DOM (measurement cells carry
`data-meas="<ch>:<key>"`, the decode transcript, BUS's packet table and
`#ejBody`, SPECTRUM's `#fftBody`/`#roBody`, `#zmMeter`) or the device LCD PNG.

The web UI is five task workspaces (SCOPE, BUS, SPECTRUM, MEASURE, COMPARE)
over a fixed instrument strip. A primitive first brings its control on screen
the way an operator does (workspace tab, pane, view — `revealControl`, what
All settings' arrows use); `op.ws(id)` clicks a workspace tab, `op.topPeak(ch)`
reads SPECTRUM's largest peak, `op.eyeOn(ch)` opens BUS's eye view on a
channel, `op.eyeRow(name)` reads an eye/jitter figure.

## Note on sources

105 workflows across 6 sources:

    tone1M   25   1 MHz square — measure/trigger/view/FFT/cursors/math/superres/
                  coupling/probe/help/single/freeze/persist/zoom/pane maximize/
                  harmonics + THD/statistics/stacked master + mask
    prbs2M   24   PRBS7 + clock — eye/jitter/big-views/two-channel/math/X-Y/refs/
                  superres/AVERAGE/ERES/spectrum
    spi      20   SPI Mode 0 — decode (hex/auto/bit-order)/cursors/refs/math/zoom/
                  coupling/peak-detect/measurements
    maskviol 12   pulse train + width violation — pulse measure/trigger/mask/zone
    uart     12   UART 8N1 115200 — decode transcript/hex/ASCII/auto/C2/wrong-baud/
                  BUS entry detection/packet table (hex + string, holds still, zoom)
    burst    12   50/150/250 MHz stepped burst — FFT/superres/zone/persist/envelope

88 (all but maskviol) are rock-solid. maskviol drives a very-low-duty pulse
(25%): autoset on such signals is a hard case AND this board's maskviol FPGA
config drifts over minutes, so that batch is best-effort — not a scope bug (the
scope measures whatever the FPGA emits).

## Operator fuzzer (`fuzz.mjs`)

Seeded random-operator campaign against the LIVE scope UI: weighted realistic
GUI actions (timebase/vertical/trigger churn, view modes, decode, analyzer
arm/disarm cycles, zoom/pan/freeze), with invariants checked after EVERY action
(no page errors, frames keep flowing, no NaN/undefined readouts, /api/status
healthy, no horizontal scroll, no 5xx). Findings land in OUT/findings.jsonl with
a screenshot each; the run continues so one pass surfaces the population.

    ITERS=200 SEED=42 OUT=/tmp/fuzz node fuzz.mjs
    ITERS=19  SEED=42 DEBUG_AT=19 node fuzz.mjs   # replay to a finding with layout diagnostics
    FUZZ_STACK=1 ...                              # page errors recorded with their stacks

The palette drives the workspaces too: tab switches, a workspace's key again
(not COMPARE's, which stacks a master), the Y/X/F view letters, SPECTRUM/BUS/
MEASURE view choices, pane collapse and maximize, All settings and help.
Segmented choices (coupling, probe, trigger source/slope/mode, acquisition,
decode format…) are clicked; a control in another workspace is revealed first.
The CI lane (`TestFuzzBrowserSynthetic`) runs 60 iterations of seed 1 against a
synthetic scope; `FUZZ_ITERS`/`FUZZ_SEED` run longer hunts.

Bugs found by the first 200-iteration run (both fixed):
  * peakNyq() crashed on frames lacking a per-sample channel array (channel
    off / envelope+roll bands) — storming page errors from FFT, spectrogram
    and zoom paths (app_fft.js; regress_missing_channel.mjs guards the class).
  * the floated .card-actions header cluster overflowed its h3, letting the
    decode role-select row paint over the auto-detect button and swallow its
    clicks (base.css flow-root containment).
