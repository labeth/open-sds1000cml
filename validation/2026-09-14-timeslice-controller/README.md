# SRAM timeslice controller — work in progress

The controller overlaps forward-only SRAM address scanning with ARM draining two
host banks. At the read target it samples free banks, reads up to two banks,
and returns to the write head. If neither bank is free it returns immediately;
unread data remains in SRAM. It never waits for ARM while ingress is accumulating
during a read excursion. A full unread SRAM stops writes before overwriting data.

Default geometry is 524288 SRAM words, two 2560-word host banks, 16 discarded
warm-up words and a 32-word read reserve. The ingress path uses 4608 RAM words
plus bridge/prefetch storage. At /256, two Q8.8 channels produce 7.8125 MB/s.
The host simulation uses 10 MB/s and can inject a 10 ms host stall.

`test_controller.py` checks the real transport, ingress bridge/RAM and controller
against a behavioral SRAM, including ordering, address wrap, tail banks, stalled
host reads and unread overwrite protection. `--full` adds physical SRAM geometry
with the 10 ms stall; `--full-steady` checks bounded backlog without that stall.
Historical `full.log` and `steady.log` predate the latest timing pipeline edits;
they are not qualification of current RTL.

`build_probe.py` tests the isolated controller with registered abstract endpoints
at 250 MHz. It does not build an FPGA image or qualify integration timing.
`initial`, `phase-flags`, and `metadata-pipeline` preserve failed timing variants.
Current work adds a separate host-bank selection cycle. See the current build
result and tests log before drawing conclusions about this version.

Remaining before deployment: close controller timing; implement paired 125 MHz
host RAM and its publication crossing; update the host ABI for non-power-of-two
banks; integrate trigger capture geometry and timeout recovery; qualify complete
FPGA timing/resources and device read-origin/warm-up behavior. No image from this
controller has been deployed, and sustained hardware streaming is not proven.

## Current timing investigation

A clean database rebuild reproduced -1.878 ns setup slack. Preserving the
bank-selection boundary and splitting read-address arithmetic improved this to
-1.337 ns (`address-pipeline`). Pipelining ordinal epoch clear and preserving
payload controls then gave -1.426 ns (`counter-control`); this is not a pass.
The critical paths now include unread-count arithmetic and frozen-tail size
selection. Address-pipeline small-geometry functional tests passed.

The build script now locks before modifying generated project state, removes
its own generated databases, and records source SHA-256 values. The simulation
runner now snapshots all RTL before compiling tests, so a long test series
cannot silently mix source versions. `current-full.log` was started before
that runner improvement and must not be treated as a single-version result.

## State-encoding experiments

The subsequent count pipeline (-1.463 ns), explicit one-hot equality decoder
(-2.969 ns), and one-hot bit decoder (-1.841 ns) did not improve timing. Their
sources/results are archived. The working controller is restored to the
best measured `address-pipeline` version (-1.337 ns), still failing 250 MHz.
Tests now refer to named controller states rather than numeric encodings.

The bank-selection version's full physical-geometry steady test passed 65539
words, 26 banks, max ingress pending 4097, max unread SRAM 5152, and 11 overlapping
scans. This is a behavioral-model result, not hardware qualification and not
qualification of the restored source. See `current-full.log`.

## Counter and address pipeline follow-up

Segmented unread arithmetic passed the added 10003-word counter-lane test,
which crosses the 10-bit section boundary in both directions. Automatic
retiming is now disabled in the isolated probe to make explicit pipeline
boundaries auditable. This initially failed by 2.699 ns. Registering the
remaining-empty check and separating tail count selection improved it to
-1.351 ns; that version passed all five functional cases. An unconditional
seek-address mux with a preparation cycle then improved setup to -1.009 ns
(`seek-pipeline`, current RTL). Its functional run is in
`seek-pipeline-tests.log`; inspect completion before claiming it passed.
No timing pass or deployment is claimed.

The historical full-geometry host-stall run also completed: 65539 words,
26 banks, maximum pending 4101, maximum unread 24437, 4 skipped busy-bank
reads and 16 overlapping scans. Its pre-snapshot runner mixed source versions
between separate cases, so it remains architectural evidence only.

## Payload setup stage (current)

Registered unread flags passed all five functional cases; timing was -1.022 ns.
A carry/borrow lane-flag variant also passed but worsened timing to -1.488 ns,
so it was archived and set aside. The current RTL starts from the unread-flags
version and separates bank eligibility from payload initialization using
PAYLOAD_SETUP. This improves isolated setup slack to -0.799 ns with hold
+0.178 ns and minimum pulse +1.513 ns. This still FAILS 250 MHz timing.
`payload-setup-tests.log` is the ongoing functional run for that source.
The testbench additionally rejects simultaneous read/write accounting.

## Direct counter and byte ordinal variants

The payload-setup version passed all five functional cases. Replacing the
unread XOR/direction control with direct exclusive read/write updates improved
setup to -0.687 ns (`direct-count`), and its five tests passed. Registering the
seek-zero decision then gave -1.020 ns and passed the same tests. The current
experiment replaces the ordinal's four 16-bit lanes with eight byte lanes;
its expanded eight-boundary/stalled-step ordinal test passes and isolated
setup is -0.765 ns (`byte-ordinal`). The full small-geometry test run is still
recorded in `byte-ordinal-tests.log`; inspect terminal completion before claiming
all cases passed. No variant meets 250 MHz yet. Historical best remains
`direct-count`; archived controller variants prior to `byte-ordinal` used the
16-bit-lane ordinal source preserved in `seek-zero/ordinal_counter.v`.

## Restored checkpoint

