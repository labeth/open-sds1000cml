# Live interleave offset calibration

Training data: calibration-frame.bin, seq 740, two channels at 2 V/div,
1 MHz triangle. Coefficients in interleave-cal.json are per-phase means minus
channel mean, preserving DC. No averaging/filtering is applied to sample data.
Revision 11 lacks a phase tag, so the joint two-channel offset fingerprint is
matched against five cyclic rotations. Calibration requires RMS residual at
most 0.5 count and next-best squared-error margin >=1; otherwise bypass.
Wrong scale, clipping, short captures or ambiguous/mismatching signals bypass.
This limits coverage; it is not a replacement for explicit hardware phase tags.

On-device corrected-frame.bin seq 6 carries Q8.8 fractional corrections,
500 MS/s, 1048576 samples/channel and a phase-checked calibration label.
100 MHz spur, Hann FFT over 262144 samples relative to the 1 MHz fundamental:
CH1 -13.01 to -54.71 dBc (41.70 dB reduction).
CH2 -27.12 to -58.45 dBc (31.33 dB reduction).
Residual per-phase means are within 0.05 ADC count. corrected-screen.png shows
both triangle traces with the previous strong ripple removed.

Calibration covers ADC offsets only, at the measured 2 V/div settings and raw
capture rates. It does not calibrate gain, aperture skew, precision-mode paths,
or all front-end ranges. Q8.8 preserves arithmetic fractions, not eight extra
ENOB. Frequency readout and FFT aliasing still need separate corrections.

Tests: engine and app Go suites passed; rotations, mismatched scales, clipping,
ambiguous patterns, strong Fs/5 interference and unchanged bypass data covered.
Deployed from /dev/acq-cal-app with SCOPE_INTERLEAVE_CAL=/dev/acq-interleave-cal.json;
PID1215, log /dev/acq-cal-log. Old app PID1190 exited before replacement. FPGA
unchanged. Scope writes confined to RAM and existing USB settings. App SHA256:
53e17e4fa901f129ccf962da332386e467df3485a2f3c02808ad253a7be67a65
