# Recall timing separation and exact FIR optimization

All scope changes were RAM-only. Revision 10 FPGA remained loaded throughout.
The app was stopped and confirmed exited before standalone helper benchmarks.

## Rejected polling experiment

Sixteen immediate readiness polls before the normal timed wait did not improve
full recall in paired raw,/16,/256 tests (`results.json`). Every counter hash
passed. The polling change was removed; no readiness policy change is retained.

## Separate validation CPU from transport

The earlier `profile-recall` wall time included SHA256 and counter checking on
ARM. A timed sink now reports both `validation_seconds` and `recall_seconds`.
These are diagnostic fields; validation is never disabled.

`sink-profile.json` shows three exact full records per path:
- Prior linear recall: 0.567–0.573 s transport, ~0.212 s validation.
- Two-pass recall: 0.315–0.318 s transport, ~0.226 s validation.

Thus two-pass isolated host recall is about 6.6 MB/s including addressing and
copying. Its DMA calls remain about 10.9 MB/s. These are frozen-record figures,
not a demonstrated sustainable streaming rate.

## CPU profile and FIR

The instrumented app exports `sram_recall_ms` (including frame unpacking) and
`condition_ms` separately from the existing total `drain_ms`. Its wall times
include competition with rendering/measurement on the single CPU.

The 15-second CPU profile attributes ~39% to conditioning, ~22% to full-record
measurements and ~25% to recall (inclusive stacks). Device wall clock is not set;
ignore the 1970 timestamp in the profile. Profile and decoded top table are saved.

The exact 63-tap ARM FIR now unrolls its 31 symmetric pairs. Coefficients,
accumulation order, 64-bit signed arithmetic, rounding, clipping and boundaries
are unchanged. No ADC samples, precision or capture depth are discarded.

`dsp-unroll.json` contains paired device tests/benchmarks with the app stopped:
- Original: 250.6–253.4 ms per 524288-sample channel.
- Unrolled: 214.8–215.0 ms, about 14–15% less time.
- Both variants pass direct convolution equivalence, DC fractions, Nyquist
  rejection/boundaries and centered passband tests on ARM.

The optimized RAM app was then started for integration verification. This work
does not add continuous streaming or change timebase planning.

Integration snapshot: 23 coherent records, zero bus errors, depth 524288/channel;
latest total drain 1248 ms versus 1299 ms before unrolling. The startup full
counter verification also passed (`app-unrolled-log.txt`).
