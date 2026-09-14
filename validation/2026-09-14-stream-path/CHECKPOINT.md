# Integrated acquisition core — fits, timing still fails

Latest writer-reservation checkpoint: `writer-reservation/build` uses 9182 LEs,
7724 registers, 44 M9Ks and 644/645 LABs. A dedicated reservation register
matches the original pending/state active definition every simulation cycle.
Capture, fault and composed integration tests pass. Setup -2.816 ns, recovery
-5.065 ns still fail; existing 135-row CDC audit passes. This saves 36 LEs
but costs two LABs relative to retained-events, and is not a timing win. Worst
setup is host fault aggregation into finite-writer frozen. Not hardware qualified.


Latest retained-events checkpoint: `retained-events/build` uses 9218 LEs,
7723 registers, 44 M9Ks and 642/645 LABs. Transport event gates use the
retained owner; public mode and source faults retain immediate selection.
Both five-operation backend configurations, composed acquisition and mode
oracle pass. Setup -2.773 ns and recovery -5.352 ns still fail; existing
135-row CDC audit passes. Worst setup is finite-writer IDLE through start
arbitration into backend launch registers. Physical qualification is incomplete.


Latest transport checkpoint: `transport-latched-validation/build` uses 9212 LEs,
7723 registers, 44 M9Ks and 643/645 LABs. Validation uses the command already
latched before VALIDATE, preserving latency and full count checks. 400000
cycle oracle comparisons and composed acquisition pass. Setup -3.452 ns and
recovery -5.689 ns still fail; the existing 135-row CDC audit passes. Worst
setup is backend launch selection into finite-recall state.DRAIN. This
supersedes the resource/timing figures below; physical qualification is pending.


Latest host-RAM checkpoint: `host-narrow-read/explicit-build` uses 9222 LEs,
7724 registers, 44 M9Ks and 643/645 LABs. Explicit mixed-width RAM saves
118 LEs without reducing capacity. Standalone portable/vendor RAM tests and
composed portable/vendor-host-RAM acquisition tests pass. Setup -4.720 ns,
recovery -6.220 ns; CDC audit fails. Worst setup crosses backend mode selection
into transport legal_command. No physical top/GPMC integration or deployment
is implied. This supersedes the resource/timing figures below for current RTL.


Latest frontend checkpoint: `pack-control/mailbox-fixed-build` uses 9340 LEs,
7726 registers, 44 M9Ks and 643/645 LABs. Setup -3.531 ns, recovery -5.398 ns;
CDC audit fails. Local pack controls use immediate disable and synchronous
release. Mailbox validity clears on disable, fixing stale data on short rearm.
Modeled chronology passes initial capture and all eight pack-clock disable
phases. This is not physical timing qualification. `event-broadcast` passed
functional tests but regressed timing/fit and was reverted.

Previous control checkpoint:  `mode-launch/balanced-build`: 9338 LEs, 7721 registers,
44 M9Ks, 640/645 LABs. Setup -3.281 ns, recovery -6.769 ns; CDC audit fails.
Integration and 10000-cycle mode comparison pass. Readiness is registered with
immediate invalidation, recall uses existing sampled record length, and backend
mode selection uses registered launch pulses. Worst setup now crosses from
acquisition control into frontend overflow_s[0] in the pack-clock domain.
This is not timing-qualified; frontend CDC/reset, physical top/GPMC integration
and full hardware qualification remain required.


Previous passing CDC evidence: `idle-settings/balanced-build` (source hashes, snapshots,
Quartus fit/STA and CDC audit). The real ADC/CIC acquisition core fits with
9328/10320 LEs, 7697 registers, 44/46 M9Ks, and 643/645 LABs used. GPMC board
top is not included; nominal spare LEs do not establish integration headroom.

All 135 existing backend, precision-config and new encode-control CDC rows
pass. Setup remains -3.969 ns; recovery -4.297 ns. Hold +0.136 ns, removal
+0.367 ns, minimum pulse +1.513 ns. Worst setup is backend control/fault
through launch logic into finite writer state.PRIME_DRAIN. Other ADC CDC/reset paths
and physical IO are unqualified. No image generated or deployed.

## Latest launch-pipeline checkpoint

`writer-launch/balanced-build` fits at 9333 LEs, 7698 registers, 44 M9Ks and
642/645 LABs. Setup -3.284 ns; recovery -6.091 ns. Two of 135 CDC rows fail;
this supersedes the previous build as current RTL evidence, not as qualification.
The finite writer reserves immediately and launches one cycle later, preserves
accepted geometry and coincident halt, and cancels pending launch on reset.
Capture, integration and added pending-launch fault simulations pass.
The encode diagnostic confirms 1.493 ns data delay but -0.307 ns setup slack
with clock skew. A net-only replacement query matched no nets and was rejected.
Constraints remain unchanged. See `writer-launch/README.md` for evidence scope.

## Rejected control experiments