The byte ordinal with lane flags passed all five functional cases but timed at
-1.271 ns. A prefix-of-byte-fullness ordinal passed its dedicated carry test
but timed at -1.831 ns; its remaining test run is `prefix-ordinal-tests.log`.
Both experiments are archived. The working sources are restored to the
`direct-count` controller and `seek-zero/ordinal_counter.v` (16-bit lanes),
which measured -0.687 ns. The expanded test retains all eight byte-boundary
seeds, including boundaries internal to each 16-bit lane. Fresh verification
is in `best-restored-tests.log`. Still no 250 MHz timing pass or deployment.

## Separate full guard and reset accounting (current)

A preserved full flag updates on the same accepted word as unread and is
asserted equal to unread's full bit on every non-reset simulation clock.
This variant passed the five functional cases but timed at -0.955 ns.
Removing duplicate reset gating from internal accounting events (whose
registers already have synchronous reset priority) improved setup to -0.620 ns,
hold +0.178 ns, minimum pulse +1.513 ns. External write/ingress/host/source
interfaces retain immediate reset gating. Evidence is `reset-accounting/`.
The added active-reset tests passed during writing and payload readout,
checking external suppression and zeroed counters/state on the reset edge.
The extended seven-case run is `active-reset-tests.log`; inspect completion.
The complete timing target remains unmet and no image has been deployed.

## Word-prefix ordinal and command preparation (current)

Four 16-bit fullness flags replace the wide ordinal carry comparisons.
`word-prefix-tests.log` passed all seven functional cases; timing was -0.818 ns.
Registering the accepted write event before the reporting ordinal gave -0.648 ns
and passed all seven cases (`write-event-tests.log`). The committed reporting
ordinal now trails acceptance by one clock; unread/full protection does not.
Marker verification includes the pending event. Physical writes remain delayed
by the transport pipeline. Future trigger metadata must respect this latency.

The current command-preparation variant updates command length unconditionally
and samples it only with the ready-gated command strobe. It also registers
seek-zero alongside the distance calculation. Setup is -0.613 ns, hold +0.179 ns,
minimum pulse +1.513 ns (`command-prep/`). Functional run is
`command-prep-tests.log`; inspect terminal completion before claiming it passed.
Timing still fails; no FPGA image or hardware qualification is claimed.

## Unread section flags and return-head capture

`command-prep` passed all seven cases. Five-bit unread sections also passed all
seven cases but timed at -0.735 ns. Per-section full/zero flags then shortened
prefix comparisons and improved setup to -0.473 ns (`count-prefix`). The
following return-head variant captures position throughout W_STOP instead of
gating address data with transport-ready. It times at -0.519 ns, with hold
+0.178 ns and minimum pulse +1.513 ns (`return-head`, current sources).
Functional runs are `count-prefix-tests.log` and `return-head-tests.log`;
inspect their terminal completion before claiming passes. No timing pass or
hardware deployment yet; count-prefix is the best measured result so far.

## Read-event and origin stages (current)

Read ordinal events are registered one cycle before counting. Bank-first
metadata and publication are delayed together so even a one-word tail is
published with its correct ordinal. Payload and unread accounting retain
immediate timing. The added 201-word/two-word-bank case exercises adjacent
bank boundaries and a one-word final bank. `read-event-tests.log` and
`origin-stage-tests.log` both passed all eight cases.

Origin position/bias are now latched on arm and added in ORIGIN_CALC before
writing. This achieved -0.118 ns setup, +0.178 ns hold and +1.513 ns minimum
pulse (`origin-stage/`). Removing the redundant unread arm clear worsened
timing (-0.649 ns) and was set aside. Current RTL is restored to origin-stage.
The probe now accepts an explicit fitter seed without changing clock limits;
seed 2 failed at -0.379 ns (`origin-seed2/`). Seed 1 remains the best measured
result. There is still no 250 MHz timing pass or new deployed image.

## Consecutive captures and bounded placement search

The testbench now supports multiple captures without reset. It checks global
counter payloads against per-capture ordinals and captures the actual physical
start address for each epoch (read flushes can move it). Two 201-word captures
with two-word host banks passed, including both one-word tails. The full
nine-case `consecutive-tests.log` passed on origin-stage RTL. The origin-clear
variant also passed nine cases but failed timing at -0.602 ns and was set aside.

Current sources remain origin-stage. A bounded seed search 4..8 produced setup
slacks -0.275, -0.439, -0.649, -0.442 and -0.627 ns respectively; each report is
archived in origin-seedN. Retiming ON at seed 1 produced -0.558 ns, also a fail.
The probe defaults remain seed 1 / retiming OFF, whose best measurement is
-0.118 ns. No clock constraint was relaxed and no image was deployed.
HOST-INTEGRATION.md records the next RAM/CDC/ABI work, explicitly unimplemented.

## Latest controller experiments and next integration work

The registered epoch-clear variant and the read-accounting rewrite passed
all nine regression cases, but timed at -0.317 and -0.761 ns respectively.
They were set aside. Physical register duplication was accepted by Quartus
but left the best origin-stage timing unchanged at -0.118 ns. A registered
bank-free arming check timed at -0.362 ns; its run is `banks-free-tests.log`.
Current RTL is again exactly `origin-stage/timeslice_controller.v`, with the
word-prefix ordinal source archived alongside it. The best timing remains a
FAIL at -0.118 ns. Probe options allow controlled seed, retiming and duplication
experiments; default is seed 1, retiming OFF, duplication OFF.

Preserve the unresolved controller timing gate while implementing the host
RAM/CDC path described in HOST-INTEGRATION.md. A component probe does not
qualify full integration, and neither a simulation pass nor a nearly passing
probe permits treating a complete FPGA image as qualified.
