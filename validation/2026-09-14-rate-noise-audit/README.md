# Rate and periodic-noise audit, 2026-09-14

## Findings

500 MS/s/channel at NORMAL 10 us/div is correct: 100 us across the screen contains 50,000 real 2 ns samples. Zoom does not imply one sample per display column. The actual metadata rate was verified on hardware.

The conspicuous periodic error was an interleave-calibration bypass. The saved user state was CH1 1 V/div, CH2 2 V/div, offsets +0.56 V, NORMAL 10 us/div, stopped. The former calibration required exactly [2,2] V/div and therefore bypassed both channels. Original full SRAM data contained five-phase DC spreads of 11.09 codes CH1 and 4.12 codes CH2.

Fixed: calibration files may explicitly list additional measured ranges per channel. The same fixed coefficients match new captures at 1, 5 and 10 V/div (joint phase-fingerprint mean-square errors 0.0114, 0.0050 and 0.0159 code² respectively, below the existing 0.25 threshold). Range 2 V/div was already measured. Arbitrary ranges are not enabled. Existing clipping, length, phase-match and ambiguity checks remain. No offsets are estimated/subtracted dynamically from the current user's waveform. The GUI visibly indicates a calibration bypass.

Hardware confirmed active correction at 1, 2, 5 and 10 V/div. In the original mixed-range configuration, the 100 MHz spur relative to the 1 MHz fundamental fell from -19.22 to -52.86 dBc on CH1 (33.64 dB improvement), and -26.81 to -46.79 dBc on CH2 (19.99 dB). Five-phase spreads fell to 0.347/0.339 ADC codes. These are separate acquisitions, so source variation and noise affect exact figures. Offset calibration does not correct ADC gain mismatch or aperture skew.

## Bit and time ordering

The lane map in the loaded revision-11 build is byte-for-byte identical to fpga/default/lanemap_seed.vh. Its provenance records independent offset-DAC sweeps and exhaustive bit-weight permutation scoring; it includes the earlier E2 CH2 low-bit swap correction.

On the saved user's triangle record, all 28 possible single bit-pair swaps were tested independently in each of the ten ADC lanes. Every candidate worsened the periodic-model residual; even the best swaps increased individual-lane RMS from 0.41–0.52 to 0.70–0.96 ADC codes. Whole-channel swap tests agree. Estimated relative lane timing errors using the 1 MHz fundamental were within ±0.26 ns, not the 2 ns steps expected from misplaced interleaved samples. This supports the existing order but is not an exhaustive new 8! permutation or full-frequency aperture calibration. No FPGA wiring/order changes were made.

## Rates and settings tested

Unit tests cover every GUI timebase (1 ns/div through 50 s/div) with NORMAL planning and every one of the 17 independent PRECISION rates. Hardware checks cover key boundaries:

| Mode | Time/div | Measured samples/s/channel | Retained samples/channel |
|---|---:|---:|---:|
| NORMAL | 1 ns, 500 ns, 10 us, 200 us | 500 M | 1,048,576 |
| NORMAL | 500 us | 31.25 M | 524,288 |
| NORMAL | 2 ms | 15.625 M | 524,288 |
| PRECISION | 500 ns and 10 us | selected 31.25 M | 524,288 |
| PRECISION | 500 ns and 10 us | selected 15.625 M | 524,288 |

The long timebases and all rate combinations were planner-tested, not individually captured on hardware; this is not an exhaustive test of every trigger/protocol/control combination. With the current approximately 6 Vpp source, lower voltage ranges can clip, so they cannot be responsibly calibrated from this input. NORMAL raw-rate behavior is unchanged; PRECISION remains independent of time/div.

Engine, DSP, app and focused control/binary web tests passed. Real-browser verification checks 500 MS/s at the restored 10 us/div, active correction and no script errors. Bus errors remained zero. An initial hardware-test assertion read a stale pre-change frame; the test was corrected to wait for fresh matching metadata, and the complete hardware run passed.

## Deployment and artifacts

RAM app /dev/acq-rate-app SHA256 6ff44e83d096642f42eb8e1c509e3d64ff2d51f2951921e0263e165766763782.
RAM profile /dev/acq-interleave-wide.json SHA256 9a97e88786db8de84d8e3e17d18ac52315c3dd6af14715a9bc00f77b4663a519.
FPGA unchanged. No scope internal permanent storage written.

Original NORMAL 10 us/div, CH1 1 V/div, CH2 2 V/div, +0.56 V offsets, AUTO trigger, CH2 rising, original trigger code and STOP state restored. Hidden precision rate restored to 31.25 MS/s. The stopped full record now has calibration applied.

original.bin/corrected.bin preserve the before/after records; comparison.json contains spur/phase statistics. order.py/order.json document bit-swap and lane-phase checks. ranges.py/ranges.json contain range measurements. hardware.py/hardware-rates.json contain rate checks. browser.mjs/web.png verify the real GUI. Header format matches the preceding precision validation.

## Stopped-view correction

Visual inspection also found that homeWindow intentionally expanded stopped records to the entire SRAM duration (2.097 ms) while the timebase selector remained 10 us/div. Removed that automatic expansion: opening or homing a stopped record now retains the 100 us screen span, matching the selected timebase. The full record remains accessible with navigation/zoom. Pure geometry checks and the hardware browser test verify the stopped span equals 100 us. This fixes an additional dense-band display symptom; it does not change samples or their rate.
