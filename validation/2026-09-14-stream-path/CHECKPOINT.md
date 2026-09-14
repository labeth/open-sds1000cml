# Integrated acquisition core — fits, timing still fails

Latest evidence: `idle-settings/balanced-build` (source hashes, snapshots,
Quartus fit/STA and CDC audit). The real ADC/CIC acquisition core fits with
9328/10320 LEs, 7697 registers, 44/46 M9Ks, and 643/645 LABs used. GPMC board
top is not included; nominal spare LEs do not establish integration headroom.

All 135 existing backend, precision-config and new encode-control CDC rows
pass. Setup remains -3.969 ns; recovery -4.297 ns. Hold +0.136 ns, removal
+0.367 ns, minimum pulse +1.513 ns. Worst setup is backend control/fault
through launch logic into finite writer state.PRIME_DRAIN. Other ADC CDC/reset paths
and physical IO are unqualified. No image generated or deployed.

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
