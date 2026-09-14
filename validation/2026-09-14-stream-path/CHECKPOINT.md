# Integrated acquisition core — not a deployable image

Current `acquisition_path.v` connects the ADC/precision source, finite trigger
writer, continuous SRAM engine and frozen recall to one physical transport and
one 5120-word host buffer. It arbitrates capture/stream/recall starts, latches
configuration, invalidates stale finite records on stream start, handles raw
first/second-sample edges and full Q8.8 thresholds, and propagates ADC faults to
both acquisition modes. It exposes an external aligned trigger-match hook.
The real board PLL/pin top and GPMC/kernel/app ABI are still separate.

Current simulation evidence:
- `integrated-acquisition/tests.txt`: real downstream RTL with ADC/CIC mocks;
  3017-word raw capture/recall (3000-word prehistory, second-sample trigger),
  48-word precision capture/recall, 6001-word stream. Every host halfword is
  checked against independently collected source words. Busy start/config
  changes, held final banks, stale recall and streaming ADC fault pass.
- `integrated-acquisition/stream-tests.txt`: updated standalone stream wrapper
  passes 10003 words with physical SRAM wrap and actual host-buffer readback.
- `integrated-acquisition/start-tests.txt`: accepted-start settings and epoch
  reset pass after the new source-fault input was propagated through wrappers.
- Earlier `finite-capture/full-tests.txt` proves the previous checkpoint's
  complete 524288-word / 2 MiB trigger capture and 205-bank recall. Its source
  manifest predates the new streaming fault input and integrated wrapper.

The first combined real-ADC/CIC build FAILED TO FIT:
- `integrated-acquisition/result.json` and exact source/report snapshots.
- Synthesis: 12782 logic elements, 9285 registers, 315904 memory bits.
- Fitter: 750 LABs required; EP4CE10 has 645 LABs. No STA or CDC audit ran.
- Precision hierarchy: 4172 combinational ALUTs, 3430 logic registers. These
  hierarchy counts are not interchangeable with the whole-design LE count.
- GPMC register logic is not included, so more headroom is still required.
- Prior shared-backend-only timing/CDC reports do not qualify this integrated
  core. No bitstream was assembled or deployed.

Next architectural change: share the arithmetic of the slow precision stages.
Keep the first /16 CIC and the first configurable stage at their required rate;
after /256, only 1.953125 million dual-channel words/s reach the remaining
stages, leaving about 51 clocks/word at 100 MHz. A scheduled state-memory
implementation can replace six parallel channel/stage datapaths while retaining
the existing CIC arithmetic, fractional precision and startup-discard behavior.
It must be compared word-for-word against the current parallel filter across
all decimation settings, bursts/stalls and epoch resets before integration.

Remaining full-goal work includes fitting/closing timing with physical ADC/IO
constraints, actual default top and 5120-word GPMC ABI integration, kernel/app
streaming and long retained history, additional decoder triggers, and hardware
qualification of ADC order, phase/read bias, continuation, capture depth and
sustained transfer under ARM stalls. Internal scope storage remains untouched.
