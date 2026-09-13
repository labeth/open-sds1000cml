# Default acquisition upgrade

Target: two 500 MS/s channels, full external SRAM record, qualified digital reduction at longer timebases, and ARM analysis/expandable protocol triggering. This work replaces the default acquisition path only after device qualification. Existing `acq_sram` revision 7 is the reference, not an interchangeable default ABI.

## ADC clock and resolution policy

Keep every ADC encode clock at 100 MHz at every timebase. The five-phase interleave yields 500 MS/s per channel; selecting a slower stored sample rate changes digital filtering/reduction, never the ADC encode frequency. Combine adjacent samples from the same capture before downsampling, rather than requiring a repeating signal or averaging successive triggers.

For an ideal average of N independent noisy samples, the resolution improvement is 0.5*log2(N) bits: 4, 16 and 64 samples correspond to 1, 2 and 3 bits. This is a noise model, not a guaranteed ENOB increase. The implemented CIC/FIR chain has weighted, overlapping impulse responses, so its actual noise bandwidth and sample correlations must be used when qualifying improvement. Keep Q8.8 storage through conditioning and measurement; sixteen storage bits do not imply a sixteen-bit ADC.

The current image supports raw 500 MS/s and precision reductions /16 through /1,048,576. Thus /16 stores 31.25 MS/s and /64 stores 7.8125 MS/s per channel. /4 is an illustrative averaging ratio, not an available hardware mode. The planner selects the smallest supported reduction that fits ten divisions into the physical record. At /16 the conditioned flat passband ends at 2.34375 MHz; at /64 it ends at 585.9375 kHz. Short pulses and higher-frequency content require raw/peak capture instead of this precision bandwidth tradeoff.

## Decisions and acceptance gates

1. Measure the supplied 1 MHz triangle before deriving per-core offset/gain/timing corrections. Separate fitting and validation records. Do not infer analog ENOB from an ideal averaging formula or report triangle harmonics as noise.
2. Raw mode retains all 2 MiB: 1,048,576 samples/channel at 500 MS/s. Reduced-rate high-resolution samples must retain fractional bits. Signal bandwidth, filter delay, effective sample interval, clipping and gaps accompany each record.
3. Improve the frozen-record read path with a sequential 16-bit GPMC pop port and EDMA; verify complete known-pattern records, burst boundaries, wrap and repeat reads before enabling it in the app. DMA failure must not silently restart from a partly advanced pop pointer.
4. At fast timebases the SRAM port is fully occupied by writes: freeze/read/rearm has dead time. At slow timebases streaming is enabled only below a measured sustainable ARM transfer rate with margin and explicit overrun detection. No synthetic continuity across a gap.
5. Filtering must precede downsampling. Preserve a peak/min-max path for short events; precision mode trades bandwidth for lower noise. Protocol matching consumes data with a known sample rate and continuity, not an interpolated display trace.
6. ARM owns extensible UART byte/string, I2C address/data and SPI data matchers initially; add other existing decoders through the same interface. Hardware edge triggers preserve pre/post history. Distinguish a hardware event from software qualification of a captured window.
7. Integrate into the normal app's controls, measurement/export path and default loader. Retain one owner of the inherited GPMC descriptor. No internal scope persistent writes during validation.

