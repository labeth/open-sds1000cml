# External SRAM acquisition integration probe

This is an isolated development image on the `acq2` branch, not yet the app's
`default` fabric. Do not deploy it through the normal app loader: its register
ABI is deliberately different. `app/cmd/acqsram` requires its distinct ID.

## Interleave mode (revision 7)

`python3 build.py --interleave --seed=2` builds factory-derived five-phase ADC encoding:
500 MS/s per channel, 1 GS/s aggregate. The ten byte streams are ordered as
E4.CH1, E5.CH2, E3.CH1, E1.CH2, E2.CH1, E4.CH2, E5.CH1, E3.CH2,
E1.CH1, E2.CH2. CH2 has a nominal +1 ns offset from CH1. Independent
encode-stop/DC tests confirmed all ten clock/core connections; AC aperture
skew and per-core gain/offset are not calibrated. See
the acq2 analysis branch.

The 80-bit/100 MHz input uses a shallow FIFO and 64-bit/125 MHz packing stage,
then writes 32 bits at 250 MHz. Full depth is 2.097152 ms per channel.
The FIFO and 2 KiB readout buffer total 18,944 logical memory bits.
The trigger checks both selected-channel samples in each SRAM word, including
the boundary from the preceding word; its timestamp remains word-granular.
Pair selection must be zero in this mode. The backend rejects faulty records.

Revision 7 keeps SRAM write clocks continuous from dummy priming through the
first record word. The host reads a discarded 16-word prefix before each
496-word payload chunk to avoid the observed burst-start read defect. Prefix
reads wrap physically without changing the record or reducing capacity.
Capture may start at any CH1/CH2 pair within the ten-byte frame; chronological
order remains intact, but per-core calibration must account for this rotation.

Additional interleave registers: read 15 is per-channel MS/s (500); read 19
bit0 is sticky FIFO fault and bit1 is ADC PLL lock. Read 1 bit11 marks a pending
atomic snapshot. Write 15 is the ten-bit encode-enable diagnostic mask (idle
only; bit `2*p` is the positive leg, bit `2*p+1` the negative leg).

`--interleave --probe` is a slower memory diagnostic for idle snapshots and
clock-stop experiments only. It cannot sustain the complete ADC stream.
**Do not load a timing-rejected 250 MHz build.** Run
`tools/hw/adc_sram/interleave_qualify.py` only after its audit passes and after
building `/tmp/acqsram-il.arm`; the runner cold-boots and uses volatile storage.

## Single-pair data path (revision 5)

The measured 100 MHz M2 reference supplies a 100 MHz capture/SRAM clock and a
+4 ns SRAM input sampling clock. All five ADC encode pairs currently run at
100 MHz **with the same phase**. `adc_unpack.v` maps the 80 raw pins to ten
8-bit unsigned cores using the existing independent DAC-sweep calibration.
It does not establish interleaved sample times.

Select one of five dual-channel ADC pairs. Two consecutive samples from that
pair form a 32-bit SRAM word, oldest sample first:

| byte (least significant first) | data |
|---|---|
| 0 | first sample CH1 |
| 1 | first sample CH2 |
| 2 | second sample CH1 |
| 3 | second sample CH2 |

The alternative test source writes a monotonically increasing 32-bit count.
Both sources use exactly the same writer, external SRAM, trigger bookkeeping,
and recall path. There is no 20K-word on-chip sample record. The only sample
RAM is a 512 x 32 host readout buffer (16,384 bits).

The record can occupy all 524,288 external words: 2 MiB, or 1,048,576 samples
per channel with one selected ADC pair. Prehistory excludes the triggering
word; posthistory includes it. The first probe reports trigger position in
32-bit words. It does not yet offer sample-half interpolation or calibrated
trigger latency. Normal mode supports a word-level threshold crossing and a
software force; auto triggers after the requested prehistory exists.

## Addressing and recall

K2 advances the factory MAX V's 19-bit sequential counter. G1 enables it;
K1 selects read/write. The counter's absolute power-up value is unnecessary:
`origin` records its position in a consistent relative coordinate system.
Sixteen priming writes precede the origin and are excluded from the record.
Each accepted payload write increments the record address. A wrap naturally retains
the newest history until trigger and posthistory freeze it.

