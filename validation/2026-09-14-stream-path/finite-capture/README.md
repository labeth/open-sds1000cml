# Finite trigger capture through the shared SRAM backend

`finite_writer.v` connects `record.v` to the external-writer interface of
`board_capture_path.v`. The source is an opaque 32-bit word: either two raw
8-bit dual-channel pairs or one dual-channel Q8.8 pair. Pre/post counts and
trigger index are in words; post count includes the triggering word. Raw
subword trigger phase still belongs to the frontend and its eventual ABI.

The caller must arbitrate start requests: `capture_allowed` excludes backend
activity, held host banks, backend faults and a simultaneous backend start.
The integration bench derives it from backend start readiness and `!start`.
Accepted counts are latched; invalid/busy starts report an error and preserve
the existing record. `frozen` becomes true only after physical write drain.

The writer reserves the shared transport, writes 16 priming words, drains them,
and starts a new continuous write transaction at the retained counter position.
This establishes an explicit physical origin without counting pending writes.
The priming clocks are outside the captured record. This differs from the old
board top's undrained priming and +2/+3 origin convention; it needs hardware
qualification, including the reader's delay and bias, before deployment.

`source_enable` marks the capture interval. Each valid word in that interval
must find `source_ready`; loss of transport readiness invalidates the epoch.
A trigger must accompany its source word. Halt excludes the word on that edge;
a halt during startup is remembered and produces an empty frozen record.
A fault requires coordinated reset of writer, backend and transport. `source_fault` accepts the synchronous ADC/precision fault. It rejects a new
start while asserted, suppresses writes on the fault edge, drains an active
transport, and invalidates even a frozen record when a late error arrives.
The board must connect that input and propagate the failed epoch to the host.

Run `python3 validation/2026-09-14-stream-path/test_finite_capture.py` for the
8192-word integration geometry, switching between trigger capture, frozen recall
and streaming without resetting the SRAM counter. `--full` uses the physical
524288-word geometry and verifies an entire triggered record. Both also run a
small mock-transport test for startup/idle halt, lost readiness, rejected rearm
and reset recovery. Runners freeze and hash the RTL and benches before compiling.
The real-transport bench compares every returned halfword with an all-bit pattern.

These are simulation checks, with ideal SRAM timing and an ideal host consumer.
They do not establish FPGA timing closure, sustained GPMC bandwidth or ADC order.
The actual default `top.v` and ARM ABI are not yet migrated to these modules.

Current recorded results: `tests.txt` passes all eight mixed operations and the
fault suite. `full-tests.txt` passes the entire 2 MiB capture/recall, with trigger
index 524271 and 17 post words, through 205 banks. Full-run simulation elapsed
16.9326881 ms including the preceding read, capture, seek, host stalls and recall;
this is not an instrument transfer-rate measurement.

`adc_acquisition_source` extracts the ADC/precision selection from the old top.
It has no downstream-ready input: `adc_interleave.consume` stays asserted for
its run interval, so SRAM reads cannot pause the ADC timeline. Decimation and
encode mask latch at enable rise. The output is registered with its valid bit;
ADC errors, selected precision errors and loss of established ADC clock lock
remain sticky until enable goes low. No output is valid before ADC lock.
The source-control test uses explicit ADC/CIC mocks, checks raw and all legal
precision settings, rejects illegal settings, and checks fault/word alignment.
It does not exercise analog pin ordering or CIC arithmetic. The real frontend
and precision RTL separately elaborate with vendor primitives left unresolved;
that is an interface check only. This source is not wired into `top.v` yet.

For finite capture, the board should keep the frontend epoch enabled through
priming and final writer drain, discarding startup words outside the writer's
source interval. This allows late frontend errors to invalidate the capture.
Both paths still need the actual top-level connection and physical timing checks.