Useful primary references: [Analog Devices, ADC input noise and averaging](https://www.analog.com/en/resources/analog-dialogue/articles/adc-input-noise.html), [Intel AN455, CIC compensation](https://cdrdv2-public.intel.com/653906/an455.pdf). Independent white-noise averaging ideally reduces standard deviation by sqrt(N); correlated noise, calibration residuals, nonlinearity and lost bandwidth limit the useful improvement.

Hardware validation artifacts are under `validation/2026-09-13-default`; the removed RE document tree is not restored.

## Qualified development checkpoint (2026-09-13)

Revision 8 adds a 4096-word read buffer and sequential halfword pop at read selector 25 (write 17 resets its index; read 26 is capacity, read 27 is the halfword index). Cache-coherent EDMA reads a complete 2 MiB known counter in about 0.94–0.96 s. Repeated reads, burst boundaries and final-word windows match exactly.

Revision 9 (`--interleave --precision --seed=4`) adds CIC3 reduction by powers of two from 16 through 1,048,576. Four further configurable stages follow the first /16 stage; the larger six-stage prototype was removed because the extra range was unnecessary for the 50 s/div maximum. Write 18 selects log2 reduction (0 = raw); reads 28/29 report reduction and storage width. Raw records contain 1,048,576 8-bit samples/channel at 500 MS/s. Precision records contain 524,288 unsigned Q8.8 samples/channel. Invalid reductions reject arming.

The qualified revision-9 candidate has 6,196 logic elements, 135,680 memory bits, 80 registered ADC inputs and +0.080 ns worst internal timing slack at 250 MHz. External SRAM timing is board-qualified, not statically closed by this audit. Device evidence is `validation/2026-09-13-default/precision-214557`: complete counter recall passes at reductions 1,16,256,4096; a 4096-word capture also passes /65536; returning to raw mode passes. All four CH1/CH2 rising/falling triangle captures put the threshold crossing at the recorded trigger index.

**Sparse write origin:** the preceding candidate returned all counter values but rotated a full reduced-rate record by one word. Short 256-word captures at /16,/32,/256,/4096 independently located word zero at the next physical address, while both initial and final raw captures used the original address. Revision 9 therefore latches origin as `position+3` for precision versus `position+2` for continuous raw writes. This is an empirical board addressing rule; the exact MAX V propagation mechanism has not been established. The simple SRAM simulation does not model that propagation. Do not extrapolate it to arbitrary gap patterns. Preserve the failed candidate/evidence rather than calling its timing pass a hardware qualification.

The experimental normal-app integration is `SCOPE_CAPTURE=default-sram`, requiring an already loaded revision 9 or 10. It has full-depth edge/normal/single capture, software window qualification through an extensible protocol registry, and an unsigned Q8.8 binary transport. Existing legacy fabric diagnostics are rejected in this mode. The default release loader has **not** been switched.

The ARM conditioner is a 63-tap symmetric integer FIR with CIC3 passband compensation. It retains the full sample count. The first and last 31 samples remain CIC-only because samples beyond the record are unavailable; conditioned measurements exclude these guards. The flat passband ends at 0.075 times the stored sample rate. Quantized response checks for every supported reduction are in `specs/precision-filter-response.json`; passband error is below 0.005 dB and the FIR stopband above 0.25 Fs is below -90 dB. These are digital filter response bounds, not analog accuracy or total alias-rejection claims. Capture metadata carries bandwidth, filter and guard length. Browser and LCD measurements retain fractions; the binary browser feed uses float-valued ADC codes decoded from Q8.8 words.

The ARMv7 app running from `/dev` reads and conditions a full precision record in about 2.4 s (initial ARMv5 test: 5.1 s). DMA alone is about 0.95 s. Both inputs measure approximately 1.000039 MHz. Source amplitude and absolute voltage accuracy have not been independently calibrated.

The subsequent exact ARM multiply-accumulate implementation reduces the isolated 524,288-sample, single-channel FIR from 618 ms to 250 ms. Coefficients, 64-bit sums, rounding, clipping and boundary behavior are unchanged. Device tests include a direct 63-tap convolution oracle on full-range pseudorandom input. In the normal app, full-depth recall plus conditioning now takes about 1.70 s with zero bus errors in the initial run. Evidence is `validation/2026-09-13-default/dsp-window`. The simpler fixed-window Go prototype was slower (663–666 ms) and was superseded by the assembly dot product. This remains a frozen-record pipeline, not qualified continuous streaming.

Relative five-slot gain/offset/skew fitting used one triangle record, with separate validation records. CH1 straight-segment residual falls from about 5.1 codes to 0.62 code; CH2 from about 1.58 to 0.64. The same coefficients validate at the wider 2 V/div range at about 0.55/0.58 code RMS. They are **not yet applied automatically**: reliable record-start phase metadata is required, especially after ring wrap. This 1 MHz test does not qualify high-frequency aperture correction.

### Qualified revision 10 host-read checkpoint

Build `--interleave --precision --hostfix --seed=9` passes internal timing with +0.045 ns worst slack, 6,553 logic elements, 135,680 memory bits, 80 ADC input registers and all 160 assigned pins verified. The host read detector synchronizes the selected-read condition before detecting its end; it no longer loses a pop when CS releases slightly before OE. The legacy detector remains the default parameter for unrelated FPGA designs. Simulation swept 525 clock-phase/CS-OE-skew combinations (525 correct reads versus 425 on the old detector), then checked all 8192 halfwords of an actual GPMC buffer read with skewed releases.

Timing closure also required predecoding command flags, registering source selection with its validity before trigger comparisons, registering the precision mailbox enable, and splitting the precision integrator into low/high arithmetic stages. The high stage carries its operand and low carry together across stalls. The exact CIC response, modulo overflow and channel order pass independent convolution and full-width stalled/rearm tests. These changes add pipeline latency, not a change to ADC clocks or stored sample intervals.

Device evidence in `validation/2026-09-13-default/host-read-223828` covers complete counter recalls at raw, /16 and /256, repeated raw operation, chunk boundaries and the final SRAM word. Read timings 16/13, 12/10 and 10/8 with gap 5 pass; the fastest tested point moves the DMA payload at approximately 10.9 MB/s. Full recall still takes about 0.78 s because it retains the warm-up-and-wrap strategy. Both channels' rising/falling triangle triggers align with the recorded trigger word in raw and /16 precision modes.

Before starting the normal engine on revision 10, the app applies 10/8/gap-5 timing only with coherent EDMA and checks every word of a full counter capture. A failure restores and verifies the previous timing; failure there prevents acquisition startup. This writes no timing file. The deployed experimental app passed that startup check and serves full Q8.8 frames with hardware triggers; measured read-plus-conditioning is approximately 1.53 s. This is not a continuous-stream qualification or a claim that the fastest safe bus timing has been exhaustively found.

## Still required before promotion

### Streaming throughput budget

Two Q8.8 channels cost four bytes per sample pair, including all eight fractional bits. Measured frozen-record recall moves 2,097,152 bytes in about 0.944 s: 2.22 MB/s, or 555,000 pairs/s. This is a measured readout rate, not a proven simultaneous capture/transfer rate. The present ARMv7 read-plus-condition path takes about 2.42 s per 524,288 pairs, approximately 216,000 pairs/s before allowing margin for other work.

Updated processing checkpoint: the exact ARM FIR improves the combined app path to about 1.70 s, approximately 309,000 pairs/s. The preceding paragraph records the initial measurement. Neither rate is the GPMC ceiling: the older owned-fpga implementation documented about 11.6 MB/s for coherent EDMA. The current SRAM profiler measures approximately 5.83 MB/s inside EDMA calls; repeated counter wraps and command waits reduce complete recall to 2.22 MB/s. Faster timing attempts on revision 9 failed pointer checks and were restored; see `recall-profile/README.md`.

| Reduction | Stored rate per channel | Both channels, Q8.8 payload |
| --- | ---: | ---: |
| /16 | 31.25 MS/s | 125 MB/s |
| /1024 | 488.281 kS/s | 1.953 MB/s |
| /2048 | 244.141 kS/s | 0.977 MB/s |
| /4096 | 122.070 kS/s | 0.488 MB/s |
| /8192 | 61.035 kS/s | 0.244 MB/s |

/1024 barely fits the measured transfer-only budget and exceeds current processing throughput. /4096 is a plausible subsequent target; begin continuity qualification at /8192 with margin for display, garbage collection and protocol processing. No continuous rate has yet been qualified, and these numbers do not establish network/export throughput. A direct FIFO may change the transfer budget and must be measured independently.

`fpga/acq_sram/stream.v` is saved as an unconnected, untested draft. `build.py --stream` rejects the request explicitly until the top-level ABI, host reader and overflow/continuity tests are implemented. The qualified build remains `--interleave --precision --seed=4`.

- Add explicit raw core-phase metadata, qualify it across trigger wrap, and apply the per-unit interleave profile without fitting away real waveform content.
- Add a separately qualified reduced-rate FIFO path to ARM with counters/overflow detection; current capture is freeze/read/rearm with measured gaps. Do not label it continuous streaming.
- Carry digital bandwidth/guard information through every export/analysis consumer and check the normal app on device across timebases and trigger modes.
- Preserve useful peak/min-max information, and integrate averaging, mask/Bode/other existing acquisition policies with bounded ARM memory. The experimental SRAM loop does not yet implement all these policies.
- Qualify bus-character triggering end to end, then promote a matched FPGA/app pair into the normal default loader and release build. Physical panel scanning remains absent in the inherited default design.
