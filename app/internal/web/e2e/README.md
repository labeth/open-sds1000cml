# Operator end-to-end workflows

Drives the REAL scope web UI the way a human does — clicking actual buttons and
reading results off the rendered measurement panel / device screen, never the
data APIs. Each workflow starts from a signal the bench's FPGA board generates
and reaches the result an operator wants (a measurement, a decode, an eye, a
pass/fail). These run against a live scope, not in CI (the CI lane runs the
fuzzer below against a synthetic scope).

## Run

Load the source on the signal generator, then run its batch against the scope:

    RUN_ALL=1 node run.mjs tone1M

`SCOPE_URL` overrides the target (default http://192.168.1.209:8080). Without
`RUN_ALL=1` the runner stops at the first failing workflow so it can be
root-caused (a control that does not work is investigated, not retried).

## Sources

105 workflows across 6 sources (`source` in `workflows.mjs`). The sources come
from the bench's FPGA signal generator, a separate project outside this
repository:

    tone1M   25   1 MHz square on C1 — measure/trigger/view/FFT/cursors/math/
                  superres/coupling/probe/help/single/freeze/persist/zoom/pane
                  maximize/harmonics + THD/statistics/stacked master + mask
    prbs2M   24   PRBS7 data on C1 + bit clock on C2 — eye/jitter/big views/
                  two-channel/math/X-Y/refs/superres/AVERAGE/ERES/spectrum
    spi      20   SPI mode 0 — decode (hex/auto/bit order)/cursors/refs/math/
                  zoom/coupling/peak-detect/measurements
    maskviol 12   pulse train + width violation — pulse measure/trigger/mask/zone
    uart     12   UART 8N1 115200 — decode transcript/hex/ASCII/auto/C2/wrong
                  baud/BUS entry detection/packet table (hex + string, zoom)
    burst    12   50/150/250 MHz stepped burst — FFT/superres/zone/persist/envelope

maskviol drives a low-duty pulse, a hard case for AUTO, so its batch is the
least stable of the six.

## Contract

`operator.mjs` primitives assert every control ACTUALLY responded and THROW
with a precise message otherwise — that is how UX and root-cause issues
surface. Results are read from the rendered DOM (measurement cells carry
`data-meas="<ch>:<key>"`, the decode transcript, BUS's packet table and
`#ejBody`, SPECTRUM's `#fftBody`/`#roBody`, `#zmMeter`) or the device LCD PNG
(`/api/screen.png`).

The web UI is six task workspaces (SCOPE, BUS, SPECTRUM, MEASURE, COMPARE,
SOUND) over a fixed instrument strip. A primitive first brings its control on
screen the way an operator does (workspace tab, pane, view — `revealControl`,
what All settings' arrows use); `op.ws(id)` clicks a workspace tab,
`op.topPeak(ch)` reads SPECTRUM's largest peak, `op.eyeOn(ch)` opens BUS's eye
view on a channel, `op.eyeRow(name)` reads an eye/jitter figure.

## Operator fuzzer (`fuzz.mjs`)

A seeded random operator against the scope's web UI: weighted realistic GUI
actions (timebase/vertical/trigger churn, view modes, decode, analyzer
arm/disarm cycles, zoom/pan/freeze), with invariants checked after EVERY action
(no page errors, frames keep flowing, no NaN/undefined readouts, /api/status
healthy, no horizontal scroll, no 5xx). Findings land in `OUT/findings.jsonl`
with a screenshot each; the run continues so one pass surfaces them all.

    ITERS=200 SEED=42 OUT=/tmp/fuzz node fuzz.mjs
    ITERS=19  SEED=42 DEBUG_AT=19 node fuzz.mjs   # replay to a finding with layout diagnostics
    FUZZ_STACK=1 ...                              # page errors recorded with their stacks

The palette drives the workspaces too: tab switches, a workspace's key again
(not COMPARE's, which stacks a master), the Y/X/F view letters, SPECTRUM/BUS/
MEASURE view choices, pane collapse and maximize, All settings and help.
Segmented choices (coupling, probe, trigger source/slope/mode, acquisition,
decode format…) are clicked; a control in another workspace is revealed first.
The CI lane (`TestFuzzBrowserSynthetic`) runs 60 iterations of seed 1 against a
synthetic scope; `FUZZ_ITERS`/`FUZZ_SEED` run longer.

`regress_missing_channel.mjs` checks that frames without a per-sample channel
array (a channel off, an envelope or roll band) never throw in the FFT,
spectrogram and decode draw paths.
