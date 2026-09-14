# Integrated acquisition core — fits, timing still fails

Latest evidence: `encode-control/integrated-build` (source hashes, snapshots,
Quartus fit/STA and CDC audit). The real ADC/CIC acquisition core fits with
9505/10320 LEs, 7706 registers, 44/46 M9Ks, and all 645 LABs used. GPMC board
top is not included; nominal spare LEs do not establish integration headroom.

All 135 existing backend, precision-config and new encode-control CDC rows
pass. Setup remains -3.094 ns; recovery -5.569 ns. Hold +0.123 ns, removal
+0.485 ns, minimum pulse +1.513 ns. Worst setup is selected_finite through
host count selection into packer count_legal_q. Other ADC CDC/reset paths
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
