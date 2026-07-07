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
Results are read from the rendered DOM (`#measBody`, decode text, `#ejBody`,
`#zmMeter`) or the device LCD PNG.

## Note on sources

100 workflows across 6 sources:

    tone1M   22   1 MHz square — measure/trigger/view/FFT/cursors/math/superres/
                  coupling/probe/help/single/freeze/persist/zoom/panel
    prbs2M   24   PRBS7 + clock — eye/jitter/big-views/two-channel/math/X-Y/refs/
                  superres/AVERAGE/ERES/spectrum
    spi      20   SPI Mode 0 — decode (hex/auto/bit-order)/cursors/refs/math/zoom/
                  coupling/peak-detect/measurements
    maskviol 12   pulse train + width violation — pulse measure/trigger/mask/zone
    uart     10   UART 8N1 115200 — decode transcript/hex/ASCII/auto/C2/wrong-baud
    burst    12   50/150/250 MHz stepped burst — FFT/superres/zone/persist/envelope

88 (all but maskviol) are rock-solid. maskviol drives a very-low-duty pulse
(25%): autoset on such signals is a hard case AND this board's maskviol FPGA
config drifts over minutes, so that batch is best-effort — not a scope bug (the
scope measures whatever the FPGA emits).

## Bugs this campaign found and fixed

- Scientific notation on readouts (`1.00e+3 ns` for a 1 µs value) — web + LCD +
  panel formatters.
- Autoset read an aliased frequency from a slow/roll start (two divergent
  autoset implementations; unified on the robust device sweep + web delegates).
- RUN did nothing after a SINGLE capture (stale run-state shadow).
- Autoset couldn't find a low-duty pulse train (amplitude-only "found" test).
- Clicking the eye diagram to enlarge it crashed the page when RJ was not yet
  available (`eng(undefined)`); `eng()` now dashes non-finite input.