`transport.v` counts **every** K2 pulse, including the extra pulse that flushes
the SRAM read pipeline. The reader seeks forward modulo 524,288 to reach
`origin + record_start + offset`. A discard-only burst performs the seek.
A requested window is then copied into the 512-word host buffer. Revision 5
retains the prefetched next word, so adjacent windows (including ring wrap)
continue without a discard sweep. Repeated
recall changes only the address counter and buffer, leaving SRAM unchanged.
Full depth is an explicit 20-bit count, never encoded as zero.

Random windows may require a forward counter wrap. Sequential full recall
uses continuation and takes about 3.85 seconds through individual ARM ioctls,
down from 9.65 seconds in the first probe. EDMA remains a separate optimization. The earlier 250 MHz memory benchmark is not
a speed qualification of this acquisition image.

## Probe ABI (CS1 selectors, 16-bit words)

Writes:
- 1: command (1 arm, 2 read to buffer, 3 discard/seek, 4 force, 5 halt,
  6 atomic ten-core snapshot, 7 continue from the prefetched word).
- 2/3: prehistory low/high; 4/5: posthistory low/high (20-bit counts).
- 6: configuration: bit0 ADC instead of ramp; bits3:1 pair index 0..4;
  bit4 normal instead of auto; bit5 falling; bit6 trigger CH2 instead of CH1.
- 7: trigger code; 8/9: transport payload count low/high.
- 16: buffer index 0..511.

Reads:
- 0: ID `0x5a52`.
- 1: bit10 valid prefetched word, bit9 command acknowledgement toggle, bit8 rejected command, bit7 invalid
  record configuration, bit6 PLL locked, bit5 triggered, bit4 frozen/done,
  bit3 recording, bit2 transport ready.
- 2/3: record length; 4/5: record start; 6/7: trigger word index;
  8/9: transport position; 10/11: recording origin.
- 12: buffer word count; 13: revision; 14: ADC map ID (`e192`).
- 17/18: selected buffer word low/high.
- 20..24: snapshot, one `{CH2,CH1}` word for E1..E5.

Configuration is staged before commands. ACK is acceptance, not completion;
wait for transport ready (and frozen for capture) before reading stable data.
Never issue read/seek while recording. The helper checks geometry and identity.

## Build and verification

`python3 build.py [SRAM_PHASE_PS]` defaults to the qualified +4000 ps read
phase and uses the installed Quartus 21.1 under the shared build lock,
checks all assigned pins and actual 8 mA SRAM DQ drive, and rejects negative
internal timing. External SRAM and ADC board timing remains empirical.

Simulations in `sim` exercise all 80 map bits, full 512K depth, wrapped trigger
records, repeated and partial recall, stalls, read pipeline flush accounting,
and the integrated counter-source capture. Simulation is not hardware proof.

Hardware runner: `python3 tools/hw/adc_sram/run.py` from repository root, after
building `/tmp/acqsram.arm`. It cold starts, stops/suspends the vendor process,
and writes only `/dev` tmpfs plus volatile Cyclone configuration through CS3
selector 7. It leaves the probe loaded for follow-up. Restore factory operation
by mains cycle. No MAX V flash or internal permanent storage is written.

The reusable `app/internal/sramcapture` backend now owns this protocol and
the diagnostic command uses it. It validates fabric/map identity, geometry,
window bounds, and completion. Its tests cover full depth, repeated/windowed
recall, continuation/fallback, partial errors, and concurrent force/wait.

The main app supports this image with `SCOPE_CAPTURE=sram` and optional
`SCOPE_SRAM_RBF=/dev/acq-capture.rbf`. This bypasses the default engine and
serves a dedicated full-depth web viewer/API on `SCOPE_HTTP` (default :8080).
Use an inherited GPMC descriptor with the vendor process suspended, as in the
hardware runner. Do not run register helpers concurrently with the app.
Standard LCD/panel integration, stacking, EDMA and analog interleave calibration remain
separate work. See the ADC-SRAM-CAPTURE report for qualification and limits.
