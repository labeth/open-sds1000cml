# E2E checkpoint audit

Goal: one scope LCD/web path with triggering and averaging, supporting faster
short records and slower deep records.

Current RAM application `/dev/acq-e2e-final-app` SHA256
`f83a738c66e6f5e063345e74ee54b9741f6f53c3d6bbe67325e31a7206545de7`.
Existing revision 11 FPGA retained; inherited GPMC descriptor; no internal
permanent-storage writes. Calibration is currently validated at 2 V/div.

- Webpage inspected live: 500 ns/div, NORM rising CH1, AVERAGE 4, 20k selected,
  about 3 fps. Human controls include 1M depth, run/stop and SINGLE.
- LCD renderer inspected: both approximately 6 Vpp triangles, both 1 MHz.
- Trigger hold/resume: +4.87047 V threshold held sequence 260 for three seconds;
  restored DAC code 31461 resumed to sequence 267; zero bus errors.
- Short average: four records, phase-aligned triangle retained. Integer sample
  shifts crop the common region; actual valid depth is separate from selection.
- Deep average: prior aligned build completed four 1M records, retained
  1,048,369 samples/channel and preserved triangle amplitude. Current-build
  repeat passed: sequence 313 stopped after four captures in 65.6 seconds,
  1,048,573 valid samples/channel, zero bus errors, requested depth 1,048,576.
  See final-status.json and final-screen.png. Short 20,480-sample averaging
  was restored to RUN afterward.
- ERES: fractional boxcar path verified earlier on hardware at both depths.
- Engine, SRAM capture and measurement tests pass, including a saved hardware
  waveform regression that reads 999929 Hz and shifted-average shape tests.

Limitations: freeze/read/rearm has acquisition dead time; averaging is disjoint
blocks, not a sliding ring; alignment is integer-sample; no measured extra ENOB
claim. Full-depth ARM processing remains slow. This checkpoint does not claim
all protocols or all timebases have been validated.
