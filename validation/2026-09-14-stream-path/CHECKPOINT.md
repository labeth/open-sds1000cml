# Integrated acquisition core — fits, not timing-qualified

Current `acquisition_path.v` combines the real ADC/precision source, finite
trigger writer, continuous SRAM engine and frozen recall over one transport and
one 5120-word host buffer. The board PLL/pin top, GPMC/kernel/app ABI and protocol
trigger integration remain outside this core. No image has been deployed.

The slow precision tail now shares one 28-bit arithmetic path and one 64x32
state RAM across six channel/stage contexts. Four input queue entries occupy
unused addresses in the same RAM. The first /16 CIC and first configurable
stage remain parallel. The integrated source selects SHARED_TAIL=1; the existing
qualified board top retains its parallel default pending migration.

Earlier arithmetic evidence in `precision-tail`:
- `full-tests.txt`: remaining decimation logs 0..12 match the frozen parallel
  CIC oracle, including random full-range/fractional data, rails, gaps, startup
  discards, bursts, overload and reset. Sixteen epochs pass.
- `queue-tests.txt`: full simultaneous push/pop in shared RAM and reset during
  an update with queued input pass against the same oracle.
- `shared-tests.txt`: the complete precision composition, including the fixed
  first CIC, stage selection and ideal FIFO interfaces, matches /16 through
  /8192 word-for-word. This is not a vendor FIFO CDC test.
- `source-tests.txt`: updated source parameter selection/control test passes.
- Previous `integrated-acquisition/tests.txt` checks downstream capture/recall
  and streaming with ADC/CIC mocks. It predates the shared-tail selection.
  Earlier full-depth capture evidence remains source-versioned in finite-capture.

Earlier real-ADC/CIC build in `precision-tail/integrated-build`:
- FIT PASSES: 9500/10320 logic elements, 7655 registers, 44/46 M9Ks.
- All 645 LABs are partially/completely used; GPMC logic is not included, so
  remaining individual LEs do not guarantee enough usable integration headroom.
- All 120 existing backend CDC audit rows pass at three corners.
- Timing FAILS: setup -7.496 ns; recovery -5.028 ns. Hold +0.142 ns,
  removal +0.490 ns, minimum pulse +1.485 ns. Added ADC CDC and IO are unqualified.
- Worst setup is the held decimation setting from source|decim_l[3] (core)
  into the 100 MHz shared filter's q register. This is not proof of a maximum
  sustainable sample frequency; configuration transfer/constraints need work.
- Exact source snapshots, settings, reports and audit results accompany the build.

The fully parallel integrated build required 750 LABs. The first scheduled tail
with a register queue required 647; `precision-tail/first-build` records that
intermediate failure. Packing the queue into state RAM lets the core fit.

Latest checkpoint: `precision-config/integrated-build` includes a held local
100 MHz configuration register and asynchronous assertion / three-clock release
for shared precision enable chains. A reproduced one-core-cycle disable bug is
fixed: filter history now resets even when disable misses slower clock edges.
Four short-reset epochs and complete /16../8192 oracle comparisons pass.
Fixed normalization slices preserve valid results while keeping the core fitted.

Latest fit: 9495 LEs, 7653 registers, 44 M9Ks, 645 LABs. All 126 backend and
configuration CDC audit rows pass. Setup -6.894 ns and recovery -5.229 ns still
fail. Worst setup is edge-trigger detection, including source data paths.
No image deployed; ADC CDC/reset and physical IO remain unqualified.

Next: pipeline trigger comparisons with aligned sample data and trigger position,
review ADC reset/CDC paths, and close timing. Then integrate/version the default
GPMC interface and kernel/app streaming, retained precision and long history.
The full goal still includes protocol triggers, all-timebase behavior and hardware
qualification of ADC order, read bias/continuation, full depth and sustained
transfer under ARM stalls.

Latest follow-on: the two-stage finite trigger pipeline keeps data and trigger
decisions aligned. 71520 oracle words across 96 configurations and integrated
capture/stream/recall pass. Host sink acceptance controls are explicitly shared;
54124 equivalence cycles pass including malformed packets and faults.

`trigger-pipeline/integrated-build` FITS: 9504 LEs, 7733 registers, 44 M9Ks,
645 LABs. All 126 backend/configuration CDC rows pass. Setup improves to
-4.416 ns, but recovery is -5.553 ns. Worst setup is now record halted through
start acceptance into captured configuration. IO and added ADC CDC remain
unqualified. No GPMC top integrated, FPGA image generated or deployment done.

Next: break the long capture-start readiness/acceptance path while preserving
busy-start rejection and atomic settings. Address ADC CDC/reset and physical
IO timing, then finish default GPMC/kernel/app integration and hardware tests
for the full original goal. Earlier failed trigger builds are source-versioned
in trigger-pipeline/first-build and shared-sink-build.

Latest mode-specific start checkpoint: `mode-start/integrated-build` fits at
9522 LEs, 7738 registers, 44 M9Ks, 645 LABs. Existing 126 CDC rows pass.
Expanded invalid-start/frozen-recall integrity tests pass. Setup -3.836 ns
(ADC encode setting crossing), core setup -3.814 ns (FIFO overflow through
start readiness into finite writer configuration). Recovery -5.725 ns.
Start acceptance behavior is unchanged; further readiness/launch pipelining
and ADC clock/reset qualification remain needed. No deployment.
