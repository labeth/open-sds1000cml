# Shared SRAM acquisition backend — not a deployable image

Current RTL connects continuous acquisition and frozen-record recall to one
5120-word host buffer and one SRAM transport. `capture_engine.v` exposes the
transport interface for board integration; `capture_path.v` includes it for
simulation and placement. Mode selection is held until transfers and host bank
ownership finish. Accepted settings pass through a launch stage, and finite
range validation uses separate arithmetic/comparison stages.

The continuous path retains both Q8.8 channels in each 32-bit word and targets
/256 and slower. The finite reader preserves opaque raw or precision words,
including the complete 524288-word / 2 MiB SRAM range. No new bitstream has been
deployed. The existing default image/app is not yet connected to this backend.

Current evidence:
- `shared-capture/tests.txt`: engine and complete wrapper each pass five
  operations without reset: 5121-word finite read, 10003-word streaming capture
  with SRAM wrap, recall of its last 8192 words, 257-word second stream, and
  recall of its last word. Every host halfword is checked. Each run rejects ten
  busy starts and incorrect release tokens, including a held final bank.
- `shared-capture/start-tests.txt`: accepted address/length settings survive
  immediate input changes; a pending second start is rejected; reset and later
  stream launch pass.
- `finite-recall/full-passed.txt`: all 524288 words recalled through 205 actual
  host-buffer banks with an ideal SRAM model; frozen contents remain unchanged.
- `finite-recall/passed.txt`: continuation and fresh-seek modes, offsets, empty
  and invalid ranges, odd tails, repeated requests, host stalls and readback.
  Fault tests cover host errors, lost freeze, short/extra read responses,
  epoch recovery and waiting for the final host release.
- `shared-capture/result.json`: 6109/10320 logic elements, 4813 registers and
  38/46 M9Ks. All 120 CDC audit rows pass across three corners. Setup FAILS at
  -1.096 ns; hold +0.145, recovery +0.559, removal +0.299, minimum pulse +1.513.
  IO is unconstrained. ADC/precision and GPMC logic are excluded from this fit.
  Worst setup path: mode selection into the stream ingress payload enable.

Earlier physical-geometry streaming component evidence (65539 words, a 10 ms
ARM pause, four skipped busy-bank scans and 16 overlaps) remains in the host-path
validation directory. It is not a hardware qualification of the new shared top.

Work still required for the full goal:
- Connect the real 100 MHz ADC encode/five-phase 2x500 MS/s frontend, precision
  processing, finite capture writer and frozen-record metadata to the shared
  transport/host path; close the complete image's timing and physical IO limits.
- Implement/version the GPMC ABI and kernel/app handling for 5120 host words,
  continuous transfer, retained precision, timebase policy and ARM processing.
- Integrate pre/post-trigger record geometry and extensible edge/UART/I2C/SPI
  triggering with the shared acquisition modes.
- Qualify ADC ordering, phase/read bias, continuation, complete capture depth,
  sustained transfer and behavior under host stalls on the device. Simulation
  readback and unconstrained placement do not prove these electrical properties.

The original packetizer bench remains sim/tb_stream_path.v; the streaming
wrapper bench is sim/tb_sram_stream_path.v. Historical experiments retain their
own sources/results and must not be mistaken for current qualification.