`halt-idle` and `mode-flags` preserve source-versioned failed experiments.
Idle halt tracking passed simulation but used 644 LABs with -4.029 ns setup.
Explicit retained operation flags also passed integration but worsened setup
to -4.916 ns at 644 LABs; acceptance still feeds multiple worst endpoints.
Both RTL experiments were reverted to the committed writer-launch baseline.
`validated-start` also passed integration/contract checks but regressed to
645 LABs and -3.853 ns setup. Its four RTL edits were likewise reverted; the
archived contract runner tests its frozen build sources. These three experiments have no remaining active RTL edits.
Next work must structurally shorten acceptance while preserving same-epoch
fault rejection, settings capture and immediate ownership reservation.

## Current readiness-cache candidate (uncommitted)

`ready-cache/balanced-build` fits at 9361 LEs, 7721 registers, 44 M9Ks and
645/645 LABs, but setup -3.471 ns and recovery -5.226 ns fail. CDC audit fails.
Integration and immediate lock/fault/reset/bank invalidation simulations pass.
Readiness can return one clock later after idle or a rejected request.
Worst path is now record-length/trigger state through recall acceptance into
backend selected_finite. Geometry validation/dispatch separation remains needed.
This intermediate RTL is not an improvement over the committed launch baseline
and is not a qualified default FPGA image.

## Current architecture
- `acquisition_path.v`: real ADC/precision source, finite trigger writer,
  continuous SRAM engine and frozen recall share one physical transport and
  5120-word host buffer. Mode acceptance is separate for capture/stream/recall.
- ADC encode settings cross through two local phase-clock stages, integrated
  path only. Seven-clock warm-up prevents retaining stale startup frames;
  disable asserts warm-up reset immediately. Legacy top defaults unchanged.
- Precision keeps the first CIC and first configurable stage parallel, then
  shares one arithmetic path and one 64x32 state/queue M9K for the slow tail.
  Decimation is captured locally; short disables reset shared filter history.
- Finite trigger comparisons have two stages with aligned ADC data and force/
  external match flags. Raw second-sample position and Q8.8 values are retained.
- Finite writer captures private settings while idle, holding accepted inputs
  through ARM. Rejected requests cannot alter frozen record metadata.
- Host packer commits/checks counts in its input/packing stages, retains checked
  12-bit descriptor payloads, and shares pair/tail data packet staging. Host sink
  shares explicit metadata/pair acceptance controls.

## Source-versioned evidence
- `encode-control`: all ten independent enables/phase polarities, stale startup
  reproduction and fix, modeled chronology at 1 GB/s with 4.5..6 ns converter
  propagation and ideal FIFO. This is not physical ADC qualification.
- `packer-count`: 79463 cycle comparisons of packets/faults with frozen oracle;
  integrated acquisition/recall/streaming pass. Earlier failed fits retained.
- `writer-settings`: finite capture and startup/drain/frozen/reset fault tests;
  settings changed immediately after acceptance and invalid rearm tested.
- `mode-start`: invalid capture/stream/recall settings and preservation of frozen
  data after rejected recall; integrated ownership and streaming tests.
- `trigger-pipeline`: 71520 reference words over 96 configurations; 54124 host
  sink equivalence cycles including malformed packets and faults.
- `precision-config`, `precision-tail`: short-epoch reset, exact arithmetic and
  queue tests; frozen full-tail log-12 proof predates later normalization edits.
- `finite-capture`, earlier finite-recall reports: full-depth simulations for
  their recorded source versions, not proof for the complete current board image.

## Remaining full goal
Close core control/data timing, ADC CDC/reset and IO timing. Integrate/version
GPMC registers and 5120-word host-bank ABI in the default FPGA/kernel/app.
Verify sustained FPGA-to-ARM streaming under stalls, retained fractional
precision, ARM processing/history and all-timebase behavior. Finish extensible
protocol triggers, and hardware qualification of ADC order/aperture, SRAM
read bias/continuation and full depth. No internal permanent scope writes.
The current core and simulations do not complete those requirements.

Latest descriptor-range and idle-settings follow-on: descriptor checks use
nonzero/equality with a bounded expected count; idle data settings capture
without acceptance/fault fan-in. Count invariant, 79463 packet-equivalence
cycles and integration pass. Aggressive build exceeds capacity at 646 LABs.

`idle-settings/balanced-build` FITS: 9328 LEs, 7697 registers, 44 M9Ks and
643/645 LABs. All 135 CDC checks pass. Setup -3.969 ns, recovery -4.297 ns;
worst path is backend control/fault into finite writer state.PRIME_DRAIN.
Reproduce with build_probe.py --acquisition --cdc --balanced --seed 2.
The default build optimization remains aggressive performance. This gives
resource headroom but is not a qualified 250 MHz core. No deployment.
Next: break launch/control paths into the finite writer and address ADC
reset/CDC plus remaining data/control timing before board/GPMC integration.
