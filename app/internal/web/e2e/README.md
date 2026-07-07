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

maskviol drives a very-low-duty pulse (25%); autoset on such signals is a hard
case and this board's maskviol FPGA config drifts over minutes, so that batch
is best-effort. tone1M (10/10) and prbs2M (12/12) are rock-solid and cover the
bulk of the scope's features.
