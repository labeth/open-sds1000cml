# SRAM capture depth and ARM math checkpoint

RAM-only ARM deployment `/dev/acq-average-app` SHA256
`55b991f0d45a8eb191828de075c4fddc1a065dce2b870de2ddfe9df7c05f1394`.
Existing revision 11 FPGA retained. GPMC inherited descriptor only.
Runtime GOGC=20, GOMEMLIMIT=40MiB; calibration in `/dev`.

Implemented selectable SRAM depth (at least the screen width), fractional
15-point ERES and bounded-memory disjoint block averaging. Averaging retains
Q8 fractions; no claim of calibrated extra ENOB. Alignment currently uses the
hardware trigger word, not an interpolated sub-sample crossing. Settings,
calibration admission, trigger-position or qualification changes reset blocks.

Verified on hardware: 20,000 samples/channel, approximately 3–4 published
frames/second with webpage attached. Calibrated four-capture averages reached
4/4 repeatedly, all observed frames triggered, zero bus errors. Earlier ERES
checkpoint verified 1,048,576 samples/channel, 2.68 seconds recall plus 7.12
seconds processing. Full-depth four-capture SINGLE passed: sequence 502, depth 1,048,576 per channel, Q8 output, triggered=true, block average 4/4, running=false and single=false, zero bus errors. The final recall took 5.08 seconds. Scope-rendered screen saved alongside this report.

Focused Go tests cover depth geometry, Q8 ERES against a direct reference,
fractional block means, reset/block rollover and 256-capture sum range.
The old sliding-ring average is not used for SRAM: its memory scales with N.

## Visual rejection of averaging result

Although the four-capture SINGLE sequencing passed, the rendered waveform
failed fidelity: the roughly 6 Vpp triangle became a flattened roughly 1.4 Vpp
waveform. Switching back to NORMAL restored the triangle immediately.
Hardware-word alignment is therefore insufficient and the averaging feature
is NOT validated. Next work must recover a waveform-derived alignment anchor
before summing, with a shape/amplitude regression test. Scope left running
NORMAL at 20k depth; do not regard the average metadata as proof of fidelity.
