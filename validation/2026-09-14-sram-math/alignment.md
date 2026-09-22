# Waveform-aligned SRAM averaging

RAM app SHA256: `f7abf154f1d9ff76a4018f15d37b0b35b032f99b640ec4fa9634e2987f4e75fa`.

The hardware trigger word stayed at 10000 while the triangle phase changed
between raw records. Averaging those word indices directly distorted the
signal. The correction finds a confirmed same-slope crossing near the hardware
anchor, shifts by an integer number of samples, and publishes the intersection
of the contributing records. No wrap or fabricated edge samples.

Short-record SINGLE passed on hardware: four 20,000-sample records produced
19,790 common samples per channel, filter reports 4/4, running=false,
zero bus errors. Scope-rendered triangle retained approximately 6 Vpp.
Fractional alignment and validated ENOB gain are not claimed.

Full-depth SINGLE also passed: sequence 11, four captures, 1,048,369 common samples/channel, 4/4 waveform-aligned average, stopped, zero bus errors. Rendered triangle preserves approximately 6 Vpp. Frequency readout remains incorrect. Images saved alongside this report. The requested-depth UI status fix is tested locally but not yet deployed.
